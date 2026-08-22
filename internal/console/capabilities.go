package console

import (
	"net/http"
	"sort"
	"strings"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
	edgeruntime "github.com/everyapi-ai/everyapi-edge/internal/runtime"
)

func (h *handler) capabilities(w http.ResponseWriter, r *http.Request) {
	capabilities := h.textCapabilities(r)
	if h.cfg.DiffusersURL == "" {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityImageGenerate, protocol.RuntimeImage, "image runtime is not configured"), unavailableCapability(protocol.CapabilityImageEdit, protocol.RuntimeImage, "image runtime is not configured"))
	} else if health, err := h.imageClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityImageGenerate, protocol.RuntimeImage, "image runtime is unavailable"), unavailableCapability(protocol.CapabilityImageEdit, protocol.RuntimeImage, "image runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeImage, health)...)
	}
	if h.cfg.SpeechURL == "" {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityAudioTTS, protocol.RuntimeSpeech, "speech runtime is not configured"))
	} else if health, err := h.speechClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityAudioTTS, protocol.RuntimeSpeech, "speech runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeSpeech, health)...)
	}
	writeJSON(w, http.StatusOK, struct {
		Capabilities []protocol.Capability `json:"capabilities"`
	}{Capabilities: capabilities})
}

func (h *handler) textCapabilities(r *http.Request) []protocol.Capability {
	models, err := h.textClient().Models(r.Context())
	if err != nil {
		return []protocol.Capability{unavailableCapability(protocol.CapabilityTextChat, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextCompletion, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextResponses, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextEmbedding, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextVision, protocol.RuntimeText, "text runtime is unavailable")}
	}
	modelsByID := map[protocol.CapabilityID][]string{}
	responsesSupported, _ := h.textClient().SupportsResponses(r.Context())
	for _, model := range models {
		native, err := h.textClient().ModelCapabilities(r.Context(), model)
		if err != nil {
			continue
		}
		for _, capability := range native {
			switch capability {
			case "completion":
				modelsByID[protocol.CapabilityTextChat] = append(modelsByID[protocol.CapabilityTextChat], model)
				modelsByID[protocol.CapabilityTextCompletion] = append(modelsByID[protocol.CapabilityTextCompletion], model)
				if responsesSupported {
					modelsByID[protocol.CapabilityTextResponses] = append(modelsByID[protocol.CapabilityTextResponses], model)
				}
			case "embedding":
				modelsByID[protocol.CapabilityTextEmbedding] = append(modelsByID[protocol.CapabilityTextEmbedding], model)
			case "vision":
				modelsByID[protocol.CapabilityTextVision] = append(modelsByID[protocol.CapabilityTextVision], model)
			}
		}
	}
	definitions := []struct {
		id   protocol.CapabilityID
		path string
	}{{protocol.CapabilityTextChat, "/v1/chat/completions"}, {protocol.CapabilityTextCompletion, "/v1/completions"}}
	if responsesSupported {
		definitions = append(definitions, struct {
			id   protocol.CapabilityID
			path string
		}{protocol.CapabilityTextResponses, "/v1/responses"})
	}
	definitions = append(definitions, struct {
		id   protocol.CapabilityID
		path string
	}{protocol.CapabilityTextEmbedding, "/v1/embeddings"}, struct {
		id   protocol.CapabilityID
		path string
	}{protocol.CapabilityTextVision, "/v1/chat/completions"})
	result := make([]protocol.Capability, 0, len(definitions))
	for _, definition := range definitions {
		if models := normalizedValues(modelsByID[definition.id]); len(models) > 0 {
			result = append(result, protocol.Capability{ID: definition.id, Runtime: protocol.RuntimeText, Status: protocol.CapabilityReady, Models: models, Paths: []string{definition.path}})
		}
	}
	return result
}

func runtimeCapabilities(kind protocol.RuntimeKind, health edgeruntime.RuntimeHealth) []protocol.Capability {
	result := make([]protocol.Capability, 0, len(health.Capabilities))
	for _, capability := range health.Capabilities {
		id := protocol.CapabilityID(capability.ID)
		if !validRuntimeCapability(kind, id) {
			continue
		}
		status := protocol.CapabilityStatus(capability.Status)
		switch status {
		case protocol.CapabilityReady, protocol.CapabilityWarming, protocol.CapabilityDegraded, protocol.CapabilityUnavailable, protocol.CapabilityUnsupported:
		case protocol.CapabilityStatus(edgeruntime.StatusStarting):
			status = protocol.CapabilityWarming
		default:
			status = protocol.CapabilityUnavailable
		}
		result = append(result, protocol.Capability{ID: id, Runtime: kind, Status: status, Models: normalizedValues(capability.Models), Paths: normalizedValues(capability.Paths), Version: strings.TrimSpace(health.Version), Reason: strings.TrimSpace(capability.Reason), Limits: protocol.CapabilityLimits{MaxInputBytes: capability.Limits.MaxInputBytes, MaxInputCharacters: capability.Limits.MaxInputCharacters, Formats: normalizedValues(capability.Limits.Formats)}})
	}
	return result
}

func validRuntimeCapability(kind protocol.RuntimeKind, id protocol.CapabilityID) bool {
	return (kind == protocol.RuntimeImage && (id == protocol.CapabilityImageGenerate || id == protocol.CapabilityImageEdit)) || (kind == protocol.RuntimeSpeech && id == protocol.CapabilityAudioTTS)
}

func unavailableCapability(id protocol.CapabilityID, kind protocol.RuntimeKind, reason string) protocol.Capability {
	return protocol.Capability{ID: id, Runtime: kind, Status: protocol.CapabilityUnavailable, Reason: reason}
}

func normalizedValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
