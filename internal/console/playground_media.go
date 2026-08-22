package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

const maxPlaygroundArtifactBytes = 32 << 20

func (h *handler) playgroundEmbedding(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	if !decodePlaygroundJSON(r, &input) || !validModelName.MatchString(input.Model) || strings.TrimSpace(input.Input) == "" || len(input.Input) > 32<<10 {
		writeError(w, http.StatusBadRequest, errors.New("a valid embedding model and input are required"))
		return
	}
	payload, _ := json.Marshal(input)
	h.proxyJSONPlayground(w, r, input.Model, "/v1/embeddings", payload, h.textClient().Do)
}

func (h *handler) playgroundImage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("image generation is unavailable on this node"))
		return
	}
	var input struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
	}
	if !decodePlaygroundJSON(r, &input) || !validModelName.MatchString(input.Model) || strings.TrimSpace(input.Prompt) == "" || len(input.Prompt) > 16<<10 {
		writeError(w, http.StatusBadRequest, errors.New("a valid image model and prompt are required"))
		return
	}
	if input.Size == "" {
		input.Size = "1024x1024"
	}
	if input.Size != "512x512" && input.Size != "1024x1024" {
		writeError(w, http.StatusBadRequest, errors.New("image size must be 512x512 or 1024x1024"))
		return
	}
	input.ResponseFormat = "b64_json"
	payload, _ := json.Marshal(input)
	h.proxyJSONPlayground(w, r, input.Model, "/v1/images/generations", payload, h.imageClient().Do)
}

func (h *handler) playgroundSpeech(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SpeechURL == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("speech synthesis is unavailable on this node"))
		return
	}
	var input struct {
		Model          string  `json:"model"`
		Input          string  `json:"input"`
		Voice          string  `json:"voice"`
		ResponseFormat string  `json:"response_format"`
		Speed          float64 `json:"speed,omitempty"`
	}
	if !decodePlaygroundJSON(r, &input) || !validModelName.MatchString(input.Model) || strings.TrimSpace(input.Input) == "" || len(input.Input) > 8<<10 || !validModelName.MatchString(input.Voice) {
		writeError(w, http.StatusBadRequest, errors.New("a valid speech model, voice, and input are required"))
		return
	}
	if input.ResponseFormat == "" {
		input.ResponseFormat = "mp3"
	}
	if !allowedSpeechFormat(input.ResponseFormat) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported speech response format"))
		return
	}
	if input.Speed == 0 {
		input.Speed = 1
	}
	if input.Speed < 0.25 || input.Speed > 4 {
		writeError(w, http.StatusBadRequest, errors.New("speech speed must be between 0.25 and 4"))
		return
	}
	payload, _ := json.Marshal(input)
	startedAt, finish := h.startPlaygroundRequest(input.Model, "/v1/audio/speech")
	defer finish(startedAt, "local speech request did not complete")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	response, err := h.speechClient().Do(ctx, http.MethodPost, "/v1/audio/speech", http.Header{"Content-Type": []string{"application/json"}}, bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New("local speech runtime is unavailable"))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local speech runtime returned HTTP %d", response.StatusCode))
		return
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "audio/") && contentType != "application/octet-stream" {
		contentType = "application/octet-stream"
	}
	response.Header.Set("Content-Type", contentType)
	if err := copyBoundedPlaygroundResponse(w, response, maxPlaygroundArtifactBytes); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	finish(startedAt, "")
}

func decodePlaygroundJSON(r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

type playgroundDo func(context.Context, string, string, http.Header, io.Reader) (*http.Response, error)

func (h *handler) proxyJSONPlayground(w http.ResponseWriter, r *http.Request, model, path string, payload []byte, do playgroundDo) {
	startedAt, finish := h.startPlaygroundRequest(model, path)
	defer finish(startedAt, "local playground request did not complete")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	response, err := do(ctx, http.MethodPost, path, http.Header{"Content-Type": []string{"application/json"}}, bytes.NewReader(payload))
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New("local runtime is unavailable"))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned HTTP %d", response.StatusCode))
		return
	}
	response.Header.Set("Content-Type", "application/json")
	if err := copyBoundedPlaygroundResponse(w, response, maxPlaygroundArtifactBytes); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	finish(startedAt, "")
}

func copyBoundedPlaygroundResponse(w http.ResponseWriter, response *http.Response, limit int64) error {
	artifact, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return errors.New("local runtime response could not be read")
	}
	if int64(len(artifact)) > limit {
		return errors.New("local runtime response exceeds the playground artifact limit")
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(artifact)
	return nil
}

func (h *handler) startPlaygroundRequest(model, path string) (time.Time, func(time.Time, string)) {
	startedAt := time.Now().UTC()
	capability, _ := protocol.CapabilityForRequest(path)
	handle := h.store.Start(RequestStart{ID: fmt.Sprintf("playground-%d", startedAt.UnixNano()), Model: model, Path: path, Capability: string(capability), Consumer: "local playground", StartedAt: startedAt})
	finished := false
	return startedAt, func(start time.Time, problem string) {
		if finished {
			return
		}
		finished = true
		h.store.Finish(handle, RequestFinish{CompletedAt: time.Now().UTC(), Duration: time.Since(start), Error: problem})
	}
}

func allowedSpeechFormat(format string) bool {
	switch format {
	case "mp3", "opus", "aac", "flac", "wav", "pcm":
		return true
	default:
		return false
	}
}
