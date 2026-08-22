package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestTextModelCapabilitiesUseNativeShowContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runtime/api/show" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "gemma3:4b" {
			t.Fatalf("model = %q", request.Model)
		}
		_, _ = w.Write([]byte(`{"capabilities":["completion","vision","completion"]}`))
	}))
	defer server.Close()

	capabilities, err := NewTextClient(server.URL+"/runtime", server.Client()).ModelCapabilities(context.Background(), "gemma3:4b")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"completion", "vision"}
	if !reflect.DeepEqual(capabilities, want) {
		t.Fatalf("capabilities = %#v, want %#v", capabilities, want)
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

func TestRuntimeHealthPreservesCapabilityLifecycleAndResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"warming","version":"image-runtime-2","backend":"cuda","device":"cuda:0","vram_bytes":1073741824,"models":["sana"],"capabilities":[{"id":"image.generate","status":"warming","models":["sana"],"paths":["/v1/images/generations"],"reason":"weights loading","limits":{"max_input_bytes":33554432}}]}`))
	}))
	defer server.Close()

	health, err := NewImageClient(server.URL, server.Client()).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != StatusWarming || health.Version != "image-runtime-2" || health.Backend != "cuda" || health.Device != "cuda:0" || health.VRAMBytes != 1073741824 {
		t.Fatalf("health = %#v", health)
	}
	if len(health.Capabilities) != 1 || health.Capabilities[0].ID != "image.generate" || health.Capabilities[0].Status != StatusWarming || health.Capabilities[0].Limits.MaxInputBytes != 33554432 {
		t.Fatalf("capabilities = %#v", health.Capabilities)
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
	router := NewRouter("http://text.internal", "http://image.internal", "http://speech.internal", http.DefaultClient)

	for path, wantKind := range map[string]Kind{
		"/v1/chat/completions":   KindText,
		"/v1/completions":        KindText,
		"/v1/responses":          KindText,
		"/v1/embeddings":         KindText,
		"/v1/models":             KindText,
		"/v1/images/generations": KindImage,
		"/v1/images/edits":       KindImage,
		"/v1/audio/speech":       KindSpeech,
	} {
		target, err := router.Resolve(path)
		if err != nil || target.Kind() != wantKind {
			t.Fatalf("resolve %q = %v, %v", path, target, err)
		}
	}
	if _, err := router.Resolve("/api/admin/exec"); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("disallowed path error = %v", err)
	}

	// The bundled speech runtime serves synthesis only, so transcription and translation must stay rejected.
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		if _, err := router.Resolve(path); !errors.Is(err, ErrPathNotAllowed) {
			t.Fatalf("resolve %q = %v, want ErrPathNotAllowed", path, err)
		}
	}

	withoutImages := NewRouter("http://text.internal", "", "http://speech.internal", http.DefaultClient)
	if _, err := withoutImages.Resolve("/v1/images/edits"); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("missing image runtime error = %v", err)
	}
}

func TestTextRuntimeReportsResponsesSupportFromVersion(t *testing.T) {
	version := "0.13.2"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
	}))
	defer server.Close()
	client := NewTextClient(server.URL, server.Client())
	supported, err := client.SupportsResponses(context.Background())
	if err != nil || supported {
		t.Fatalf("0.13.2 support = %v, %v; want false", supported, err)
	}
	version = "0.13.3"
	supported, err = client.SupportsResponses(context.Background())
	if err != nil || !supported {
		t.Fatalf("0.13.3 support = %v, %v; want true", supported, err)
	}
	version = "0.14.0-rc1"
	supported, err = client.SupportsResponses(context.Background())
	if err != nil || !supported {
		t.Fatalf("0.14.0-rc1 support = %v, %v; want true", supported, err)
	}
}

// A supplier who runs images but not speech must be told which runtime is missing; reporting every gap as the image runtime sent them to the wrong service.
func TestRouterNamesTheUnavailableRuntime(t *testing.T) {
	router := NewRouter("http://text.internal", "http://image.internal", "", http.DefaultClient)

	_, err := router.Resolve("/v1/audio/speech")
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("missing speech runtime error = %v", err)
	}
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || unavailable.Kind != KindSpeech {
		t.Fatalf("error = %#v, want KindSpeech", err)
	}
	if err.Error() != "the local speech runtime is not configured" {
		t.Fatalf("message = %q", err)
	}
}
