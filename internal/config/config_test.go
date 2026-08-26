package config

import (
	"strings"
	"testing"
)

func validBase() Config {
	return Config{
		GatewayURL:            "https://api.everyapi.ai",
		NodeID:                7,
		OllamaURL:             "http://ollama:11434",
		IdentityPath:          "/var/lib/everyapi-edge/identity.json",
		ConsoleAddr:           "127.0.0.1:8421",
		ResourcePolicy:        defaultResourcePolicy(),
		MaxConcurrentRequests: 4,
	}
}

func TestParseWorkloads(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"chat", []string{"chat"}},
		{"Chat, CODING ,image", []string{"chat", "coding", "image"}}, // case/space tolerant
		{"chat,,coding", []string{"chat", "coding"}},                 // empty segments dropped
	}
	for _, c := range cases {
		got := parseWorkloads(c.raw)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("parseWorkloads(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestFromEnvReadsOptionalDiffusersRuntimeURL(t *testing.T) {
	t.Setenv("EVERYAPI_DIFFUSERS_URL", "http://diffusers:8188")
	if got := FromEnv().DiffusersURL; got != "http://diffusers:8188" {
		t.Fatalf("DiffusersURL = %q", got)
	}
}

func TestDefaultModelStorageUsesEveryAPIHomeDirectory(t *testing.T) {
	t.Setenv("EVERYAPI_OLLAMA_STORAGE_PATH", "")
	t.Setenv("OLLAMA_MODELS", "")
	t.Setenv("HOME", "/tmp/everyapi-edge-home")
	if got, want := defaultOllamaStoragePath(), "/tmp/everyapi-edge-home/.everyapi/edge"; got != want {
		t.Fatalf("default model storage = %q, want %q", got, want)
	}
}

func TestValidateAcceptsKnownWorkloads(t *testing.T) {
	cfg := validBase()
	cfg.Workloads = []string{"chat", "render", "embedding"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("known workloads should validate, got: %v", err)
	}
}

func TestValidateRejectsUnknownWorkload(t *testing.T) {
	cfg := validBase()
	cfg.Workloads = []string{"chat", "mining"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("unknown workload should fail Validate")
	}
	if !strings.Contains(err.Error(), "mining") {
		t.Errorf("error should name the offending value, got: %v", err)
	}
	if !strings.Contains(err.Error(), "EVERYAPI_WORKLOADS") {
		t.Errorf("error should name the env var, got: %v", err)
	}
}

func TestValidateRejectsInvalidConsoleAddress(t *testing.T) {
	cfg := validBase()
	cfg.ConsoleAddr = "not an address"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "EVERYAPI_CONSOLE_ADDR") {
		t.Fatalf("expected console address error, got %v", err)
	}
}

func TestValidateAllowsLoopbackConsoleWithoutToken(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8421", "[::1]:8421", "localhost:8421"} {
		cfg := validBase()
		cfg.ConsoleAddr = address
		if err := cfg.Validate(); err != nil {
			t.Fatalf("loopback console %q should not require a pairing token: %v", address, err)
		}
	}
}

func TestValidateRequiresTokenForLANConsole(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8421", ":8421", "192.168.1.8:8421", "edge-box.local:8421"} {
		cfg := validBase()
		cfg.ConsoleAddr = address
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "EVERYAPI_CONSOLE_TOKEN") {
			t.Fatalf("LAN console %q should require a pairing token, got %v", address, err)
		}
	}
}

func TestValidateRejectsShortLANConsoleToken(t *testing.T) {
	cfg := validBase()
	cfg.ConsoleAddr = "0.0.0.0:8421"
	cfg.ConsoleToken = "too-short"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "at least 32 characters") {
		t.Fatalf("short pairing token should fail validation, got %v", err)
	}
}

func TestFromEnvReadsConsoleTokenWithoutLoggingIt(t *testing.T) {
	token := strings.Repeat("a1", 32)
	t.Setenv("EVERYAPI_CONSOLE_TOKEN", token)
	cfg := FromEnv()
	if cfg.ConsoleToken != token {
		t.Fatalf("ConsoleToken = %q", cfg.ConsoleToken)
	}
	if strings.Contains(cfg.String(), token) {
		t.Fatal("Config.String exposed the console pairing token")
	}
}

// The console's Host allowlist is what a supplier reaches for after a wildcard bind stops answering to their LAN
// name, so the env var has to survive the shapes people actually paste: extra spaces, mixed case, and a port
// copied out of the browser's address bar.
func TestFromEnvNormalizesConsoleAllowedHosts(t *testing.T) {
	t.Setenv("EVERYAPI_CONSOLE_ALLOWED_HOSTS", " Studio.Local:8421 , edge.internal ,, [fd00::1]:8421 ")
	cfg := FromEnv()
	want := []string{"studio.local", "edge.internal", "fd00::1"}
	if len(cfg.ConsoleAllowedHosts) != len(want) {
		t.Fatalf("ConsoleAllowedHosts = %#v", cfg.ConsoleAllowedHosts)
	}
	for i, host := range want {
		if cfg.ConsoleAllowedHosts[i] != host {
			t.Fatalf("ConsoleAllowedHosts = %#v, want %#v", cfg.ConsoleAllowedHosts, want)
		}
	}

	t.Setenv("EVERYAPI_CONSOLE_ALLOWED_HOSTS", "")
	if hosts := FromEnv().ConsoleAllowedHosts; hosts != nil {
		t.Fatalf("unset allowlist = %#v, want nil", hosts)
	}
}

func TestValidateRejectsNegativeVRAM(t *testing.T) {
	cfg := validBase()
	cfg.VRAMTotalGB = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "EVERYAPI_VRAM_GB") {
		t.Fatalf("expected VRAM error, got %v", err)
	}
}

