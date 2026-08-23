// Package config gathers the agent's startup configuration from environment variables. Env (not flags / config file) because the canonical packaging is docker-compose, where env vars are the supplier's only interface to the running container.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// Config is what main.go assembles before constructing the client.
type Config struct {
	// LocalPreview runs the local control room without registering or reconnecting to a gateway. It exists for LAN UI evaluation only.
	LocalPreview bool
	// GatewayURL — base URL of the EveryAPI gateway. Required.
	GatewayURL string
	// NodeID — the EdgeNode primary key the seller created via the dashboard. Required, positive int.
	NodeID int64
	// RegistrationToken — the one-time secret the dashboard handed over. Required on first run, OPTIONAL on subsequent runs (the Ed25519 identity in IdentityPath takes over).
	RegistrationToken string
	// IdentityPath — where the Ed25519 keypair is persisted. The docker-compose default mounts $HOST_VOL into the container at this path so the key survives container restarts.
	IdentityPath string
	// OllamaURL — where to forward inbound buyer requests.
	OllamaURL string
	// DiffusersURL is an optional local image-generation/editing runtime. It is intentionally separate from Ollama because diffusion pipelines use a different model format and API surface.
	DiffusersURL string
	// SpeechURL is an optional local text-to-speech runtime, separate again because Kokoro synthesis returns audio bytes rather than the token stream Ollama produces.
	SpeechURL string
	// TranscriptionURL is an optional local speech-to-text runtime serving transcription and translation independently from synthesis.
	TranscriptionURL string
	VideoURL         string
	RenderURL        string
	RerankURL        string
	// NodeName / Hardware / Location — supplier-declared metadata reported on every connect. Picked up from env so the docker-compose .env is the single config seam.
	NodeName    string
	GPUModel    string
	VRAMTotalGB int
	CountryISO2 string
	// ResourcePolicy controls admission independently for each local runtime. Separate limits prevent a long image or video job from consuming the text pool and let the operator reserve enough VRAM for the selected workload before a local request starts.
	ResourcePolicy protocol.ResourcePolicy
	// Workloads — capability declaration (EVERYAPI_WORKLOADS, comma- separated; see protocol.KnownWorkloads). Optional: the gateway only uses this to backfill nodes whose seller never declared workloads in the dashboard — the dashboard value wins otherwise.
	Workloads []string
	// ConsoleAddr is the local HTTP listener for the embedded supplier console. A direct binary defaults to loopback; Compose deliberately overrides it to 0.0.0.0 inside the container while Compose publishes the port on the supplier's trusted LAN. A direct binary remains loopback-only by default.
	ConsoleAddr string
	// ConsoleToken pairs a browser with a non-loopback Control Room. Installers generate and persist it; direct loopback-only binaries may leave it empty.
	ConsoleToken string
	// OllamaStoragePath is the model root visible to the agent process. It is used by the local console for storage inspection and migration planning.
	OllamaStoragePath string
	// MaxConcurrentRequests bounds accepted gateway work before the agent reports node_busy. GPU-backed requests are serialized separately, so this is the total queue plus CPU TTS capacity.
	MaxConcurrentRequests int
}

// Validate returns the first config defect, or nil if the agent can start. main.go calls this before doing anything expensive (keypair generation, network dials) so misconfiguration fails in <100ms.
func (c Config) Validate() error {
	if !c.LocalPreview {
		if strings.TrimSpace(c.GatewayURL) == "" {
			return errors.New("EVERYAPI_GATEWAY is required (e.g. https://api.everyapi.ai)")
		}
		if c.NodeID <= 0 {
			return errors.New("EVERYAPI_NODE_ID is required and must be a positive integer")
		}
	}
	if strings.TrimSpace(c.OllamaURL) == "" {
		return errors.New("OLLAMA_URL is required (e.g. http://ollama:11434)")
	}
	if c.IdentityPath == "" {
		return errors.New("EVERYAPI_IDENTITY_PATH must be set or the agent will not persist its keypair")
	}
	host, _, err := net.SplitHostPort(c.ConsoleAddr)
	if err != nil {
		return fmt.Errorf("EVERYAPI_CONSOLE_ADDR must be host:port: %w", err)
	}
	consoleToken := strings.TrimSpace(c.ConsoleToken)
	if consoleToken != "" && len(consoleToken) < 32 {
		return errors.New("EVERYAPI_CONSOLE_TOKEN must be at least 32 characters")
	}
	if !isLoopbackConsoleHost(host) && consoleToken == "" {
		return errors.New("EVERYAPI_CONSOLE_TOKEN is required when EVERYAPI_CONSOLE_ADDR is not loopback")
	}
	if c.VRAMTotalGB < 0 {
		return errors.New("EVERYAPI_VRAM_GB must not be negative")
	}
	if err := validateResourcePolicy(c.ResourcePolicy, c.VRAMTotalGB); err != nil {
		return err
	}
	if c.MaxConcurrentRequests <= 0 || c.MaxConcurrentRequests > 64 {
		return errors.New("EVERYAPI_MAX_CONCURRENT_REQUESTS must be between 1 and 64")
	}
	for _, w := range c.Workloads {
		if !knownWorkload(w) {
			return fmt.Errorf("EVERYAPI_WORKLOADS contains unknown value %q (allowed: %s)",
				w, strings.Join(protocol.KnownWorkloads, ", "))
		}
	}
	return nil
}

