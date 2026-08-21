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
	StatusUnavailable Status = "unavailable"
)

// RuntimeHealth is the discovery contract every local runtime serves on /health. The agent reports the union of the ready model lists to the gateway before each handshake.
type RuntimeHealth struct {
	Status Status
	Models []string
	Error  string
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
	if err := checkResponse(response); err != nil {
		return RuntimeHealth{}, err
	}

	var payload struct {
		Status Status   `json:"status"`
		Models []string `json:"models"`
		Error  string   `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&payload); err != nil {
		return RuntimeHealth{}, fmt.Errorf("decode local %s runtime health: %w", target.kind, err)
	}
	seen := make(map[string]struct{}, len(payload.Models))
	models := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		name := strings.TrimSpace(model)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		models = append(models, name)
	}
	sort.Strings(models)
	return RuntimeHealth{Status: payload.Status, Models: models, Error: payload.Error}, nil
}
