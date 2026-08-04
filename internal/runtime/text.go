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
