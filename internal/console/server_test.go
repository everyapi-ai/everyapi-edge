package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
	edgeruntime "github.com/everyapi-ai/everyapi-edge/internal/runtime"
)

func TestResourceSettingsAPIReadsAndSavesPolicy(t *testing.T) {
	policy := protocol.ResourcePolicy{
		Text:   protocol.RuntimeResourcePolicy{MaxConcurrent: 4, ReserveVRAMMB: 1024},
		Image:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 4096},
		Speech: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
		Video:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 8192},
		Render: protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Rerank: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
	}
	saved := make(chan protocol.ResourcePolicy, 1)
	h := NewHandlers(Config{
		LoadResourceSettings: func() (ResourceSettings, error) {
			return ResourceSettings{ResourcePolicy: policy, DrainState: protocol.DrainStateServing, ActiveRequests: 2}, nil
		},
		SaveResourcePolicy: func(_ context.Context, next protocol.ResourcePolicy) (ResourceSettings, error) {
			saved <- next
			return ResourceSettings{ResourcePolicy: next, DrainState: protocol.DrainStateServing}, nil
		},
	}, NewStore(16)).Control

	getResponse := httptest.NewRecorder()
	h.ServeHTTP(getResponse, consoleHTTPRequest(http.MethodGet, "/api/settings", nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	var got ResourceSettings
	if err := json.Unmarshal(getResponse.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if got.ResourcePolicy != policy || got.ActiveRequests != 2 {
		t.Fatalf("GET settings = %#v", got)
	}

	next := policy
	next.Image.MaxConcurrent = 2
	body, _ := json.Marshal(struct {
		ResourcePolicy protocol.ResourcePolicy `json:"resource_policy"`
	}{ResourcePolicy: next})
	putResponse := httptest.NewRecorder()
	h.ServeHTTP(putResponse, consoleHTTPRequest(http.MethodPut, "/api/settings", bytes.NewReader(body)))
	if putResponse.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putResponse.Code, putResponse.Body.String())
	}
	if actual := <-saved; actual != next {
		t.Fatalf("saved = %#v, want %#v", actual, next)
	}
}

func TestDrainAPIStartsAndCancelsDrain(t *testing.T) {
	actions := make(chan bool, 2)
	h := NewHandlers(Config{
		SetDrain: func(_ context.Context, enabled bool) (ResourceSettings, error) {
			actions <- enabled
			state := protocol.DrainStateServing
			if enabled {
				state = protocol.DrainStateDraining
			}
			return ResourceSettings{DrainState: state}, nil
		},
	}, NewStore(16)).Control

	for _, test := range []struct {
		body string
		want bool
	}{
		{body: `{"enabled":true}`, want: true},
		{body: `{"enabled":false}`, want: false},
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/drain", strings.NewReader(test.body)))
		if response.Code != http.StatusOK {
			t.Fatalf("POST status = %d, body = %s", response.Code, response.Body.String())
		}
		if got := <-actions; got != test.want {
			t.Fatalf("drain enabled = %t, want %t", got, test.want)
		}
	}
}

func TestHandlerListsModelsWithoutLocalToken(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:8b","size":5200000000,"details":{"parameter_size":"8B","quantization_level":"Q4_K_M"}}]}`))
	}))
	defer ollama.Close()

	h := NewHandler(Config{OllamaURL: ollama.URL, VRAMTotalGB: 24}, NewStore(16))

	req := consoleHTTPRequest(http.MethodGet, "/api/models", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("models status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "qwen3:8b") {
		t.Fatalf("models response omitted model: %s", response.Body.String())
	}
}

func TestRuntimeCapabilitiesExposeStartingAsWarming(t *testing.T) {
	capabilities := runtimeCapabilities(protocol.RuntimeImage, edgeruntime.RuntimeHealth{Capabilities: []edgeruntime.RuntimeCapability{{ID: string(protocol.CapabilityImageGenerate), Status: edgeruntime.StatusStarting}}})
	if len(capabilities) != 1 || capabilities[0].Status != protocol.CapabilityWarming {
		t.Fatalf("starting capabilities = %+v", capabilities)
	}
}

func newRuntimeControlHandlers(t *testing.T) Handlers {
	t.Helper()
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/ps" {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[]}`)
	}))
	t.Cleanup(runtime.Close)
	return NewHandlers(Config{OllamaURL: runtime.URL}, NewStore(16))
}

