package console

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var webAssets embed.FS

// Config is local-only console configuration.
type Config struct {
	OllamaURL    string
	DiffusersURL string
	StoragePath  string
	VRAMTotalGB  int
	NodeName     string
	AgentVersion string
	GPUModel     string
	Platform     string
	CountryISO2  string
	Version      string
	Update       func(context.Context, func(UpdateStatus)) error
}

type UpdateStatus struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NodeProfile is the startup identity of this local agent. It intentionally
// contains no credentials, gateway URL, or host filesystem details.
type NodeProfile struct {
	Name         string `json:"name"`
	AgentVersion string `json:"agent_version"`
	GPUModel     string `json:"gpu_model"`
	Platform     string `json:"platform"`
	CountryISO2  string `json:"country_iso2"`
	VRAMTotalGB  int    `json:"vram_total_gb"`
}

type Model struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Details    struct {
		ParameterSize     string `json:"parameter_size,omitempty"`
		QuantizationLevel string `json:"quantization_level,omitempty"`
	} `json:"details,omitempty"`
}

// Runtime is the live state reported by Ollama rather than the agent's
// historical request counters. It lets the local console distinguish a model
// that is merely installed from one consuming VRAM right now.
type Runtime struct {
	Version string         `json:"version"`
	Models  []RuntimeModel `json:"models"`
}

