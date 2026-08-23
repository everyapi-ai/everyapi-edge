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
