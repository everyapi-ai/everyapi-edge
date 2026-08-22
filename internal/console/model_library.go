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
)

func (h *handler) nodeProfile(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, NodeProfile{
		Name:         h.cfg.NodeName,
		AgentVersion: h.cfg.AgentVersion,
		GPUModel:     h.cfg.GPUModel,
		Platform:     h.cfg.Platform,
		CountryISO2:  h.cfg.CountryISO2,
		VRAMTotalGB:  h.cfg.VRAMTotalGB,
	})
}

func (h *handler) modelCapabilities(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("name"))
	if !validModelName.MatchString(model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid model name is required"))
		return
	}
	capabilities, err := h.localModelCapabilities(r.Context(), model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, capabilities)
}

func (h *handler) localModelCapabilities(ctx context.Context, model string) (ModelCapabilities, error) {
	payload, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: model})
	if err != nil {
		return ModelCapabilities{}, err
	}
	response, err := h.textClient().Do(
		ctx,
		http.MethodPost,
		"/api/show",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err != nil {
		return ModelCapabilities{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelCapabilities{}, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var result ModelCapabilities
	if err := json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&result); err != nil {
		return ModelCapabilities{}, fmt.Errorf("decode local model capabilities: %w", err)
	}
	result.Model = model
	if result.Capabilities == nil {
		result.Capabilities = []string{}
	}
	return result, nil
}

func supportsCapability(capabilities ModelCapabilities, wanted string) bool {
	for _, capability := range capabilities.Capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func (h *handler) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.localModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Models []Model `json:"models"`
	}{Models: models})
}

func (h *handler) localModels(ctx context.Context) ([]Model, error) {
	response, err := h.textClient().Do(ctx, http.MethodGet, "/api/tags", nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var payload struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local models: %w", err)
	}
	if payload.Models == nil {
		payload.Models = []Model{}
	}
	return payload.Models, nil
}

func (h *handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validModelName.MatchString(name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	loaded, err := h.modelLoaded(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if loaded {
		writeError(w, http.StatusConflict, errors.New("unload the model before removing it"))
		return
	}
	if r.URL.Query().Get("confirm_unloaded") != "true" {
		writeError(w, http.StatusConflict, errors.New("refresh the runtime state and confirm removal before removing this model"))
		return
	}
	body, _ := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: name})
	response, err := h.textClient().Do(
		r.Context(),
		http.MethodDelete,
		"/api/delete",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(body),
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", response.Status))
		return
	}
	if h.cfg.ModelsChanged != nil {
		h.cfg.ModelsChanged()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) modelLoaded(ctx context.Context, name string) (bool, error) {
	models, err := h.loadedRuntimeModels(ctx)
	if err != nil {
		return false, err
	}
	for _, model := range models {
		if model.Name == name {
			return true, nil
		}
	}
	return false, nil
}
