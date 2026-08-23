// Package console provides the supplier-local Edge Control Room. It is kept deliberately separate from the gateway protocol: local operational data is useful even while the gateway is unreachable, and request bodies never enter this package.
package console

import (
	"math"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// maxLogMessageBytes prevents a failing dependency or an untrusted peer from turning the bounded log entry count into unbounded process memory use.
const maxLogMessageBytes = 4 * 1024

var runtimeBrand = regexp.MustCompile(`(?i)\bollama\b`)

func sanitizeRuntimeBrand(message string) string {
	return runtimeBrand.ReplaceAllString(message, "local runtime")
}

// RequestStart contains the only request metadata the local UI is allowed to retain. In particular, it deliberately has no request body or headers.
type RequestStart struct {
	ID         string
	Model      string
	Path       string
	Capability string
	Consumer   string
	StartedAt  time.Time
}

// RequestFinish records the terminal usage supplied by Ollama.
type RequestFinish struct {
	CompletedAt      time.Time
	PromptTokens     int
	CompletionTokens int
	Duration         time.Duration
	TTFT             time.Duration
	Error            string
}

// Request is a redacted local request history entry.
type Request struct {
	ID               string    `json:"id"`
	Model            string    `json:"model"`
	Path             string    `json:"path"`
	Capability       string    `json:"capability,omitempty"`
	Consumer         string    `json:"consumer"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	DurationMs       int64     `json:"duration_ms,omitempty"`
	TTFTMs           int64     `json:"ttft_ms,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	Error            string    `json:"error,omitempty"`
}

// LogEntry is a bounded copy of the agent's own log line. It is local process telemetry, not an upstream inference transcript.
type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Overview is the live operational rollup exposed by /api/overview.
type Overview struct {
	AgentVersion             string    `json:"agent_version"`
	UpdateState              string    `json:"update_state,omitempty"`
	UpdateVersion            string    `json:"update_version,omitempty"`
	UpdateError              string    `json:"update_error,omitempty"`
	ActiveRequests           int       `json:"active_requests"`
	CompletedRequests        int64     `json:"completed_requests"`
	FailedRequests           int64     `json:"failed_requests"`
	PromptTokens             int64     `json:"prompt_tokens"`
	CompletionTokens         int64     `json:"completion_tokens"`
	LoadedVRAMBytes          int64     `json:"loaded_vram_bytes"`
	VRAMTotalGB              int       `json:"vram_total_gb"`
	ReservedVRAMBytes        int64     `json:"reserved_vram_bytes"`
	AvailableVRAMBytes       int64     `json:"available_vram_bytes"`
	SettledEarningsMicros    int64     `json:"settled_earnings_micros"`
	SettledEarningsAvailable bool      `json:"settled_earnings_available"`
	GatewayState             string    `json:"gateway_state"`
	GatewayLastConnectedAt   time.Time `json:"gateway_last_connected_at,omitempty"`
	GatewayLastError         string    `json:"gateway_last_error,omitempty"`
	GatewayReconnectAttempt  int       `json:"gateway_reconnect_attempt"`
	GatewayNextReconnectAt   time.Time `json:"gateway_next_reconnect_at,omitempty"`
	GatewayRoundTripMs       int64     `json:"gateway_round_trip_ms,omitempty"`
}

// Settlement is a gateway-committed, node-specific seller receipt. Amount is USD micros so the control room never has to infer a currency conversion.
type Settlement struct {
	RequestID          string    `json:"request_id"`
	SellerAmountMicros int64     `json:"seller_amount_micros"`
	SettledAt          time.Time `json:"settled_at"`
}

// RequestHandle is returned by Start and handed to Finish. It avoids exposing request maps to the forwarder and makes double-finishes harmless.
type RequestHandle struct{ id string }

// Store is a bounded in-memory record of the current process. Restarting the agent clears it; durable billing remains authoritative at the gateway.
type Store struct {
	mu                sync.RWMutex
	capacity          int
	active            map[string]*Request
	history           []Request
	settlements       map[string]Settlement
	settlementHistory []Settlement
	logs              []LogEntry
	overview          Overview
	performance       map[string]protocol.RuntimePerformanceSample
}

// Log appends one local agent line. Standard log output is already line-based; the caller removes trailing newlines before recording it.
func (s *Store) Log(level, message string) {
	if message == "" {
		return
	}
	message = sanitizeRuntimeBrand(message)
	if len(message) > maxLogMessageBytes {
		const truncation = "…(truncated)"
		message = message[:maxLogMessageBytes-len(truncation)] + truncation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, LogEntry{At: time.Now().UTC(), Level: level, Message: message})
	if len(s.logs) > s.capacity {
		s.logs = append([]LogEntry(nil), s.logs[len(s.logs)-s.capacity:]...)
	}
}

func (s *Store) Logs() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]LogEntry, len(s.logs))
	for i := range s.logs {
		items[len(s.logs)-1-i] = s.logs[i]
	}
	return items
}