type RuntimeModel struct {
	Name          string `json:"name"`
	SizeVRAM      int64  `json:"size_vram"`
	ContextLength int64  `json:"context_length,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
}

// ImageRuntime is the local Diffusers service capability. It is separate from
// Ollama because image generation/editing uses diffusion pipelines rather than
// Ollama's text and multimodal inference APIs.
type ImageRuntime struct {
	Status string   `json:"status"`
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

// Storage describes the model directory visible to this agent process. The
// path is supplied by the bundle rather than inferred from Ollama's HTTP API:
// Ollama deliberately does not disclose host filesystem paths over HTTP.
type Storage struct {
	Path           string `json:"path"`
	Accessible     bool   `json:"accessible"`
	UsedBytes      int64  `json:"used_bytes"`
	TotalBytes     int64  `json:"total_bytes"`
	AvailableBytes int64  `json:"available_bytes"`
	Error          string `json:"error,omitempty"`
}

type MigrationPlan struct {
	Source      Storage  `json:"source"`
	Destination Storage  `json:"destination"`
	Ready       bool     `json:"ready"`
	Blockers    []string `json:"blockers"`
}

// migrationJob copies model files before the user repoints their local
// runtime. Copying is deliberately non-destructive: an interrupted transfer
// must leave the current model library intact and usable.
type migrationJob struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Status      string `json:"status"`
	Completed   int64  `json:"completed"`
	Total       int64  `json:"total"`
	Error       string `json:"error,omitempty"`
	Done        bool   `json:"done"`
}

// PlaygroundMessage is deliberately a small subset of OpenAI's chat message
// shape. The local console is for testing the installed model, not a generic
// proxy that accepts arbitrary Ollama options or tool calls.
type PlaygroundMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type PlaygroundUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type PlaygroundResponse struct {
	Model   string          `json:"model"`
	Content string          `json:"content"`
	Usage   PlaygroundUsage `json:"usage"`
}

type ModelCapabilities struct {
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
}

// ModelBenchmark is a one-token local generation measurement. It reports the
// runtime's own token counters instead of guessing from model parameter size.
type ModelBenchmark struct {
	Model           string  `json:"model"`
	EvalCount       int     `json:"eval_count"`
	EvalDurationNS  int64   `json:"eval_duration_ns"`
	TotalDurationNS int64   `json:"total_duration_ns"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

type playgroundInput struct {
	Model       string              `json:"model"`
	Messages    []PlaygroundMessage `json:"messages"`
	System      string              `json:"system"`
	Temperature *float64            `json:"temperature,omitempty"`
	Stream      bool                `json:"stream"`
}

type playgroundStreamEvent struct {
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Model   string          `json:"model,omitempty"`
	Usage   PlaygroundUsage `json:"usage,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type pullJob struct {
	Name               string  `json:"name"`
	Status             string  `json:"status"`
	Completed          int64   `json:"completed,omitempty"`
	Total              int64   `json:"total,omitempty"`
	RateBytesPerSecond float64 `json:"rate_bytes_per_second,omitempty"`
	SecondsRemaining   int64   `json:"seconds_remaining,omitempty"`
	Error              string  `json:"error,omitempty"`
	Done               bool    `json:"done"`
	cancelled          bool
	cancel             context.CancelFunc
	sampledAt          time.Time
	sampledBytes       int64
}

type pullQueueSnapshot struct {
	Active *pullJob   `json:"active"`
	Queued []*pullJob `json:"queued"`
	Latest *pullJob   `json:"latest"`
}

type handler struct {
	cfg              Config
	store            *Store
	httpClient       *http.Client
	mu               sync.RWMutex
	pull             *pullJob
	pullQueue        []*pullJob
	latestPull       *pullJob
	migration        *migrationJob
	storage          Storage
	storageAt        time.Time
	pickStorage      func() (string, error)
	storageAvailable func(string) (int64, error)
	update           UpdateStatus
}

var validModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)

const (
	imageEditingUnavailableMessage = "Image editing is unavailable on this node"
	imageEditingGPURequiredMessage = "A CUDA-capable GPU is required for image editing."
)

func safeImageRuntimeError(message string) string {
	if message == imageEditingGPURequiredMessage {
		return message
	}
	return imageEditingUnavailableMessage
}

func NewHandler(cfg Config, store *Store) http.Handler {
	return newHandler(cfg, store, chooseStorageDirectory)
}

func newHandler(cfg Config, store *Store, picker func() (string, error)) http.Handler {
	cfg.OllamaURL = strings.TrimRight(strings.TrimSpace(cfg.OllamaURL), "/")
	cfg.DiffusersURL = strings.TrimRight(strings.TrimSpace(cfg.DiffusersURL), "/")
	if store == nil {
		store = NewStore(200)
	}
	h := &handler{cfg: cfg, store: store, httpClient: &http.Client{Timeout: 10 * time.Second}, pickStorage: picker, storageAvailable: availableStorageBytes}
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.index)
	mux.HandleFunc("/api/", h.api)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (h *handler) index(w http.ResponseWriter, _ *http.Request) {
	data, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "console assets unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (h *handler) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/overview":
		h.overview(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/update":
		h.startUpdate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/node":
		h.nodeProfile(w)
	case r.Method == http.MethodGet && r.URL.Path == "/api/requests":
		writeJSON(w, http.StatusOK, h.store.Requests())
	case r.Method == http.MethodGet && r.URL.Path == "/api/logs":
		writeJSON(w, http.StatusOK, h.store.Logs())
	case r.Method == http.MethodGet && r.URL.Path == "/api/settlements":
		writeJSON(w, http.StatusOK, h.store.Settlements())
	case r.Method == http.MethodGet && r.URL.Path == "/api/models":
		h.listModels(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/models/capabilities":
		h.modelCapabilities(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/runtime":
		h.runtime(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/image-runtime":
		h.imageRuntime(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/image-runtime/model":
		h.selectImageRuntimeModel(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/image/edit":
		h.imageEdit(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runtime/unload-all":
		h.unloadAllRuntimeModels(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runtime/unload":
		h.unloadRuntimeModel(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/storage":
		h.storageStatus(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/storage/plan":
		h.storageMigrationPlan(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/storage/migrate":
		h.startStorageMigration(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/storage/migrate":
		writeJSON(w, http.StatusOK, h.migrationSnapshot())
	case r.Method == http.MethodPost && r.URL.Path == "/api/storage/pick":
		h.storagePicker(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/playground/chat":
		h.playgroundChat(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/models/pull":
		h.startPull(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/models/benchmark":
		h.benchmarkModel(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/models":
		h.deleteModel(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/models/pull":
		writeJSON(w, http.StatusOK, h.pullSnapshot())
	case r.Method == http.MethodDelete && r.URL.Path == "/api/models/pull":
		h.cancelPull(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) nodeProfile(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, NodeProfile{
		Name:         h.cfg.NodeName,
		AgentVersion: h.cfg.AgentVersion,
		GPUModel:     h.cfg.GPUModel,
		Platform:     h.cfg.Platform,
		CountryISO2:  h.cfg.CountryISO2,
		VRAMTotalGB:  h.cfg.VRAMTotalGB,
	})
}

func (h *handler) modelCapabilities(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("name"))
	if !validModelName.MatchString(model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid model name is required"))
		return
	}
	capabilities, err := h.localModelCapabilities(r.Context(), model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, capabilities)
}

func (h *handler) localModelCapabilities(ctx context.Context, model string) (ModelCapabilities, error) {
	payload, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: model})
	if err != nil {
		return ModelCapabilities{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.OllamaURL+"/api/show", bytes.NewReader(payload))
	if err != nil {
		return ModelCapabilities{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.httpClient.Do(request)
	if err != nil {
		return ModelCapabilities{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelCapabilities{}, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var result ModelCapabilities
	if err := json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&result); err != nil {
		return ModelCapabilities{}, fmt.Errorf("decode local model capabilities: %w", err)
	}
	result.Model = model
	if result.Capabilities == nil {
		result.Capabilities = []string{}
	}
	return result, nil
}

func supportsCapability(capabilities ModelCapabilities, wanted string) bool {
	for _, capability := range capabilities.Capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func (h *handler) benchmarkModel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Model         string `json:"model"`
		ReleaseLoaded bool   `json:"release_loaded"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	if h.store.Overview().ActiveRequests > 0 {
		writeError(w, http.StatusConflict, errors.New("wait until current local requests finish before benchmarking a model"))
		return
	}
	resident, err := h.loadedRuntimeModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, model := range resident {
		if model.Name == input.Model && !input.ReleaseLoaded {
			writeError(w, http.StatusConflict, errors.New("release the resident model before running a quick benchmark"))
			return
		}
	}
	result, err := h.runModelBenchmark(r.Context(), input.Model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) runModelBenchmark(ctx context.Context, model string) (ModelBenchmark, error) {
	payload, err := json.Marshal(struct {
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		Stream    bool   `json:"stream"`
		KeepAlive int    `json:"keep_alive"`
		Options   struct {
			NumPredict int `json:"num_predict"`
		} `json:"options"`
	}{
		Model: model, Prompt: "Reply with OK.", Stream: false, KeepAlive: 0,
		Options: struct {
			NumPredict int `json:"num_predict"`
		}{NumPredict: 1},
	})
	if err != nil {
		return ModelBenchmark{}, err
	}
	benchmarkContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(benchmarkContext, http.MethodPost, h.cfg.OllamaURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return ModelBenchmark{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Do(request)
	if err != nil {
		return ModelBenchmark{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelBenchmark{}, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var output struct {
		EvalCount     int   `json:"eval_count"`
		EvalDuration  int64 `json:"eval_duration"`
		TotalDuration int64 `json:"total_duration"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 512<<10)).Decode(&output); err != nil {
		return ModelBenchmark{}, fmt.Errorf("decode local benchmark result: %w", err)
	}
	if output.EvalCount <= 0 || output.EvalDuration <= 0 {
		return ModelBenchmark{}, errors.New("local runtime returned no generation timing")
	}
	return ModelBenchmark{
		Model: model, EvalCount: output.EvalCount, EvalDurationNS: output.EvalDuration, TotalDurationNS: output.TotalDuration,
		TokensPerSecond: float64(output.EvalCount) * float64(time.Second) / float64(output.EvalDuration),
	}, nil
}

func (h *handler) imageRuntime(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "unavailable", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.cfg.DiffusersURL+"/health", nil)
	if err != nil {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "offline", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	response, err := h.httpClient.Do(request)
	if err != nil {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "offline", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "offline", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	var runtime ImageRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&runtime); err != nil {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "offline", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	if runtime.Status == "" {
		runtime.Status = "ready"
	}
	if runtime.Models == nil {
		runtime.Models = []string{}
	}
	if runtime.Error != "" {
		runtime.Error = safeImageRuntimeError(runtime.Error)
	}
	writeJSON(w, http.StatusOK, runtime)
}

func (h *handler) selectImageRuntimeModel(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New(imageEditingUnavailableMessage))
		return
	}
	var input struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid image model name is required"))
		return
	}
	payload, _ := json.Marshal(struct {
		Model string `json:"model"`
	}{Model: input.Model})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.cfg.DiffusersURL+"/v1/models/select", bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.httpClient.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	var runtime ImageRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&runtime); err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	if runtime.Status == "" {
		runtime.Status = "ready"
	}
	if runtime.Models == nil {
		runtime.Models = []string{}
	}
	writeJSON(w, http.StatusOK, runtime)
}

func (h *handler) imageEdit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New(imageEditingUnavailableMessage))
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		writeError(w, http.StatusBadRequest, errors.New("image edit requires multipart/form-data"))
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.cfg.DiffusersURL+"/v1/images/edits", io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	request.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	response, err := h.httpClient.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 16<<20))
}

func (h *handler) unloadRuntimeModel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	if err := h.unloadRuntimeModelByName(r.Context(), input.Model); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) unloadAllRuntimeModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.loadedRuntimeModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, model := range models {
		if err := h.unloadRuntimeModelByName(r.Context(), model.Name); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) unloadRuntimeModelByName(ctx context.Context, name string) error {
	payload, _ := json.Marshal(struct {
		Model     string `json:"model"`
		KeepAlive int    `json:"keep_alive"`
		Stream    bool   `json:"stream"`
	}{Model: name, KeepAlive: 0, Stream: false})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.OllamaURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("local runtime returned %s", response.Status)
	}
	return nil
}

func (h *handler) loadedRuntimeModels(ctx context.Context) ([]RuntimeModel, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.OllamaURL+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	response, err := h.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var payload struct {
		Models []RuntimeModel `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local runtime state: %w", err)
	}
	return payload.Models, nil
}

func (h *handler) playgroundChat(w http.ResponseWriter, r *http.Request) {
	var input playgroundInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 6<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("a valid model and chat messages are required"))
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if !validModelName.MatchString(input.Model) || len(input.Messages) == 0 || len(input.Messages) > 40 {
		writeError(w, http.StatusBadRequest, errors.New("a valid model and chat messages are required"))
		return
	}
	for _, message := range input.Messages {
		if (message.Role != "system" && message.Role != "user" && message.Role != "assistant") || (strings.TrimSpace(message.Content) == "" && len(message.Images) == 0) || len(message.Content) > 32<<10 || len(message.Images) > 4 {
			writeError(w, http.StatusBadRequest, errors.New("each chat message needs a role and non-empty content"))
			return
		}
		if len(message.Images) > 0 && message.Role != "user" {
			writeError(w, http.StatusBadRequest, errors.New("only user messages can include images"))
			return
		}
		for _, image := range message.Images {
			decoded, err := base64.StdEncoding.DecodeString(image)
			if err != nil || len(decoded) == 0 || len(decoded) > 4<<20 {
				writeError(w, http.StatusBadRequest, errors.New("each image must be a valid file up to 4 MB"))
				return
			}
		}
	}
	input.System = strings.TrimSpace(input.System)
	if len(input.System) > 8<<10 {
		writeError(w, http.StatusBadRequest, errors.New("system prompt is too long"))
		return
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		writeError(w, http.StatusBadRequest, errors.New("temperature must be between 0 and 2"))
		return
	}
	if input.System != "" {
		input.Messages = append([]PlaygroundMessage{{Role: "system", Content: input.System}}, input.Messages...)
	}
	canLoad, err := h.canStartPlaygroundModel(r.Context(), input.Model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !canLoad {
		writeError(w, http.StatusConflict, errors.New("not enough available local memory to load this model; unload another model first"))
		return
	}
	startedAt := time.Now().UTC()
	containsImages := false
	for _, message := range input.Messages {
		containsImages = containsImages || len(message.Images) > 0
	}
	playgroundPath := "/v1/chat/completions"
	if containsImages {
		playgroundPath = "/api/chat"
	}
	handle := h.store.Start(RequestStart{
		ID:        fmt.Sprintf("playground-%d", startedAt.UnixNano()),
		Model:     input.Model,
		Path:      playgroundPath,
		Consumer:  "local playground",
		StartedAt: startedAt,
	})
	finished := false
	finish := func(usage PlaygroundUsage, problem string) {
		if finished {
			return
		}
		finished = true
		h.store.Finish(handle, RequestFinish{
			CompletedAt:      time.Now().UTC(),
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			Duration:         time.Since(startedAt),
			Error:            problem,
		})
	}
	defer func() { finish(PlaygroundUsage{}, "local playground request did not complete") }()
	if containsImages {
		capabilities, err := h.localModelCapabilities(r.Context(), input.Model)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if !supportsCapability(capabilities, "vision") {
			writeError(w, http.StatusUnprocessableEntity, errors.New("the selected model does not support images"))
			return
		}
		h.playgroundImageChat(w, r, input, finish)
		return
	}

	var streamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	}
	if input.Stream {
		streamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	payload, err := json.Marshal(struct {
		Model         string              `json:"model"`
		Messages      []PlaygroundMessage `json:"messages"`
		Temperature   *float64            `json:"temperature,omitempty"`
		Stream        bool                `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options,omitempty"`
	}{Model: input.Model, Messages: input.Messages, Temperature: input.Temperature, Stream: input.Stream, StreamOptions: streamOptions})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.OllamaURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", response.Status))
		return
	}
	if input.Stream {
		usage, err := h.streamPlaygroundResponse(w, response.Body, input.Model)
		if err != nil {
			finish(PlaygroundUsage{}, err.Error())
			return
		}
		finish(usage, "")
		return
	}
	var output struct {
		Model   string `json:"model"`
		Choices []struct {
			Message PlaygroundMessage `json:"message"`
		} `json:"choices"`
		Usage PlaygroundUsage `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&output); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local runtime chat response: %w", err))
		return
	}
	if len(output.Choices) == 0 || output.Choices[0].Message.Content == "" {
		writeError(w, http.StatusBadGateway, errors.New("local runtime returned an empty chat response"))
		return
	}
	if output.Model == "" {
		output.Model = input.Model
	}
	finish(output.Usage, "")
	writeJSON(w, http.StatusOK, PlaygroundResponse{Model: output.Model, Content: output.Choices[0].Message.Content, Usage: output.Usage})
}

// canStartPlaygroundModel prevents local chat from bypassing the same memory
// budget shown in the model library. A loaded model is already accounted for;
// a cold model is conservatively estimated from its installed artifact size.
func (h *handler) canStartPlaygroundModel(ctx context.Context, model string) (bool, error) {
	if h.cfg.VRAMTotalGB <= 0 {
		return true, nil
	}
	loaded, err := h.loadedRuntimeModels(ctx)
	if err != nil {
		return false, err
	}
	var loadedBytes int64
	for _, resident := range loaded {
		if resident.Name == model {
			return true, nil
		}
		loadedBytes += resident.SizeVRAM
	}
	installed, err := h.localModels(ctx)
	if err != nil {
		return false, err
	}
	for _, candidate := range installed {
		if candidate.Name != model {
			continue
		}
		available := availableMemoryBytes(h.cfg.VRAMTotalGB, loadedBytes, memoryReserveBytes(h.cfg.VRAMTotalGB))
		return candidate.Size <= available, nil
	}
	// Let the runtime report an unknown model in its own normal response.
	return true, nil
}

func (h *handler) playgroundImageChat(w http.ResponseWriter, r *http.Request, input playgroundInput, finish func(PlaygroundUsage, string)) {
	options := struct {
		Temperature *float64 `json:"temperature,omitempty"`
	}{Temperature: input.Temperature}
	payload, err := json.Marshal(struct {
		Model    string              `json:"model"`
		Messages []PlaygroundMessage `json:"messages"`
		Stream   bool                `json:"stream"`
		Options  any                 `json:"options,omitempty"`
	}{Model: input.Model, Messages: input.Messages, Stream: input.Stream, Options: options})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.OllamaURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Minute}).Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", response.Status))
		return
	}
	if input.Stream {
		usage, err := h.streamImagePlaygroundResponse(w, response.Body, input.Model)
		if err != nil {
			finish(PlaygroundUsage{}, err.Error())
			return
		}
		finish(usage, "")
		return
	}
	var output struct {
		Model           string            `json:"model"`
		Message         PlaygroundMessage `json:"message"`
		PromptEvalCount int               `json:"prompt_eval_count"`
		EvalCount       int               `json:"eval_count"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&output); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local image chat response: %w", err))
		return
	}
	if output.Message.Content == "" {
		writeError(w, http.StatusBadGateway, errors.New("local runtime returned an empty chat response"))
		return
	}
	if output.Model == "" {
		output.Model = input.Model
	}
	usage := PlaygroundUsage{PromptTokens: output.PromptEvalCount, CompletionTokens: output.EvalCount, TotalTokens: output.PromptEvalCount + output.EvalCount}
	finish(usage, "")
	writeJSON(w, http.StatusOK, PlaygroundResponse{Model: output.Model, Content: output.Message.Content, Usage: usage})
}

func (h *handler) streamImagePlaygroundResponse(w http.ResponseWriter, body io.Reader, fallbackModel string) (PlaygroundUsage, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	model, usage := fallbackModel, PlaygroundUsage{}
	scanner := bufio.NewScanner(io.LimitReader(body, 16<<20))
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		var chunk struct {
			Model           string            `json:"model"`
			Message         PlaygroundMessage `json:"message"`
			Done            bool              `json:"done"`
			PromptEvalCount int               `json:"prompt_eval_count"`
			EvalCount       int               `json:"eval_count"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			problem := "decode local image chat stream: " + err.Error()
			writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
			return PlaygroundUsage{}, errors.New(problem)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Message.Content != "" {
			writePlaygroundSSE(w, playgroundStreamEvent{Type: "delta", Content: chunk.Message.Content})
		}
		if chunk.Done {
			usage = PlaygroundUsage{PromptTokens: chunk.PromptEvalCount, CompletionTokens: chunk.EvalCount, TotalTokens: chunk.PromptEvalCount + chunk.EvalCount}
		}
	}
	if err := scanner.Err(); err != nil {
		problem := "read local image chat stream: " + err.Error()
		writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
		return PlaygroundUsage{}, errors.New(problem)
	}
	writePlaygroundSSE(w, playgroundStreamEvent{Type: "done", Model: model, Usage: usage})
	return usage, nil
}

func (h *handler) streamPlaygroundResponse(w http.ResponseWriter, body io.Reader, fallbackModel string) (PlaygroundUsage, error) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	model, usage := fallbackModel, PlaygroundUsage{}
	scanner := bufio.NewScanner(io.LimitReader(body, 16<<20))
	scanner.Buffer(make([]byte, 32<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta PlaygroundMessage `json:"delta"`
			} `json:"choices"`
			Usage PlaygroundUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			problem := "decode local runtime chat stream: " + err.Error()
			writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
			return PlaygroundUsage{}, errors.New(problem)
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				writePlaygroundSSE(w, playgroundStreamEvent{Type: "delta", Content: choice.Delta.Content})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		problem := "read local runtime chat stream: " + err.Error()
		writePlaygroundSSE(w, playgroundStreamEvent{Type: "error", Error: problem})
		return PlaygroundUsage{}, errors.New(problem)
	}
	writePlaygroundSSE(w, playgroundStreamEvent{Type: "done", Model: model, Usage: usage})
	return usage, nil
}

func writePlaygroundSSE(w http.ResponseWriter, event playgroundStreamEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (h *handler) storagePicker(w http.ResponseWriter, _ *http.Request) {
	path, err := h.pickStorage()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Path string `json:"path"`
	}{Path: path})
}

func (h *handler) storageMigrationPlan(w http.ResponseWriter, r *http.Request) {
	source, destination, err := storageMigrationPaths(r, h.cfg.StoragePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, h.migrationPlan(source, destination))
}

func storageMigrationPaths(r *http.Request, defaultSource string) (string, string, error) {
	var input struct {
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil {
		return "", "", errors.New("a destination directory is required")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = defaultSource
	}
	if !filepath.IsAbs(source) {
		return "", "", errors.New("source must be an absolute directory")
	}
	destination := strings.TrimSpace(input.Destination)
	if !filepath.IsAbs(destination) {
		return "", "", errors.New("destination must be an absolute directory")
	}
	return filepath.Clean(source), filepath.Clean(destination), nil
}

func (h *handler) migrationPlan(sourcePath, destinationPath string) MigrationPlan {
	source := inspectStorage(sourcePath)
	destination := inspectStorage(destinationPath)
	plan := MigrationPlan{Source: source, Destination: destination, Blockers: []string{}}
	if !source.Accessible {
		plan.Blockers = append(plan.Blockers, "the current model directory is not accessible to the agent")
	}
	if !destination.Accessible {
		plan.Blockers = append(plan.Blockers, "the destination directory is not accessible to the agent")
	}
	if filepath.Clean(source.Path) == filepath.Clean(destination.Path) {
		plan.Blockers = append(plan.Blockers, "the destination must be different from the current model directory")
	}
	if isDescendantDirectory(source.Path, destination.Path) {
		plan.Blockers = append(plan.Blockers, "the destination must not be inside the source directory")
	}
	if destination.Accessible {
		entries, err := os.ReadDir(destination.Path)
		if err != nil {
			plan.Blockers = append(plan.Blockers, "the destination directory cannot be read by the agent")
		} else if len(entries) > 0 {
			plan.Blockers = append(plan.Blockers, "choose an empty destination directory to avoid overwriting existing files")
		}
	}
	h.mu.RLock()
	downloadActive := h.pull != nil
	h.mu.RUnlock()
	if downloadActive {
		plan.Blockers = append(plan.Blockers, "wait for the current model download to finish before copying files")
	}
	plan.Ready = len(plan.Blockers) == 0
	return plan
}

func isDescendantDirectory(parent, candidate string) bool {
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return false
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(resolvedParent, resolvedCandidate)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (h *handler) startStorageMigration(w http.ResponseWriter, r *http.Request) {
	source, destination, err := storageMigrationPaths(r, h.cfg.StoragePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	plan := h.migrationPlan(source, destination)
	if !plan.Ready {
		writeJSON(w, http.StatusConflict, plan)
		return
	}

	h.mu.Lock()
	if h.pull != nil {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("wait for the current model download to finish before copying files"))
		return
	}
	if h.migration != nil && !h.migration.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("a model migration is already running"))
		return
	}
	job := &migrationJob{Source: plan.Source.Path, Destination: plan.Destination.Path, Status: "copying", Total: plan.Source.UsedBytes}
	h.migration = job
	response := *job
	h.mu.Unlock()
	go h.runStorageMigration(job)
	writeJSON(w, http.StatusAccepted, response)
}

func (h *handler) migrationSnapshot() migrationJob {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.migration == nil {
		return migrationJob{Status: "idle", Done: true}
	}
	return *h.migration
}

func (h *handler) runStorageMigration(job *migrationJob) {
	err := copyStorage(job.Source, job.Destination, func(copied int64) {
		h.mu.Lock()
		if h.migration == job {
			h.migration.Completed = copied
		}
		h.mu.Unlock()
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.migration != job {
		return
	}
	job.Done = true
	if err != nil {
		job.Status, job.Error = "failed", err.Error()
		return
	}
	job.Status = "complete"
	job.Completed = job.Total
}

func copyStorage(source, destination string, report func(int64)) error {
	var copied int64
	return filepath.WalkDir(source, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destinationPath := filepath.Join(destination, relative)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(destinationPath, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symbolic link %q", relative)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := os.Open(sourcePath)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != info.Size() {
			return fmt.Errorf("copied %d of %d bytes for %q", written, info.Size(), relative)
		}
		copied += written
		report(copied)
		return nil
	})
}

func (h *handler) storageStatus(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	cached, fresh := h.storage, time.Since(h.storageAt) < 30*time.Second
	h.mu.RUnlock()
	if fresh {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	status := inspectStorage(h.cfg.StoragePath)
	h.mu.Lock()
	h.storage, h.storageAt = status, time.Now()
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, status)
}

func inspectStorage(path string) Storage {
	status := Storage{Path: path}
	if path == "" {
		status.Error = "local model storage path is not configured"
		return status
	}
	if _, err := os.Stat(path); err != nil {
		status.Error = err.Error()
		return status
	}
	var used int64
	if err := filepath.WalkDir(path, func(entryPath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			used += info.Size()
		}
		return nil
	}); err != nil {
		status.Error = err.Error()
		return status
	}
	total, available, err := storageCapacity(path)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Accessible, status.UsedBytes, status.TotalBytes, status.AvailableBytes = true, used, total, available
	return status
}

func (h *handler) runtime(w http.ResponseWriter, r *http.Request) {
	versionRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.cfg.OllamaURL+"/api/version", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	versionResponse, err := h.httpClient.Do(versionRequest)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer versionResponse.Body.Close()
	if versionResponse.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", versionResponse.Status))
		return
	}
	var result Runtime
	if err := json.NewDecoder(io.LimitReader(versionResponse.Body, 64<<10)).Decode(&result); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local runtime version: %w", err))
		return
	}

	psRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.cfg.OllamaURL+"/api/ps", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	psResponse, err := h.httpClient.Do(psRequest)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer psResponse.Body.Close()
	if psResponse.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", psResponse.Status))
		return
	}
	var payload struct {
		Models []RuntimeModel `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(psResponse.Body, 2<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local runtime state: %w", err))
		return
	}
	result.Models = payload.Models
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	overview := h.store.Overview()
	overview.VRAMTotalGB = h.cfg.VRAMTotalGB
	overview.AgentVersion = h.cfg.Version
	if overview.AgentVersion == "" {
		overview.AgentVersion = h.cfg.AgentVersion
	}
	h.mu.RLock()
	overview.UpdateState = h.update.State
	overview.UpdateVersion = h.update.Version
	overview.UpdateError = h.update.Error
	h.mu.RUnlock()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.cfg.OllamaURL+"/api/ps", nil)
	if err == nil {
		response, doErr := h.httpClient.Do(request)
		if doErr == nil {
			defer response.Body.Close()
			if response.StatusCode == http.StatusOK {
				var payload struct {
					Models []struct {
						SizeVRAM int64 `json:"size_vram"`
					} `json:"models"`
				}
				if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload) == nil {
					for _, model := range payload.Models {
						overview.LoadedVRAMBytes += model.SizeVRAM
					}
				}
			}
		}
	}
	overview.ReservedVRAMBytes = memoryReserveBytes(overview.VRAMTotalGB)
	overview.AvailableVRAMBytes = availableMemoryBytes(overview.VRAMTotalGB, overview.LoadedVRAMBytes, overview.ReservedVRAMBytes)
	writeJSON(w, http.StatusOK, overview)
}

func (h *handler) startUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Update == nil {
		writeError(w, http.StatusNotImplemented, errors.New("this agent does not support updates"))
		return
	}
	h.mu.Lock()
	if h.update.State == "checking" || h.update.State == "downloading" || h.update.State == "restarting" {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("update already in progress"))
		return
	}
	h.update = UpdateStatus{State: "checking"}
	h.mu.Unlock()
	go func() {
		err := h.cfg.Update(context.Background(), func(status UpdateStatus) {
			h.mu.Lock()
			h.update = status
			h.mu.Unlock()
		})
		if err != nil {
			h.mu.Lock()
			h.update = UpdateStatus{State: "failed", Error: err.Error()}
			h.mu.Unlock()
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

const gibibyte = int64(1024 * 1024 * 1024)

// memoryReserveBytes leaves room for the operating system and Ollama's KV
// cache. A model that merely fits the device will become unstable as its
// context grows, so this reservation is part of admission rather than a UI
// warning.
func memoryReserveBytes(totalGB int) int64 {
	if totalGB <= 0 {
		return 0
	}
	total := int64(totalGB) * gibibyte
	reserve := total / 5
	if reserve < 4*gibibyte {
		reserve = 4 * gibibyte
	}
	if reserve > total {
		return total
	}
	return reserve
}

func availableMemoryBytes(totalGB int, loadedBytes, reservedBytes int64) int64 {
	available := int64(totalGB)*gibibyte - loadedBytes - reservedBytes
	if available < 0 {
		return 0
	}
	return available
}

func (h *handler) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.localModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Models []Model `json:"models"`
	}{Models: models})
}

func (h *handler) localModels(ctx context.Context) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.OllamaURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	response, err := h.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var payload struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local models: %w", err)
	}
	if payload.Models == nil {
		payload.Models = []Model{}
	}
	return payload.Models, nil
}

