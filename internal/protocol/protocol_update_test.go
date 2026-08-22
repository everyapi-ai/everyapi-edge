package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUpdateProtocolBodiesRoundTrip(t *testing.T) {
	command := UpdateBody{Action: UpdateActionLatest}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var got UpdateBody
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != command || FrameUpdate != FrameType("update") || FrameUpdateStatus != FrameType("update_status") {
		t.Fatalf("update protocol did not round-trip: %#v", got)
	}
}

func TestNodeMetaCarriesTypedCapabilities(t *testing.T) {
	field, ok := reflect.TypeOf(NodeMeta{}).FieldByName("Capabilities")
	if !ok {
		t.Fatal("NodeMeta has no typed Capabilities field")
	}
	if got := field.Type.String(); got != "[]protocol.Capability" {
		t.Fatalf("Capabilities type = %s", got)
	}
}

func TestCapabilityForRequestUsesExactProtocolPaths(t *testing.T) {
	tests := map[string]CapabilityID{
		"/api/chat":                CapabilityTextChat,
		"/v1/chat/completions":     CapabilityTextChat,
		"/v1/completions":          CapabilityTextCompletion,
		"/v1/responses":            CapabilityTextResponses,
		"/v1/embeddings":           CapabilityTextEmbedding,
		"/v1/images/generations":   CapabilityImageGenerate,
		"/v1/images/edits":         CapabilityImageEdit,
		"/v1/audio/speech":         CapabilityAudioTTS,
		"/v1/audio/transcriptions": CapabilityAudioTranscription,
		"/v1/audio/translations":   CapabilityAudioTranslation,
	}
	for path, want := range tests {
		got, ok := CapabilityForRequest(path)
		if !ok || got != want {
			t.Errorf("CapabilityForRequest(%q) = %q, %v; want %q, true", path, got, ok, want)
		}
	}
	if got, ok := CapabilityForRequest("/v1/images/unknown"); ok || got != "" {
		t.Fatalf("unsupported path mapped to %q, %v", got, ok)
	}
}
