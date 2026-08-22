package forward

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func TestHandleRejectsUnknownPath(t *testing.T) {
	f := New("http://localhost:11434", "", "")
	_, err := f.Handle(context.Background(), protocol.RequestBody{Path: "/api/admin/exec"}, nopSend)
	if err == nil || err.Code != "path_not_allowed" {
		t.Fatalf("expected path_not_allowed, got %+v", err)
	}
}

func TestHandleRoutesImageGenerationToDiffusers(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("image generation must not reach Ollama")
	}))
	defer ollama.Close()
	diffusers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("path = %q, want image generation path", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2U="}]}`))
	}))
	defer diffusers.Close()

	f := New(ollama.URL, diffusers.URL, "")
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{
		Path: "/v1/images/generations",
		Body: json.RawMessage(`{"model":"Efficient-Large-Model/Sana_600M_1024px_diffusers","prompt":"robot"}`),
	}, nopSend)
	if errBody != nil {
		t.Fatalf("Handle: %+v", errBody)
	}
}

func TestHandleRoutesChatToOllamaWhenDiffusersIsConfigured(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want chat completions", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer ollama.Close()
	diffusers := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("chat must not reach Diffusers")
	}))
	defer diffusers.Close()

	f := New(ollama.URL, diffusers.URL, "")
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{Path: "/v1/chat/completions"}, nopSend)
	if errBody != nil {
		t.Fatalf("Handle: %+v", errBody)
	}
}

func TestHandleRejectsImageRequestWhenDiffusersIsUnconfigured(t *testing.T) {
	f := New("http://localhost:11434", "", "")
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{Path: "/v1/images/generations"}, nopSend)
	if errBody == nil || errBody.Code != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable, got %+v", errBody)
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

	f := New(srv.URL, "", "")
	var chunks []protocol.ChunkBody
	send := func(c protocol.ChunkBody) error { chunks = append(chunks, c); return nil }
	done, errBody := f.Handle(context.Background(), protocol.RequestBody{
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

func TestHandleReportsRedactedRequestMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":3,"completion_tokens":5}}`))
	}))
	defer srv.Close()

	var started RequestEvent
	var finished RequestEvent
	f := New(srv.URL, "", "")
	f.Observer = ObserverFuncs{
		StartedFunc:  func(event RequestEvent) { started = event },
		FinishedFunc: func(event RequestEvent) { finished = event },
	}
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{
		Method:      http.MethodPost,
		Path:        "/v1/chat/completions",
		ConsumerRef: "customer-opaque",
		Body:        json.RawMessage(`{"model":"qwen3:8b","messages":[{"role":"user","content":"do not persist this"}]}`),
	}, nopSend)
	if errBody != nil {
		t.Fatalf("Handle: %+v", errBody)
	}
	if started.Model != "qwen3:8b" || started.Path != "/v1/chat/completions" || started.Consumer != "customer-opaque" {
		t.Fatalf("started = %+v", started)
	}
	if finished.PromptTokens != 3 || finished.CompletionTokens != 5 || finished.Error != "" {
		t.Fatalf("finished = %+v", finished)
	}
}

func TestHandleEmitsAtLeastOneChunkOnEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	f := New(srv.URL, "", "")
	var chunks []protocol.ChunkBody
	send := func(c protocol.ChunkBody) error { chunks = append(chunks, c); return nil }
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{Path: "/v1/chat/completions"}, send)
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
	f := New("http://127.0.0.1:1", "", "")
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{Path: "/v1/chat/completions"}, nopSend)
	if errBody == nil || errBody.Code != "upstream_unreachable" {
		t.Fatalf("expected upstream_unreachable, got %+v", errBody)
	}
}

