package console

import (
	"fmt"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func TestPerformanceSamplesUseBoundedPrivacySafeEWMA(t *testing.T) {
	store := NewStore(200)
	base := time.Unix(1_700_000_000, 0).UTC()
	first := store.Start(RequestStart{ID: "one", Model: "qwen3:8b", Path: "/v1/chat/completions", Capability: string(protocol.CapabilityTextChat), Consumer: "customer-private", StartedAt: base})
	store.Finish(first, RequestFinish{CompletedAt: base.Add(time.Second), Duration: time.Second, TTFT: 100 * time.Millisecond, CompletionTokens: 10})
	second := store.Start(RequestStart{ID: "two", Model: "qwen3:8b", Path: "/v1/chat/completions", Capability: string(protocol.CapabilityTextChat), Consumer: "different-private", StartedAt: base.Add(time.Second)})
	store.Finish(second, RequestFinish{CompletedAt: base.Add(1500 * time.Millisecond), Duration: 500 * time.Millisecond, TTFT: 50 * time.Millisecond, CompletionTokens: 20})

	samples := store.PerformanceSamples(base.Add(2 * time.Second))
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(samples))
	}
	sample := samples[0]
	if sample.Runtime != protocol.RuntimeText || sample.Capability != protocol.CapabilityTextChat || sample.Model != "qwen3:8b" {
		t.Fatalf("unexpected sample identity: %+v", sample)
	}
	if sample.TTFTMs != 88 || sample.DurationMs != 875 || sample.OutputUnits != 13 || sample.UnitsPerSecond != 17.5 || !sample.Succeeded {
		t.Fatalf("unexpected EWMA values: %+v", sample)
	}
}

func TestPerformanceSamplesAreCappedAndExpire(t *testing.T) {
	store := NewStore(200)
	base := time.Unix(1_700_000_000, 0).UTC()
	for index := 0; index < protocol.MaxPerformanceSamples+5; index++ {
		handle := store.Start(RequestStart{ID: fmt.Sprintf("request-%d", index), Model: fmt.Sprintf("model-%d", index), Path: "/v1/chat/completions", Capability: string(protocol.CapabilityTextChat), StartedAt: base})
		store.Finish(handle, RequestFinish{CompletedAt: base.Add(time.Duration(index) * time.Second), Duration: time.Second, CompletionTokens: 1})
	}
	if samples := store.PerformanceSamples(base.Add(time.Minute)); len(samples) != protocol.MaxPerformanceSamples {
		t.Fatalf("samples = %d, want cap %d", len(samples), protocol.MaxPerformanceSamples)
	}
	if samples := store.PerformanceSamples(base.Add(12 * time.Minute)); len(samples) != 0 {
		t.Fatalf("stale samples = %d, want 0", len(samples))
	}
}
