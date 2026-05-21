// Package forward is the RequestHandler installed in the WS client:
// translates an inbound protocol.RequestBody into a real HTTP call
// against the local Ollama, streams the response body back as
// Chunk frames, and finalises with Done carrying the token counts
// Ollama returns.
//
// Path whitelist is enforced — a compromised gateway must not be
// able to coerce the agent into POST'ing to arbitrary local URLs.
// Only the OpenAI-compatible /v1/* paths Ollama exposes are
// allowed; anything else returns an Error frame immediately
// without touching the network.
package forward

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// Forwarder is what main.go constructs and hands to the WS client
// as the RequestHandler.
type Forwarder struct {
	OllamaURL  string        // e.g. http://ollama:11434
	HTTPClient *http.Client  // defaulted in New() if nil
	// ChunkBytes caps each base64-encoded Chunk frame payload.
	// Ollama emits SSE in ~60-200 byte chunks; we batch up to this
	// many DECODED bytes per Chunk frame to limit JSON envelope
	// overhead without piling memory on slow gateway links.
	ChunkBytes int
}

const (
	// DefaultChunkBytes — 8 KiB per frame is a good balance between
	// per-frame envelope cost (the base64 expansion + JSON keys
	// run ~40 bytes overhead) and adapter-side back-pressure
	// granularity. A streaming completion at ~200 tokens/s with
	// ~5 bytes/token feeds ~1 KB/s, so 8 KB = ~8s of buffered
	// output — comfortably under the 5-min total-response watchdog
	// on the gateway side.
	DefaultChunkBytes = 8 * 1024

	// requestTimeout caps the entire forwarded request. Ollama on a
	// slow GPU can run minutes on a long-context completion; we
	// match the gateway's 5-min watchdog so both ends time out
	// together rather than one tearing down state while the other
	// keeps waiting.
	requestTimeout = 5 * time.Minute
)

// New constructs a Forwarder with defaults filled in.
func New(ollamaURL string) *Forwarder {
	return &Forwarder{
		OllamaURL: strings.TrimRight(ollamaURL, "/"),
		HTTPClient: &http.Client{
			// No client-level timeout — we use a per-request context
			// timeout in Handle so streaming long completions
			// doesn't trip an idle-conn watchdog.
		},
		ChunkBytes: DefaultChunkBytes,
	}
}

// allowedPaths is the OpenAI-compatible surface Ollama exposes
// under /v1/. The gateway never sends anything else (the relay
// adapter constructs RequestBody.Path from the buyer's request),
// but defense in depth: validating here means a future relay-side
// regression or malicious gateway can't trick the agent into
// hitting /api/admin/exec or similar invented routes.
var allowedPaths = map[string]bool{
	"/v1/chat/completions": true,
	"/v1/completions":      true,
	"/v1/embeddings":       true,
	"/v1/models":           true,
}

