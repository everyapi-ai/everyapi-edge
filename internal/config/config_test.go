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
