// Package forward is the RequestHandler installed in the WS client: translates an inbound protocol.RequestBody into a real HTTP call against the selected local runtime, streams the response body back as Chunk frames, and finalises with Done carrying the token counts Ollama returns.
//
// Path whitelist is enforced — a compromised gateway must not be able to coerce the agent into POST'ing to arbitrary local URLs. Only the OpenAI-compatible /v1/* paths the configured runtimes expose are allowed; anything else returns an Error frame immediately without touching the network.
package forward

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
	edgeruntime "github.com/everyapi-ai/everyapi-edge/internal/runtime"
)

// Forwarder is what main.go constructs and hands to the WS client as the RequestHandler.
type Forwarder struct {
	runtimes *edgeruntime.Router
	// Observer receives redacted request lifecycle events for the local Edge console. It never receives request bodies or headers.
	Observer Observer
	sequence atomic.Uint64
	// ChunkBytes caps each base64-encoded Chunk frame payload. Ollama emits SSE in ~60-200 byte chunks; we batch up to this many DECODED bytes per Chunk frame to limit JSON envelope overhead without piling memory on slow gateway links.
	ChunkBytes int
}

// RequestEvent is safe to retain locally: it contains operational metadata and Ollama usage only, never a buyer prompt, API key, or forwarded headers.
type RequestEvent struct {
	ID               string
	Consumer         string
	Model            string
	Path             string
	StartedAt        time.Time
	Duration         time.Duration
	PromptTokens     int
	CompletionTokens int
	Error            string
}

// Observer observes redacted forwarded-request lifecycle events.
type Observer interface {
	Started(RequestEvent)
	Finished(RequestEvent)
}

// ObserverFuncs makes small integrations and tests ergonomic without adding a callback dependency to the forwarding hot path.
type ObserverFuncs struct {
	StartedFunc  func(RequestEvent)
	FinishedFunc func(RequestEvent)
}

func (o ObserverFuncs) Started(event RequestEvent) {
	if o.StartedFunc != nil {
		o.StartedFunc(event)
	}
}

func (o ObserverFuncs) Finished(event RequestEvent) {
	if o.FinishedFunc != nil {
		o.FinishedFunc(event)
	}
}

const (
	// DefaultChunkBytes — 8 KiB per frame is a good balance between per-frame envelope cost (the base64 expansion + JSON keys run ~40 bytes overhead) and adapter-side back-pressure granularity. A streaming completion at ~200 tokens/s with ~5 bytes/token feeds ~1 KB/s, so 8 KB = ~8s of buffered output — comfortably under the 5-min total-response watchdog on the gateway side.
	DefaultChunkBytes = 8 * 1024

	// requestTimeout caps the entire forwarded request. Ollama on a slow GPU can run minutes on a long-context completion; we match the gateway's 5-min watchdog so both ends time out together rather than one tearing down state while the other keeps waiting.
	requestTimeout = 5 * time.Minute
)

// New constructs a Forwarder with defaults filled in.
func New(ollamaURL, diffusersURL string) *Forwarder {
	httpClient := &http.Client{
		// No client-level timeout — Handle owns the streaming deadline.
	}
	return &Forwarder{
		runtimes:   edgeruntime.NewRouter(ollamaURL, diffusersURL, httpClient),
		ChunkBytes: DefaultChunkBytes,
	}
}

