package forward

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func TestHandleRejectsUnknownPath(t *testing.T) {
	f := New("http://localhost:11434")
	_, err := f.Handle(protocol.RequestBody{Path: "/api/admin/exec"}, nopSend)
	if err == nil || err.Code != "path_not_allowed" {
		t.Fatalf("expected path_not_allowed, got %+v", err)
	}
}

func TestHandleForwardsAndStreamsBytes(t *testing.T) {
	wantBody := `{"id":"123","object":"chat.completion","usage":{"prompt_tokens":5,"completion_tokens":7}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wantBody))
	}))
	defer srv.Close()

	f := New(srv.URL)
	var chunks []protocol.ChunkBody
	send := func(c protocol.ChunkBody) error { chunks = append(chunks, c); return nil }
	done, errBody := f.Handle(protocol.RequestBody{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   json.RawMessage(`{"model":"llama"}`),
	}, send)
	if errBody != nil {
		t.Fatalf("Handle: %+v", errBody)
	}
	if len(chunks) < 1 {
		t.Fatal("expected at least one chunk")
	}
	if chunks[0].StatusCode != 200 {
		t.Fatalf("first chunk status: got %d, want 200", chunks[0].StatusCode)
	}
	if chunks[0].Headers["Content-Type"] != "application/json" {
		t.Fatalf("first chunk headers: %+v", chunks[0].Headers)
	}
	// Concatenate bytes from all chunks; should equal wantBody.
	var assembled bytes.Buffer
	for _, c := range chunks {
		raw, err := base64.StdEncoding.DecodeString(c.Bytes)
		if err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		assembled.Write(raw)
	}
	if assembled.String() != wantBody {
		t.Fatalf("assembled body mismatch:\n got %q\nwant %q", assembled.String(), wantBody)
	}
	if done.PromptTokens != 5 || done.CompletionTokens != 7 {
		t.Fatalf("token counts: got %d/%d, want 5/7", done.PromptTokens, done.CompletionTokens)
	}
}

func TestHandleEmitsAtLeastOneChunkOnEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	f := New(srv.URL)
	var chunks []protocol.ChunkBody
	send := func(c protocol.ChunkBody) error { chunks = append(chunks, c); return nil }
	_, errBody := f.Handle(protocol.RequestBody{Path: "/v1/chat/completions"}, send)
	if errBody != nil {
		t.Fatalf("Handle: %+v", errBody)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want exactly 1 (headers-only)", len(chunks))
	}
	if chunks[0].StatusCode != 200 {
		t.Fatalf("status: got %d, want 200", chunks[0].StatusCode)
	}
}

func TestHandleSurfacesUpstreamUnreachable(t *testing.T) {
	// Point at a closed port — DialContext should fail quickly.
	f := New("http://127.0.0.1:1")
	_, errBody := f.Handle(protocol.RequestBody{Path: "/v1/chat/completions"}, nopSend)
	if errBody == nil || errBody.Code != "upstream_unreachable" {
		t.Fatalf("expected upstream_unreachable, got %+v", errBody)
	}
}

func TestUsageScannerHandlesSSETail(t *testing.T) {
	// Simulate Ollama's SSE: many data: lines followed by a usage
	// block in the last chunk.
	var u usageScanner
	for i := 0; i < 50; i++ {
		_, _ = u.Write([]byte(`data: {"id":"x","choices":[{"delta":{"content":"a"}}]}` + "\n"))
	}
	_, _ = u.Write([]byte(`data: {"usage":{"prompt_tokens":12,"completion_tokens":34}}` + "\n"))
	_, _ = u.Write([]byte(`data: [DONE]` + "\n"))
	p, c := u.Tokens()
	if p != 12 || c != 34 {
		t.Fatalf("Tokens: got %d/%d, want 12/34", p, c)
	}
}

func TestUsageScannerIgnoresUsageInStringValue(t *testing.T) {
	// Adversarial input: model produces escaped JSON-shaped content
	// that looks like a usage block. Without the `"usage":{` anchor
	// the naive `"usage"` substring search would match and parse
	// whatever JSON object happens to follow.
	var u usageScanner
	_, _ = u.Write([]byte(`{"choices":[{"delta":{"content":"\"usage\": {\"prompt_tokens\": 999}"}}]}`))
	p, c := u.Tokens()
	if p != 0 || c != 0 {
		t.Fatalf("usage-in-string-value: got %d/%d, want 0/0", p, c)
	}
}

func TestUsageScannerIgnoresUsageSubstringInKey(t *testing.T) {
	// A field named "usage_metadata" must not match either.
	var u usageScanner
	_, _ = u.Write([]byte(`{"usage_metadata":{"prompt_tokens": 999}}`))
	p, c := u.Tokens()
	if p != 0 || c != 0 {
		t.Fatalf("usage_metadata: got %d/%d, want 0/0", p, c)
	}
}

func TestUsageScannerReturnsZerosOnAbsentUsage(t *testing.T) {
	var u usageScanner
	_, _ = u.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	p, c := u.Tokens()
	if p != 0 || c != 0 {
		t.Fatalf("absent usage: got %d/%d, want 0/0", p, c)
	}
}

func TestUsageScannerHandlesBufferOverflow(t *testing.T) {
	var u usageScanner
	// Write > 64 KiB before the usage block to exercise the tail-
	// keep-half compaction.
	for i := 0; i < 1000; i++ {
		_, _ = u.Write(bytes.Repeat([]byte("x"), 256))
	}
	_, _ = u.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	p, c := u.Tokens()
	if p != 1 || c != 2 {
		t.Fatalf("after overflow: got %d/%d, want 1/2", p, c)
	}
}

func TestContextNotPlumbedYet(t *testing.T) {
	// Reminder test: until we plumb ctx through Handle, the
	// requestTimeout constant is the only watchdog. Keep this
	// stub so a future refactor doesn't forget to add real
	// context cancellation.
	if reqTimeout() == 0 {
		t.Fatal("requestTimeout sentinel is zero")
	}
	// Silence linter; ctx unused in current implementation.
	_ = context.Background
	_ = strings.TrimSpace
}

func nopSend(protocol.ChunkBody) error { return nil }
