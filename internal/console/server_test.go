package console

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestHandlerProtectsManagementAPIAndListsModels(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:8b","size":5200000000,"details":{"parameter_size":"8B","quantization_level":"Q4_K_M"}}]}`))
	}))
	defer ollama.Close()

	h := NewHandler(Config{OllamaURL: ollama.URL, Token: "local-secret", VRAMTotalGB: 24}, NewStore(16))

	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	req.Header.Set("Authorization", "Bearer local-secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("models status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "qwen3:8b") {
		t.Fatalf("models response omitted model: %s", response.Body.String())
	}
}

// The console UI is a compiled bundle (console-web, built by `make console`),
// so this asserts on the shape the Go side owns rather than on UI copy: the
// mount point React renders into, and the fact that everything is inlined. It
// deliberately does NOT match translated strings — those live in the bundle's
// i18n dictionary and get minified, and pinning them here would make every copy
// edit a Go test failure.
func TestEmbeddedControlRoomServesSelfContainedDocument(t *testing.T) {
	h := NewHandler(Config{OllamaURL: "http://ollama:11434", Token: "local-secret"}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("index status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `<div id="root">`) {
		t.Fatalf("embedded page is missing the React mount point: %.400s", body)
	}
	// The handler serves exactly one route for the UI, so an asset reference
	// that survived the build would 404 at runtime and leave a blank console.
	if externalAsset.MatchString(body) {
		t.Fatalf("embedded page references an external asset; the bundle must be inlined: %s",
			externalAsset.FindString(body))
	}
	// A stale placeholder (or an un-built checkout) would pass the checks above
	// while shipping no application at all.
	if len(body) < 100_000 {
		t.Fatalf("embedded page is %d bytes; the console bundle looks unbuilt", len(body))
	}
}

var externalAsset = regexp.MustCompile(`<script\b[^>]*\bsrc=|<link\b[^>]*\bhref=`)

func TestOverviewIncludesLoadedVRAMFromOllama(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"size_vram":2147483648},{"size_vram":1073741824}]}`))
	}))
	defer ollama.Close()
	h := NewHandler(Config{OllamaURL: ollama.URL, Token: "local-secret", VRAMTotalGB: 24}, NewStore(16))
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("Authorization", "Bearer local-secret")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"loaded_vram_bytes":3221225472`) || !strings.Contains(response.Body.String(), `"vram_total_gb":24`) {
		t.Fatalf("overview = %d %s", response.Code, response.Body.String())
	}
}

func TestStoreRecordsRequestWithoutPersistingPrompt(t *testing.T) {
	store := NewStore(16)
	request := store.Start(RequestStart{ID: "req-1", Model: "qwen3:8b", Path: "/v1/chat/completions"})
	store.Finish(request, RequestFinish{
		CompletedAt:      time.Unix(1_700_000_005, 0),
		PromptTokens:     12,
		CompletionTokens: 34,
		Duration:         5 * time.Second,
	})

	overview := store.Overview()
	if overview.CompletedRequests != 1 || overview.PromptTokens != 12 || overview.CompletionTokens != 34 {
		t.Fatalf("overview = %+v", overview)
	}
	requests := store.Requests()
	if len(requests) != 1 || requests[0].Model != "qwen3:8b" || requests[0].Path != "/v1/chat/completions" {
		t.Fatalf("requests = %+v", requests)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitFor(ctx, func() bool { return store.Overview().CompletedRequests == 1 }); err != nil {
		t.Fatal(err)
	}
}

func TestStoreKeepsBoundedLocalLogs(t *testing.T) {
	store := NewStore(2)
	store.Log("info", "connected")
	store.Log("warn", "Ollama is slow")
	store.Log("error", "request failed")
	logs := store.Logs()
	if len(logs) != 2 || logs[0].Message != "request failed" || logs[1].Message != "Ollama is slow" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestStoreTruncatesOversizedLogMessage(t *testing.T) {
	store := NewStore(2)
	store.Log("warn", strings.Repeat("x", maxLogMessageBytes+100))
	logs := store.Logs()
	if len(logs) != 1 || len(logs[0].Message) > maxLogMessageBytes || !strings.HasSuffix(logs[0].Message, "…(truncated)") {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestStoreRecordsIdempotentSettlement(t *testing.T) {
	store := NewStore(1)
	receipt := Settlement{RequestID: "receipt-1", SellerAmountMicros: 125_000, SettledAt: time.Unix(1_700_000_000, 0)}
	store.Settle(receipt)
	store.Settle(receipt)
	if got := store.Overview(); !got.SettledEarningsAvailable || got.SettledEarningsMicros != 125_000 {
		t.Fatalf("overview = %+v", got)
	}
	if got := store.Settlements(); len(got) != 1 || got[0].RequestID != receipt.RequestID {
		t.Fatalf("settlements = %+v", got)
	}
	store.Settle(Settlement{RequestID: "receipt-2", SellerAmountMicros: 50_000, SettledAt: time.Unix(1_700_000_001, 0)})
	if got := store.Overview().SettledEarningsMicros; got != 50_000 {
		t.Fatalf("bounded settlement total = %d, want 50000", got)
	}
}

func waitFor(ctx context.Context, condition func() bool) error {
	for {
		if condition() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func TestHandlerStartsModelPullAndExposesProgress(t *testing.T) {
	pullStarted := make(chan struct{}, 1)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Name != "qwen3:8b" {
			t.Fatalf("model = %q", body.Name)
		}
		pullStarted <- struct{}{}
		_, _ = w.Write([]byte("{\"status\":\"pulling manifest\"}\n{\"status\":\"success\"}\n"))
	}))
	defer ollama.Close()

	h := NewHandler(Config{OllamaURL: ollama.URL, Token: "local-secret"}, NewStore(16))
	req := httptest.NewRequest(http.MethodPost, "/api/models/pull", strings.NewReader(`{"name":"qwen3:8b"}`))
	req.Header.Set("Authorization", "Bearer local-secret")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusAccepted {
		t.Fatalf("pull status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case <-pullStarted:
	case <-time.After(time.Second):
		t.Fatal("pull did not start")
	}
}