func TestFromEnvReadsBoundedRequestConcurrency(t *testing.T) {
	t.Setenv("EVERYAPI_MAX_CONCURRENT_REQUESTS", "4")
	if got := FromEnv().MaxConcurrentRequests; got != 4 {
		t.Fatalf("MaxConcurrentRequests = %d, want 4", got)
	}
}

func TestFromEnvUsesGlobalConcurrencyAsPerRuntimeFallback(t *testing.T) {
	t.Setenv("EVERYAPI_MAX_CONCURRENT_REQUESTS", "8")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_TEXT", "6")
	policy := FromEnv().ResourcePolicy
	if policy.Text.MaxConcurrent != 6 {
		t.Fatalf("text concurrency = %d, want explicit override 6", policy.Text.MaxConcurrent)
	}
	for name, got := range map[string]int{"image": policy.Image.MaxConcurrent, "speech": policy.Speech.MaxConcurrent, "video": policy.Video.MaxConcurrent, "render": policy.Render.MaxConcurrent, "rerank": policy.Rerank.MaxConcurrent} {
		if got != 8 {
			t.Fatalf("%s concurrency = %d, want global fallback 8", name, got)
		}
	}
}

func TestValidateRejectsInvalidRequestConcurrency(t *testing.T) {
	cfg := validBase()
	cfg.MaxConcurrentRequests = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "EVERYAPI_MAX_CONCURRENT_REQUESTS") {
		t.Fatalf("expected concurrency error, got %v", err)
	}
}

func TestFromEnvRejectsMalformedRequestConcurrency(t *testing.T) {
	t.Setenv("EVERYAPI_LOCAL_PREVIEW", "true")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_REQUESTS", "many")
	if err := FromEnv().Validate(); err == nil || !strings.Contains(err.Error(), "EVERYAPI_MAX_CONCURRENT_REQUESTS") {
		t.Fatalf("expected malformed concurrency error, got %v", err)
	}
}

func TestValidateAllowsLocalPreviewWithoutGatewayCredentials(t *testing.T) {
	cfg := validBase()
	cfg.LocalPreview = true
	cfg.GatewayURL = ""
	cfg.NodeID = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("local preview should not need gateway credentials, got: %v", err)
	}
}

func TestFromEnvBuildsPerRuntimeResourcePolicy(t *testing.T) {
	t.Setenv("EVERYAPI_MAX_CONCURRENT_TEXT", "6")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_IMAGE", "1")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_SPEECH", "3")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_VIDEO", "1")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_RENDER", "1")
	t.Setenv("EVERYAPI_MAX_CONCURRENT_RERANK", "4")
	t.Setenv("EVERYAPI_RESERVE_VRAM_MB_TEXT", "1024")
	t.Setenv("EVERYAPI_RESERVE_VRAM_MB_IMAGE", "4096")
	t.Setenv("EVERYAPI_RESERVE_VRAM_MB_SPEECH", "512")
	t.Setenv("EVERYAPI_RESERVE_VRAM_MB_VIDEO", "8192")
	t.Setenv("EVERYAPI_RESERVE_VRAM_MB_RENDER", "8192")
	t.Setenv("EVERYAPI_RESERVE_VRAM_MB_RERANK", "2048")

	policy := FromEnv().ResourcePolicy
	if policy.Text.MaxConcurrent != 6 || policy.Text.ReserveVRAMMB != 1024 {
		t.Fatalf("text policy = %#v", policy.Text)
	}
	if policy.Image.MaxConcurrent != 1 || policy.Image.ReserveVRAMMB != 4096 {
		t.Fatalf("image policy = %#v", policy.Image)
	}
	if policy.Speech.MaxConcurrent != 3 || policy.Speech.ReserveVRAMMB != 512 {
		t.Fatalf("speech policy = %#v", policy.Speech)
	}
	if policy.Video.MaxConcurrent != 1 || policy.Video.ReserveVRAMMB != 8192 {
		t.Fatalf("video policy = %#v", policy.Video)
	}
	if policy.Render.MaxConcurrent != 1 || policy.Render.ReserveVRAMMB != 8192 {
		t.Fatalf("render policy = %#v", policy.Render)
	}
	if policy.Rerank.MaxConcurrent != 4 || policy.Rerank.ReserveVRAMMB != 2048 {
		t.Fatalf("rerank policy = %#v", policy.Rerank)
	}
}

func TestFromEnvUsesSafeResourcePolicyDefaults(t *testing.T) {
	policy := FromEnv().ResourcePolicy
	if policy.Text.MaxConcurrent != 4 || policy.Image.MaxConcurrent != 1 || policy.Speech.MaxConcurrent != 2 || policy.Video.MaxConcurrent != 1 || policy.Render.MaxConcurrent != 1 || policy.Rerank.MaxConcurrent != 2 {
		t.Fatalf("unexpected defaults: %#v", policy)
	}
}

func TestValidateRejectsInvalidResourcePolicy(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "zero concurrency", mutate: func(cfg *Config) { cfg.ResourcePolicy.Image.MaxConcurrent = 0 }, want: "image max concurrency"},
		{name: "excess concurrency", mutate: func(cfg *Config) { cfg.ResourcePolicy.Text.MaxConcurrent = 65 }, want: "text max concurrency"},
		{name: "negative reserve", mutate: func(cfg *Config) { cfg.ResourcePolicy.Speech.ReserveVRAMMB = -1 }, want: "speech VRAM reserve"},
		{name: "reserve larger than device", mutate: func(cfg *Config) { cfg.VRAMTotalGB = 8; cfg.ResourcePolicy.Video.ReserveVRAMMB = 9 * 1024 }, want: "video VRAM reserve"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBase()
			cfg.ResourcePolicy = defaultResourcePolicy()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}
