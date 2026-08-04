package console

import (
	"encoding/json"
	"log"
	"net/http"
)

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	message := publicErrorMessage(status, err)
	if message != err.Error() {
		log.Printf("console API error (%d %s): %v", status, errorCode(status), err)
	}
	writeJSON(w, status, errorEnvelope{Error: APIError{
		Code:      errorCode(status),
		Message:   message,
		Retryable: status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout,
	}})
}

func writePrivateError(w http.ResponseWriter, status int, message string, err error) {
	log.Printf("console API error (%d %s): %v", status, errorCode(status), err)
	writeJSON(w, status, errorEnvelope{Error: APIError{
		Code:      errorCode(status),
		Message:   message,
		Retryable: status == http.StatusTooManyRequests || status >= http.StatusInternalServerError,
	}})
}

func publicErrorMessage(status int, err error) string {
	if status < http.StatusInternalServerError {
		return err.Error()
	}
	switch status {
	case http.StatusNotImplemented:
		return "This operation is not supported."
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return "The local runtime request failed."
	case http.StatusServiceUnavailable:
		return "The local runtime is unavailable."
	default:
		return "The control room encountered an internal error."
	}
}

func errorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "unsupported_operation"
	case http.StatusNotImplemented:
		return "not_supported"
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return "runtime_error"
	case http.StatusServiceUnavailable:
		return "runtime_unavailable"
	default:
		return "internal_error"
	}
}
