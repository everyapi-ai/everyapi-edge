// Package console provides the supplier-local Edge Control Room. It is kept
// deliberately separate from the gateway protocol: local operational data is
// useful even while the gateway is unreachable, and request bodies never enter
// this package.
package console

import (
	"sort"
	"sync"
	"time"
)

// maxLogMessageBytes prevents a failing dependency or an untrusted peer from
// turning the bounded log entry count into unbounded process memory use.
const maxLogMessageBytes = 4 * 1024

// RequestStart contains the only request metadata the local UI is allowed to
// retain. In particular, it deliberately has no request body or headers.
type RequestStart struct {
	ID        string
	Model     string
	Path      string
	Consumer  string
	StartedAt time.Time
}

// RequestFinish records the terminal usage supplied by Ollama.
type RequestFinish struct {
	CompletedAt      time.Time
	PromptTokens     int
	CompletionTokens int
	Duration         time.Duration
	Error            string
}

// Request is a redacted local request history entry.
type Request struct {
	ID               string    `json:"id"`
	Model            string    `json:"model"`
	Path             string    `json:"path"`
	Consumer         string    `json:"consumer"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	DurationMs       int64     `json:"duration_ms,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	Error            string    `json:"error,omitempty"`
}

// LogEntry is a bounded copy of the agent's own log line. It is local process
// telemetry, not an upstream inference transcript.
type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Overview is the live operational rollup exposed by /api/overview.
type Overview struct {
	ActiveRequests           int   `json:"active_requests"`
	CompletedRequests        int64 `json:"completed_requests"`
	FailedRequests           int64 `json:"failed_requests"`
	PromptTokens             int64 `json:"prompt_tokens"`
	CompletionTokens         int64 `json:"completion_tokens"`
	LoadedVRAMBytes          int64 `json:"loaded_vram_bytes"`
	VRAMTotalGB              int   `json:"vram_total_gb"`
	SettledEarningsMicros    int64 `json:"settled_earnings_micros"`
	SettledEarningsAvailable bool  `json:"settled_earnings_available"`
}

// Settlement is a gateway-committed, node-specific seller receipt. Amount is
// USD micros so the control room never has to infer a currency conversion.
type Settlement struct {
	RequestID          string    `json:"request_id"`
	SellerAmountMicros int64     `json:"seller_amount_micros"`
	SettledAt          time.Time `json:"settled_at"`
}

// RequestHandle is returned by Start and handed to Finish. It avoids exposing
// request maps to the forwarder and makes double-finishes harmless.
type RequestHandle struct{ id string }

// Store is a bounded in-memory record of the current process. Restarting the
// agent clears it; durable billing remains authoritative at the gateway.
type Store struct {
	mu                sync.RWMutex
	capacity          int
	active            map[string]*Request
	history           []Request
	settlements       map[string]Settlement
	settlementHistory []Settlement
	logs              []LogEntry
	overview          Overview
}

// Log appends one local agent line. Standard log output is already line-based;
// the caller removes trailing newlines before recording it.
func (s *Store) Log(level, message string) {
	if message == "" {
		return
	}
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
	return &Store{capacity: capacity, active: make(map[string]*Request), settlements: make(map[string]Settlement)}
}

// Settle stores one final receipt. It is deliberately idempotent because the
// gateway replays recent committed receipts after an agent reconnects.
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
	r := &Request{ID: start.ID, Model: start.Model, Path: start.Path, Consumer: start.Consumer, StartedAt: start.StartedAt}
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
	r.PromptTokens = finish.PromptTokens
	r.CompletionTokens = finish.CompletionTokens
	r.Error = finish.Error
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
