package console

import (
	"context"
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
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityAudioTTS, protocol.RuntimeSpeech, "speech runtime is not configured"), unavailableCapability(protocol.CapabilityAudioTranscription, protocol.RuntimeSpeech, "speech runtime is not configured"), unavailableCapability(protocol.CapabilityAudioTranslation, protocol.RuntimeSpeech, "speech runtime is not configured"))
	} else if health, err := h.speechClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityAudioTTS, protocol.RuntimeSpeech, "speech runtime is unavailable"), unavailableCapability(protocol.CapabilityAudioTranscription, protocol.RuntimeSpeech, "speech runtime is unavailable"), unavailableCapability(protocol.CapabilityAudioTranslation, protocol.RuntimeSpeech, "speech runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeSpeech, health)...)
	}
	if h.cfg.TranscriptionURL == "" {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityAudioTranscription, protocol.RuntimeSpeech, "transcription runtime is not configured"), unavailableCapability(protocol.CapabilityAudioTranslation, protocol.RuntimeSpeech, "transcription runtime is not configured"))
	} else if health, err := h.transcriptionClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityAudioTranscription, protocol.RuntimeSpeech, "transcription runtime is unavailable"), unavailableCapability(protocol.CapabilityAudioTranslation, protocol.RuntimeSpeech, "transcription runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeSpeech, health)...)
	}
	if h.cfg.VideoURL == "" {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityVideoGenerate, protocol.RuntimeVideo, "video runtime is not configured"))
	} else if health, err := h.videoClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityVideoGenerate, protocol.RuntimeVideo, "video runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeVideo, health)...)
	}
	if h.cfg.RenderURL == "" {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityRenderExecute, protocol.RuntimeRender, "render runtime is not configured"))
	} else if health, err := h.renderClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityRenderExecute, protocol.RuntimeRender, "render runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeRender, health)...)
	}
	if h.cfg.RerankURL == "" {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityTextRerank, protocol.RuntimeRerank, "rerank runtime is not configured"))
	} else if health, err := h.rerankClient().Health(r.Context()); err != nil {
		capabilities = append(capabilities, unavailableCapability(protocol.CapabilityTextRerank, protocol.RuntimeRerank, "rerank runtime is unavailable"))
	} else {
		capabilities = append(capabilities, runtimeCapabilities(protocol.RuntimeRerank, health)...)
	}
	writeJSON(w, http.StatusOK, struct {
		Capabilities []protocol.Capability `json:"capabilities"`
	}{Capabilities: bestCapabilityPerID(capabilities)})
}

// capabilityStatusRank orders statuses from most to least serviceable so a capability offered by two runtimes is reported at the better of the two.
func capabilityStatusRank(status protocol.CapabilityStatus) int {
	switch status {
	case protocol.CapabilityReady:
		return 0
	case protocol.CapabilityWarming:
		return 1
	case protocol.CapabilityDegraded:
		return 2
	case protocol.CapabilityUnavailable:
		return 3
	default:
		return 4
	}
}

// bestCapabilityPerID collapses the speech and transcription services' overlapping claims. Both can serve audio.transcription and audio.translation, so an unconfigured node used to report each of them twice with contradictory reasons; the forwarder routes to whichever service answers, and this reports that same single truth.
func bestCapabilityPerID(capabilities []protocol.Capability) []protocol.Capability {
	result := make([]protocol.Capability, 0, len(capabilities))
	index := make(map[protocol.CapabilityID]int, len(capabilities))
	for _, capability := range capabilities {
		position, seen := index[capability.ID]
		if !seen {
			index[capability.ID] = len(result)
			result = append(result, capability)
			continue
		}
		if capabilityStatusRank(capability.Status) < capabilityStatusRank(result[position].Status) {
			result[position] = capability
		}
	}
	return result
}

