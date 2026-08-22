package console

import (
	"context"
	"embed"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	edgeruntime "github.com/everyapi-ai/everyapi-edge/internal/runtime"
)

//go:embed web/index.html
var webAssets embed.FS

// Config is local-only console configuration.
type Config struct {
	OllamaURL          string
	DiffusersURL       string
	SpeechURL          string
	ConsoleToken       string
	StoragePath        string
	VRAMTotalGB        int
	NodeName           string
	AgentVersion       string
	GPUModel           string
	Platform           string
	CountryISO2        string
	Version            string
	Update             func(context.Context, func(UpdateStatus)) error
	LoadUpdateSettings func() (UpdateSettings, error)
	SaveAutoUpdate     func(bool) (UpdateSettings, error)
	ModelsChanged      func()
}

type UpdateStatus struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type UpdateSettings struct {
	AutoUpdate         bool `json:"auto_update"`
	CheckIntervalHours int  `json:"check_interval_hours"`
}

var ErrUpdateInProgress = errors.New("update already in progress")

// NodeProfile is the startup identity of this local agent. It intentionally contains no credentials, gateway URL, or host filesystem details.
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

// Runtime is the live state reported by Ollama rather than the agent's historical request counters. It lets the local console distinguish a model that is merely installed from one consuming VRAM right now.
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

// ImageRuntime is the local Diffusers service capability. It is separate from Ollama because image generation/editing uses diffusion pipelines rather than Ollama's text and multimodal inference APIs.
type ImageRuntime struct {
	Status string   `json:"status"`
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

// Storage describes the model directory visible to this agent process. The path is supplied by the bundle rather than inferred from Ollama's HTTP API: Ollama deliberately does not disclose host filesystem paths over HTTP.
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

// migrationJob copies model files before the user repoints their local runtime. Copying is deliberately non-destructive: an interrupted transfer must leave the current model library intact and usable.
type migrationJob struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Status      string `json:"status"`
	Completed   int64  `json:"completed"`
	Total       int64  `json:"total"`
	Error       string `json:"error,omitempty"`
	Done        bool   `json:"done"`
}

// PlaygroundMessage is deliberately a small subset of OpenAI's chat message shape. The local console is for testing the installed model, not a generic proxy that accepts arbitrary Ollama options or tool calls.
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

// ModelBenchmark is a one-token local generation measurement. It reports the runtime's own token counters instead of guessing from model parameter size.
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
	cfg                  Config
	store                *Store
	httpClient           *http.Client
	textRuntime          *edgeruntime.TextClient
	textStreamingRuntime *edgeruntime.TextClient
	imageRuntimeClient   *edgeruntime.ImageClient
	speechRuntimeClient  *edgeruntime.SpeechClient
	mu                   sync.RWMutex
	pull                 *pullJob
	pullQueue            []*pullJob
	latestPull           *pullJob
	migration            *migrationJob
	storage              Storage
	storageAt            time.Time
	pickStorage          func() (string, error)
	storageAvailable     func(string) (int64, error)
	update               UpdateStatus
	updateStarting       bool
}

func (h *handler) textClient() *edgeruntime.TextClient {
	if h.textRuntime != nil {
		return h.textRuntime
	}
	return edgeruntime.NewTextClient(h.cfg.OllamaURL, h.httpClient)
}

func (h *handler) longTextClient() *edgeruntime.TextClient {
	if h.textStreamingRuntime != nil {
		return h.textStreamingRuntime
	}
	return edgeruntime.NewTextClient(h.cfg.OllamaURL, &http.Client{})
}

func (h *handler) imageClient() *edgeruntime.ImageClient {
	if h.imageRuntimeClient != nil {
		return h.imageRuntimeClient
	}
	return edgeruntime.NewImageClient(h.cfg.DiffusersURL, h.httpClient)
}

func (h *handler) speechClient() *edgeruntime.SpeechClient {
	if h.speechRuntimeClient != nil {
		return h.speechRuntimeClient
	}
	return edgeruntime.NewSpeechClient(h.cfg.SpeechURL, h.httpClient)
}

var validModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)

func NewHandler(cfg Config, store *Store) http.Handler {
	return NewHandlers(cfg, store).Browser
}

func newHandler(cfg Config, store *Store, picker func() (string, error)) http.Handler {
	return newHandlers(cfg, store, picker).Control
}

// Handlers separates the browser management surface from the in-process gateway control surface. Both share the same feature state, but only browser mutations require same-origin evidence.
type Handlers struct {
	Browser            http.Handler
	Control            http.Handler
	ReportUpdateStatus func(UpdateStatus)
}

func NewHandlers(cfg Config, store *Store) Handlers {
	return newHandlers(cfg, store, chooseStorageDirectory)
}

func newHandlers(cfg Config, store *Store, picker func() (string, error)) Handlers {
	cfg.OllamaURL = strings.TrimRight(strings.TrimSpace(cfg.OllamaURL), "/")
	cfg.DiffusersURL = strings.TrimRight(strings.TrimSpace(cfg.DiffusersURL), "/")
	cfg.SpeechURL = strings.TrimRight(strings.TrimSpace(cfg.SpeechURL), "/")
	if store == nil {
		store = NewStore(200)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	h := &handler{
		cfg:                  cfg,
		store:                store,
		httpClient:           httpClient,
		textRuntime:          edgeruntime.NewTextClient(cfg.OllamaURL, httpClient),
		textStreamingRuntime: edgeruntime.NewTextClient(cfg.OllamaURL, &http.Client{}),
		imageRuntimeClient:   edgeruntime.NewImageClient(cfg.DiffusersURL, httpClient),
		speechRuntimeClient:  edgeruntime.NewSpeechClient(cfg.SpeechURL, httpClient),
		pickStorage:          picker,
		storageAvailable:     availableStorageBytes,
	}
	browser := http.NewServeMux()
	browser.HandleFunc("/", h.index)
	authenticator := newSessionAuthenticator(cfg.ConsoleToken)
	browser.Handle("/api/session", sameOriginMutations(http.HandlerFunc(authenticator.session)))
	browser.Handle("/api/", sameOriginMutations(authenticator.require(http.HandlerFunc(h.api))))
	control := http.NewServeMux()
	control.HandleFunc("/api/", h.api)
	return Handlers{
		Browser:            securityHeaders(browser),
		Control:            securityHeaders(control),
		ReportUpdateStatus: h.reportUpdateStatus,
	}
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
