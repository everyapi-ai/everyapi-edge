package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const maxDiscoveryResponseBytes = 4 << 20

type TextClient struct {
	target *Target
}

func NewTextClient(baseURL string, client HTTPDoer) *TextClient {
	return &TextClient{target: newTarget(KindText, baseURL, client)}
}

func (c *TextClient) Do(ctx context.Context, method, path string, headers http.Header, body io.Reader) (*http.Response, error) {
	return c.target.Do(ctx, method, path, headers, body)
}

// InstalledModel names one model the local text runtime has on disk together with the marker that changes when the same tag is re-pulled. Callers that cache anything derived from a model's contents key on both: the name alone cannot tell `qwen3:8b` from the `qwen3:8b` that replaced it.
type InstalledModel struct {
	Name    string
	Version string
}

// InstalledModels reads the native tag list. Version carries Ollama's `modified_at` and is empty when the runtime does not report one, which callers must read as "cannot tell whether this changed".
func (c *TextClient) InstalledModels(ctx context.Context) ([]InstalledModel, error) {
	response, err := c.target.Do(ctx, http.MethodGet, "/api/tags", nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := checkResponse(response); err != nil {
		return nil, err
	}

	var payload struct {
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local text runtime models: %w", err)
	}
	models := make([]InstalledModel, 0, len(payload.Models))
	seen := make(map[string]struct{}, len(payload.Models))
	for _, model := range payload.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		models = append(models, InstalledModel{Name: name, Version: strings.TrimSpace(model.ModifiedAt)})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func (c *TextClient) Models(ctx context.Context) ([]string, error) {
	installed, err := c.InstalledModels(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(installed))
	for _, model := range installed {
		models = append(models, model.Name)
	}
	return models, nil
}

func (c *TextClient) SupportsResponses(ctx context.Context) (bool, error) {
	response, err := c.target.Do(ctx, http.MethodGet, "/api/version", nil, nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if err := checkResponse(response); err != nil {
		return false, err
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&payload); err != nil {
		return false, fmt.Errorf("decode local text runtime version: %w", err)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(strings.TrimSpace(payload.Version), "%d.%d.%d", &major, &minor, &patch); err != nil {
		return false, fmt.Errorf("decode local text runtime version: %w", err)
	}
	return major > 0 || minor > 13 || minor == 13 && patch >= 3, nil
}

// ModelCapabilities asks the local text runtime for the concrete operations one installed model supports. Model tags alone are not enough: an embedding-only model and a chat model can both be present in /api/tags but cannot serve the same endpoint.
func (c *TextClient) ModelCapabilities(ctx context.Context, model string) ([]string, error) {
	payload, err := json.Marshal(struct {
		Model string `json:"model"`
	}{Model: strings.TrimSpace(model)})
	if err != nil {
		return nil, err
	}
	response, err := c.target.Do(ctx, http.MethodPost, "/api/show", http.Header{"Content-Type": []string{"application/json"}}, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := checkResponse(response); err != nil {
		return nil, err
	}
	var result struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode local text runtime model capabilities: %w", err)
	}
	return normalizedStrings(result.Capabilities), nil
}
