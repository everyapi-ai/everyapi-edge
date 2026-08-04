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

type ImageHealth struct {
	Status Status
	Models []string
	Error  string
}

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
	response, err := c.target.Do(ctx, http.MethodGet, "/health", nil, nil)
	if err != nil {
		return ImageHealth{}, err
	}
	defer response.Body.Close()
	if err := checkResponse(response); err != nil {
		return ImageHealth{}, err
	}

	var payload struct {
		Status Status   `json:"status"`
		Models []string `json:"models"`
		Error  string   `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&payload); err != nil {
		return ImageHealth{}, fmt.Errorf("decode local image runtime health: %w", err)
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
	return ImageHealth{Status: payload.Status, Models: models, Error: payload.Error}, nil
}