func TestBrowserHandlerRejectsCrossSiteMutation(t *testing.T) {
	handlers := newRuntimeControlHandlers(t)
	request := consoleHTTPRequest(http.MethodPost, "/api/runtime/unload-all", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()

	handlers.Browser.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site mutation status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestBrowserHandlerRejectsMutationWithoutOriginEvidence(t *testing.T) {
	handlers := newRuntimeControlHandlers(t)
	request := consoleHTTPRequest(http.MethodPost, "/api/runtime/unload-all", nil)
	response := httptest.NewRecorder()

	handlers.Browser.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("metadata-free browser mutation status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestBrowserHandlerAllowsSameOriginMutation(t *testing.T) {
	handlers := newRuntimeControlHandlers(t)
	request := consoleHTTPRequest(http.MethodPost, "/api/runtime/unload-all", nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()

	handlers.Browser.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("same-origin mutation status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestControlHandlerDoesNotRequireBrowserFetchMetadata(t *testing.T) {
	handlers := newRuntimeControlHandlers(t)
	response := httptest.NewRecorder()

	handlers.Control.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/runtime/unload-all", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("internal control status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
}

func TestHandlerReportsTheConfiguredNodeProfile(t *testing.T) {
	h := NewHandler(Config{
		OllamaURL:    "http://local-runtime:11434",
		NodeName:     "studio-gpu",
		AgentVersion: "v1.2.3",
		GPUModel:     "RTX 4090",
		Platform:     "linux/amd64",
		CountryISO2:  "JP",
		VRAMTotalGB:  24,
	}, NewStore(16))

	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/node", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("node profile status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"name":"studio-gpu"`, `"agent_version":"v1.2.3"`, `"gpu_model":"RTX 4090"`, `"platform":"linux/amd64"`, `"country_iso2":"JP"`, `"vram_total_gb":24`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("node profile missing %s: %s", want, response.Body.String())
		}
	}
}

func TestOverviewFallsBackToTheConfiguredAgentVersion(t *testing.T) {
	h := NewHandler(Config{AgentVersion: "v1.2.3"}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/overview", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"agent_version":"v1.2.3"`) {
		t.Fatalf("overview version = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerRejectsRemovingModelLoadedInMemory(t *testing.T) {
	localRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b"}]}`))
		case "/api/delete":
			t.Fatal("a loaded model must not be deleted before it is unloaded")
		default:
			t.Fatalf("unexpected local runtime path %q", r.URL.Path)
		}
	}))
	defer localRuntime.Close()

	h := NewHandlers(Config{OllamaURL: localRuntime.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodDelete, "/api/models?name=llama3.1%3A8b", nil))

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "unload the model before removing it") {
		t.Fatalf("remove loaded model = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerRequiresConfirmedRemovalAfterRuntimeRefresh(t *testing.T) {
	localRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/delete":
			t.Fatal("a raw removal request must not delete a model")
		default:
			t.Fatalf("unexpected local runtime path %q", r.URL.Path)
		}
	}))
	defer localRuntime.Close()

	h := NewHandlers(Config{OllamaURL: localRuntime.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodDelete, "/api/models?name=llama3.1%3A8b", nil))

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "confirm removal") {
		t.Fatalf("unconfirmed remove = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerReportsLocalModelCapabilities(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/show" {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		var input struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.Name != "qwen3-vl:8b" {
			t.Fatalf("capability request model = %q", input.Name)
		}
		_, _ = w.Write([]byte(`{"capabilities":["completion","vision","tools"]}`))
	}))
	defer ollama.Close()

	h := NewHandler(Config{OllamaURL: ollama.URL}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/models/capabilities?name=qwen3-vl%3A8b", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model":"qwen3-vl:8b"`) || !strings.Contains(response.Body.String(), `"vision"`) {
		t.Fatalf("model capabilities = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerReportsExactNodeCapabilities(t *testing.T) {
	textRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"0.13.3"}`)
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3:8b"}]}`)
		case "/api/show":
			_, _ = io.WriteString(w, `{"capabilities":["completion"]}`)
		default:
			t.Fatalf("unexpected text path %s", r.URL.Path)
		}
	}))
	defer textRuntime.Close()
	imageRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","version":"image-1","capabilities":[{"id":"image.generate","status":"ready","models":["sana"],"paths":["/v1/images/generations"]}]}`)
	}))
	defer imageRuntime.Close()
	speechRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"warming","version":"speech-1","capabilities":[{"id":"audio.tts","status":"warming","models":["kokoro"],"paths":["/v1/audio/speech"],"reason":"loading"}]}`)
	}))
	defer speechRuntime.Close()

	h := NewHandler(Config{OllamaURL: textRuntime.URL, DiffusersURL: imageRuntime.URL, SpeechURL: speechRuntime.URL}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/capabilities", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities = %d %s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"id":"text.chat"`, `"id":"text.completion"`, `"id":"text.responses"`, `"id":"image.generate"`, `"version":"image-1"`, `"id":"audio.tts"`, `"status":"warming"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("capabilities missing %s: %s", want, response.Body.String())
		}
	}
}

// The console UI is a compiled bundle (console-web, built by `make console`), so this asserts on the shape the Go side owns rather than on UI copy: the mount point React renders into, and the fact that everything is inlined. It deliberately does NOT match translated strings — those live in the bundle's i18n dictionary and get minified, and pinning them here would make every copy edit a Go test failure.
func TestEmbeddedControlRoomServesSelfContainedDocument(t *testing.T) {
	h := NewHandler(Config{OllamaURL: "http://ollama:11434"}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("index status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `<div id="root">`) {
		t.Fatalf("embedded page is missing the React mount point: %.400s", body)
	}
	// The handler serves exactly one route for the UI, so an asset reference that survived the build would 404 at runtime and leave a blank console.
	if externalAsset.MatchString(body) {
		t.Fatalf("embedded page references an external asset; the bundle must be inlined: %s",
			externalAsset.FindString(body))
	}
	// A stale placeholder (or an un-built checkout) would pass the checks above while shipping no application at all.
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
	h := NewHandler(Config{OllamaURL: ollama.URL, VRAMTotalGB: 24}, NewStore(16))
	req := consoleHTTPRequest(http.MethodGet, "/api/overview", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"loaded_vram_bytes":3221225472`) || !strings.Contains(response.Body.String(), `"vram_total_gb":24`) {
		t.Fatalf("overview = %d %s", response.Code, response.Body.String())
	}
	var overview struct {
		ReservedVRAMBytes  int64 `json:"reserved_vram_bytes"`
		AvailableVRAMBytes int64 `json:"available_vram_bytes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	const gib = int64(1024 * 1024 * 1024)
	wantReserve := int64(24*gib) / 5
	wantAvailable := int64(24*gib) - int64(3*gib) - wantReserve
	if overview.ReservedVRAMBytes != wantReserve || overview.AvailableVRAMBytes != wantAvailable {
		t.Fatalf("memory budget = reserve %d available %d, want reserve %d available %d", overview.ReservedVRAMBytes, overview.AvailableVRAMBytes, wantReserve, wantAvailable)
	}
}

func TestOverviewIncludesAllManagedRuntimeMemory(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"size_vram":2147483648}]}`)
	}))
	defer ollama.Close()
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","vram_bytes":1073741824}`)
	}))
	defer image.Close()
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","vram_bytes":536870912}`)
	}))
	defer speech.Close()

	h := NewHandler(Config{OllamaURL: ollama.URL, DiffusersURL: image.URL, SpeechURL: speech.URL, VRAMTotalGB: 24}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/overview", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"loaded_vram_bytes":3758096384`) {
		t.Fatalf("overview = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerShowsOllamaRuntime(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.32.5"}`))
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b","size_vram":4294967296,"context_length":32768,"expires_at":"2026-08-03T01:30:00Z"}]}`))
		default:
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
	}))
	defer ollama.Close()

	store := NewStore(16)
	h := NewHandler(Config{OllamaURL: ollama.URL}, store)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/runtime", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("runtime status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"version":"0.32.5"`, `"name":"llama3.1:8b"`, `"size_vram":4294967296`, `"context_length":32768`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("runtime response missing %s: %s", want, response.Body.String())
		}
	}
}

func TestHandlerReportsDiffusersRuntimeReadiness(t *testing.T) {
	diffusers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected Diffusers path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}`))
	}))
	defer diffusers.Close()

	h := NewHandler(Config{OllamaURL: "http://ollama:11434", DiffusersURL: diffusers.URL}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/image-runtime", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) || !strings.Contains(response.Body.String(), `Qwen/Qwen-Image-Edit-2511`) {
		t.Fatalf("image runtime = %d %s", response.Code, response.Body.String())
	}
}

func TestImageRuntimeAvailabilityDoesNotExposeItsImplementation(t *testing.T) {
	h := NewHandler(Config{}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/image-runtime", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"error":"Image editing is unavailable on this node"`) || strings.Contains(strings.ToLower(response.Body.String()), "diffusers") {
		t.Fatalf("image runtime availability = %d %s", response.Code, response.Body.String())
	}
}