func TestUsageScannerHandlesSSETail(t *testing.T) {
	// Simulate Ollama's SSE: many data: lines followed by a usage block in the last chunk.
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
	// Adversarial input: model produces escaped JSON-shaped content that looks like a usage block. Without the `"usage":{` anchor the naive `"usage"` substring search would match and parse whatever JSON object happens to follow.
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
	// Write > 64 KiB before the usage block to exercise the tail- keep-half compaction.
	for i := 0; i < 1000; i++ {
		_, _ = u.Write(bytes.Repeat([]byte("x"), 256))
	}
	_, _ = u.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	p, c := u.Tokens()
	if p != 1 || c != 2 {
		t.Fatalf("after overflow: got %d/%d, want 1/2", p, c)
	}
}

func TestHandleAbortsOnCancelledContext(t *testing.T) {
	// A hung upstream + a cancelled session context must abort the in-flight forwarded request promptly instead of streaming forever — this is the goroutine/HTTP leak the per-request context guards.
	reached := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(reached)
		<-r.Context().Done() // hang until the client aborts the request
	}))
	defer srv.Close()

	f := New(srv.URL, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-reached // cancel only once the request is in flight upstream
		cancel()
	}()

	start := time.Now()
	_, errBody := f.Handle(ctx, protocol.RequestBody{Path: "/v1/chat/completions"}, nopSend)
	if errBody == nil {
		t.Fatal("expected an error after ctx cancellation, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Handle did not abort promptly on ctx cancel: took %s", elapsed)
	}
}

// Speech responses are the first non-text payload the agent forwards. The chunk envelope is JSON, so raw audio only survives if it is base64-encoded on the way out — a byte-exact round trip is the property that makes TTS deliverable at all.
func TestHandleStreamsSpeechAudioBytesIntact(t *testing.T) {
	audio := make([]byte, 3*DefaultChunkBytes+17)
	for i := range audio {
		audio[i] = byte(i % 251)
	}
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("path = %q, want speech path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audio)
	}))
	defer speech.Close()

	f := New("http://localhost:11434", "", speech.URL)
	var got []byte
	var chunks []protocol.ChunkBody
	send := func(c protocol.ChunkBody) error {
		chunks = append(chunks, c)
		decoded, err := base64.StdEncoding.DecodeString(c.Bytes)
		if err != nil {
			t.Fatalf("chunk is not valid base64: %v", err)
		}
		got = append(got, decoded...)
		return nil
	}
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{
		Method: http.MethodPost,
		Path:   "/v1/audio/speech",
		Body:   json.RawMessage(`{"model":"hexgrad/Kokoro-82M","input":"hello","voice":"af_alloy"}`),
	}, send)
	if errBody != nil {
		t.Fatalf("Handle: %+v", errBody)
	}
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want the payload split across several frames", len(chunks))
	}
	if chunks[0].Headers["Content-Type"] != "audio/mpeg" {
		t.Fatalf("first chunk Content-Type = %q, want audio/mpeg", chunks[0].Headers["Content-Type"])
	}
	if !bytes.Equal(got, audio) {
		t.Fatalf("reassembled audio differs from upstream: got %d bytes, want %d", len(got), len(audio))
	}
}

func TestHandleRejectsSpeechWhenRuntimeIsUnconfigured(t *testing.T) {
	f := New("http://localhost:11434", "", "")
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{Path: "/v1/audio/speech"}, nopSend)
	if errBody == nil || errBody.Code != "runtime_unavailable" {
		t.Fatalf("expected runtime_unavailable, got %+v", errBody)
	}
	if errBody.Message != "the local speech runtime is not configured" {
		t.Fatalf("message = %q, want it to name the speech runtime", errBody.Message)
	}
}

// The bundled speech runtime serves synthesis only, so transcription must be refused by the path whitelist.
func TestHandleRejectsTranscriptionEvenWithSpeechRuntime(t *testing.T) {
	speech := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("transcription must not reach the speech runtime")
	}))
	defer speech.Close()

	f := New("http://localhost:11434", "", speech.URL)
	_, errBody := f.Handle(context.Background(), protocol.RequestBody{Path: "/v1/audio/transcriptions"}, nopSend)
	if errBody == nil || errBody.Code != "path_not_allowed" {
		t.Fatalf("expected path_not_allowed, got %+v", errBody)
	}
}

func nopSend(protocol.ChunkBody) error { return nil }