func validateResourcePolicy(policy protocol.ResourcePolicy, totalVRAMGB int) error {
	policies := []struct {
		name   string
		policy protocol.RuntimeResourcePolicy
	}{
		{name: "text", policy: policy.Text},
		{name: "image", policy: policy.Image},
		{name: "speech", policy: policy.Speech},
		{name: "video", policy: policy.Video},
		{name: "render", policy: policy.Render},
		{name: "rerank", policy: policy.Rerank},
	}
	for _, item := range policies {
		if item.policy.MaxConcurrent < 1 || item.policy.MaxConcurrent > 64 {
			return fmt.Errorf("%s max concurrency must be between 1 and 64", item.name)
		}
		if item.policy.ReserveVRAMMB < 0 {
			return fmt.Errorf("%s VRAM reserve must not be negative", item.name)
		}
		if totalVRAMGB > 0 && item.policy.ReserveVRAMMB > int64(totalVRAMGB)*1024 {
			return fmt.Errorf("%s VRAM reserve exceeds the detected device total", item.name)
		}
	}
	return nil
}

func defaultResourcePolicy() protocol.ResourcePolicy {
	return protocol.ResourcePolicy{
		Text:   protocol.RuntimeResourcePolicy{MaxConcurrent: 4},
		Image:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Speech: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
		Video:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Render: protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Rerank: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
	}
}

func resourcePolicyFromEnv() protocol.ResourcePolicy {
	policy := defaultResourcePolicy()
	globalRaw := os.Getenv("EVERYAPI_MAX_CONCURRENT_REQUESTS")
	if strings.TrimSpace(globalRaw) != "" {
		if global := parseConcurrentRequests(globalRaw); global > 0 {
			policy.Text.MaxConcurrent = global
			policy.Image.MaxConcurrent = global
			policy.Speech.MaxConcurrent = global
			policy.Video.MaxConcurrent = global
			policy.Render.MaxConcurrent = global
			policy.Rerank.MaxConcurrent = global
		}
	}
	policy.Text = resourceRuntimePolicyFromEnv("TEXT", policy.Text.MaxConcurrent)
	policy.Image = resourceRuntimePolicyFromEnv("IMAGE", policy.Image.MaxConcurrent)
	policy.Speech = resourceRuntimePolicyFromEnv("SPEECH", policy.Speech.MaxConcurrent)
	policy.Video = resourceRuntimePolicyFromEnv("VIDEO", policy.Video.MaxConcurrent)
	policy.Render = resourceRuntimePolicyFromEnv("RENDER", policy.Render.MaxConcurrent)
	policy.Rerank = resourceRuntimePolicyFromEnv("RERANK", policy.Rerank.MaxConcurrent)
	return policy
}

func resourceRuntimePolicyFromEnv(name string, defaultMax int) protocol.RuntimeResourcePolicy {
	return protocol.RuntimeResourcePolicy{
		MaxConcurrent: int(parseInt64(defaultStr(os.Getenv("EVERYAPI_MAX_CONCURRENT_"+name), strconv.Itoa(defaultMax)))),
		ReserveVRAMMB: parseInt64(os.Getenv("EVERYAPI_RESERVE_VRAM_MB_" + name)),
	}
}

func knownWorkload(w string) bool {
	for _, k := range protocol.KnownWorkloads {
		if w == k {
			return true
		}
	}
	return false
}