func TestImageRuntimePreservesTheSafeGPUReadinessReason(t *testing.T) {
	diffusers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected image runtime path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"unavailable","models":[],"error":"A CUDA-capable GPU is required for image editing."}`))
	}))
	defer diffusers.Close()

	h := NewHandler(Config{DiffusersURL: diffusers.URL}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/image-runtime", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"error":"A CUDA-capable GPU is required for image editing."`) {
		t.Fatalf("image runtime capability = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerSelectsTheActiveImageEditingModel(t *testing.T) {
	modelsChanged := make(chan struct{}, 1)
	diffusers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/models/select" {
			t.Fatalf("unexpected image runtime request %s %s", r.Method, r.URL.Path)
		}
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.Model != "Qwen/Qwen-Image-Edit-2509" {
			t.Fatalf("selected image model = %q", input.Model)
		}
		_, _ = w.Write([]byte(`{"status":"ready","models":["Qwen/Qwen-Image-Edit-2509"]}`))
	}))
	defer diffusers.Close()

	h := NewHandlers(Config{OllamaURL: "http://ollama:11434", DiffusersURL: diffusers.URL, ModelsChanged: func() { modelsChanged <- struct{}{} }}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/image-runtime/model", strings.NewReader(`{"model":"Qwen/Qwen-Image-Edit-2509"}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `Qwen/Qwen-Image-Edit-2509`) {
		t.Fatalf("select image model = %d %s", response.Code, response.Body.String())
	}
	select {
	case <-modelsChanged:
	case <-time.After(time.Second):
		t.Fatal("successful image model selection did not report a capability change")
	}
}

func TestHandlerProxiesImageEditsToDiffusers(t *testing.T) {
	diffusers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/images/edits" {
			t.Fatalf("unexpected Diffusers request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("prompt") != "make it neon" || r.FormValue("model") != "Qwen/Qwen-Image-Edit-2511" {
			t.Fatalf("form = %#v", r.MultipartForm.Value)
		}
		file, _, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if data, _ := io.ReadAll(file); string(data) != "image-bytes" {
			t.Fatalf("image = %q", data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"Qwen/Qwen-Image-Edit-2511","b64_json":"abc"}`))
	}))
	defer diffusers.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("prompt", "make it neon")
	_ = writer.WriteField("model", "Qwen/Qwen-Image-Edit-2511")
	part, _ := writer.CreateFormFile("image", "source.png")
	_, _ = part.Write([]byte("image-bytes"))
	_ = writer.Close()
	h := NewHandlers(Config{OllamaURL: "http://ollama:11434", DiffusersURL: diffusers.URL}, NewStore(16)).Control
	request := consoleHTTPRequest(http.MethodPost, "/api/image/edit", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"b64_json":"abc"`) {
		t.Fatalf("image edit = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerRunsImageSpeechAndEmbeddingPlaygrounds(t *testing.T) {
	textRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected text runtime request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"model":"nomic-embed","data":[{"embedding":[0.25,-0.5]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)
	}))
	defer textRuntime.Close()
	imageRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected image runtime request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"cG5n"}]}`)
	}))
	defer imageRuntime.Close()
	speechRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("unexpected speech runtime request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("wave-bytes"))
	}))
	defer speechRuntime.Close()

	h := NewHandlers(Config{OllamaURL: textRuntime.URL, DiffusersURL: imageRuntime.URL, SpeechURL: speechRuntime.URL}, NewStore(16)).Control
	for _, test := range []struct {
		path, body, contains, contentType string
	}{
		{path: "/api/playground/embedding", body: `{"model":"nomic-embed","input":"hello"}`, contains: `"embedding":[0.25,-0.5]`, contentType: "application/json"},
		{path: "/api/playground/image", body: `{"model":"sana","prompt":"a lighthouse","size":"512x512"}`, contains: `"b64_json":"cG5n"`, contentType: "application/json"},
		{path: "/api/playground/speech", body: `{"model":"kokoro","input":"hello","voice":"af_heart","response_format":"wav"}`, contains: "wave-bytes", contentType: "audio/wav"},
	} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, test.path, strings.NewReader(test.body)))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.contains) || !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) {
			t.Fatalf("%s = %d %s (%s)", test.path, response.Code, response.Body.String(), response.Header().Get("Content-Type"))
		}
	}
}

func TestCopyBoundedPlaygroundResponseRejectsOversizedArtifactBeforeWriting(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"audio/wav"}}, Body: io.NopCloser(strings.NewReader("oversized"))}
	recorder := httptest.NewRecorder()
	err := copyBoundedPlaygroundResponse(recorder, response, 4)
	if err == nil || recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("copy result = code %d, bytes %d, err %v", recorder.Code, recorder.Body.Len(), err)
	}
}

func TestHandlerRejectsTrailingPlaygroundJSON(t *testing.T) {
	h := NewHandlers(Config{OllamaURL: "http://127.0.0.1:1"}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/embedding", strings.NewReader(`{"model":"nomic-embed","input":"hello"} {}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerUnloadsOllamaRuntimeModel(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/generate" {
			t.Fatalf("unexpected Ollama request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model     string `json:"model"`
			KeepAlive int    `json:"keep_alive"`
			Stream    bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "llama3.1:8b" || body.KeepAlive != 0 || body.Stream {
			t.Fatalf("unload body = %+v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/runtime/unload", strings.NewReader(`{"model":"llama3.1:8b"}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unload status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerUnloadsAllRuntimeModels(t *testing.T) {
	var unloaded []string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			if r.Method != http.MethodGet {
				t.Fatalf("runtime list method = %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b"},{"name":"gemma3:27b"}]}`))
		case "/api/generate":
			if r.Method != http.MethodPost {
				t.Fatalf("runtime unload method = %s", r.Method)
			}
			var body struct {
				Model     string `json:"model"`
				KeepAlive int    `json:"keep_alive"`
				Stream    bool   `json:"stream"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.KeepAlive != 0 || body.Stream {
				t.Fatalf("unload body = %+v", body)
			}
			unloaded = append(unloaded, body.Model)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected runtime request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/runtime/unload-all", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("unload all status = %d, body=%s", response.Code, response.Body.String())
	}
	if got, want := strings.Join(unloaded, ","), "llama3.1:8b,gemma3:27b"; got != want {
		t.Fatalf("unloaded = %q, want %q", got, want)
	}
}

func TestHandlerBenchmarksAnUnloadedModelAndReleasesIt(t *testing.T) {
	localRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/generate":
			var body struct {
				Model     string `json:"model"`
				Prompt    string `json:"prompt"`
				Stream    bool   `json:"stream"`
				KeepAlive *int   `json:"keep_alive"`
				Options   struct {
					NumPredict int `json:"num_predict"`
				} `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Model != "llama3.1:8b" || body.Prompt != "Reply with OK." || body.Stream || body.KeepAlive == nil || *body.KeepAlive != 0 || body.Options.NumPredict != 1 {
				t.Fatalf("benchmark request = %+v", body)
			}
			_, _ = w.Write([]byte(`{"eval_count":4,"eval_duration":200000000,"total_duration":1200000000}`))
		default:
			t.Fatalf("unexpected runtime request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer localRuntime.Close()

	h := NewHandlers(Config{OllamaURL: localRuntime.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/models/benchmark", strings.NewReader(`{"model":"llama3.1:8b"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("benchmark status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"model":"llama3.1:8b"`, `"eval_count":4`, `"tokens_per_second":20`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("benchmark missing %s: %s", want, response.Body.String())
		}
	}
}

func TestHandlerRequiresExplicitReleaseBeforeBenchmarkingAResidentModel(t *testing.T) {
	localRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b"}]}`))
		case "/api/generate":
			t.Fatal("a resident model must not be released without explicit confirmation")
		default:
			t.Fatalf("unexpected runtime request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer localRuntime.Close()

	h := NewHandlers(Config{OllamaURL: localRuntime.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/models/benchmark", strings.NewReader(`{"model":"llama3.1:8b"}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "release the resident model") {
		t.Fatalf("resident benchmark = %d %s", response.Code, response.Body.String())
	}
}

func TestPlaygroundBlocksASecondModelThatExceedsTheLiveMemoryBudget(t *testing.T) {
	localRuntime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma3:27b","size_vram":5368709120}]}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b","size":4294967296}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"model":"llama3.1:8b","choices":[{"message":{"role":"assistant","content":"this must not run"}}]}`))
		default:
			t.Fatalf("unexpected runtime request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer localRuntime.Close()

	h := NewHandlers(Config{OllamaURL: localRuntime.URL, VRAMTotalGB: 8}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"llama3.1:8b","messages":[{"role":"user","content":"Hello"}]}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "unload another model") {
		t.Fatalf("memory admission = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerRunsLocalPlaygroundChat(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected Ollama request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "llama3.1:8b" || body.Stream || len(body.Messages) != 1 || body.Messages[0].Content != "Hello" {
			t.Fatalf("forwarded body = %+v", body)
		}
		_, _ = w.Write([]byte(`{"model":"llama3.1:8b","choices":[{"message":{"role":"assistant","content":"Hi there"}}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
	}))
	defer ollama.Close()

	store := NewStore(16)
	h := NewHandlers(Config{OllamaURL: ollama.URL}, store).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"llama3.1:8b","messages":[{"role":"user","content":"Hello"}]}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("playground status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"content":"Hi there"`, `"prompt_tokens":4`, `"total_tokens":6`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("playground response missing %s: %s", want, response.Body.String())
		}
	}
	if overview := store.Overview(); overview.CompletedRequests != 1 || overview.PromptTokens != 4 || overview.CompletionTokens != 2 {
		t.Fatalf("playground request was not added to local telemetry: %+v", overview)
	}
	requests := store.Requests()
	if len(requests) != 1 || requests[0].Consumer != "local playground" || requests[0].Model != "llama3.1:8b" {
		t.Fatalf("playground traffic = %+v", requests)
	}
}

func TestHandlerDoesNotExposeRuntimeBrandInPlaygroundErrors(t *testing.T) {
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected runtime request %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer runtime.Close()

	h := NewHandlers(Config{OllamaURL: runtime.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"llama3.1:8b","messages":[{"role":"user","content":"Hello"}]}`)))
	if response.Code != http.StatusBadGateway || strings.Contains(strings.ToLower(response.Body.String()), "ollama") || !strings.Contains(response.Body.String(), "local runtime") {
		t.Fatalf("playground error = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerRunsImagePlaygroundChatAgainstTheLocalRuntime(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/api/show" {
			_, _ = w.Write([]byte(`{"capabilities":["completion","vision"]}`))
			return
		}
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string   `json:"role"`
				Content string   `json:"content"`
				Images  []string `json:"images"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "qwen3-vl:8b" || body.Stream || len(body.Messages) != 1 || body.Messages[0].Content != "What is in this image?" || len(body.Messages[0].Images) != 1 || body.Messages[0].Images[0] != "aW1hZ2U=" {
			t.Fatalf("forwarded image chat = %+v", body)
		}
		_, _ = w.Write([]byte(`{"model":"qwen3-vl:8b","message":{"role":"assistant","content":"A cat."},"prompt_eval_count":3,"eval_count":4}`))
	}))
	defer ollama.Close()

	store := NewStore(16)
	h := NewHandlers(Config{OllamaURL: ollama.URL}, store).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"qwen3-vl:8b","stream":false,"messages":[{"role":"user","content":"What is in this image?","images":["aW1hZ2U="]}]}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"A cat."`) || !strings.Contains(response.Body.String(), `"prompt_tokens":3`) {
		t.Fatalf("image playground response = %d %s", response.Code, response.Body.String())
	}
	if requests := store.Requests(); len(requests) != 1 || requests[0].Path != "/api/chat" {
		t.Fatalf("image playground traffic = %+v", requests)
	}
}

func TestHandlerRejectsImagePlaygroundChatForTextOnlyModel(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/show" {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"capabilities":["completion","tools"]}`))
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"llama3.1:8b","messages":[{"role":"user","content":"What is in this image?","images":["aW1hZ2U="]}]}`)))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "does not support images") {
		t.Fatalf("text-only image playground = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerStreamsImagePlaygroundChat(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/api/show" {
			_, _ = w.Write([]byte(`{"capabilities":["completion","vision"]}`))
			return
		}
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected local runtime request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte("{\"message\":{\"content\":\"A\"},\"done\":false}\n"))
		_, _ = w.Write([]byte("{\"model\":\"qwen3-vl:8b\",\"message\":{\"content\":\" cat.\"},\"done\":true,\"prompt_eval_count\":3,\"eval_count\":4}\n"))
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"qwen3-vl:8b","stream":true,"messages":[{"role":"user","content":"Describe it","images":["aW1hZ2U="]}]}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("image playground stream = %d %s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"type":"delta","content":"A"`, `"type":"delta","content":" cat."`, `"type":"done","model":"qwen3-vl:8b"`, `"total_tokens":7`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("image playground stream missing %s: %s", want, response.Body.String())
		}
	}
}

