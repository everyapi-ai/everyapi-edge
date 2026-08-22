package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

func (h *handler) runtime(w http.ResponseWriter, r *http.Request) {
	versionResponse, err := h.textClient().Do(r.Context(), http.MethodGet, "/api/version", nil, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer versionResponse.Body.Close()
	if versionResponse.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("local runtime returned %s", versionResponse.Status))
		return
	}
	var result Runtime
	if err := json.NewDecoder(io.LimitReader(versionResponse.Body, 64<<10)).Decode(&result); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode local runtime version: %w", err))
		return
	}

	models, err := h.loadedRuntimeModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result.Models = models
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	overview := h.store.Overview()
	overview.VRAMTotalGB = h.cfg.VRAMTotalGB
	overview.AgentVersion = h.cfg.Version
	if overview.AgentVersion == "" {
		overview.AgentVersion = h.cfg.AgentVersion
	}
	h.mu.RLock()
	overview.UpdateState = h.update.State
	overview.UpdateVersion = h.update.Version
	overview.UpdateError = h.update.Error
	h.mu.RUnlock()
	if models, err := h.loadedRuntimeModels(r.Context()); err == nil {
		for _, model := range models {
			overview.LoadedVRAMBytes += model.SizeVRAM
		}
	}
	if h.cfg.DiffusersURL != "" {
		if health, err := h.imageClient().Health(r.Context()); err == nil && health.VRAMBytes > 0 {
			overview.LoadedVRAMBytes += health.VRAMBytes
		}
	}
	if h.cfg.SpeechURL != "" {
		if health, err := h.speechClient().Health(r.Context()); err == nil && health.VRAMBytes > 0 {
			overview.LoadedVRAMBytes += health.VRAMBytes
		}
	}
	overview.ReservedVRAMBytes = memoryReserveBytes(overview.VRAMTotalGB)
	overview.AvailableVRAMBytes = availableMemoryBytes(overview.VRAMTotalGB, overview.LoadedVRAMBytes, overview.ReservedVRAMBytes)
	writeJSON(w, http.StatusOK, overview)
}

func (h *handler) startUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Update == nil {
		writeError(w, http.StatusNotImplemented, errors.New("this agent does not support updates"))
		return
	}
	h.mu.Lock()
	if h.updateStarting || h.update.State == "checking" || h.update.State == "downloading" || h.update.State == "restarting" {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("update already in progress"))
		return
	}
	h.updateStarting = true
	h.mu.Unlock()
	go func() {
		err := h.cfg.Update(context.Background(), h.reportUpdateStatus)
		h.mu.Lock()
		h.updateStarting = false
		if err != nil && !errors.Is(err, ErrUpdateInProgress) {
			h.update = UpdateStatus{State: "failed", Error: err.Error()}
		}
		h.mu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (h *handler) reportUpdateStatus(status UpdateStatus) {
	h.mu.Lock()
	h.update = status
	h.mu.Unlock()
}

func (h *handler) updateSettings(w http.ResponseWriter) {
	if h.cfg.LoadUpdateSettings == nil {
		writeError(w, http.StatusNotImplemented, errors.New("this agent does not support automatic update settings"))
		return
	}
	settings, err := h.cfg.LoadUpdateSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) saveUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SaveAutoUpdate == nil {
		writeError(w, http.StatusNotImplemented, errors.New("this agent does not support automatic update settings"))
		return
	}
	var input struct {
		AutoUpdate *bool `json:"auto_update"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.AutoUpdate == nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("auto_update must be a boolean"))
		return
	}
	settings, err := h.cfg.SaveAutoUpdate(*input.AutoUpdate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

const gibibyte = int64(1024 * 1024 * 1024)

// memoryReserveBytes leaves room for the operating system and Ollama's KV cache. A model that merely fits the device will become unstable as its context grows, so this reservation is part of admission rather than a UI warning.
func memoryReserveBytes(totalGB int) int64 {
	if totalGB <= 0 {
		return 0
	}
	total := int64(totalGB) * gibibyte
	reserve := total / 5
	if reserve < 4*gibibyte {
		reserve = 4 * gibibyte
	}
	if reserve > total {
		return total
	}
	return reserve
}

func availableMemoryBytes(totalGB int, loadedBytes, reservedBytes int64) int64 {
	available := int64(totalGB)*gibibyte - loadedBytes - reservedBytes
	if available < 0 {
		return 0
	}
	return available
}
