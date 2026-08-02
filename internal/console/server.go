package console

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var webAssets embed.FS

// Config is local-only console configuration. Token is required by NewHandler
// so an accidentally broad port mapping cannot grant model-management access.
type Config struct {
	OllamaURL   string
	Token       string
	VRAMTotalGB int
}

type Model struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at,omitempty"`
	Details    struct {
		ParameterSize     string `json:"parameter_size,omitempty"`
		QuantizationLevel string `json:"quantization_level,omitempty"`
	} `json:"details,omitempty"`
}

type pullJob struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Completed int64  `json:"completed,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Error     string `json:"error,omitempty"`
	Done      bool   `json:"done"`
}

type handler struct {
	cfg        Config
	store      *Store
	httpClient *http.Client
	mu         sync.RWMutex
	pull       *pullJob
}

var validModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$`)

func NewHandler(cfg Config, store *Store) http.Handler {
	cfg.OllamaURL = strings.TrimRight(strings.TrimSpace(cfg.OllamaURL), "/")
	if store == nil {
		store = NewStore(200)
	}
	h := &handler{cfg: cfg, store: store, httpClient: &http.Client{Timeout: 10 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.index)
	mux.Handle("/api/", h.requireToken(http.HandlerFunc(h.api)))
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (h *handler) index(w http.ResponseWriter, _ *http.Request) {
	data, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "console assets unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (h *handler) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.Token == "" {
			http.Error(w, "console token is not configured", http.StatusServiceUnavailable)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(h.cfg.Token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="EveryAPI Edge"`)
			http.Error(w, "management authorization required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/overview":
		h.overview(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/requests":
		writeJSON(w, http.StatusOK, h.store.Requests())
	case r.Method == http.MethodGet && r.URL.Path == "/api/logs":
		writeJSON(w, http.StatusOK, h.store.Logs())
	case r.Method == http.MethodGet && r.URL.Path == "/api/settlements":
		writeJSON(w, http.StatusOK, h.store.Settlements())
	case r.Method == http.MethodGet && r.URL.Path == "/api/models":
		h.listModels(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/models/pull":
		h.startPull(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/models":
		h.deleteModel(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/models/pull":
		writeJSON(w, http.StatusOK, h.pullSnapshot())
	default:
		http.NotFound(w, r)
	}
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	overview := h.store.Overview()
	overview.VRAMTotalGB = h.cfg.VRAMTotalGB
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.cfg.OllamaURL+"/api/ps", nil)
	if err == nil {
		response, doErr := h.httpClient.Do(request)
		if doErr == nil {
			defer response.Body.Close()
			if response.StatusCode == http.StatusOK {
				var payload struct {
					Models []struct {
						SizeVRAM int64 `json:"size_vram"`
					} `json:"models"`
				}
				if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload) == nil {
					for _, model := range payload.Models {
						overview.LoadedVRAMBytes += model.SizeVRAM
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, overview)
}

func (h *handler) listModels(w http.ResponseWriter, r *http.Request) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, h.cfg.OllamaURL+"/api/tags", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	response, err := h.httpClient.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("Ollama returned %s", response.Status))
		return
	}
	var payload struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("decode Ollama models: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *handler) startPull(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input); err != nil || !validModelName.MatchString(input.Name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid Ollama model name is required"))
		return
	}
	h.mu.Lock()
	if h.pull != nil && !h.pull.Done {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, errors.New("another model download is already running"))
		return
	}
	h.pull = &pullJob{Name: input.Name, Status: "queued"}
	job := h.pull
	response := *job
	h.mu.Unlock()
	go h.runPull(job)
	writeJSON(w, http.StatusAccepted, response)
}

func (h *handler) pullSnapshot() *pullJob {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.pull == nil {
		return nil
	}
	copy := *h.pull
	return &copy
}

func (h *handler) runPull(job *pullJob) {
	payload, _ := json.Marshal(struct {
		Name   string `json:"name"`
		Stream bool   `json:"stream"`
	}{Name: job.Name, Stream: true})
	request, err := http.NewRequest(http.MethodPost, h.cfg.OllamaURL+"/api/pull", bytes.NewReader(payload))
	if err == nil {
		request.Header.Set("Content-Type", "application/json")
		response, doErr := (&http.Client{Timeout: time.Hour}).Do(request)
		if doErr != nil {
			err = doErr
		} else {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("Ollama returned %s", response.Status)
			} else {
				scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
				for scanner.Scan() {
					var update pullJob
					if json.Unmarshal(scanner.Bytes(), &update) == nil {
						h.mu.Lock()
						job.Status, job.Completed, job.Total = update.Status, update.Completed, update.Total
						h.mu.Unlock()
					}
				}
				if scanErr := scanner.Err(); scanErr != nil {
					err = scanErr
				}
			}
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	job.Done = true
	if err != nil {
		job.Error = err.Error()
		job.Status = "failed"
	} else if job.Status == "" {
		job.Status = "success"
	}
}

func (h *handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !validModelName.MatchString(name) {
		writeError(w, http.StatusBadRequest, errors.New("a valid Ollama model name is required"))
		return
	}
	body, _ := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: name})
	request, err := http.NewRequestWithContext(r.Context(), http.MethodDelete, h.cfg.OllamaURL+"/api/delete", bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.httpClient.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, fmt.Errorf("Ollama returned %s", response.Status))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