func TestHandlerStreamsLocalPlaygroundChat(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream        bool    `json:"stream"`
			Temperature   float64 `json:"temperature"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream || !body.StreamOptions.IncludeUsage || body.Temperature != 0.4 || len(body.Messages) != 2 || body.Messages[0].Role != "system" {
			t.Fatalf("forwarded stream body = %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"there\"}}],\"model\":\"llama3.1:8b\",\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/playground/chat", strings.NewReader(`{"model":"llama3.1:8b","system":"Be concise","temperature":0.4,"stream":true,"messages":[{"role":"user","content":"Hello"}]}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream status = %d, type=%q, body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	for _, want := range []string{`"type":"delta","content":"Hello"`, `"type":"done","model":"llama3.1:8b"`, `"total_tokens":7`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("stream response missing %s: %s", want, response.Body.String())
		}
	}
}

func TestHandlerReportsOllamaStorageLocationAndUsage(t *testing.T) {
	storage := t.TempDir()
	if err := os.Mkdir(filepath.Join(storage, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, "blobs", "sha256-test"), []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(Config{OllamaURL: "http://ollama:11434", StoragePath: storage}, NewStore(16))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/storage", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("storage status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"path":"` + storage + `"`, `"accessible":true`, `"used_bytes":5`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("storage response missing %s: %s", want, response.Body.String())
		}
	}
	var status struct {
		TotalBytes     int64 `json:"total_bytes"`
		AvailableBytes int64 `json:"available_bytes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.TotalBytes <= 0 || status.AvailableBytes <= 0 || status.TotalBytes < status.AvailableBytes {
		t.Fatalf("storage capacity = %+v", status)
	}
}

