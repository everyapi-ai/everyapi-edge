package console

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
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

func (h *handler) startPull(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	h.mu.Lock()
	if h.migration != nil && !h.migration.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("wait for the model migration to finish before downloading another model"))
		return
	}
	h.mu.Unlock()

	installed, err := h.localModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	for _, model := range installed {
		if model.Name == input.Name {
			writeError(w, http.StatusConflict, errors.New("this model is already installed"))
			return
		}
	}
	h.mu.Lock()
	if h.migration != nil && !h.migration.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("wait for the model migration to finish before downloading another model"))
		return
	}
	if h.pull != nil && h.pull.Name == input.Name {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("this model download is already running"))
		return
	}
	for _, queued := range h.pullQueue {
		if queued.Name == input.Name {
			h.mu.Unlock()
			writeError(w, http.StatusConflict, errors.New("this model is already queued for download"))
			return
		}
	}
	job := &pullJob{Name: input.Name, Status: "queued"}
	startNow := h.pull == nil
	if startNow {
		h.pull = job
	} else {
		h.pullQueue = append(h.pullQueue, job)
	}
	response := *job
	h.mu.Unlock()
	if startNow {
		go h.runPull(job)
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (h *handler) pullSnapshot() pullQueueSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	snapshot := pullQueueSnapshot{Queued: make([]*pullJob, 0, len(h.pullQueue))}
	if h.pull != nil {
		active := *h.pull
		snapshot.Active = &active
	}
	if h.latestPull != nil {
		latest := *h.latestPull
		snapshot.Latest = &latest
	}
	for _, job := range h.pullQueue {
		copy := *job
		snapshot.Queued = append(snapshot.Queued, &copy)
	}
	return snapshot
}

func (h *handler) cancelPull(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validModelName.MatchString(name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid local model name is required"))
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pull != nil && h.pull.Name == name {
		h.pull.cancelled = true
		if h.pull.cancel != nil {
			h.pull.cancel()
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	for index, job := range h.pullQueue {
		if job.Name != name {
			continue
		}
		h.pullQueue = append(h.pullQueue[:index], h.pullQueue[index+1:]...)
		job.Done, job.Status, job.Error = true, "cancelled", ""
		latest := *job
		h.latestPull = &latest
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, errors.New("this model is not queued or downloading"))
}

func (h *handler) runPull(job *pullJob) {
	payload, _ := json.Marshal(struct {
		Name   string `json:"name"`
		Stream bool   `json:"stream"`
	}{Name: job.Name, Stream: true})
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	job.cancel = cancel
	if job.cancelled {
		cancel()
	}
	h.mu.Unlock()
	defer cancel()
	response, err := h.longTextClient().Do(
		ctx,
		http.MethodPost,
		"/api/pull",
		http.Header{"Content-Type": []string{"application/json"}},
		bytes.NewReader(payload),
	)
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			err = fmt.Errorf("local runtime returned %s", response.Status)
		} else {
			scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
			succeeded := false
			for scanner.Scan() {
				line := bytes.TrimSpace(scanner.Bytes())
				if len(line) == 0 {
					continue
				}
				var update pullJob
				if decodeErr := json.Unmarshal(line, &update); decodeErr != nil {
					err = fmt.Errorf("decode local runtime pull response: %w", decodeErr)
					break
				}
				if update.Error != "" {
					err = fmt.Errorf("local runtime pull failed: %s", update.Error)
					break
				}
				if storageErr := h.pullStorageError(update); storageErr != nil {
					err = storageErr
					cancel()
					break
				}
				h.mu.Lock()
				updatePullProgress(job, update, time.Now())
				h.mu.Unlock()
				if update.Status == "success" {
					succeeded = true
				}
			}
			if scanErr := scanner.Err(); scanErr != nil && err == nil {
				err = scanErr
			}
			if err == nil && !succeeded {
				err = errors.New("local runtime pull ended without success")
			}
		}
	}
	h.mu.Lock()
	job.Done = true
	job.cancel = nil
	if job.cancelled {
		job.Error = ""
		job.Status = "cancelled"
	} else if err != nil {
		job.Error = err.Error()
		job.Status = "failed"
	} else if job.Status == "" {
		job.Status = "success"
	}
	latest := *job
	h.latestPull = &latest
	var next *pullJob
	if h.pull == job {
		h.pull = nil
		if len(h.pullQueue) > 0 {
			next, h.pullQueue = h.pullQueue[0], h.pullQueue[1:]
			h.pull = next
		}
	}
	h.mu.Unlock()
	if next != nil {
		go h.runPull(next)
	}
}

func availableStorageBytes(path string) (int64, error) {
	_, available, err := storageCapacity(path)
	return available, err
}

// pullStorageError compares only the remaining bytes in the current layer with
// free space. Bytes already written must not be counted twice, and an
// unavailable capacity probe must not turn into a false rejection.
func (h *handler) pullStorageError(update pullJob) error {
	if update.Total <= update.Completed || h.cfg.StoragePath == "" {
		return nil
	}
	available := h.storageAvailable
	if available == nil {
		available = availableStorageBytes
	}
	free, err := available(h.cfg.StoragePath)
	if err != nil || update.Total-update.Completed <= free {
		return nil
	}
	return fmt.Errorf("not enough free disk space for this model download (%s remaining, %s available)", formatByteCount(update.Total-update.Completed), formatByteCount(free))
}

func formatByteCount(bytes int64) string {
	const gigabyte = 1024 * 1024 * 1024
	const megabyte = 1024 * 1024
	if bytes >= gigabyte {
		return fmt.Sprintf("%.1f GB", float64(bytes)/gigabyte)
	}
	return fmt.Sprintf("%d MB", bytes/megabyte)
}

// updatePullProgress turns the runtime's byte counters into a conservative
// transfer estimate. The counters can reset between layers, so only forward
// progress contributes to the rate and a reset clears the prior sample.
func updatePullProgress(job *pullJob, update pullJob, observedAt time.Time) {
	job.Status, job.Completed, job.Total = update.Status, update.Completed, update.Total
	if job.Total <= 0 || job.Completed < 0 {
		job.RateBytesPerSecond, job.SecondsRemaining = 0, 0
		job.sampledAt, job.sampledBytes = observedAt, job.Completed
		return
	}
	if job.sampledAt.IsZero() || job.Completed < job.sampledBytes {
		job.RateBytesPerSecond, job.SecondsRemaining = 0, 0
		job.sampledAt, job.sampledBytes = observedAt, job.Completed
		return
	}
	elapsed := observedAt.Sub(job.sampledAt).Seconds()
	if elapsed <= 0 || job.Completed == job.sampledBytes {
		return
	}
	rate := float64(job.Completed-job.sampledBytes) / elapsed
	// Pull status arrives per layer. An exponential moving average prevents a
	// tiny finishing layer from making the ETA jump wildly.
	if job.RateBytesPerSecond > 0 {
		rate = job.RateBytesPerSecond*0.65 + rate*0.35
	}
	job.RateBytesPerSecond = rate
	remaining := job.Total - job.Completed
	if remaining > 0 && rate > 0 {
		job.SecondsRemaining = int64(math.Ceil(float64(remaining) / rate))
	} else {
		job.SecondsRemaining = 0
	}
	job.sampledAt, job.sampledBytes = observedAt, job.Completed
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
