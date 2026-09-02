package protocol

import "strings"

func CapabilityForRequest(path string) (CapabilityID, bool) {
	lowerPath := strings.ToLower(path)
	if strings.HasPrefix(lowerPath, "/v1/videos/") {
		return CapabilityVideoGenerate, true
	}
	if strings.HasPrefix(lowerPath, "/v1/render/jobs/") {
		return CapabilityRenderExecute, true
	}
	switch lowerPath {
	case "/v1/chat/completions", "/api/chat":
		return CapabilityTextChat, true
	case "/v1/completions":
		return CapabilityTextCompletion, true
	case "/v1/responses":
		return CapabilityTextResponses, true
	case "/v1/embeddings":
		return CapabilityTextEmbedding, true
	case "/v1/images/generations":
		return CapabilityImageGenerate, true
	case "/v1/images/edits":
		return CapabilityImageEdit, true
	case "/v1/audio/speech":
		return CapabilityAudioTTS, true
	case "/v1/audio/transcriptions":
		return CapabilityAudioTranscription, true
	case "/v1/audio/translations":
		return CapabilityAudioTranslation, true
	case "/v1/videos", "/v1/video/generations":
		return CapabilityVideoGenerate, true
	case "/v1/render/jobs":
		return CapabilityRenderExecute, true
	case "/v1/rerank":
		return CapabilityTextRerank, true
	default:
		return "", false
	}
}

// runtimeStartingStatus mirrors the "starting" value the Python runtimes report from `/health` before a model finishes warming. It is deliberately not part of the CapabilityStatus set on the wire: the gateway only ever sees the normalized form.
const runtimeStartingStatus CapabilityStatus = "starting"

// CapabilityBelongsToRuntime reports whether a runtime of this kind is allowed to serve this capability. A runtime that advertises somebody else's capability is ignored rather than trusted, so a misconfigured local service cannot make the node claim work it cannot do.
func CapabilityBelongsToRuntime(id CapabilityID, kind RuntimeKind) bool {
	switch kind {
	case RuntimeText:
		return id == CapabilityTextChat || id == CapabilityTextCompletion || id == CapabilityTextEmbedding || id == CapabilityTextVision
	case RuntimeImage:
		return id == CapabilityImageGenerate || id == CapabilityImageEdit
	case RuntimeSpeech:
		return id == CapabilityAudioTTS || id == CapabilityAudioTranscription || id == CapabilityAudioTranslation
	case RuntimeVideo:
		return id == CapabilityVideoGenerate
	case RuntimeRender:
		return id == CapabilityRenderExecute
	case RuntimeRerank:
		return id == CapabilityTextRerank
	default:
		return false
	}
}

// NormalizeCapabilityStatus maps whatever a local runtime reported onto the closed set the protocol defines. Anything unrecognised becomes unavailable rather than being forwarded, so an unknown status can never be mistaken for a billable one. The session baseline and the periodic probe must both run a reported capability through this, or an unchanged node produces a permanent fingerprint mismatch and re-registers forever.
func NormalizeCapabilityStatus(status CapabilityStatus) CapabilityStatus {
	switch status {
	case CapabilityReady, CapabilityWarming, CapabilityDegraded, CapabilityUnavailable, CapabilityUnsupported:
		return status
	case runtimeStartingStatus:
		return CapabilityWarming
	default:
		return CapabilityUnavailable
	}
}