func TestHandlerPreflightsStorageMigrationTarget(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "model"), []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandlers(Config{OllamaURL: "http://ollama:11434", StoragePath: source}, NewStore(16)).Browser
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleRequest(http.MethodPost, "/api/storage/plan", `{"destination":"`+target+`"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("migration plan status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"ready":true`, `"path":"` + target + `"`, `"used_bytes":5`, `"blockers":[]`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("migration plan missing %s: %s", want, response.Body.String())
		}
	}
}

func TestHandlerPreflightsAnExplicitStorageMigrationSource(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "model"), []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(Config{OllamaURL: "http://ollama:11434", StoragePath: filepath.Join(t.TempDir(), "empty-default")}, NewStore(16)).Browser
	response := httptest.NewRecorder()
	body := `{"source":"` + source + `","destination":"` + target + `"}`
	h.ServeHTTP(response, consoleRequest(http.MethodPost, "/api/storage/plan", body))
	if response.Code != http.StatusOK {
		t.Fatalf("migration plan status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"ready":true`, `"path":"` + source + `"`, `"used_bytes":5`, `"blockers":[]`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("explicit source migration plan missing %s: %s", want, response.Body.String())
		}
	}
}

func TestHandlerRejectsStorageMigrationDestinationInsideSource(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(source, "everyapi-copy")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(Config{OllamaURL: "http://ollama:11434", StoragePath: source}, NewStore(16)).Browser
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleRequest(http.MethodPost, "/api/storage/plan", `{"destination":"`+destination+`"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("migration plan status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"ready":false`) || !strings.Contains(response.Body.String(), "destination must not be inside the source directory") {
		t.Fatalf("nested destination plan = %s", response.Body.String())
	}
}

func TestHandlerCopiesModelsToPreparedMigrationTarget(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "blobs", "sha256-model"), []byte("model-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandlers(Config{OllamaURL: "http://ollama:11434", StoragePath: source}, NewStore(16)).Browser
	start := httptest.NewRecorder()
	h.ServeHTTP(start, consoleRequest(http.MethodPost, "/api/storage/migrate", `{"destination":"`+target+`"}`))
	if start.Code != http.StatusAccepted {
		t.Fatalf("migration start status = %d, body=%s", start.Code, start.Body.String())
	}

	var status struct {
		Completed int64  `json:"completed"`
		Total     int64  `json:"total"`
		Done      bool   `json:"done"`
		Error     string `json:"error"`
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, consoleRequest(http.MethodGet, "/api/storage/migrate", ""))
		if response.Code != http.StatusOK {
			t.Fatalf("migration status = %d, body=%s", response.Code, response.Body.String())
		}
		if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
			t.Fatal(err)
		}
		if status.Done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !status.Done || status.Error != "" || status.Completed != int64(len("model-data")) || status.Total != int64(len("model-data")) {
		t.Fatalf("migration status = %+v", status)
	}
	if copied, err := os.ReadFile(filepath.Join(target, "blobs", "sha256-model")); err != nil || string(copied) != "model-data" {
		t.Fatalf("copied model = %q, err=%v", copied, err)
	}
	if original, err := os.ReadFile(filepath.Join(source, "blobs", "sha256-model")); err != nil || string(original) != "model-data" {
		t.Fatalf("source model = %q, err=%v", original, err)
	}
}

// TestGatewayControlStorageMigrationRefusesForeignPaths pins the confinement of the two allowlisted storage
// endpoints. The gateway reaches them on a supplier's machine, so a caller that names its own source or
// destination could stat, size and recursively copy arbitrary trees on hardware it does not own. Only the
// configured model directory, and a destination the person at the machine chose in the native picker, are
// legitimate over that link.
func TestGatewayControlStorageMigrationRefusesForeignPaths(t *testing.T) {
	source, target, foreign := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "id_ed25519"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	control := newHandlers(Config{OllamaURL: "http://ollama:11434", StoragePath: source}, NewStore(16), func() (string, error) {
		return target, nil
	}).Control

	probe := httptest.NewRecorder()
	control.ServeHTTP(probe, consoleHTTPRequest(http.MethodPost, "/api/storage/plan", strings.NewReader(`{"source":"`+foreign+`","destination":"`+target+`"}`)))
	if probe.Code != http.StatusBadRequest {
		t.Fatalf("foreign source plan status = %d, body=%s", probe.Code, probe.Body.String())
	}
	if strings.Contains(probe.Body.String(), "used_bytes") || strings.Contains(probe.Body.String(), foreign) {
		t.Fatalf("foreign source plan disclosed host filesystem state: %s", probe.Body.String())
	}

	unpicked := httptest.NewRecorder()
	control.ServeHTTP(unpicked, consoleHTTPRequest(http.MethodPost, "/api/storage/plan", strings.NewReader(`{"destination":"`+foreign+`"}`)))
	if unpicked.Code != http.StatusBadRequest {
		t.Fatalf("unpicked destination plan status = %d, body=%s", unpicked.Code, unpicked.Body.String())
	}
	if strings.Contains(unpicked.Body.String(), "used_bytes") {
		t.Fatalf("unpicked destination plan disclosed host filesystem state: %s", unpicked.Body.String())
	}

	copyAttempt := httptest.NewRecorder()
	control.ServeHTTP(copyAttempt, consoleHTTPRequest(http.MethodPost, "/api/storage/migrate", strings.NewReader(`{"source":"`+foreign+`","destination":"`+target+`"}`)))
	if copyAttempt.Code != http.StatusBadRequest {
		t.Fatalf("foreign source migrate status = %d, body=%s", copyAttempt.Code, copyAttempt.Body.String())
	}
	if _, err := os.Stat(filepath.Join(target, "id_ed25519")); !os.IsNotExist(err) {
		t.Fatalf("foreign tree was copied into the destination: %v", err)
	}
}

// TestGatewayControlStorageMigrationAcceptsOperatorPickedDestination keeps the shipped remote flow working: the
// dashboard asks the node's own native picker for a destination, then plans a migration out of the configured
// model directory.
func TestGatewayControlStorageMigrationAcceptsOperatorPickedDestination(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "model"), []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	control := newHandlers(Config{OllamaURL: "http://ollama:11434", StoragePath: source}, NewStore(16), func() (string, error) {
		return target, nil
	}).Control

	pick := httptest.NewRecorder()
	control.ServeHTTP(pick, consoleHTTPRequest(http.MethodPost, "/api/storage/pick", nil))
	if pick.Code != http.StatusOK {
		t.Fatalf("storage picker status = %d, body=%s", pick.Code, pick.Body.String())
	}

	plan := httptest.NewRecorder()
	control.ServeHTTP(plan, consoleHTTPRequest(http.MethodPost, "/api/storage/plan", strings.NewReader(`{"source":"`+source+`","destination":"`+target+`"}`)))
	if plan.Code != http.StatusOK {
		t.Fatalf("picked destination plan status = %d, body=%s", plan.Code, plan.Body.String())
	}
	for _, want := range []string{`"ready":true`, `"path":"` + target + `"`, `"used_bytes":5`, `"blockers":[]`} {
		if !strings.Contains(plan.Body.String(), want) {
			t.Fatalf("picked destination plan missing %s: %s", want, plan.Body.String())
		}
	}
}