func (h *handler) startPull(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	h.mu.Lock()
	if h.migration != nil && !h.migration.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("wait for the model migration to finish before downloading another model"))
		return
	}
	h.mu.Unlock()

	installed, err := h.localModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, model := range installed {
		if model.Name == input.Name {
			writeError(w, http.StatusConflict, errors.New("this model is already installed"))
			return
		}
	}
	h.mu.Lock()
	if h.migration != nil && !h.migration.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("wait for the model migration to finish before downloading another model"))
		return
	}
	if h.pull != nil && h.pull.Name == input.Name {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("this model download is already running"))
		return
	}
	for _, queued := range h.pullQueue {
		if queued.Name == input.Name {
			h.mu.Unlock()
			writeError(w, http.StatusConflict, errors.New("this model is already queued for download"))
			return
		}
	}
	job := &pullJob{Name: input.Name, Status: "queued"}
	startNow := h.pull == nil
	if startNow {
		h.pull = job
	} else {
		h.pullQueue = append(h.pullQueue, job)
	}
	response := *job
	h.mu.Unlock()
	if startNow {
		go h.runPull(job)
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *handler) pullSnapshot() pullQueueSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	snapshot := pullQueueSnapshot{Queued: make([]*pullJob, 0, len(h.pullQueue))}
	if h.pull != nil {
		active := *h.pull
		snapshot.Active = &active
	}
	if h.latestPull != nil {
		latest := *h.latestPull
		snapshot.Latest = &latest
	}
	for _, job := range h.pullQueue {
		copy := *job
		snapshot.Queued = append(snapshot.Queued, &copy)
	}
	return snapshot
}

