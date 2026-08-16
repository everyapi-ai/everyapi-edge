// Package runtime owns the wire boundary between Edge and local inference engines. Callers work with typed health, discovery, and routing contracts instead of constructing engine-specific URLs throughout the application.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Kind string

const (
	KindText  Kind = "text"
	KindImage Kind = "image"
)

var (
	ErrPathNotAllowed     = errors.New("runtime path is not allowed")
	ErrRuntimeUnavailable = errors.New("runtime is not configured")
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type UpstreamError struct {
	StatusCode int
	Retryable  bool
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("local runtime returned HTTP %d", e.StatusCode)
}

type RequestError struct {
	Op  string
	Err error
}

func (e *RequestError) Error() string { return e.Err.Error() }
func (e *RequestError) Unwrap() error { return e.Err }

type Target struct {
	kind    Kind
	baseURL string
	client  HTTPDoer
}

func newTarget(kind Kind, baseURL string, client HTTPDoer) *Target {
	if client == nil {
		client = http.DefaultClient
	}
	return &Target{kind: kind, baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (t *Target) Kind() Kind { return t.kind }

func (t *Target) Do(ctx context.Context, method, path string, headers http.Header, body io.Reader) (*http.Response, error) {
	endpoint, err := joinURL(t.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("build local runtime endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, &RequestError{Op: "build", Err: err}
	}
	request.Header = headers.Clone()
	response, err := t.client.Do(request)
	if err != nil {
		return nil, &RequestError{Op: "send", Err: err}
	}
	return response, nil
}

func joinURL(baseURL, path string) (string, error) {
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return "", errors.New("runtime URL must be absolute")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.TrimLeft(path, "/")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

type Router struct {
	text  *Target
	image *Target
}

func NewRouter(textURL, imageURL string, client HTTPDoer) *Router {
	return &Router{
		text:  newTarget(KindText, textURL, client),
		image: newTarget(KindImage, imageURL, client),
	}
}

func (r *Router) Resolve(path string) (*Target, error) {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/models":
		if r.text.baseURL == "" {
			return nil, ErrRuntimeUnavailable
		}
		return r.text, nil
	case "/v1/images/generations", "/v1/images/edits":
		if r.image.baseURL == "" {
			return nil, ErrRuntimeUnavailable
		}
		return r.image, nil
	default:
		return nil, ErrPathNotAllowed
	}
}

func checkResponse(response *http.Response) error {
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	return &UpstreamError{
		StatusCode: response.StatusCode,
		Retryable:  response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
	}
}
