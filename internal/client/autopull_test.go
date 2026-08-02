package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPullOllamaModelDrainsStream covers the happy path: a streaming pull
// is drained to completion and reported as success.
func TestPullOllamaModelDrainsStream(t *testing.T) {
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody.Store(string(buf[:n]))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pulling"}` + "\n" + `{"status":"success"}` + "\n"))
	}))
	defer srv.Close()

	if err := pullOllamaModel(context.Background(), srv.URL, "qwen3"); err != nil {
		t.Fatalf("pullOllamaModel: %v", err)
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, `"name":"qwen3"`) {
		t.Fatalf("model name not forwarded to ollama, body=%q", body)
	}
}

// TestPullOllamaModelSurfacesHTTPError pins that a non-200 from ollama is an
// error rather than a silent no-op — otherwise a node would report success
// for a model it never downloaded.
func TestPullOllamaModelSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such model", http.StatusNotFound)
	}))
	defer srv.Close()

	if err := pullOllamaModel(context.Background(), srv.URL, "ghost"); err == nil {
		t.Fatal("expected an error for a 404 from ollama")
	}
}

// TestPullRecommendedModelsCapsAndContinues pins the two safety properties
// of the batch path: the gateway cannot make the agent pull an unbounded
// number of models onto someone else's disk, and one failing model does not
// abort the rest.
func TestPullRecommendedModelsCapsAndContinues(t *testing.T) {
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := pulls.Add(1)
		if n == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
	}))
	defer srv.Close()

	c := &Client{cfg: Config{OllamaURL: srv.URL, Log: func(string, string) {}}}

	oversized := make([]string, maxAutoPullModels+5)
	for i := range oversized {
		oversized[i] = "m"
	}
	c.pullRecommendedModels(context.Background(), oversized)
	if got := int(pulls.Load()); got != maxAutoPullModels {
		t.Fatalf("pulled %d models, want the cap %d", got, maxAutoPullModels)
	}
}

// TestPullRecommendedModelsNoOllamaURL pins that auto-pull stays inert when
// no local runtime is configured, rather than issuing requests to "".
func TestPullRecommendedModelsNoOllamaURL(t *testing.T) {
	c := &Client{cfg: Config{Log: func(string, string) {}}}
	c.pullRecommendedModels(context.Background(), []string{"qwen3"})
}