// FromEnv reads every recognised variable. Missing optional fields stay zero-valued; required-field defects surface from Validate().
func FromEnv() Config {
	return Config{
		LocalPreview:          parseBool(os.Getenv("EVERYAPI_LOCAL_PREVIEW")),
		GatewayURL:            os.Getenv("EVERYAPI_GATEWAY"),
		NodeID:                parseInt64(os.Getenv("EVERYAPI_NODE_ID")),
		RegistrationToken:     strings.TrimSpace(os.Getenv("EVERYAPI_REGISTRATION_TOKEN")),
		IdentityPath:          defaultStr(os.Getenv("EVERYAPI_IDENTITY_PATH"), "/var/lib/everyapi-edge/identity.json"),
		OllamaURL:             defaultStr(os.Getenv("OLLAMA_URL"), "http://ollama:11434"),
		DiffusersURL:          strings.TrimSpace(os.Getenv("EVERYAPI_DIFFUSERS_URL")),
		SpeechURL:             strings.TrimSpace(os.Getenv("EVERYAPI_SPEECH_URL")),
		TranscriptionURL:      strings.TrimSpace(os.Getenv("EVERYAPI_TRANSCRIPTION_URL")),
		VideoURL:              strings.TrimSpace(os.Getenv("EVERYAPI_VIDEO_URL")),
		RenderURL:             strings.TrimSpace(os.Getenv("EVERYAPI_RENDER_URL")),
		RerankURL:             strings.TrimSpace(os.Getenv("EVERYAPI_RERANK_URL")),
		NodeName:              os.Getenv("EVERYAPI_NODE_NAME"),
		GPUModel:              os.Getenv("EVERYAPI_GPU_MODEL"),
		VRAMTotalGB:           int(parseInt64(os.Getenv("EVERYAPI_VRAM_GB"))),
		CountryISO2:           strings.ToUpper(os.Getenv("EVERYAPI_COUNTRY")),
		ResourcePolicy:        resourcePolicyFromEnv(),
		Workloads:             parseWorkloads(os.Getenv("EVERYAPI_WORKLOADS")),
		ConsoleAddr:           defaultStr(os.Getenv("EVERYAPI_CONSOLE_ADDR"), "127.0.0.1:8421"),
		ConsoleToken:          strings.TrimSpace(os.Getenv("EVERYAPI_CONSOLE_TOKEN")),
		OllamaStoragePath:     defaultOllamaStoragePath(),
		MaxConcurrentRequests: parseConcurrentRequests(os.Getenv("EVERYAPI_MAX_CONCURRENT_REQUESTS")),
	}
}

func isLoopbackConsoleHost(host string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSpace(host), ".")
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

func parseBool(raw string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && value
}

func defaultOllamaStoragePath() string {
	if configured := strings.TrimSpace(os.Getenv("EVERYAPI_OLLAMA_STORAGE_PATH")); configured != "" {
		return configured
	}
	if configured := strings.TrimSpace(os.Getenv("OLLAMA_MODELS")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".everyapi", "edge")
}

// parseWorkloads splits the comma-separated declaration, trimming whitespace and lowercasing so ".env hand-edits" like "Chat, CODING" still parse. Unknown values survive parsing and fail Validate() — dropping them here would hide the typo from the supplier.
func parseWorkloads(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if w := strings.ToLower(strings.TrimSpace(part)); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseConcurrentRequests(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 4
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return -1
	}
	return value
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// String renders a config for logging WITHOUT the registration token — printing that to logs is exactly the leak we hashed it for on the server side.
func (c Config) String() string {
	hadToken := "no"
	if c.RegistrationToken != "" {
		hadToken = "yes (length=" + strconv.Itoa(len(c.RegistrationToken)) + ")"
	}
	return fmt.Sprintf(
		"Config{LocalPreview=%t Gateway=%s NodeID=%d Ollama=%s OllamaStorage=%s Identity=%s ConsoleAddr=%s ConsoleToken=%t MaxConcurrentRequests=%d NodeName=%q Country=%s RegistrationToken=%s}",
		c.LocalPreview, c.GatewayURL, c.NodeID, c.OllamaURL, c.OllamaStoragePath, c.IdentityPath, c.ConsoleAddr, c.ConsoleToken != "", c.MaxConcurrentRequests, c.NodeName, c.CountryISO2, hadToken,
	)
}