func TestHandlerDefersModelDownloadsWhileStorageMigrationIsCopying(t *testing.T) {
	h := &handler{
		cfg:        Config{OllamaURL: "http://ollama:11434"},
		store:      NewStore(16),
		httpClient: &http.Client{Timeout: time.Second},
		migration:  &migrationJob{Status: "copying"},
	}
	response := httptest.NewRecorder()
	h.startPull(response, consoleHTTPRequest(http.MethodPost, "/api/models/pull", strings.NewReader(`{"name":"qwen3:8b"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("download during migration status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerCancelsQueuedModelDownload(t *testing.T) {
	h := &handler{pullQueue: []*pullJob{{Name: "qwen3:14b", Status: "queued"}}}
	response := httptest.NewRecorder()
	h.api(response, consoleHTTPRequest(http.MethodDelete, "/api/models/pull?name=qwen3:14b", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("cancel queued download status = %d, body=%s", response.Code, response.Body.String())
	}
	if len(h.pullQueue) != 0 || h.latestPull == nil || h.latestPull.Status != "cancelled" || !h.latestPull.Done {
		t.Fatalf("queue after cancellation = %+v, latest=%+v", h.pullQueue, h.latestPull)
	}
}

func TestPullProgressReportsARealTransferRateAndRemainingTime(t *testing.T) {
	job := &pullJob{}
	started := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	updatePullProgress(job, pullJob{Status: "downloading", Completed: 100 << 20, Total: 1000 << 20}, started)
	updatePullProgress(job, pullJob{Status: "downloading", Completed: 300 << 20, Total: 1000 << 20}, started.Add(2*time.Second))

	if job.RateBytesPerSecond != 100<<20 {
		t.Fatalf("transfer rate = %v, want %d", job.RateBytesPerSecond, 100<<20)
	}
	if job.SecondsRemaining != 7 {
		t.Fatalf("seconds remaining = %d, want 7", job.SecondsRemaining)
	}
}

func TestHandlerStopsModelPullBeforeItExhaustsModelStorage(t *testing.T) {
	pullStarted := make(chan struct{}, 1)
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/pull":
			pullStarted <- struct{}{}
			_, _ = w.Write([]byte(`{"status":"downloading","completed":0,"total":134217728}` + "\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			t.Fatalf("unexpected local runtime path %q", r.URL.Path)
		}
	}))
	defer runtime.Close()

	h := &handler{
		cfg:              Config{OllamaURL: runtime.URL, StoragePath: t.TempDir()},
		store:            NewStore(16),
		httpClient:       runtime.Client(),
		storageAvailable: func(string) (int64, error) { return 64 << 20, nil },
	}
	response := httptest.NewRecorder()
	h.startPull(response, consoleHTTPRequest(http.MethodPost, "/api/models/pull", strings.NewReader(`{"name":"qwen3:8b"}`)))
	if response.Code != http.StatusAccepted {
		t.Fatalf("pull start status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case <-pullStarted:
	case <-time.After(time.Second):
		t.Fatal("model pull did not start")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := h.pullSnapshot()
		if snapshot.Latest != nil && snapshot.Latest.Done {
			if !strings.Contains(snapshot.Latest.Error, "not enough free disk space") {
				t.Fatalf("storage-full pull error = %q", snapshot.Latest.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("storage-full pull did not finish")
}

func TestHandlerCancelsActiveModelDownload(t *testing.T) {
	cancelled := make(chan struct{})
	h := &handler{pull: &pullJob{Name: "qwen3:14b", Status: "downloading", cancel: func() { close(cancelled) }}}
	response := httptest.NewRecorder()
	h.api(response, consoleHTTPRequest(http.MethodDelete, "/api/models/pull?name=qwen3:14b", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("cancel active download status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("active pull context was not cancelled")
	}
	if !h.pull.cancelled {
		t.Fatalf("active pull was not marked cancelled: %+v", h.pull)
	}
}

func TestHandlerUsesNativeStoragePickerPath(t *testing.T) {
	picker := func() (string, error) { return "/Volumes/models/ollama", nil }
	h := newHandler(Config{OllamaURL: "http://ollama:11434"}, NewStore(16), picker)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/storage/pick", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("storage picker status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"path":"/Volumes/models/ollama"`) {
		t.Fatalf("storage picker response = %s", response.Body.String())
	}
}

func TestCapabilitiesReportEachIDOnceAtItsBestStatus(t *testing.T) {
	// Speech and transcription are separate services that both claim audio.transcription and audio.translation, so an unconfigured node used to answer with each of them twice and contradictory reasons.
	h := newHandler(Config{OllamaURL: "http://ollama:11434"}, NewStore(16), nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/capabilities", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d, body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Capabilities []protocol.Capability `json:"capabilities"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	seen := map[protocol.CapabilityID]int{}
	for _, capability := range payload.Capabilities {
		seen[capability.ID]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("capability %s reported %d times: %s", id, count, response.Body.String())
		}
	}
	if len(payload.Capabilities) == 0 {
		t.Fatal("expected the node to report its unavailable capabilities")
	}

	best := bestCapabilityPerID([]protocol.Capability{
		unavailableCapability(protocol.CapabilityAudioTranscription, protocol.RuntimeSpeech, "speech runtime is not configured"),
		{ID: protocol.CapabilityAudioTranscription, Runtime: protocol.RuntimeSpeech, Status: protocol.CapabilityReady, Models: []string{"whisper-small"}},
	})
	if len(best) != 1 || best[0].Status != protocol.CapabilityReady {
		t.Fatalf("a capability served by one of two runtimes must report the serviceable status: %+v", best)
	}
}

func TestHandlerReportsPickerRemedyButStillRedactsPathFailures(t *testing.T) {
	unavailable := func() (string, error) { return "", fmt.Errorf("%w; install zenity or kdialog", errPickerUnavailable) }
	h := newHandler(Config{OllamaURL: "http://ollama:11434"}, NewStore(16), unavailable)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/storage/pick", nil))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing picker status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "install zenity or kdialog") {
		t.Fatalf("a headless node must be told how to install a chooser: %s", response.Body.String())
	}

	denied := func() (string, error) { return "", errors.New("open /Users/operator/private: permission denied") }
	h = newHandler(Config{OllamaURL: "http://ollama:11434"}, NewStore(16), denied)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/storage/pick", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("denied picker status = %d, body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/Users/operator") {
		t.Fatalf("a picker failure carrying a host path must stay redacted: %s", response.Body.String())
	}
}

