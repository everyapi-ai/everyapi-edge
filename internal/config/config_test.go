package config

import (
	"strings"
	"testing"
)

func validBase() Config {
	return Config{
		GatewayURL:   "https://api.everyapi.ai",
		NodeID:       7,
		OllamaURL:    "http://ollama:11434",
		IdentityPath: "/var/lib/everyapi-edge/identity.json",
		ConsoleAddr:  "127.0.0.1:8421",
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

func TestValidateRejectsNegativeVRAM(t *testing.T) {
	cfg := validBase()
	cfg.VRAMTotalGB = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "EVERYAPI_VRAM_GB") {
		t.Fatalf("expected VRAM error, got %v", err)
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
