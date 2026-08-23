package console

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func (h *handler) resourceSettings(w http.ResponseWriter) {
	if h.cfg.LoadResourceSettings == nil {
		writeError(w, http.StatusNotImplemented, errors.New("resource settings are unavailable"))
		return
	}
	settings, err := h.cfg.LoadResourceSettings()
	if err != nil {
		writePrivateError(w, http.StatusInternalServerError, "Resource settings could not be loaded.", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) saveResourceSettings(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SaveResourcePolicy == nil {
		writeError(w, http.StatusNotImplemented, errors.New("resource settings are unavailable"))
		return
	}
	var input struct {
		ResourcePolicy protocol.ResourcePolicy `json:"resource_policy"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid resource settings"))
		return
	}
	settings, err := h.cfg.SaveResourcePolicy(r.Context(), input.ResourcePolicy)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) setDrain(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SetDrain == nil {
		writeError(w, http.StatusNotImplemented, errors.New("drain control is unavailable"))
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Enabled == nil {
		writeError(w, http.StatusBadRequest, errors.New("enabled is required"))
		return
	}
	settings, err := h.cfg.SetDrain(r.Context(), *input.Enabled)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
