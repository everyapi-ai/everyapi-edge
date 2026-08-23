package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type Status string

const (
	StatusReady       Status = "ready"
	StatusStarting    Status = "starting"
	StatusWarming     Status = "warming"
	StatusDegraded    Status = "degraded"
	StatusUnavailable Status = "unavailable"
	StatusUnsupported Status = "unsupported"
)

type RuntimeLimits struct {
	MaxInputBytes      int64    `json:"max_input_bytes,omitempty"`
	MaxInputCharacters int      `json:"max_input_characters,omitempty"`
	Formats            []string `json:"formats,omitempty"`
	Voices             []string `json:"voices,omitempty"`
	Languages          []string `json:"languages,omitempty"`
}

type RuntimeCapability struct {
	ID     string        `json:"id"`
	Status Status        `json:"status"`
	Models []string      `json:"models,omitempty"`
	Paths  []string      `json:"paths,omitempty"`
	Reason string        `json:"reason,omitempty"`
	Limits RuntimeLimits `json:"limits,omitempty"`
}

// RuntimeHealth is the discovery contract every local runtime serves on /health. The agent reports the union of the ready model lists to the gateway before each handshake.
type RuntimeHealth struct {
	Status       Status
	Models       []string
	Error        string
	Version      string
	Backend      string
	Device       string
	VRAMBytes    int64
	Capabilities []RuntimeCapability
}

// ImageHealth is the image runtime's name for that shared contract, kept so the console and discovery call sites read in terms of the runtime they query.
type ImageHealth = RuntimeHealth

type ImageClient struct {
	target *Target
}

func NewImageClient(baseURL string, client HTTPDoer) *ImageClient {
	return &ImageClient{target: newTarget(KindImage, baseURL, client)}
}

func (c *ImageClient) Do(ctx context.Context, method, path string, headers http.Header, body io.Reader) (*http.Response, error) {
	return c.target.Do(ctx, method, path, headers, body)
}

func (c *ImageClient) Health(ctx context.Context) (ImageHealth, error) {
	return fetchHealth(ctx, c.target)
}

func fetchHealth(ctx context.Context, target *Target) (RuntimeHealth, error) {
	response, err := target.Do(ctx, http.MethodGet, "/health", nil, nil)
	if err != nil {
		return RuntimeHealth{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		if err := checkResponse(response); err != nil {
			return RuntimeHealth{}, err
		}
	}

	var payload struct {
		Status       Status              `json:"status"`
		Models       []string            `json:"models"`
		Error        string              `json:"error"`
		Version      string              `json:"version"`
		Backend      string              `json:"backend"`
		Device       string              `json:"device"`
		VRAMBytes    int64               `json:"vram_bytes"`
		Capabilities []RuntimeCapability `json:"capabilities"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&payload); err != nil {
		return RuntimeHealth{}, fmt.Errorf("decode local %s runtime health: %w", target.kind, err)
	}
	if payload.Status == "" {
		payload.Status = StatusUnavailable
	}
	if response.StatusCode == http.StatusServiceUnavailable && payload.Status == StatusReady {
		payload.Status = StatusUnavailable
	}
	models := normalizedStrings(payload.Models)
	for i := range payload.Capabilities {
		payload.Capabilities[i].Models = normalizedStrings(payload.Capabilities[i].Models)
		payload.Capabilities[i].Paths = normalizedStrings(payload.Capabilities[i].Paths)
		payload.Capabilities[i].Limits.Formats = normalizedStrings(payload.Capabilities[i].Limits.Formats)
		payload.Capabilities[i].Limits.Voices = normalizedStrings(payload.Capabilities[i].Limits.Voices)
		payload.Capabilities[i].Limits.Languages = normalizedStrings(payload.Capabilities[i].Limits.Languages)
	}
	return RuntimeHealth{
		Status: payload.Status, Models: models, Error: payload.Error, Version: payload.Version,
		Backend: payload.Backend, Device: payload.Device, VRAMBytes: payload.VRAMBytes, Capabilities: payload.Capabilities,
	}, nil
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
