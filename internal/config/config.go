// Package config gathers the agent's startup configuration from
// environment variables. Env (not flags / config file) because the
// canonical packaging is docker-compose, where env vars are the
// supplier's only interface to the running container.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is what main.go assembles before constructing the client.
type Config struct {
	// GatewayURL — base URL of the EveryAPI gateway. Required.
	GatewayURL string
	// NodeID — the EdgeNode primary key the seller created via the
	// dashboard. Required, positive int.
	NodeID int64
	// RegistrationToken — the one-time secret the dashboard handed
	// over. Required on first run, OPTIONAL on subsequent runs
	// (the Ed25519 identity in IdentityPath takes over).
	RegistrationToken string
	// IdentityPath — where the Ed25519 keypair is persisted. The
	// docker-compose default mounts $HOST_VOL into the container at
	// this path so the key survives container restarts.
	IdentityPath string
	// OllamaURL — where to forward inbound buyer requests.
	OllamaURL string
	// NodeName / Hardware / Location — supplier-declared metadata
	// reported on every connect. Picked up from env so the
	// docker-compose .env is the single config seam.
	NodeName    string
	GPUModel    string
	VRAMTotalGB int
	CountryISO2 string
}

// Validate returns the first config defect, or nil if the agent
// can start. main.go calls this before doing anything expensive
// (keypair generation, network dials) so misconfiguration fails
// in <100ms.
func (c Config) Validate() error {
	if strings.TrimSpace(c.GatewayURL) == "" {
		return errors.New("EVERYAPI_GATEWAY is required (e.g. https://api.everyapi.ai)")
	}
	if c.NodeID <= 0 {
		return errors.New("EVERYAPI_NODE_ID is required and must be a positive integer")
	}
	if strings.TrimSpace(c.OllamaURL) == "" {
		return errors.New("OLLAMA_URL is required (e.g. http://ollama:11434)")
	}
	if c.IdentityPath == "" {
		return errors.New("EVERYAPI_IDENTITY_PATH must be set or the agent will not persist its keypair")
	}
	return nil
}

// FromEnv reads every recognised variable. Missing optional fields
// stay zero-valued; required-field defects surface from Validate().
func FromEnv() Config {
	return Config{
		GatewayURL:        os.Getenv("EVERYAPI_GATEWAY"),
		NodeID:            parseInt64(os.Getenv("EVERYAPI_NODE_ID")),
		RegistrationToken: strings.TrimSpace(os.Getenv("EVERYAPI_REGISTRATION_TOKEN")),
		IdentityPath:      defaultStr(os.Getenv("EVERYAPI_IDENTITY_PATH"), "/var/lib/everyapi-edge/identity.json"),
		OllamaURL:         defaultStr(os.Getenv("OLLAMA_URL"), "http://ollama:11434"),
		NodeName:          os.Getenv("EVERYAPI_NODE_NAME"),
		GPUModel:          os.Getenv("EVERYAPI_GPU_MODEL"),
		VRAMTotalGB:       int(parseInt64(os.Getenv("EVERYAPI_VRAM_GB"))),
		CountryISO2:       strings.ToUpper(os.Getenv("EVERYAPI_COUNTRY")),
	}
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

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// String renders a config for logging WITHOUT the registration token —
// printing that to logs is exactly the leak we hashed it for on the
// server side.
func (c Config) String() string {
	hadToken := "no"
	if c.RegistrationToken != "" {
		hadToken = "yes (length=" + strconv.Itoa(len(c.RegistrationToken)) + ")"
	}
	return fmt.Sprintf(
		"Config{Gateway=%s NodeID=%d Ollama=%s Identity=%s NodeName=%q Country=%s RegistrationToken=%s}",
		c.GatewayURL, c.NodeID, c.OllamaURL, c.IdentityPath, c.NodeName, c.CountryISO2, hadToken,
	)
}