func TestPickedStorageDirectoryTrimsPickerOutputAndRejectsCancellation(t *testing.T) {
	path, err := pickedStorageDirectory([]byte("  /Volumes/models/everyapi\n"))
	if err != nil || path != "/Volumes/models/everyapi" {
		t.Fatalf("picked path = %q, err=%v", path, err)
	}
	if _, err := pickedStorageDirectory([]byte(" \n")); err == nil {
		t.Fatal("empty picker output must be treated as a cancelled directory selection")
	}
}

func TestStoreRecordsRequestWithoutPersistingPrompt(t *testing.T) {
	store := NewStore(16)
	request := store.Start(RequestStart{ID: "req-1", Model: "qwen3:8b", Path: "/v1/chat/completions", Capability: "text.chat"})
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
	if len(requests) != 1 || requests[0].Model != "qwen3:8b" || requests[0].Path != "/v1/chat/completions" || requests[0].Capability != "text.chat" {
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
	if len(logs) != 2 || logs[0].Message != "request failed" || logs[1].Message != "local runtime is slow" {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestStoreMasksRuntimeBrandInOperationalErrors(t *testing.T) {
	store := NewStore(4)
	store.SetGatewayState("offline", "Ollama upstream refused the session")
	handle := store.Start(RequestStart{ID: "request-1", Model: "llama3.1:8b"})
	store.Finish(handle, RequestFinish{Error: "OLLAMA returned 500"})

	if gateway := store.Overview().GatewayLastError; strings.Contains(strings.ToLower(gateway), "ollama") || !strings.Contains(gateway, "local runtime") {
		t.Fatalf("gateway error = %q", gateway)
	}
	requests := store.Requests()
	if len(requests) != 1 || strings.Contains(strings.ToLower(requests[0].Error), "ollama") || !strings.Contains(requests[0].Error, "local runtime") {
		t.Fatalf("request errors = %+v", requests)
	}
}

func TestStorePublishesGatewayConnectionState(t *testing.T) {
	store := NewStore(16)
	if initial := store.Overview(); initial.GatewayState != "connecting" {
		t.Fatalf("initial gateway state = %q", initial.GatewayState)
	}

	store.SetGatewayState("online", "")
	online := store.Overview()
	if online.GatewayState != "online" || online.GatewayLastConnectedAt.IsZero() || online.GatewayLastError != "" {
		t.Fatalf("online gateway state = %+v", online)
	}

	store.SetGatewayState("offline", "gateway refused the session")
	offline := store.Overview()
	if offline.GatewayState != "offline" || offline.GatewayLastError != "gateway refused the session" {
		t.Fatalf("offline gateway state = %+v", offline)
	}

	retryAt := time.Date(2026, time.August, 3, 10, 30, 0, 0, time.UTC)
	store.ScheduleGatewayReconnect(retryAt, 2)
	retrying := store.Overview()
	if retrying.GatewayReconnectAttempt != 2 || !retrying.GatewayNextReconnectAt.Equal(retryAt) {
		t.Fatalf("retry diagnostics = %+v", retrying)
	}

	store.SetGatewayState("online", "")
	connectedAgain := store.Overview()
	if connectedAgain.GatewayReconnectAttempt != 0 || !connectedAgain.GatewayNextReconnectAt.IsZero() {
		t.Fatalf("online state retained retry diagnostics = %+v", connectedAgain)
	}
}

func TestStorePublishesLocalPreviewState(t *testing.T) {
	store := NewStore(16)
	store.SetGatewayState("preview", "")

	preview := store.Overview()
	if preview.GatewayState != "preview" || preview.GatewayLastError != "" {
		t.Fatalf("preview state = %+v", preview)
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
	modelsChanged := make(chan struct{}, 1)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}
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

	h := NewHandlers(Config{OllamaURL: ollama.URL, ModelsChanged: func() { modelsChanged <- struct{}{} }}, NewStore(16)).Control
	req := consoleHTTPRequest(http.MethodPost, "/api/models/pull", strings.NewReader(`{"name":"qwen3:8b"}`))
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
	select {
	case <-modelsChanged:
	case <-time.After(time.Second):
		t.Fatal("successful model pull did not report a capability change")
	}
}

func TestHandlerModelPullSurfacesStreamingError(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":"proxyconnect tcp: connection refused"}` + "\n"))
	}))
	defer ollama.Close()

	h := &handler{cfg: Config{OllamaURL: ollama.URL}, store: NewStore(16), storageAvailable: availableStorageBytes}
	job := &pullJob{Name: "qwen3:8b", Status: "queued"}
	h.pull = job
	h.runPull(job)

	snapshot := h.pullSnapshot()
	if snapshot.Latest == nil || snapshot.Latest.Status != "failed" ||
		!strings.Contains(snapshot.Latest.Error, "proxyconnect tcp: connection refused") {
		t.Fatalf("streaming pull result = %+v", snapshot.Latest)
	}
}

func TestHandlerModelPullRequiresTerminalSuccess(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
	}))
	defer ollama.Close()

	h := &handler{cfg: Config{OllamaURL: ollama.URL}, store: NewStore(16), storageAvailable: availableStorageBytes}
	job := &pullJob{Name: "qwen3:8b", Status: "queued"}
	h.pull = job
	h.runPull(job)

	snapshot := h.pullSnapshot()
	if snapshot.Latest == nil || snapshot.Latest.Status != "failed" ||
		!strings.Contains(snapshot.Latest.Error, "without success") {
		t.Fatalf("truncated pull result = %+v", snapshot.Latest)
	}
}

func TestHandlerExposesVersionAndStartsLatestUpdate(t *testing.T) {
	started := make(chan struct{}, 1)
	h := NewHandlers(Config{
		Version: "1.2.3",
		Update: func(_ context.Context, report func(UpdateStatus)) error {
			report(UpdateStatus{State: "downloading", Version: "1.2.4"})
			started <- struct{}{}
			return nil
		},
	}, NewStore(16)).Control

	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/update", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("update did not start")
	}

	response = httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/overview", nil))
	var overview Overview
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if overview.AgentVersion != "1.2.3" || overview.UpdateState != "downloading" || overview.UpdateVersion != "1.2.4" {
		t.Fatalf("overview = %#v", overview)
	}
}

func TestHandlerPersistsAutomaticUpdateSettings(t *testing.T) {
	autoUpdate := false
	h := NewHandlers(Config{
		LoadUpdateSettings: func() (UpdateSettings, error) {
			return UpdateSettings{AutoUpdate: autoUpdate, CheckIntervalHours: 24}, nil
		},
		SaveAutoUpdate: func(enabled bool) (UpdateSettings, error) {
			autoUpdate = enabled
			return UpdateSettings{AutoUpdate: autoUpdate, CheckIntervalHours: 24}, nil
		},
	}, NewStore(16)).Control

	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/update/settings", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", response.Code, response.Body.String())
	}
	var settings UpdateSettings
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.AutoUpdate || settings.CheckIntervalHours != 24 {
		t.Fatalf("initial settings = %#v", settings)
	}

	response = httptest.NewRecorder()
	request := consoleHTTPRequest(http.MethodPut, "/api/update/settings", strings.NewReader(`{"auto_update":true}`))
	request.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if !autoUpdate || !settings.AutoUpdate || settings.CheckIntervalHours != 24 {
		t.Fatalf("saved settings = %#v, callback=%t", settings, autoUpdate)
	}
}

