package console

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// textRuntimeStub counts what the capability endpoint actually asks Ollama for, which is the whole point of the cache: the answer must not change, only the number of round trips.
type textRuntimeStub struct {
	mu       sync.Mutex
	tags     string
	showCall atomic.Int64
	tagCall  atomic.Int64
}

func (s *textRuntimeStub) setTags(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags = body
}

func (s *textRuntimeStub) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			s.tagCall.Add(1)
			s.mu.Lock()
			body := s.tags
			s.mu.Unlock()
			_, _ = io.WriteString(w, body)
		case "/api/show":
			s.showCall.Add(1)
			_, _ = io.WriteString(w, `{"capabilities":["completion"]}`)
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"0.12.0"}`)
		default:
			t := r.URL.Path
			http.Error(w, fmt.Sprintf("unexpected text runtime path %s", t), http.StatusNotFound)
		}
	}))
}

func taggedModels(version string, names ...string) string {
	entries := make([]string, 0, len(names))
	for _, name := range names {
		if version == "" {
			entries = append(entries, fmt.Sprintf(`{"name":%q}`, name))
			continue
		}
		entries = append(entries, fmt.Sprintf(`{"name":%q,"modified_at":%q}`, name, version))
	}
	return `{"models":[` + strings.Join(entries, ",") + `]}`
}

func readCapabilities(t *testing.T, h http.Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/capabilities", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, body=%s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func TestCapabilitiesAskOllamaOncePerModelVersion(t *testing.T) {
	stub := &textRuntimeStub{}
	stub.setTags(taggedModels("2026-09-01T10:00:00Z", "qwen3:8b", "gemma3:27b", "nomic-embed-text"))
	server := stub.server()
	defer server.Close()
	h := newHandler(Config{OllamaURL: server.URL}, NewStore(16), nil)

	first := readCapabilities(t, h)
	afterFirst := stub.showCall.Load()
	if afterFirst != 3 {
		t.Fatalf("first pass issued %d /api/show calls, want one per model", afterFirst)
	}

	for range 4 {
		if again := readCapabilities(t, h); again != first {
			t.Fatalf("a cached pass changed the answer:\nfirst=%s\nagain=%s", first, again)
		}
	}
	if extra := stub.showCall.Load() - afterFirst; extra != 0 {
		t.Fatalf("four further polls re-asked %d times for models that had not changed", extra)
	}
	if stub.tagCall.Load() < 5 {
		t.Fatalf("the installed list must still be read every pass, got %d", stub.tagCall.Load())
	}
}

func TestCapabilitiesReaskWhenAModelIsRepulled(t *testing.T) {
	stub := &textRuntimeStub{}
	stub.setTags(taggedModels("2026-09-01T10:00:00Z", "qwen3:8b"))
	server := stub.server()
	defer server.Close()
	h := newHandler(Config{OllamaURL: server.URL}, NewStore(16), nil)

	readCapabilities(t, h)
	readCapabilities(t, h)
	if stub.showCall.Load() != 1 {
		t.Fatalf("unchanged model asked %d times", stub.showCall.Load())
	}

	// The same tag, re-pulled: the name is identical and only modified_at moves.
	stub.setTags(taggedModels("2026-09-02T08:30:00Z", "qwen3:8b"))
	readCapabilities(t, h)
	if stub.showCall.Load() != 2 {
		t.Fatalf("a re-pulled model was served from cache (%d calls)", stub.showCall.Load())
	}
}

func TestCapabilitiesNeverCacheARuntimeThatReportsNoVersion(t *testing.T) {
	stub := &textRuntimeStub{}
	stub.setTags(taggedModels("", "qwen3:8b"))
	server := stub.server()
	defer server.Close()
	h := newHandler(Config{OllamaURL: server.URL}, NewStore(16), nil)

	readCapabilities(t, h)
	readCapabilities(t, h)
	readCapabilities(t, h)
	if stub.showCall.Load() != 3 {
		t.Fatalf("a runtime reporting no modified_at was cached anyway (%d calls for 3 polls)", stub.showCall.Load())
	}
}

func TestCapabilitiesForgetModelsThatWereRemoved(t *testing.T) {
	// A node that installs and removes models over months must not accumulate an entry per model it has ever held, so a removed model is dropped rather than kept against a future reinstall.
	stub := &textRuntimeStub{}
	const version = "2026-09-01T10:00:00Z"
	stub.setTags(taggedModels(version, "qwen3:8b", "gemma3:27b"))
	server := stub.server()
	defer server.Close()
	h := newHandler(Config{OllamaURL: server.URL}, NewStore(16), nil)

	readCapabilities(t, h)
	if stub.showCall.Load() != 2 {
		t.Fatalf("first pass asked %d times for two models", stub.showCall.Load())
	}

	stub.setTags(taggedModels(version, "qwen3:8b"))
	readCapabilities(t, h)
	if extra := stub.showCall.Load() - 2; extra != 0 {
		t.Fatalf("removing a model re-asked for the remaining one (%d extra calls)", extra)
	}

	// Reinstalled at the very same version: only a dropped entry forces a fresh ask.
	stub.setTags(taggedModels(version, "qwen3:8b", "gemma3:27b"))
	readCapabilities(t, h)
	if extra := stub.showCall.Load() - 2; extra != 1 {
		t.Fatalf("a reinstalled model was answered from a stale entry (%d extra calls, want 1)", extra)
	}
}
