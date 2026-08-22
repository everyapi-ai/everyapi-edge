package console

import (
	"errors"
	"net/http"
)

func (h *handler) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/overview":
		h.overview(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/update":
		h.startUpdate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/update/settings":
		h.updateSettings(w)
	case r.Method == http.MethodPut && r.URL.Path == "/api/update/settings":
		h.saveUpdateSettings(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/node":
		h.nodeProfile(w)
	case r.Method == http.MethodGet && r.URL.Path == "/api/requests":
		writeJSON(w, http.StatusOK, h.store.Requests())
	case r.Method == http.MethodGet && r.URL.Path == "/api/logs":
		writeJSON(w, http.StatusOK, h.store.Logs())
	case r.Method == http.MethodGet && r.URL.Path == "/api/settlements":
		writeJSON(w, http.StatusOK, h.store.Settlements())
	case r.Method == http.MethodGet && r.URL.Path == "/api/models":
		h.listModels(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/models/capabilities":
		h.modelCapabilities(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/capabilities":
		h.capabilities(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/runtime":
		h.runtime(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/image-runtime":
		h.imageRuntime(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/image-runtime/model":
		h.selectImageRuntimeModel(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/image/edit":
		h.imageEdit(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runtime/unload-all":
		h.unloadAllRuntimeModels(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/runtime/unload":
		h.unloadRuntimeModel(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/storage":
		h.storageStatus(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/storage/plan":
		h.storageMigrationPlan(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/storage/migrate":
		h.startStorageMigration(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/storage/migrate":
		writeJSON(w, http.StatusOK, h.migrationSnapshot())
	case r.Method == http.MethodPost && r.URL.Path == "/api/storage/pick":
		h.storagePicker(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/playground/chat":
		h.playgroundChat(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/playground/embedding":
		h.playgroundEmbedding(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/playground/image":
		h.playgroundImage(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/playground/speech":
		h.playgroundSpeech(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/models/pull":
		h.startPull(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/models/benchmark":
		h.benchmarkModel(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/models":
		h.deleteModel(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/models/pull":
		writeJSON(w, http.StatusOK, h.pullSnapshot())
	case r.Method == http.MethodDelete && r.URL.Path == "/api/models/pull":
		h.cancelPull(w, r)
	default:
		writeError(w, http.StatusNotFound, errors.New("API route not found"))
	}
}