// allowedPaths is the OpenAI-compatible surface Ollama exposes under /v1/. The gateway never sends anything else (the relay adapter constructs RequestBody.Path from the buyer's request), but defense in depth: validating here means a future relay-side regression or malicious gateway can't trick the agent into hitting /api/admin/exec or similar invented routes. Handle is the protocol.RequestHandler signature the WS client expects. Builds the outbound HTTP request, streams the response in ChunkBytes-sized batches, and returns Done with the token counts parsed off Ollama's final SSE event (when streaming) or JSON body (when not).
func (f *Forwarder) Handle(ctx context.Context, req protocol.RequestBody, send func(protocol.ChunkBody) error) (done protocol.DoneBody, fault *protocol.ErrorBody) {
	target, err := f.runtimes.Resolve(req.Path)
	if errors.Is(err, edgeruntime.ErrPathNotAllowed) {
		return protocol.DoneBody{}, &protocol.ErrorBody{
			Code:    "path_not_allowed",
			Message: fmt.Sprintf("agent refuses to forward %q (whitelist enforced)", req.Path),
		}
	}
	if errors.Is(err, edgeruntime.ErrRuntimeUnavailable) {
		return protocol.DoneBody{}, &protocol.ErrorBody{
			Code:    "runtime_unavailable",
			Message: "the local image runtime is not configured",
		}
	}
	if err != nil {
		return protocol.DoneBody{}, &protocol.ErrorBody{Code: "runtime_unavailable", Message: err.Error()}
	}
	if req.Method == "" {
		req.Method = http.MethodPost
	}
	event := RequestEvent{ID: fmt.Sprintf("local-%d", f.sequence.Add(1)), Consumer: req.ConsumerRef, Model: requestModel(req.Body), Path: req.Path, StartedAt: time.Now().UTC()}
	if f.Observer != nil {
		f.Observer.Started(event)
		defer func() {
			event.Duration = time.Since(event.StartedAt)
			event.PromptTokens = done.PromptTokens
			event.CompletionTokens = done.CompletionTokens
			if fault != nil {
				event.Error = fault.Code
			}
			f.Observer.Finished(event)
		}()
	}

	// Bound the whole forwarded request: caps a hung or slow upstream (and aborts immediately if the session ctx is cancelled mid-call) so neither the goroutine nor the Ollama connection outlives the session. Matches the gateway's 5-min response watchdog.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	headers := make(http.Header, len(req.Headers)+1)
	for k, v := range req.Headers {
		headers.Set(k, v)
	}
	// Default Content-Type if the gateway didn't forward one.
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}

	started := time.Now()
	resp, err := target.Do(ctx, req.Method, req.Path, headers, bytes.NewReader(req.Body))
	if err != nil {
		var requestError *edgeruntime.RequestError
		if errors.As(err, &requestError) && requestError.Op == "build" {
			return protocol.DoneBody{}, &protocol.ErrorBody{Code: "request_build_failed", Message: err.Error()}
		}
		return protocol.DoneBody{}, &protocol.ErrorBody{Code: "upstream_unreachable", Message: err.Error()}
	}
	defer resp.Body.Close()

	// First chunk carries the upstream status + headers so the gateway-side adapter can patch its synthetic *http.Response before the body bytes start arriving. Subsequent chunks just stream bytes.
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

	// Always send at least one chunk so the gateway-side waitForFirstChunk doesn't time out on an immediately-EOF response (e.g. a buggy upstream returning 200 + empty body).
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

// requestModel extracts only the public model selector. A malformed body is forwarded unchanged to Ollama, but the console renders it as "unknown"; copying the body merely to make telemetry prettier would violate the local privacy boundary.
func requestModel(body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.Model) == "" {
		return "unknown"
	}
	return payload.Model
}

// usageScanner sniffs Ollama's response for token counts so the agent can stuff them into the Done frame for billing. Works on both SSE chunked output (looks for the final `data: {... "usage": {...} ...}` line) and plain JSON (parses the whole body at EOF). Best-effort — a 0/0 Done is acceptable, the gateway already has fallback estimates.
type usageScanner struct {
	buf bytes.Buffer
}

func (u *usageScanner) Write(p []byte) (int, error) {
	// Cap accumulated bytes at 64 KiB — long responses don't need to be re-scanned in full; the usage block lands near the end.
	const maxBuf = 64 * 1024
	if u.buf.Len() > maxBuf {
		// Keep only the tail — usage is always at the end.
		tail := u.buf.Bytes()[u.buf.Len()-maxBuf/2:]
		u.buf.Reset()
		u.buf.Write(tail)
	}
	return u.buf.Write(p)
}

// Tokens returns (prompt, completion). 0/0 if we couldn't find a usage block. Tolerant of partial frames at the buffer head — we scan from the tail.
//
// Anchor pattern is `"usage":{` (with both the closing key-quote AND the colon AND the opening brace) so model-output strings containing the literal token `"usage"` — e.g. a chat response like `{"content":"\"usage\""}` — don't false-match. The OpenAI / Ollama usage block always has the exact `"usage":{...}` shape; restricting the pattern to that form rules out the string-value case the naïve `"usage"` substring search was vulnerable to.
func (u *usageScanner) Tokens() (prompt int, completion int) {
	data := u.buf.Bytes()
	idx := bytes.LastIndex(data, []byte(`"usage":{`))
	if idx < 0 {
		return 0, 0
	}
	// Start at the `{` (penultimate byte of the anchor — skip the `"usage":` prefix to land on the JSON object opener).
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