func TestHandlerPersistsAutomaticUpdateMaintenanceWindow(t *testing.T) {
	current := UpdateSettings{AutoUpdate: false, CheckIntervalHours: 24, MaintenanceStart: "01:00", MaintenanceEnd: "03:00"}
	h := NewHandlers(Config{
		LoadUpdateSettings: func() (UpdateSettings, error) { return current, nil },
		SaveUpdateSettings: func(enabled bool, start, end string) (UpdateSettings, error) {
			current.AutoUpdate = enabled
			current.MaintenanceStart = start
			current.MaintenanceEnd = end
			return current, nil
		},
	}, NewStore(16)).Control

	response := httptest.NewRecorder()
	request := consoleHTTPRequest(http.MethodPut, "/api/update/settings", strings.NewReader(`{"auto_update":true,"maintenance_start":"23:30","maintenance_end":"02:00"}`))
	request.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if !current.AutoUpdate || current.MaintenanceStart != "23:30" || current.MaintenanceEnd != "02:00" {
		t.Fatalf("saved settings = %#v", current)
	}
}

func TestHandlerRejectsInvalidAutomaticUpdateMaintenanceWindow(t *testing.T) {
	saved := false
	h := NewHandlers(Config{
		LoadUpdateSettings: func() (UpdateSettings, error) {
			return UpdateSettings{MaintenanceStart: "00:00", MaintenanceEnd: "00:00"}, nil
		},
		SaveUpdateSettings: func(bool, string, string) (UpdateSettings, error) {
			saved = true
			return UpdateSettings{}, nil
		},
	}, NewStore(16)).Control

	response := httptest.NewRecorder()
	request := consoleHTTPRequest(http.MethodPut, "/api/update/settings", strings.NewReader(`{"auto_update":true,"maintenance_start":"24:00","maintenance_end":"02:00"}`))
	request.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if saved {
		t.Fatal("invalid maintenance window reached persistence")
	}
}

func TestHandlersExposeBackgroundAutomaticUpdateStatus(t *testing.T) {
	handlers := NewHandlers(Config{Version: "1.2.3"}, NewStore(16))
	handlers.ReportUpdateStatus(UpdateStatus{State: "checking", Version: "1.2.4"})

	response := httptest.NewRecorder()
	handlers.Control.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/overview", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var overview Overview
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if overview.UpdateState != "checking" || overview.UpdateVersion != "1.2.4" {
		t.Fatalf("overview = %#v", overview)
	}
}

func TestManualUpdateConflictDoesNotOverwriteAutomaticUpdateStatus(t *testing.T) {
	updateReturned := make(chan struct{})
	handlers := NewHandlers(Config{
		Update: func(context.Context, func(UpdateStatus)) error {
			close(updateReturned)
			return ErrUpdateInProgress
		},
	}, NewStore(16))
	handlers.ReportUpdateStatus(UpdateStatus{State: "current", Version: "1.2.3"})

	response := httptest.NewRecorder()
	handlers.Control.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/update", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case <-updateReturned:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manual update conflict")
	}

	deadline := time.Now().Add(time.Second)
	for {
		response = httptest.NewRecorder()
		handlers.Control.ServeHTTP(response, consoleHTTPRequest(http.MethodGet, "/api/overview", nil))
		var overview Overview
		if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
			t.Fatal(err)
		}
		if overview.UpdateState != "checking" {
			if overview.UpdateState != "current" || overview.UpdateVersion != "1.2.3" || overview.UpdateError != "" {
				t.Fatalf("overview = %#v", overview)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual update conflict remained stuck in checking")
		}
		runtime.Gosched()
	}
}

func TestHandlerRejectsMalformedAutomaticUpdateSettings(t *testing.T) {
	saved := false
	h := NewHandlers(Config{
		SaveAutoUpdate: func(enabled bool) (UpdateSettings, error) {
			saved = enabled
			return UpdateSettings{}, nil
		},
	}, NewStore(16)).Control

	response := httptest.NewRecorder()
	request := consoleHTTPRequest(http.MethodPut, "/api/update/settings", strings.NewReader(`{"auto_update":"yes"}`))
	request.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if saved {
		t.Fatal("malformed automatic update setting reached persistence")
	}
}

func TestHandlerRejectsPullForAnInstalledModel(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:8b"}]}`))
		case "/api/pull":
			t.Fatal("installed model must not start a pull")
		default:
			t.Fatalf("unexpected Ollama path %q", r.URL.Path)
		}
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	response := httptest.NewRecorder()
	h.ServeHTTP(response, consoleHTTPRequest(http.MethodPost, "/api/models/pull", strings.NewReader(`{"name":"qwen3:8b"}`)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "already installed") {
		t.Fatalf("installed pull = %d %s", response.Code, response.Body.String())
	}
}

func TestHandlerQueuesModelPullsInsteadOfRejectingTheSecondRequest(t *testing.T) {
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			_, _ = w.Write([]byte(`{"models":[]}`))
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		started <- body.Name
		if body.Name == "qwen3:8b" {
			<-releaseFirst
		}
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer ollama.Close()

	h := NewHandlers(Config{OllamaURL: ollama.URL}, NewStore(16)).Control
	post := func(name string) *httptest.ResponseRecorder {
		req := consoleHTTPRequest(http.MethodPost, "/api/models/pull", strings.NewReader(`{"name":"`+name+`"}`))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		return response
	}
	if response := post("qwen3:8b"); response.Code != http.StatusAccepted {
		t.Fatalf("first pull status = %d, body=%s", response.Code, response.Body.String())
	}
	select {
	case got := <-started:
		if got != "qwen3:8b" {
			t.Fatalf("first started %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first pull did not start")
	}
	if response := post("gemma3:4b"); response.Code != http.StatusAccepted {
		t.Fatalf("queued pull status = %d, body=%s", response.Code, response.Body.String())
	}
	queue := httptest.NewRecorder()
	h.ServeHTTP(queue, consoleHTTPRequest(http.MethodGet, "/api/models/pull", nil))
	if queue.Code != http.StatusOK || !strings.Contains(queue.Body.String(), `"active":{"name":"qwen3:8b"`) || !strings.Contains(queue.Body.String(), `"queued":[{"name":"gemma3:4b"`) {
		t.Fatalf("queue = %d %s", queue.Code, queue.Body.String())
	}
	close(releaseFirst)
	select {
	case got := <-started:
		if got != "gemma3:4b" {
			t.Fatalf("second started %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued pull did not start")
	}
}