func (h *handler) textCapabilities(r *http.Request) []protocol.Capability {
	models, err := h.textClient().InstalledModels(r.Context())
	if err != nil {
		return []protocol.Capability{unavailableCapability(protocol.CapabilityTextChat, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextCompletion, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextResponses, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextEmbedding, protocol.RuntimeText, "text runtime is unavailable"), unavailableCapability(protocol.CapabilityTextVision, protocol.RuntimeText, "text runtime is unavailable")}
	}
	modelsByID := map[protocol.CapabilityID][]string{}
	responsesSupported, _ := h.textClient().SupportsResponses(r.Context())
	h.forgetUninstalledModelCapabilities(models)
	for _, installed := range models {
		model := installed.Name
		native, err := h.cachedModelCapabilities(r.Context(), installed)
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
		result = append(result, protocol.Capability{ID: id, Runtime: kind, Status: status, Models: normalizedValues(capability.Models), Paths: normalizedValues(capability.Paths), Version: strings.TrimSpace(health.Version), Reason: strings.TrimSpace(capability.Reason), Limits: protocol.CapabilityLimits{MaxInputBytes: capability.Limits.MaxInputBytes, MaxInputCharacters: capability.Limits.MaxInputCharacters, Formats: normalizedValues(capability.Limits.Formats), Voices: normalizedValues(capability.Limits.Voices), Languages: normalizedValues(capability.Limits.Languages)}})
	}
	return result
}

func validRuntimeCapability(kind protocol.RuntimeKind, id protocol.CapabilityID) bool {
	return (kind == protocol.RuntimeImage && (id == protocol.CapabilityImageGenerate || id == protocol.CapabilityImageEdit)) || (kind == protocol.RuntimeSpeech && (id == protocol.CapabilityAudioTTS || id == protocol.CapabilityAudioTranscription || id == protocol.CapabilityAudioTranslation)) || (kind == protocol.RuntimeVideo && id == protocol.CapabilityVideoGenerate) || (kind == protocol.RuntimeRender && id == protocol.CapabilityRenderExecute) || (kind == protocol.RuntimeRerank && id == protocol.CapabilityTextRerank)
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

// cachedModelCapabilities answers from the last reply for this exact model version. A runtime that reports no version is never cached: without a marker there is no way to tell a stale answer from a current one, so it keeps today's behaviour of asking every time.
func (h *handler) cachedModelCapabilities(ctx context.Context, installed edgeruntime.InstalledModel) ([]string, error) {
	if installed.Version != "" {
		h.modelCapabilityMu.Lock()
		entry, ok := h.modelCapabilityCache[installed.Name]
		h.modelCapabilityMu.Unlock()
		if ok && entry.version == installed.Version {
			return entry.capabilities, nil
		}
	}
	native, err := h.textClient().ModelCapabilities(ctx, installed.Name)
	if err != nil {
		return nil, err
	}
	if installed.Version == "" {
		return native, nil
	}
	h.modelCapabilityMu.Lock()
	if h.modelCapabilityCache == nil {
		h.modelCapabilityCache = map[string]cachedModelCapability{}
	}
	h.modelCapabilityCache[installed.Name] = cachedModelCapability{version: installed.Version, capabilities: native}
	h.modelCapabilityMu.Unlock()
	return native, nil
}

// forgetUninstalledModelCapabilities keeps the cache the size of the model library rather than of everything the node has ever had installed.
func (h *handler) forgetUninstalledModelCapabilities(models []edgeruntime.InstalledModel) {
	h.modelCapabilityMu.Lock()
	defer h.modelCapabilityMu.Unlock()
	if len(h.modelCapabilityCache) == 0 {
		return
	}
	installed := make(map[string]struct{}, len(models))
	for _, model := range models {
		installed[model.Name] = struct{}{}
	}
	for name := range h.modelCapabilityCache {
		if _, ok := installed[name]; !ok {
			delete(h.modelCapabilityCache, name)
		}
	}
}