func (h *handler) cancelPull(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validModelName.MatchString(name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pull != nil && h.pull.Name == name {
		h.pull.cancelled = true
		if h.pull.cancel != nil {
			h.pull.cancel()
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	for index, job := range h.pullQueue {
		if job.Name != name {
			continue
		}
		h.pullQueue = append(h.pullQueue[:index], h.pullQueue[index+1:]...)
		job.Done, job.Status, job.Error = true, "cancelled", ""
		latest := *job
		h.latestPull = &latest
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, errors.New("this model is not queued or downloading"))
}

func (h *handler) runPull(job *pullJob) {
	payload, _ := json.Marshal(struct {
		Name   string `json:"name"`
		Stream bool   `json:"stream"`
	}{Name: job.Name, Stream: true})
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	job.cancel = cancel
	if job.cancelled {
		cancel()
	}
	h.mu.Unlock()
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.OllamaURL+"/api/pull", bytes.NewReader(payload))
	if err == nil {
		request.Header.Set("Content-Type", "application/json")
		response, doErr := (&http.Client{Timeout: time.Hour}).Do(request)
		if doErr != nil {
			err = doErr
		} else {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("local runtime returned %s", response.Status)
			} else {
				scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
				for scanner.Scan() {
					var update pullJob
					if json.Unmarshal(scanner.Bytes(), &update) == nil {
						if storageErr := h.pullStorageError(update); storageErr != nil {
							err = storageErr
							cancel()
							break
						}
						h.mu.Lock()
						updatePullProgress(job, update, time.Now())
						h.mu.Unlock()
					}
				}
				if scanErr := scanner.Err(); scanErr != nil && err == nil {
					err = scanErr
				}
			}
		}
	}
	h.mu.Lock()
	job.Done = true
	job.cancel = nil
	if job.cancelled {
		job.Error = ""
		job.Status = "cancelled"
	} else if err != nil {
		job.Error = err.Error()
		job.Status = "failed"
	} else if job.Status == "" {
		job.Status = "success"
	}
	latest := *job
	h.latestPull = &latest
	var next *pullJob
	if h.pull == job {
		h.pull = nil
		if len(h.pullQueue) > 0 {
			next, h.pullQueue = h.pullQueue[0], h.pullQueue[1:]
			h.pull = next
		}
	}
	h.mu.Unlock()
	if next != nil {
		go h.runPull(next)
	}
}

func availableStorageBytes(path string) (int64, error) {
	_, available, err := storageCapacity(path)
	return available, err
}

// pullStorageError compares only the remaining bytes in the current layer with
// free space. Bytes already written must not be counted twice, and an
// unavailable capacity probe must not turn into a false rejection.
func (h *handler) pullStorageError(update pullJob) error {
	if update.Total <= update.Completed || h.cfg.StoragePath == "" {
		return nil
	}
	available := h.storageAvailable
	if available == nil {
		available = availableStorageBytes
	}
	free, err := available(h.cfg.StoragePath)
	if err != nil || update.Total-update.Completed <= free {
		return nil
	}
	return fmt.Errorf("not enough free disk space for this model download (%s remaining, %s available)", formatByteCount(update.Total-update.Completed), formatByteCount(free))
}

func formatByteCount(bytes int64) string {
	const gigabyte = 1024 * 1024 * 1024
	const megabyte = 1024 * 1024
	if bytes >= gigabyte {
		return fmt.Sprintf("%.1f GB", float64(bytes)/gigabyte)
	}
	return fmt.Sprintf("%d MB", bytes/megabyte)
}

// updatePullProgress turns the runtime's byte counters into a conservative
// transfer estimate. The counters can reset between layers, so only forward
// progress contributes to the rate and a reset clears the prior sample.
func updatePullProgress(job *pullJob, update pullJob, observedAt time.Time) {
	job.Status, job.Completed, job.Total = update.Status, update.Completed, update.Total
	if job.Total <= 0 || job.Completed < 0 {
		job.RateBytesPerSecond, job.SecondsRemaining = 0, 0
		job.sampledAt, job.sampledBytes = observedAt, job.Completed
		return
	}
	if job.sampledAt.IsZero() || job.Completed < job.sampledBytes {
		job.RateBytesPerSecond, job.SecondsRemaining = 0, 0
		job.sampledAt, job.sampledBytes = observedAt, job.Completed
		return
	}
	elapsed := observedAt.Sub(job.sampledAt).Seconds()
	if elapsed <= 0 || job.Completed == job.sampledBytes {
		return
	}
	rate := float64(job.Completed-job.sampledBytes) / elapsed
	// Pull status arrives per layer. An exponential moving average prevents a
	// tiny finishing layer from making the ETA jump wildly.
	if job.RateBytesPerSecond > 0 {
		rate = job.RateBytesPerSecond*0.65 + rate*0.35
	}
	job.RateBytesPerSecond = rate
	remaining := job.Total - job.Completed
	if remaining > 0 && rate > 0 {
		job.SecondsRemaining = int64(math.Ceil(float64(remaining) / rate))
	} else {
		job.SecondsRemaining = 0
	}
	job.sampledAt, job.sampledBytes = observedAt, job.Completed
}

func (h *handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validModelName.MatchString(name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	loaded, err := h.modelLoaded(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if loaded {
		writeError(w, http.StatusConflict, errors.New("unload the model before removing it"))
		return
	}
	if r.URL.Query().Get("confirm_unloaded") != "true" {
		writeError(w, http.StatusConflict, errors.New("refresh the runtime state and confirm removal before removing this model"))
		return
	}
	body, _ := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: name})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, h.cfg.OllamaURL+"/api/delete", bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.httpClient.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", response.Status))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) modelLoaded(ctx context.Context, name string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.OllamaURL+"/api/ps", nil)
	if err != nil {
		return false, err
	}
	response, err := h.httpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var payload struct {
		Models []RuntimeModel `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return false, fmt.Errorf("decode local runtime state: %w", err)
	}
	for _, model := range payload.Models {
		if model.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
