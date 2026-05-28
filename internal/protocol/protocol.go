// Package protocol mirrors the EveryAPI edge node WebSocket protocol
// definitions from backend/pkg/edge in the gateway repo. This file is
// intentionally a DUPLICATE — the agent module is independent and
// can't import the gateway-side package without dragging in the full
// backend module dependency tree.
//
// IMPORTANT: any change to backend/pkg/edge/protocol.go or types.go
// MUST be mirrored here in the same PR. A future refactor will move
// these definitions into clients/sdk so both sides import from a
// shared module; until then, treat the gateway-side file as
// canonical and copy from there.
package protocol

import (
	"encoding/json"
	"time"
)

const (
	ProtocolVersion = "1.0"

	HeartbeatInterval = 10 * time.Second
	HeartbeatTimeout  = 30 * time.Second

	MaxFrameBytes = 1 << 20 // 1 MiB
)

type FrameType string

const (
	FrameAuth       FrameType = "auth"
	FrameWelcome    FrameType = "welcome"
	FrameHeartbeat  FrameType = "heartbeat"
	FrameRequest    FrameType = "request"
	FrameChunk      FrameType = "chunk"
	FrameDone       FrameType = "done"
	FrameError      FrameType = "error"
	FrameDisconnect FrameType = "disconnect"
	FrameLog        FrameType = "log"
)

type Frame struct {
	Type FrameType       `json:"type"`
	ID   string          `json:"id,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

type AuthBody struct {
	NodeID            int64    `json:"node_id"`
	ProtocolVersion   string   `json:"protocol_version"`
	AgentVersion      string   `json:"agent_version"`
	RegistrationToken string   `json:"registration_token,omitempty"`
	Pubkey            string   `json:"pubkey,omitempty"`
	Challenge         string   `json:"challenge,omitempty"`
	Signature         string   `json:"signature,omitempty"`
	Meta              NodeMeta `json:"meta"`
}

type WelcomeBody struct {
	SessionID         string   `json:"session_id"`
	ProtocolVersion   string   `json:"protocol_version"`
	RecommendedModels []string `json:"recommended_models,omitempty"`
}

type HeartbeatBody struct {
	NowUnixMs  int64   `json:"now_unix_ms"`
	GPUUtilPct int     `json:"gpu_util_pct,omitempty"`
	VRAMUsedGB float64 `json:"vram_used_gb,omitempty"`
	ActiveReqs int     `json:"active_requests,omitempty"`
}

type RequestBody struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
	Stream  bool              `json:"stream,omitempty"`
}

type ChunkBody struct {
	StatusCode int               `json:"status_code,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Bytes      string            `json:"bytes,omitempty"` // base64
}

type DoneBody struct {
	PromptTokens     int   `json:"prompt_tokens,omitempty"`
	CompletionTokens int   `json:"completion_tokens,omitempty"`
	DurationMs       int64 `json:"duration_ms,omitempty"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DisconnectBody struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// Disconnect codes. Mirrors backend/pkg/edge/protocol.go — see that
// file for the canonical-source policy. The terminal codes here drive
// the agent's "stop reconnecting" decision in main.go's
// runWithReconnect; everything else is treated as transient and
// retried with exponential backoff.
const (
	// DisconnectCodeNodeRevoked — gateway soft-deleted the EdgeNode
	// row while this session was live. Terminal on the agent side:
	// persist a sentinel and exit so the seller doesn't have to
	// chase docker logs to understand the spin loop.
	DisconnectCodeNodeRevoked = "node_revoked"
)

// LogBody — single line of agent log output. The agent's logger hooks
// into Client.SendLog which serialises to a Frame{Type: FrameLog};
// the gateway's per-session ring buffer (backend/internal/edge) holds
// the most recent ~200 lines.
type LogBody struct {
	UnixMs int64  `json:"unix_ms"`
	Level  string `json:"level,omitempty"`
	Msg    string `json:"msg"`
}