// Handle is the protocol.RequestHandler signature the WS client
// expects. Builds the outbound HTTP request, streams the response
// in ChunkBytes-sized batches, and returns Done with the token
// counts parsed off Ollama's final SSE event (when streaming) or
// JSON body (when not).
func (f *Forwarder) Handle(req protocol.RequestBody, send func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
	if !allowedPaths[req.Path] {
		return protocol.DoneBody{}, &protocol.ErrorBody{
			Code:    "path_not_allowed",
			Message: fmt.Sprintf("agent refuses to forward %q (whitelist enforced)", req.Path),
		}
	}
	if req.Method == "" {
		req.Method = http.MethodPost
	}

	url := f.OllamaURL + req.Path
	hReq, err := http.NewRequest(req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		return protocol.DoneBody{}, &protocol.ErrorBody{Code: "request_build_failed", Message: err.Error()}
	}
	for k, v := range req.Headers {
		hReq.Header.Set(k, v)
	}
	// Default Content-Type if the gateway didn't forward one.
	if hReq.Header.Get("Content-Type") == "" {
		hReq.Header.Set("Content-Type", "application/json")
	}

	started := time.Now()
	resp, err := f.HTTPClient.Do(hReq)
	if err != nil {
		return protocol.DoneBody{}, &protocol.ErrorBody{Code: "upstream_unreachable", Message: err.Error()}
	}
	defer resp.Body.Close()

	// First chunk carries the upstream status + headers so the
	// gateway-side adapter can patch its synthetic *http.Response
	// before the body bytes start arriving. Subsequent chunks
	// just stream bytes.
	headerCopy := make(map[string]string, 4)
	for _, h := range []string{"Content-Type", "Cache-Control", "X-Request-Id"} {
		if v := resp.Header.Get(h); v != "" {
			headerCopy[h] = v
		}
	}

	usage := protocol.DoneBody{}
	tee := &usageScanner{}
	body := io.TeeReader(resp.Body, tee)

	firstChunk := true
	chunkSize := f.ChunkBytes
	if chunkSize <= 0 {
		chunkSize = DefaultChunkBytes
	}
	buf := make([]byte, chunkSize)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			chunk := protocol.ChunkBody{
				Bytes: base64.StdEncoding.EncodeToString(buf[:n]),
			}
			if firstChunk {
				chunk.StatusCode = resp.StatusCode
				chunk.Headers = headerCopy
				firstChunk = false
			}
			if err := send(chunk); err != nil {
				return protocol.DoneBody{}, &protocol.ErrorBody{Code: "send_failed", Message: err.Error()}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return protocol.DoneBody{}, &protocol.ErrorBody{Code: "upstream_read_failed", Message: readErr.Error()}
		}
	}

	// Always send at least one chunk so the gateway-side
	// waitForFirstChunk doesn't time out on an immediately-EOF
	// response (e.g. a buggy upstream returning 200 + empty body).
	if firstChunk {
		_ = send(protocol.ChunkBody{
			StatusCode: resp.StatusCode,
			Headers:    headerCopy,
		})
	}

	usage.PromptTokens, usage.CompletionTokens = tee.Tokens()
	usage.DurationMs = time.Since(started).Milliseconds()
	return usage, nil
}

// usageScanner sniffs Ollama's response for token counts so the
// agent can stuff them into the Done frame for billing. Works on
// both SSE chunked output (looks for the final `data: {...
// "usage": {...} ...}` line) and plain JSON (parses the whole body
// at EOF). Best-effort — a 0/0 Done is acceptable, the gateway
// already has fallback estimates.
type usageScanner struct {
	buf bytes.Buffer
}

func (u *usageScanner) Write(p []byte) (int, error) {
	// Cap accumulated bytes at 64 KiB — long responses don't need
	// to be re-scanned in full; the usage block lands near the end.
	const maxBuf = 64 * 1024
	if u.buf.Len() > maxBuf {
		// Keep only the tail — usage is always at the end.
		tail := u.buf.Bytes()[u.buf.Len()-maxBuf/2:]
		u.buf.Reset()
		u.buf.Write(tail)
	}
	return u.buf.Write(p)
}

// Tokens returns (prompt, completion). 0/0 if we couldn't find a
// usage block. Tolerant of partial frames at the buffer head — we
// scan from the tail.
//
// Anchor pattern is `"usage":{` (with both the closing key-quote AND
// the colon AND the opening brace) so model-output strings containing
// the literal token `"usage"` — e.g. a chat response like
// `{"content":"\"usage\""}` — don't false-match. The OpenAI / Ollama
// usage block always has the exact `"usage":{...}` shape; restricting
// the pattern to that form rules out the string-value case the naïve
// `"usage"` substring search was vulnerable to.
func (u *usageScanner) Tokens() (prompt int, completion int) {
	data := u.buf.Bytes()
	idx := bytes.LastIndex(data, []byte(`"usage":{`))
	if idx < 0 {
		return 0, 0
	}
	// Start at the `{` (penultimate byte of the anchor — skip the
	// `"usage":` prefix to land on the JSON object opener).
	start := idx + len(`"usage":`)
	depth := 0
	end := -1
	for i := start; i < len(data); i++ {
		switch data[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		return 0, 0
	}
	var parsed struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	if err := json.Unmarshal(data[start:end], &parsed); err != nil {
		return 0, 0
	}
	return parsed.PromptTokens, parsed.CompletionTokens
}

// reqTimeout is exported as a function so a test can swap a shorter
// deadline in. Currently unused — present for the next iteration
// when we add a context-aware Handle.
func reqTimeout() time.Duration { return requestTimeout }

var _ = reqTimeout // keep the symbol around without lint noise
