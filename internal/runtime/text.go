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

func (c *TextClient) Models(ctx context.Context) ([]string, error) {
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
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local text runtime models: %w", err)
	}
	models := make([]string, 0, len(payload.Models))
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
		models = append(models, name)
	}
	sort.Strings(models)
	return models, nil
}

// SupportsResponses verifies the Ollama runtime version before the agent advertises /v1/responses. Ollama added the stateless endpoint in 0.13.3; model completion capability alone is insufficient on older native macOS installations.
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
