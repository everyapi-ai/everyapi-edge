package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (h *handler) benchmarkModel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Model         string `json:"model"`
		ReleaseLoaded bool   `json:"release_loaded"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	if h.store.Overview().ActiveRequests > 0 {
		writeError(w, http.StatusConflict, errors.New("wait until current local requests finish before benchmarking a model"))
		return
	}
	resident, err := h.loadedRuntimeModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, model := range resident {
		if model.Name == input.Model && !input.ReleaseLoaded {
			writeError(w, http.StatusConflict, errors.New("release the resident model before running a quick benchmark"))
			return
		}
	}
	result, err := h.runModelBenchmark(r.Context(), input.Model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) runModelBenchmark(ctx context.Context, model string) (ModelBenchmark, error) {
	payload, err := json.Marshal(struct {
		Model     string `json:"model"`
		Prompt    string `json:"prompt"`
		Stream    bool   `json:"stream"`
		KeepAlive int    `json:"keep_alive"`
		Options   struct {
			NumPredict int `json:"num_predict"`
		} `json:"options"`
	}{
		Model: model, Prompt: "Reply with OK.", Stream: false, KeepAlive: 0,
		Options: struct {
			NumPredict int `json:"num_predict"`
		}{NumPredict: 1},
	})
	if err != nil {
		return ModelBenchmark{}, err
	}
	benchmarkContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	response, err := h.longTextClient().Do(
		benchmarkContext,
		http.MethodPost,
		"/api/generate",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err != nil {
		return ModelBenchmark{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ModelBenchmark{}, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var output struct {
		EvalCount     int   `json:"eval_count"`
		EvalDuration  int64 `json:"eval_duration"`
		TotalDuration int64 `json:"total_duration"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 512<<10)).Decode(&output); err != nil {
		return ModelBenchmark{}, fmt.Errorf("decode local benchmark result: %w", err)
	}
	if output.EvalCount <= 0 || output.EvalDuration <= 0 {
		return ModelBenchmark{}, errors.New("local runtime returned no generation timing")
	}
	return ModelBenchmark{
		Model: model, EvalCount: output.EvalCount, EvalDurationNS: output.EvalDuration, TotalDurationNS: output.TotalDuration,
		TokensPerSecond: float64(output.EvalCount) * float64(time.Second) / float64(output.EvalDuration),
	}, nil
}

func (h *handler) unloadRuntimeModel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Model) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	if err := h.unloadRuntimeModelByName(r.Context(), input.Model); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) unloadAllRuntimeModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.loadedRuntimeModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, model := range models {
		if err := h.unloadRuntimeModelByName(r.Context(), model.Name); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) unloadRuntimeModelByName(ctx context.Context, name string) error {
	payload, _ := json.Marshal(struct {
		Model     string `json:"model"`
		KeepAlive int    `json:"keep_alive"`
		Stream    bool   `json:"stream"`
	}{Model: name, KeepAlive: 0, Stream: false})
	response, err := h.textClient().Do(
		ctx,
		http.MethodPost,
		"/api/generate",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("local runtime returned %s", response.Status)
	}
	return nil
}

func (h *handler) loadedRuntimeModels(ctx context.Context) ([]RuntimeModel, error) {
	response, err := h.textClient().Do(ctx, http.MethodGet, "/api/ps", nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local runtime returned %s", response.Status)
	}
	var payload struct {
		Models []RuntimeModel `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode local runtime state: %w", err)
	}
	return payload.Models, nil
}