func NewStore(capacity int) *Store {
	if capacity <= 0 {
		capacity = 200
	}
	return &Store{capacity: capacity, active: make(map[string]*Request), settlements: make(map[string]Settlement), performance: make(map[string]protocol.RuntimePerformanceSample), overview: Overview{GatewayState: "connecting"}}
}

// SetGatewayState records the real upstream session state separately from the local control room HTTP listener. A reachable local page must not imply that the node is currently able to receive gateway work.
func (s *Store) SetGatewayState(state, reason string) {
	if state != "connecting" && state != "online" && state != "offline" && state != "preview" {
		state = "offline"
	}
	reason = sanitizeRuntimeBrand(reason)
	if len(reason) > maxLogMessageBytes {
		reason = reason[:maxLogMessageBytes]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overview.GatewayState = state
	s.overview.GatewayLastError = reason
	if state == "online" {
		s.overview.GatewayLastConnectedAt = time.Now().UTC()
		s.overview.GatewayLastError = ""
		s.overview.GatewayReconnectAttempt = 0
		s.overview.GatewayNextReconnectAt = time.Time{}
	}
	if state == "preview" {
		s.overview.GatewayReconnectAttempt = 0
		s.overview.GatewayNextReconnectAt = time.Time{}
	}
}

// ScheduleGatewayReconnect records a retry the agent has actually scheduled. It intentionally does not derive a date from a generic offline state: an authentication or terminal failure must never be presented as a promised reconnect that the process will not make.
func (s *Store) ScheduleGatewayReconnect(next time.Time, attempt int) {
	if next.IsZero() || attempt < 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overview.GatewayReconnectAttempt = attempt
	s.overview.GatewayNextReconnectAt = next.UTC()
}

func (s *Store) SetGatewayRoundTrip(duration time.Duration) {
	if duration < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overview.GatewayRoundTripMs = duration.Milliseconds()
}

// Settle stores one final receipt. It is deliberately idempotent because the gateway replays recent committed receipts after an agent reconnects.
func (s *Store) Settle(receipt Settlement) {
	if receipt.RequestID == "" || receipt.SettledAt.IsZero() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.settlements[receipt.RequestID]; exists {
		return
	}
	s.settlements[receipt.RequestID] = receipt
	s.settlementHistory = append(s.settlementHistory, receipt)
	s.overview.SettledEarningsAvailable = true
	s.overview.SettledEarningsMicros += receipt.SellerAmountMicros
	if len(s.settlementHistory) > s.capacity {
		old := s.settlementHistory[0]
		delete(s.settlements, old.RequestID)
		s.overview.SettledEarningsMicros -= old.SellerAmountMicros
		s.settlementHistory = append([]Settlement(nil), s.settlementHistory[len(s.settlementHistory)-s.capacity:]...)
	}
}

func (s *Store) Settlements() []Settlement {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := append([]Settlement(nil), s.settlementHistory...)
	sort.Slice(items, func(i, j int) bool { return items[i].SettledAt.After(items[j].SettledAt) })
	return items
}

func (s *Store) Start(start RequestStart) RequestHandle {
	if start.StartedAt.IsZero() {
		start.StartedAt = time.Now().UTC()
	}
	if start.Consumer == "" {
		start.Consumer = "gateway customer"
	}
	r := &Request{ID: start.ID, Model: start.Model, Path: start.Path, Capability: start.Capability, Consumer: start.Consumer, StartedAt: start.StartedAt}
	s.mu.Lock()
	s.active[start.ID] = r
	s.overview.ActiveRequests = len(s.active)
	s.mu.Unlock()
	return RequestHandle{id: start.ID}
}

func (s *Store) Finish(handle RequestHandle, finish RequestFinish) {
	if finish.CompletedAt.IsZero() {
		finish.CompletedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.active[handle.id]
	if !ok {
		return
	}
	delete(s.active, handle.id)
	s.overview.ActiveRequests = len(s.active)
	r.CompletedAt = finish.CompletedAt
	r.DurationMs = finish.Duration.Milliseconds()
	r.TTFTMs = finish.TTFT.Milliseconds()
	r.PromptTokens = finish.PromptTokens
	r.CompletionTokens = finish.CompletionTokens
	r.Error = sanitizeRuntimeBrand(finish.Error)
	s.overview.CompletedRequests++
	if finish.Error != "" {
		s.overview.FailedRequests++
	} else {
		s.overview.PromptTokens += int64(finish.PromptTokens)
		s.overview.CompletionTokens += int64(finish.CompletionTokens)
	}
	s.history = append(s.history, *r)
	if len(s.history) > s.capacity {
		s.history = append([]Request(nil), s.history[len(s.history)-s.capacity:]...)
	}
	s.updatePerformance(*r, finish)
}

const (
	performanceEWMAAlpha = 0.25
	performanceMaxAge    = 10 * time.Minute
)

func (s *Store) updatePerformance(request Request, finish RequestFinish) {
	capability := protocol.CapabilityID(request.Capability)
	runtimeKind := runtimeForCapability(capability)
	if runtimeKind == "" || request.DurationMs <= 0 || request.CompletedAt.IsZero() {
		return
	}
	model := request.Model
	if model == "unknown" {
		model = ""
	}
	outputUnits := int64(finish.CompletionTokens)
	if outputUnits <= 0 && finish.Error == "" {
		outputUnits = 1
	}
	unitsPerSecond := float64(0)
	if outputUnits > 0 {
		unitsPerSecond = float64(outputUnits) / finish.Duration.Seconds()
	}
	sample := protocol.RuntimePerformanceSample{Runtime: runtimeKind, Capability: capability, Model: model, TTFTMs: request.TTFTMs, DurationMs: request.DurationMs, OutputUnits: outputUnits, UnitsPerSecond: unitsPerSecond, Succeeded: finish.Error == "", UnixMs: request.CompletedAt.UnixMilli()}
	key := string(runtimeKind) + "\x00" + string(capability) + "\x00" + model
	if previous, ok := s.performance[key]; ok {
		sample.TTFTMs = ewmaInt(previous.TTFTMs, sample.TTFTMs)
		sample.DurationMs = ewmaInt(previous.DurationMs, sample.DurationMs)
		sample.OutputUnits = ewmaInt(previous.OutputUnits, sample.OutputUnits)
		sample.UnitsPerSecond = ewmaFloat(previous.UnitsPerSecond, sample.UnitsPerSecond)
	}
	s.performance[key] = sample
	if len(s.performance) <= protocol.MaxPerformanceSamples {
		return
	}
	oldestKey := ""
	oldestAt := int64(math.MaxInt64)
	for candidateKey, candidate := range s.performance {
		if candidate.UnixMs < oldestAt {
			oldestKey, oldestAt = candidateKey, candidate.UnixMs
		}
	}
	delete(s.performance, oldestKey)
}

func ewmaInt(previous, current int64) int64 {
	if previous <= 0 {
		return current
	}
	if current <= 0 {
		return previous
	}
	return int64(math.Round((1-performanceEWMAAlpha)*float64(previous) + performanceEWMAAlpha*float64(current)))
}

func ewmaFloat(previous, current float64) float64 {
	if previous <= 0 {
		return current
	}
	if current <= 0 {
		return previous
	}
	return (1-performanceEWMAAlpha)*previous + performanceEWMAAlpha*current
}

func runtimeForCapability(capability protocol.CapabilityID) protocol.RuntimeKind {
	switch capability {
	case protocol.CapabilityImageGenerate, protocol.CapabilityImageEdit:
		return protocol.RuntimeImage
	case protocol.CapabilityAudioTTS, protocol.CapabilityAudioTranscription, protocol.CapabilityAudioTranslation:
		return protocol.RuntimeSpeech
	case protocol.CapabilityVideoGenerate:
		return protocol.RuntimeVideo
	case protocol.CapabilityRenderExecute:
		return protocol.RuntimeRender
	case protocol.CapabilityTextRerank:
		return protocol.RuntimeRerank
	case protocol.CapabilityTextChat, protocol.CapabilityTextCompletion, protocol.CapabilityTextResponses, protocol.CapabilityTextEmbedding, protocol.CapabilityTextVision:
		return protocol.RuntimeText
	default:
		return ""
	}
}

func (s *Store) PerformanceSamples(now time.Time) []protocol.RuntimePerformanceSample {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cutoff := now.Add(-performanceMaxAge).UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	samples := make([]protocol.RuntimePerformanceSample, 0, len(s.performance))
	for key, sample := range s.performance {
		if sample.UnixMs < cutoff {
			delete(s.performance, key)
			continue
		}
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].UnixMs != samples[j].UnixMs {
			return samples[i].UnixMs > samples[j].UnixMs
		}
		if samples[i].Capability != samples[j].Capability {
			return samples[i].Capability < samples[j].Capability
		}
		return samples[i].Model < samples[j].Model
	})
	return samples
}

func (s *Store) Overview() Overview {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.overview
}

func (s *Store) Requests() []Request {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := append([]Request(nil), s.history...)
	sort.Slice(items, func(i, j int) bool { return items[i].CompletedAt.After(items[j].CompletedAt) })
	return items
}
