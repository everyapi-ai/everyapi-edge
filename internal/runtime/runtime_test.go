package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestTextModelsAreNormalized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/api/tags" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":" qwen3:8b "},{"name":""},{"name":"qwen3:8b"},{"name":"gemma3:4b"}]}`))
	}))
	defer server.Close()

	models, err := NewTextClient(server.URL+"/runtime", server.Client()).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gemma3:4b", "qwen3:8b"}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
}

func TestImageHealthIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready","models":["z-model","a-model","z-model"]}`))
	}))
	defer server.Close()

	health, err := NewImageClient(server.URL, server.Client()).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != StatusReady || !reflect.DeepEqual(health.Models, []string{"a-model", "z-model"}) {
		t.Fatalf("health = %#v", health)
	}
}

func TestClientPreservesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewTextClient(server.URL, server.Client()).Models(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestRuntimeErrorDoesNotExposeImplementationBrand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private upstream failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := NewTextClient(server.URL, server.Client()).Models(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusServiceUnavailable || !upstream.Retryable {
		t.Fatalf("error = %#v", err)
	}
	if err.Error() != "local runtime returned HTTP 503" {
		t.Fatalf("public error = %q", err)
	}
}

func TestRouterAllowsOnlyKnownRuntimePaths(t *testing.T) {
	router := NewRouter("http://text.internal", "http://image.internal", http.DefaultClient)

	for path, wantKind := range map[string]Kind{
		"/v1/chat/completions":   KindText,
		"/v1/completions":        KindText,
		"/v1/embeddings":         KindText,
		"/v1/models":             KindText,
		"/v1/images/generations": KindImage,
		"/v1/images/edits":       KindImage,
	} {
		target, err := router.Resolve(path)
		if err != nil || target.Kind() != wantKind {
			t.Fatalf("resolve %q = %v, %v", path, target, err)
		}
	}
	if _, err := router.Resolve("/api/admin/exec"); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("disallowed path error = %v", err)
	}

	withoutImages := NewRouter("http://text.internal", "", http.DefaultClient)
	if _, err := withoutImages.Resolve("/v1/images/edits"); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("missing image runtime error = %v", err)
	}
}
