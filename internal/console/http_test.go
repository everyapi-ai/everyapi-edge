package console

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteErrorUsesStructuredEnvelope(t *testing.T) {
	response := httptest.NewRecorder()
	writeError(response, http.StatusBadGateway, errors.New("dial http://local-runtime:11434 from /Users/operator/.cache"))

	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "runtime_error" || payload.Error.Message != "The local runtime request failed." || !payload.Error.Retryable {
		t.Fatalf("error payload = %#v", payload.Error)
	}
	if strings.Contains(response.Body.String(), "local-runtime") || strings.Contains(response.Body.String(), "/Users/operator") {
		t.Fatalf("error payload exposed private runtime details: %s", response.Body.String())
	}
}

func TestWriteErrorPreservesActionableClientMessage(t *testing.T) {
	response := httptest.NewRecorder()
	writeError(response, http.StatusConflict, errors.New("unload the model before removing it"))

	var payload errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Message != "unload the model before removing it" {
		t.Fatalf("client error message = %q", payload.Error.Message)
	}
}

func TestWritePrivateErrorNeverExposesFilesystemDetails(t *testing.T) {
	response := httptest.NewRecorder()
	writePrivateError(response, http.StatusBadRequest, "Storage directory selection failed.", errors.New("open /Users/operator/private: permission denied"))

	if !strings.Contains(response.Body.String(), "Storage directory selection failed.") || strings.Contains(response.Body.String(), "/Users/operator") {
		t.Fatalf("private error payload = %s", response.Body.String())
	}
}

func TestUnknownAPIRouteUsesJSONError(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	response := httptest.NewRecorder()
	NewHandler(Config{}, NewStore(1)).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	var payload errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "not_found" {
		t.Fatalf("error = %#v", payload.Error)
	}
}
