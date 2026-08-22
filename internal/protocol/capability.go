package protocol

import "strings"

func CapabilityForRequest(path string) (CapabilityID, bool) {
	switch strings.ToLower(path) {
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
	default:
		return "", false
	}
}
