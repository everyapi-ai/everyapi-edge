package runtime

import (
	"context"
	"io"
	"net/http"
)

// SpeechHealth is the speech runtime's name for the shared /health contract.
type SpeechHealth = RuntimeHealth

type SpeechClient struct {
	target *Target
}

func NewSpeechClient(baseURL string, client HTTPDoer) *SpeechClient {
	return &SpeechClient{target: newTarget(KindSpeech, baseURL, client)}
}

func (c *SpeechClient) Do(ctx context.Context, method, path string, headers http.Header, body io.Reader) (*http.Response, error) {
	return c.target.Do(ctx, method, path, headers, body)
}

func (c *SpeechClient) Health(ctx context.Context) (SpeechHealth, error) {
	return fetchHealth(ctx, c.target)
}
