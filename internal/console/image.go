package console

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	imageEditingUnavailableMessage = "Image editing is unavailable on this node"
	imageEditingGPURequiredMessage = "A CUDA-capable GPU is required for image editing."
)

func safeImageRuntimeError(message string) string {
	if message == imageEditingGPURequiredMessage {
		return message
	}
	return imageEditingUnavailableMessage
}

func (h *handler) imageRuntime(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "unavailable", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	health, err := h.imageClient().Health(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, ImageRuntime{Status: "offline", Models: []string{}, Error: imageEditingUnavailableMessage})
		return
	}
	runtime := ImageRuntime{Status: string(health.Status), Models: health.Models, Error: health.Error}
	if runtime.Status == "" {
		runtime.Status = "ready"
	}
	if runtime.Models == nil {
		runtime.Models = []string{}
	}
	if runtime.Error != "" {
		runtime.Error = safeImageRuntimeError(runtime.Error)
	}
	writeJSON(w, http.StatusOK, runtime)
}

func (h *handler) selectImageRuntimeModel(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New(imageEditingUnavailableMessage))
		return
	}
	var input struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid image model name is required"))
		return
	}
	payload, _ := json.Marshal(struct {
		Model string `json:"model"`
	}{Model: input.Model})
	response, err := h.imageClient().Do(
		r.Context(),
		http.MethodPost,
		"/v1/models/select",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	var runtime ImageRuntime
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&runtime); err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	if runtime.Status == "" {
		runtime.Status = "ready"
	}
	if runtime.Models == nil {
		runtime.Models = []string{}
	}
	writeJSON(w, http.StatusOK, runtime)
}

func (h *handler) imageEdit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.DiffusersURL == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New(imageEditingUnavailableMessage))
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		writeError(w, http.StatusBadRequest, errors.New("image edit requires multipart/form-data"))
		return
	}
	response, err := h.imageClient().Do(
		r.Context(),
		http.MethodPost,
		"/v1/images/edits",
		http.Header{"Content-Type": []string{r.Header.Get("Content-Type")}},
		io.LimitReader(r.Body, 32<<20),
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		writeError(w, http.StatusBadGateway, errors.New(imageEditingUnavailableMessage))
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 16<<20))
}
