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
		"/v1/videos":               CapabilityVideoGenerate,
		"/v1/video/generations":    CapabilityVideoGenerate,
		"/v1/render/jobs/render_1": CapabilityRenderExecute,
		"/v1/render/jobs":          CapabilityRenderExecute,
		"/v1/rerank":               CapabilityTextRerank,
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

func TestCompleteOperationsProtocolRoundTrip(t *testing.T) {
	if ProtocolVersion != "1.3" {
		t.Fatalf("protocol version = %q, want 1.3", ProtocolVersion)
	}
	policy := ResourcePolicy{
		Text:   RuntimeResourcePolicy{MaxConcurrent: 4, ReserveVRAMMB: 1024},
		Image:  RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 4096},
		Speech: RuntimeResourcePolicy{MaxConcurrent: 2},
		Video:  RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 8192},
		Render: RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 8192},
		Rerank: RuntimeResourcePolicy{MaxConcurrent: 2, ReserveVRAMMB: 2048},
	}
	meta := NodeMeta{ResourcePolicy: policy}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var gotMeta NodeMeta
	if err := json.Unmarshal(raw, &gotMeta); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotMeta.ResourcePolicy, policy) {
		t.Fatalf("resource policy = %#v, want %#v", gotMeta.ResourcePolicy, policy)
	}

	heartbeat := HeartbeatBody{
		DrainState:  DrainStateDraining,
		Performance: []RuntimePerformanceSample{{Runtime: RuntimeText, Capability: CapabilityTextChat, Model: "llama3.1:8b", TTFTMs: 125, DurationMs: 1000, OutputUnits: 20, UnitsPerSecond: 20, Succeeded: true, UnixMs: 1_700_000_000_000}},
	}
	raw, err = json.Marshal(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	var gotHeartbeat HeartbeatBody
	if err := json.Unmarshal(raw, &gotHeartbeat); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotHeartbeat, heartbeat) {
		t.Fatalf("heartbeat = %#v, want %#v", gotHeartbeat, heartbeat)
	}

	diagnostics := DiagnosticsBody{Events: []DiagnosticEvent{{UnixMs: 1_700_000_000_001, Level: "error", Code: "runtime_degraded", Runtime: RuntimeImage, Message: "local runtime health check failed"}}}
	raw, err = json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var gotDiagnostics DiagnosticsBody
	if err := json.Unmarshal(raw, &gotDiagnostics); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotDiagnostics, diagnostics) || FrameDiagnostics != FrameType("diagnostics") {
		t.Fatalf("diagnostics protocol did not round-trip: %#v", gotDiagnostics)
	}

	status := DrainStatusBody{State: DrainStateDrained, ActiveRequests: 0}
	raw, err = json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var gotStatus DrainStatusBody
	if err := json.Unmarshal(raw, &gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status || FrameDrain != FrameType("drain") || FrameDrainStatus != FrameType("drain_status") {
		t.Fatalf("drain protocol did not round-trip: %#v", gotStatus)
	}
}

func TestCapabilityLimitsPublishSpeechDiscovery(t *testing.T) {
	limits := CapabilityLimits{Formats: []string{"mp3", "wav"}, Voices: []string{"af_alloy", "zf_xiaobei"}, Languages: []string{"en", "zh"}}
	raw, err := json.Marshal(limits)
	if err != nil {
		t.Fatal(err)
	}
	var got CapabilityLimits
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, limits) {
		t.Fatalf("limits = %#v, want %#v", got, limits)
	}
}

func TestDrainCommandAndDetailedUpdateStatusRoundTrip(t *testing.T) {
	drain := DrainBody{Action: DrainActionStart}
	raw, err := json.Marshal(drain)
	if err != nil {
		t.Fatal(err)
	}
	var gotDrain DrainBody
	if err := json.Unmarshal(raw, &gotDrain); err != nil {
		t.Fatal(err)
	}
	if gotDrain != drain || DrainActionCancel != "cancel" {
		t.Fatalf("drain command did not round-trip: %#v", gotDrain)
	}

	status := UpdateStatusBody{State: UpdateStateFailed, Version: "1.3.0", CheckedAtUnixMs: 1_700_000_000_000, NextCheckAtUnixMs: 1_700_086_400_000, InstalledVersion: "1.2.9", LatestVersion: "1.3.0", RollbackReason: "candidate exited before reconnect"}
	raw, err = json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var gotStatus UpdateStatusBody
	if err := json.Unmarshal(raw, &gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status {
		t.Fatalf("update status did not round-trip: %#v", gotStatus)
	}
}
