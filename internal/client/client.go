// Package client owns the agent's WebSocket connection to the EveryAPI gateway: opens the connection, completes Auth, runs the read/write loops until terminated, and surfaces inbound Request frames to a Handler (the forwarder in internal/forward, wired by main).
//
// One Client = one logical "connect or die"; main.go wraps it in the reconnect loop so a transient network blip doesn't take the agent down.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// Config is what main.go passes in. Everything is required except HTTPClient (defaulted) and Handler (allowed nil for tests).
type Config struct {
	// GatewayURL — base URL of the EveryAPI gateway, e.g. https://api.everyapi.ai. The agent derives WS / HTTP endpoints off this base (wss host for /edge/connect, https for the challenge endpoint).
	GatewayURL string
	// NodeID is the EdgeNode primary key the gateway minted when the seller registered through the dashboard.
	NodeID int64
	// RegistrationToken is the one-time secret returned with the node row. Used on FIRST connect only; subsequent connects rely on the Ed25519 identity. Empty after the first successful Welcome.
	RegistrationToken string
	// Identity is the loaded Ed25519 keypair (see internal/identity).
	Identity identity.Decoded
	// Meta is the snapshot the agent reports on every connect — hardware, location, currently-installed models, agent version.
	Meta protocol.NodeMeta
	// TextModels is the Ollama-only model snapshot. Meta.Models also contains image and speech models, so it cannot safely detect out-of-band text model changes by itself.
	TextModels []string
	// TextResponsesSupported records the Ollama version feature gate independently of whether an installed completion model currently advertises that capability.
	TextResponsesSupported bool
	// Handler is invoked for every inbound Request frame. The returned io.Reader streams response chunks; closing it signals the agent that the request is done. A nil Handler drops Request frames on the floor (test-only).
	Handler RequestHandler
	// OnConnected runs immediately after the gateway accepts the Auth frame and sends Welcome. Callers must return promptly; it is used by the agent entrypoint to make a successful first registration observable to its installer.
	OnConnected func()
	// HTTPClient is used for the challenge endpoint. Defaulted in New() if nil; injectable for tests.
	HTTPClient *http.Client
	// ResourcePolicy caps inbound work independently for each runtime so a long media job cannot consume the text pool. Zero fields receive safe defaults in New.
	ResourcePolicy protocol.ResourcePolicy
	// MaxConcurrentRequests is retained as a legacy test/config fallback. When ResourcePolicy is empty and this is positive, the value is applied to every runtime; production wiring uses ResourcePolicy.
	MaxConcurrentRequests int
	// Log receives agent diagnostics that would otherwise only go to stderr. main routes this into both docker logs and the supplier-local console. OllamaURL is the local model runtime, used only by the auto-pull path: the gateway's Welcome frame names models this node is missing from its owner's declared target set, and the agent pulls them here. Empty disables auto-pull.
	OllamaURL string
	// Local runtime URLs are queried only for their bounded /health resource snapshots so heartbeat scheduling accounts for every GPU runtime managed by this node.
	DiffusersURL     string
	SpeechURL        string
	TranscriptionURL string
	VideoURL         string
	RenderURL        string
	RerankURL        string
	// VRAMTotalGB is the scheduler budget discovered at agent startup. It is sent with every heartbeat so the gateway can preserve a runtime safety reserve when it balances requests across nodes.
	VRAMTotalGB int
	// GatewayRoundTrip receives the measured heartbeat ACK latency over the authenticated WebSocket.
	GatewayRoundTrip func(time.Duration)
	// Performance returns bounded privacy-safe per-runtime EWMAs. It must never include request or response content; heartbeatTelemetry validates the structural envelope before sending it.
	Performance func() []protocol.RuntimePerformanceSample
	// MetadataChanged is a process-lifetime coalesced signal shared by successive Client instances. Sharing it prevents a model mutation that completes between sessions from being lost before the next Auth snapshot is built. New creates a private channel when omitted.
	MetadataChanged chan struct{}

	Log func(level, message string)
	// Settlement receives gateway-committed seller receipts. It must be idempotent because reconnect replay can deliver a receipt twice.
	Settlement func(protocol.SettlementBody)
	// Update runs the fixed latest-stable updater. The gateway controls only when it runs; it cannot supply a version, URL, checksum, or command.
	Update func(context.Context, func(protocol.UpdateStatusBody)) error
	// ControlHandler executes administrator-only Control Room operations. It is optional so older agents reject remote management explicitly.
	ControlHandler ControlHandler
}

// RequestHandler is what main.go installs to forward inbound requests. The agent gives it the parsed RequestBody and a sender that emits Chunk frames; the handler returns a Done frame body when finished or an Error if forwarding failed. Implementations run in their own goroutine so concurrent requests don't serialise.
//
// ctx is the session context: it is cancelled when the WS session ends (clean shutdown or read error), so a long-running forward can abort its in-flight upstream call instead of leaking past the connection it belongs to.
type RequestHandler func(ctx context.Context, req protocol.RequestBody, send func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody)

// ControlHandler returns one bounded API response for an allowlisted Control Room operation. It is deliberately distinct from inference RequestHandler.
type ControlHandler func(ctx context.Context, req protocol.ControlRequestBody) (protocol.ChunkBody, *protocol.ErrorBody)

const controlConcurrentRequests = 2

// The handshake challenge is a compact JSON envelope containing one nonce. Keep a generous ceiling while preventing a hostile gateway or proxy from growing the edge agent without bound before the WebSocket session starts.
const maxChallengeResponseBytes int64 = 1 << 20

func readChallengeResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxChallengeResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxChallengeResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxChallengeResponseBytes)
	}
	return data, nil
}

// TerminalDisconnectError is the typed wrapper for a gateway-side Disconnect frame whose Code marks the session as unrecoverable (currently: node_revoked). The reconnect loop in main.go checks `errors.As(err, &*TerminalDisconnectError{})` and exits cleanly instead of backing off — the operator's intent on the server side (delete the node row) shouldn't be undone by a tight retry loop on the supplier side.
type TerminalDisconnectError struct {
	Code   string
	Reason string
}

var ErrMetadataChanged = errors.New("local runtime metadata changed")

func (e *TerminalDisconnectError) Error() string {
	return "gateway disconnect (terminal): " + e.Code + ": " + e.Reason
}

// Client is a single WebSocket session. New() returns one not yet connected; Run() connects, authenticates, and blocks until the session ends.
type Client struct {
	cfg Config

	// closeOnce wraps the conn-shutdown + done-fire path so multiple callers (Run's defer, the reader/writer loop's own error returns, and any external Close()) can race in without double-closing the conn or done channel.
	closeOnce sync.Once
	conn      *websocket.Conn
	sendQ     chan protocol.Frame
	// done is closed exactly once by closeConn. Senders (SendLog, sendFrame) select on it so a concurrent close doesn't panic them with "send on closed channel" — instead the send arm loses the race and the sender exits via the done arm. writerLoop also selects on it to exit cleanly after closeConn fires from outside its own error path.
	done chan struct{}

	// pools bound inference independently by runtime. sem aliases the text pool temporarily for legacy package tests; production admission always selects through pools.
	pools      map[protocol.RuntimeKind]chan struct{}
	poolLimits map[protocol.RuntimeKind]int
	sem        chan struct{}
	controlSem chan struct{}
	// wg tracks in-flight handlers so Run can drain them before returning — combined with session-ctx cancellation, no handler (or its upstream HTTP call) outlives the session it belongs to.
	wg sync.WaitGroup
	// inflight is the live handler count, surfaced to the gateway in each heartbeat (HeartbeatBody.ActiveReqs) so it has back-pressure visibility into how loaded this node is.
	inflight atomic.Int64
	// vramUsedBytes is the latest bounded runtime telemetry snapshot. Admission reads it without a network round trip so resource policy does not add latency to every buyer request.
	vramUsedBytes atomic.Int64

	// welcomeReceived flips true after the gateway accepts the Auth frame and we successfully parse a Welcome back. The reconnect loop in main.go reads this via WelcomeReceived() to decide whether the in-process RegistrationToken has been consumed server-side — without that gate, an Auth rejection (token already used, wrong node id, signature failure) would still burn the token in main's outer loop and brick the agent.
	welcomeReceived      atomic.Bool
	requestMu            sync.Mutex
	requestBodies        map[string]*requestBodyAssembly
	requestBodyBytes     int64
	activeRequests       map[string]*activeRequest
	metadataChanged      chan struct{}
	now                  func() time.Time
	runtimeStateMu       sync.Mutex
	runtimeFingerprints  map[protocol.RuntimeKind]string
	runtimeMismatches    map[protocol.RuntimeKind]int
	runtimeCandidates    map[protocol.RuntimeKind]string
	runtimeRefreshQueued bool
	monitoredRuntimes    map[protocol.RuntimeKind]bool
	drainMu              sync.Mutex
	draining             bool
	drainChanged         chan struct{}
	drainCommandID       string
}

type requestBodyAssembly struct {
	start     protocol.RequestStartBody
	body      []byte
	updatedAt time.Time
}

type activeRequest struct {
	cancel context.CancelFunc
}

// WelcomeReceived reports whether this Client successfully completed the WS handshake. False until the gateway's Welcome frame is read; stays false if the connection terminated during/before Auth.
func (c *Client) WelcomeReceived() bool {
	return c.welcomeReceived.Load()
}

// New constructs a Client with defaults filled in.
func New(cfg Config) (*Client, error) {
	if cfg.GatewayURL == "" {
		return nil, errors.New("client: GatewayURL is required")
	}
	if cfg.NodeID <= 0 {
		return nil, errors.New("client: NodeID is required and must be positive")
	}
	if len(cfg.Identity.Public) == 0 {
		return nil, errors.New("client: Identity is empty (call identity.LoadOrGenerate first)")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	cfg.ResourcePolicy = normalizeResourcePolicy(cfg.ResourcePolicy, cfg.MaxConcurrentRequests)
	metadataChanged := cfg.MetadataChanged
	if metadataChanged == nil {
		metadataChanged = make(chan struct{}, 1)
	}
	pools, poolLimits := resourcePools(cfg.ResourcePolicy)
	return &Client{
		cfg:             cfg,
		sendQ:           make(chan protocol.Frame, 32),
		done:            make(chan struct{}),
		pools:           pools,
		poolLimits:      poolLimits,
		sem:             pools[protocol.RuntimeText],
		controlSem:      make(chan struct{}, controlConcurrentRequests),
		requestBodies:   make(map[string]*requestBodyAssembly),
		activeRequests:  make(map[string]*activeRequest),
		metadataChanged: metadataChanged,
		drainChanged:    make(chan struct{}),
		now:             time.Now,
		runtimeFingerprints: map[protocol.RuntimeKind]string{
			protocol.RuntimeText:   textRuntimeFingerprint(cfg.TextModels, cfg.TextResponsesSupported),
			protocol.RuntimeImage:  capabilityFingerprint(protocol.RuntimeImage, cfg.Meta.Capabilities),
			protocol.RuntimeSpeech: capabilityFingerprint(protocol.RuntimeSpeech, cfg.Meta.Capabilities),
			protocol.RuntimeVideo:  capabilityFingerprint(protocol.RuntimeVideo, cfg.Meta.Capabilities),
			protocol.RuntimeRender: capabilityFingerprint(protocol.RuntimeRender, cfg.Meta.Capabilities),
			protocol.RuntimeRerank: capabilityFingerprint(protocol.RuntimeRerank, cfg.Meta.Capabilities),
		},
		runtimeMismatches: make(map[protocol.RuntimeKind]int),
		runtimeCandidates: make(map[protocol.RuntimeKind]string),
		monitoredRuntimes: map[protocol.RuntimeKind]bool{
			protocol.RuntimeText:   cfg.OllamaURL != "" && cfg.TextModels != nil,
			protocol.RuntimeImage:  cfg.DiffusersURL != "",
			protocol.RuntimeSpeech: cfg.SpeechURL != "" || cfg.TranscriptionURL != "",
			protocol.RuntimeVideo:  cfg.VideoURL != "",
			protocol.RuntimeRender: cfg.RenderURL != "",
			protocol.RuntimeRerank: cfg.RerankURL != "",
		},
	}, nil
}

func normalizeResourcePolicy(policy protocol.ResourcePolicy, legacyMax int) protocol.ResourcePolicy {
	defaults := protocol.ResourcePolicy{
		Text:   protocol.RuntimeResourcePolicy{MaxConcurrent: 4},
		Image:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Speech: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
		Video:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Render: protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Rerank: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
	}
	if legacyMax > 0 && policy == (protocol.ResourcePolicy{}) {
		defaults.Text.MaxConcurrent = legacyMax
		defaults.Image.MaxConcurrent = legacyMax
		defaults.Speech.MaxConcurrent = legacyMax
		defaults.Video.MaxConcurrent = legacyMax
		defaults.Render.MaxConcurrent = legacyMax
		defaults.Rerank.MaxConcurrent = legacyMax
		return defaults
	}
	fillResourcePolicy(&policy.Text, defaults.Text)
	fillResourcePolicy(&policy.Image, defaults.Image)
	fillResourcePolicy(&policy.Speech, defaults.Speech)
	fillResourcePolicy(&policy.Video, defaults.Video)
	fillResourcePolicy(&policy.Render, defaults.Render)
	fillResourcePolicy(&policy.Rerank, defaults.Rerank)
	return policy
}

func fillResourcePolicy(policy *protocol.RuntimeResourcePolicy, fallback protocol.RuntimeResourcePolicy) {
	if policy.MaxConcurrent <= 0 {
		policy.MaxConcurrent = fallback.MaxConcurrent
	}
}

func resourcePools(policy protocol.ResourcePolicy) (map[protocol.RuntimeKind]chan struct{}, map[protocol.RuntimeKind]int) {
	limits := map[protocol.RuntimeKind]int{
		protocol.RuntimeText:   policy.Text.MaxConcurrent,
		protocol.RuntimeImage:  policy.Image.MaxConcurrent,
		protocol.RuntimeSpeech: policy.Speech.MaxConcurrent,
		protocol.RuntimeVideo:  policy.Video.MaxConcurrent,
		protocol.RuntimeRender: policy.Render.MaxConcurrent,
		protocol.RuntimeRerank: policy.Rerank.MaxConcurrent,
	}
	pools := make(map[protocol.RuntimeKind]chan struct{}, len(limits))
	for kind, limit := range limits {
		pools[kind] = make(chan struct{}, limit)
	}
	return pools, limits
}

// Run connects, authenticates, and runs the session. Returns when the connection ends — nil for a clean shutdown, non-nil for any error. Callers re-invoke after a backoff for the reconnect loop.
func (c *Client) Run(ctx context.Context) error {
	if err := c.connectAndAuth(ctx); err != nil {
		return err
	}
	c.log("info", "gateway session authenticated")
	// sessionCtx cancels when Run returns for ANY reason (read error, write error, or parent cancel). In-flight handlers derive their upstream-call deadline from it, so a gateway-side disconnect aborts their HTTP work instead of leaving it to run against the local Ollama after the session is gone.
	sessionCtx, cancel := context.WithCancel(ctx)
	// readerDone closes only after readerLoop has fully returned. Shutdown MUST wait on it before wg.Wait(): every wg.Add happens on the reader goroutine (inside dispatch), so starting wg.Wait while the reader is still running races Add against Wait — either a "sync: WaitGroup misuse" panic or a Wait that returns while a freshly-spawned handler is still in flight.
	readerDone := make(chan struct{})
	monitorDone := make(chan struct{})
	// Defers run LIFO: closeConn first (close done + conn → unblock a parked ReadMessage and any parked sender), then wait for the reader to exit (after which no further wg.Add can happen), then cancel (abort in-flight upstream calls), then wg.Wait (drain handlers) so Run never returns while goroutines it spawned are still touching the connection.
	defer c.wg.Wait()
	defer func() { <-monitorDone }()
	defer cancel()
	defer func() { <-readerDone }()
	defer c.closeConn()

	readErr := make(chan error, 1)
	writeErr := make(chan error, 1)

	go func() {
		// readErr is buffered, so the send completes even when Run is already past its select; readerDone then closes strictly after readerLoop (and any dispatch it was inside) returned.
		defer close(readerDone)
		readErr <- c.readerLoop(sessionCtx)
	}()
	go func() { writeErr <- c.writerLoop(sessionCtx) }()
	go func() {
		defer close(monitorDone)
		c.runtimeMonitorLoop(sessionCtx)
	}()

	select {
	case err := <-readErr:
		return err
	case err := <-writeErr:
		return err
	case <-c.metadataChanged:
		return ErrMetadataChanged
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RequestMetadataRefresh ends the current authenticated session so the reconnect path can rediscover installed models and runtime capabilities before sending the next Auth frame. The signal is coalesced because multiple successful model mutations before reconnect need only one fresh snapshot.
func (c *Client) RequestMetadataRefresh() {
	if c == nil || c.metadataChanged == nil {
		return
	}
	select {
	case c.metadataChanged <- struct{}{}:
	default:
	}
}

// connectAndAuth dials the WS, sends the first Auth frame, waits for Welcome. On reconnect, fetches a challenge first and signs it.
func (c *Client) connectAndAuth(ctx context.Context) error {
	authBody := protocol.AuthBody{
		NodeID:          c.cfg.NodeID,
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    c.cfg.Meta.AgentVer,
		Meta:            c.cfg.Meta,
	}
	if c.cfg.RegistrationToken != "" {
		// First-connect or owner-authorized identity recovery: one-time credential + pubkey.
		if strings.HasPrefix(c.cfg.RegistrationToken, "edgerekey_") {
			authBody.RekeyToken = c.cfg.RegistrationToken
		} else {
			authBody.RegistrationToken = c.cfg.RegistrationToken
		}
		authBody.Pubkey = c.cfg.Identity.EncodedPubkey()
	} else {
		// Reconnect: fetch challenge, sign it.
		challenge, err := c.fetchChallenge(ctx)
		if err != nil {
			return fmt.Errorf("fetch challenge: %w", err)
		}
		raw, err := base64.StdEncoding.DecodeString(challenge)
		if err != nil {
			return fmt.Errorf("decode challenge: %w", err)
		}
		sig := c.cfg.Identity.Sign(raw)
		authBody.Challenge = challenge
		authBody.Signature = base64.StdEncoding.EncodeToString(sig)
	}

	wsURL, err := c.wsEndpoint()
	if err != nil {
		return err
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second
	conn, resp, err := dialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		hint := ""
		if resp != nil {
			hint = fmt.Sprintf(" (server returned %s)", resp.Status)
		}
		return fmt.Errorf("ws dial %s%s: %w", wsURL.Host, hint, err)
	}
	conn.SetReadLimit(protocol.MaxFrameBytes)

	bodyJSON, err := json.Marshal(authBody)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("marshal auth body: %w", err)
	}
	frame := protocol.Frame{Type: protocol.FrameAuth, Body: bodyJSON}
	frameJSON, _ := json.Marshal(frame)
	_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, frameJSON); err != nil {
		_ = conn.Close()
		return fmt.Errorf("write auth frame: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	_, msg, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read welcome: %w", err)
	}
	var welcome protocol.Frame
	if err := json.Unmarshal(msg, &welcome); err != nil {
		_ = conn.Close()
		return fmt.Errorf("decode welcome envelope: %w", err)
	}
	if welcome.Type != protocol.FrameWelcome {
		_ = conn.Close()
		return unexpectedHandshakeFrameError()
	}
	// Clear the deadline — heartbeat reset takes over from here.
	_ = conn.SetReadDeadline(time.Time{})

	c.conn = conn
	// Welcome acknowledged — the registration token (if any) has now been consumed server-side. WelcomeReceived() gates the reconnect loop's token burn so an Auth rejection earlier in the handshake (before Welcome) doesn't lose the token for retry.
	c.welcomeReceived.Store(true)

	// Auto-pull whatever the gateway says this node is missing from its owner's declared target set. Off the handshake goroutine: a pull runs for minutes to hours, and the session must start serving buyer traffic with the models already present rather than waiting.
	var welcomeBody protocol.WelcomeBody
	if err := json.Unmarshal(welcome.Body, &welcomeBody); err == nil && len(welcomeBody.RecommendedModels) > 0 {
		go c.pullRecommendedModels(ctx, welcomeBody.RecommendedModels)
	}

	if c.cfg.OnConnected != nil {
		c.cfg.OnConnected()
	}
	return nil
}

// fetchChallenge POSTs to /edge/handshake/challenge and returns the base64 nonce. Short timeout — if the gateway is slow here we'd rather fail fast and let the reconnect loop retry than block the agent for minutes.
func (c *Client) fetchChallenge(ctx context.Context) (string, error) {
	endpoint, err := c.challengeEndpoint()
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]int64{"node_id": c.cfg.NodeID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := readChallengeResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read challenge response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("challenge endpoint returned %s", resp.Status)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Challenge string `json:"challenge"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode challenge response: %w", err)
	}
	if !payload.Success || payload.Data.Challenge == "" {
		return "", errors.New("challenge endpoint returned empty challenge")
	}
	return payload.Data.Challenge, nil
}

func unexpectedHandshakeFrameError() error {
	return errors.New("unexpected gateway handshake frame")
}

// readerLoop drains inbound frames, routing Request → Handler, dropping heartbeats (their only job is keeping the read deadline alive).
func (c *Client) readerLoop(ctx context.Context) error {
	_ = c.conn.SetReadDeadline(time.Now().Add(protocol.HeartbeatTimeout))
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("ws read: %w", err)
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(protocol.HeartbeatTimeout))

		var frame protocol.Frame
		if err := json.Unmarshal(raw, &frame); err != nil {
			// Malformed inbound from the gateway is a bug worth surfacing but not session-terminating.
			c.log("warn", "malformed inbound frame from gateway")
			continue
		}
		c.expireRequestBodies(c.currentTime())
		switch frame.Type {
		case protocol.FrameHeartbeat:
			// Liveness only — read deadline already reset above.
			continue
		case protocol.FrameHeartbeatAck:
			c.handleHeartbeatAck(frame.Body)
			continue
		case protocol.FrameRequest, protocol.FrameControlRequest:
			if err := c.dispatch(ctx, frame); err != nil {
				if errors.Is(err, errReaderClosed) {
					return nil
				}
				return err
			}
		case protocol.FrameRequestStart:
			c.startRequestBody(frame)
		case protocol.FrameRequestBody:
			c.appendRequestBody(frame)
		case protocol.FrameRequestEnd:
			if err := c.finishRequestBody(ctx, frame); err != nil {
				if errors.Is(err, errReaderClosed) {
					return nil
				}
				return err
			}
		case protocol.FrameRequestCancel:
			c.cancelRequest(frame.ID)
		case protocol.FrameDisconnect:
			var body protocol.DisconnectBody
			if uErr := json.Unmarshal(frame.Body, &body); uErr != nil {
				// Surface the parse failure on stderr instead of silently dropping it — a malformed Disconnect body whose Code field doesn't decode would otherwise fall to the transient "gateway disconnect" path with an empty code and the agent would reconnect forever against a gateway that meant to send a terminal signal. Treat unparseable Disconnect as transient (return a generic error) so the next reconnect can fetch a fresh, parseable frame; but at least the operator sees the parse error in docker logs. Do not copy frame.Body into a local log: a malformed remote peer controls it, and the Control Room must not become a secret-bearing frame dump.
				c.log("warn", "malformed Disconnect frame from gateway")
				return errors.New("malformed gateway disconnect frame")
			}
			// Terminal codes propagate as typed errors so runWithReconnect in main.go can stop the reconnect loop instead of treating them like a generic blip. Everything else collapses into a generic "gateway disconnect" wrap and the outer loop retries with backoff.
			if body.Code == protocol.DisconnectCodeNodeRevoked {
				return &TerminalDisconnectError{Code: body.Code, Reason: "node revoked server-side"}
			}
			return errors.New("gateway disconnect")
		case protocol.FrameSettlement:
			c.handleSettlement(frame.Body)
		case protocol.FrameUpdate:
			c.handleUpdate(ctx, frame)
		case protocol.FrameDrain:
			c.handleDrain(frame)
		default:
			c.log("warn", "unexpected gateway frame type")
		}
	}
}

func (c *Client) handleHeartbeatAck(raw json.RawMessage) {
	if c.cfg.GatewayRoundTrip == nil {
		return
	}
	var heartbeat protocol.HeartbeatBody
	if json.Unmarshal(raw, &heartbeat) != nil || heartbeat.NowUnixMs <= 0 {
		return
	}
	roundTrip := time.Since(time.UnixMilli(heartbeat.NowUnixMs))
	if roundTrip < 0 || roundTrip > protocol.HeartbeatTimeout {
		return
	}
	c.cfg.GatewayRoundTrip(roundTrip)
}

func (c *Client) handleUpdate(ctx context.Context, frame protocol.Frame) {
	emit := func(status protocol.UpdateStatusBody) {
		body, err := json.Marshal(status)
		if err != nil {
			return
		}
		if err := c.sendFrame(protocol.Frame{Type: protocol.FrameUpdateStatus, ID: frame.ID, Body: body}); err != nil {
			c.log("warn", "could not report update status")
		}
	}
	var body protocol.UpdateBody
	if err := json.Unmarshal(frame.Body, &body); err != nil || body.Action != protocol.UpdateActionLatest || frame.ID == "" {
		emit(protocol.UpdateStatusBody{State: protocol.UpdateStateFailed, Error: "unsupported update command"})
		return
	}
	if c.cfg.Update == nil {
		emit(protocol.UpdateStatusBody{State: protocol.UpdateStateFailed, Error: "this agent does not support remote updates"})
		return
	}
	go func() {
		if err := c.cfg.Update(ctx, emit); err != nil {
			emit(protocol.UpdateStatusBody{State: protocol.UpdateStateFailed, Error: err.Error()})
		}
	}()
}

func (c *Client) handleSettlement(body json.RawMessage) {
	var receipt protocol.SettlementBody
	if err := json.Unmarshal(body, &receipt); err != nil || receipt.RequestID == "" || receipt.SettledAtUnixMs <= 0 {
		c.log("warn", "malformed settlement receipt from gateway")
		return
	}
	if c.cfg.Settlement != nil {
		c.cfg.Settlement(receipt)
	}
}

// errReaderClosed is the internal sentinel dispatch returns when the session is closing via the done channel (vs ctx cancellation). The reader loop maps it to a clean (nil) exit, matching the original inline behavior where a done-close meant "stop, no error".
var errReaderClosed = errors.New("reader closed")

// errCodeNodeBusy is the Error-frame code dispatch emits when the concurrency pool is full. Gateway-side this surfaces as a pre-chunk FrameError: waitForFirstChunk (relay/channel/edge/adaptor.go) fails the DoRequest before any byte reaches the buyer, the relay wraps it as a retryable 500 (ErrorCodeDoRequestFailed), and shouldRetry reroutes the request to another channel/node.
const errCodeNodeBusy = "node_busy"

const (
	requestBodyAssemblyTimeout  = 30 * time.Second
	maxBufferedRequestBodyBytes = 64 << 20
)

// dispatch try-acquires a concurrency slot and starts a handler goroutine for an inbound Request frame. It MUST NOT block on a full pool: the WS read deadline (HeartbeatTimeout, 30s) is only refreshed by successful reads, while forwarded requests run for minutes — a reader parked here would let the deadline expire, so the next ReadMessage after a slot freed would fail with an i/o timeout and tear the whole session down (aborting every in-flight handler). Parking would also stall Disconnect-frame handling (incl. node_revoked) for the duration of the saturation. Instead, a full pool rejects the request immediately with a node_busy Error frame and the reader keeps draining; the gateway retries the buyer request on another node (see errCodeNodeBusy). Returns ctx.Err() if the session context was cancelled, errReaderClosed if closeConn fired, or nil once the frame was either handed to a handler or rejected.
func (c *Client) dispatch(ctx context.Context, frame protocol.Frame) error {
	if frame.Type == protocol.FrameControlRequest {
		return c.startHandler(ctx, frame.ID, "", c.controlSem, controlConcurrentRequests, "control", func(requestCtx context.Context) { c.handleControl(requestCtx, frame) })
	}
	var request protocol.RequestBody
	if err := json.Unmarshal(frame.Body, &request); err != nil {
		c.trySendError(frame.ID, "malformed_request", err.Error())
		return nil
	}
	return c.dispatchRequest(ctx, frame.ID, request)
}

func (c *Client) dispatchRequest(ctx context.Context, id string, request protocol.RequestBody) error {
	kind := runtimeForRequest(request.Path)
	pool := c.pools[kind]
	return c.startHandler(ctx, id, kind, pool, c.poolLimits[kind], string(kind), func(requestCtx context.Context) { c.handleRequestBody(requestCtx, id, request) })
}

func runtimeForRequest(path string) protocol.RuntimeKind {
	capability, ok := protocol.CapabilityForRequest(path)
	if !ok {
		return protocol.RuntimeText
	}
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
	default:
		return protocol.RuntimeText
	}
}

func (c *Client) startHandler(ctx context.Context, id string, kind protocol.RuntimeKind, pool chan struct{}, limit int, label string, run func(context.Context)) error {
	if pool == nil {
		c.trySendError(id, "runtime_unavailable", fmt.Sprintf("%s runtime has no admission pool", label))
		return nil
	}
	if kind != "" && c.isDraining() {
		c.trySendError(id, "node_draining", "node is draining and is not accepting new inference requests")
		return nil
	}
	if kind != "" {
		reserveMB := c.reserveVRAMMB(kind)
		if totalBytes := int64(c.cfg.VRAMTotalGB) << 30; reserveMB > 0 && totalBytes > 0 && totalBytes-c.vramUsedBytes.Load() < reserveMB<<20 {
			c.trySendError(id, "insufficient_vram", fmt.Sprintf("%s runtime requires at least %d MiB free VRAM before admission", label, reserveMB))
			return nil
		}
	}
	select {
	case pool <- struct{}{}:
		// Slot acquired — but if done/ctx became ready at the same time, select picks an arm at random and can land here mid-shutdown. Re-check before wg.Add so no handler spawns once the session is closing (Run's reader-exit barrier makes a late Add safe for wg.Wait, but the handler would only burn an upstream call against a dead session).
		select {
		case <-c.done:
			<-pool
			return errReaderClosed
		case <-ctx.Done():
			<-pool
			return ctx.Err()
		default:
		}
		if kind != "" && c.isDraining() {
			<-pool
			c.trySendError(id, "node_draining", "node is draining and is not accepting new inference requests")
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errReaderClosed
	default:
		// Pool full. Reject without blocking the reader: emitError → sendFrame would park up to 5s if sendQ is saturated, and a parked reader stops refreshing the WS read deadline — the very stall this reject path exists to avoid. Drop the reject if the queue is full; the gateway's first-chunk wait still times out and reroutes, so it degrades gracefully.
		c.trySendError(id, errCodeNodeBusy, fmt.Sprintf("%s runtime is at max concurrent requests (%d)", label, limit))
		return nil
	}
	requestCtx, requestCancel := context.WithCancel(ctx)
	active := &activeRequest{cancel: requestCancel}
	c.requestMu.Lock()
	if _, exists := c.activeRequests[id]; exists {
		c.requestMu.Unlock()
		requestCancel()
		<-pool
		c.trySendError(id, "malformed_request", "duplicate active request id")
		return nil
	}
	c.activeRequests[id] = active
	c.requestMu.Unlock()
	c.wg.Add(1)
	c.inflight.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.finishInflightRequest()
		defer func() { <-pool }()
		defer requestCancel()
		defer c.finishActiveRequest(id, active)
		run(requestCtx)
	}()
	return nil
}

func (c *Client) reserveVRAMMB(kind protocol.RuntimeKind) int64 {
	switch kind {
	case protocol.RuntimeImage:
		return c.cfg.ResourcePolicy.Image.ReserveVRAMMB
	case protocol.RuntimeSpeech:
		return c.cfg.ResourcePolicy.Speech.ReserveVRAMMB
	case protocol.RuntimeVideo:
		return c.cfg.ResourcePolicy.Video.ReserveVRAMMB
	case protocol.RuntimeRender:
		return c.cfg.ResourcePolicy.Render.ReserveVRAMMB
	case protocol.RuntimeRerank:
		return c.cfg.ResourcePolicy.Rerank.ReserveVRAMMB
	default:
		return c.cfg.ResourcePolicy.Text.ReserveVRAMMB
	}
}

func (c *Client) finishInflightRequest() {
	if c.inflight.Add(-1) == 0 {
		c.signalDrainChanged()
		c.reportDrainStatus()
	}
}

// BeginDrain stops new inference admission while leaving active request contexts untouched. The caller can wait on WaitForDrained before maintenance without turning a graceful pause into a buyer-visible cancellation.
func (c *Client) BeginDrain() {
	c.drainMu.Lock()
	if !c.draining {
		c.draining = true
		c.signalDrainChangedLocked()
	}
	c.drainMu.Unlock()
}

// CancelDrain returns the node to serving admission. It is safe to call when the node is already serving.
func (c *Client) CancelDrain() {
	c.drainMu.Lock()
	if c.draining {
		c.draining = false
		c.drainCommandID = ""
		c.signalDrainChangedLocked()
	}
	c.drainMu.Unlock()
}

func (c *Client) isDraining() bool {
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	return c.draining
}

// DrainState derives the terminal drained state from the admission flag and live handler count so it cannot drift from actual work.
func (c *Client) DrainState() string {
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	return c.drainStateLocked()
}

// ActiveRequests returns the number of inference handlers that have passed admission and not yet completed.
func (c *Client) ActiveRequests() int {
	if c == nil {
		return 0
	}
	return int(c.inflight.Load())
}

func (c *Client) drainStateLocked() string {
	if !c.draining {
		return protocol.DrainStateServing
	}
	if c.inflight.Load() == 0 {
		return protocol.DrainStateDrained
	}
	return protocol.DrainStateDraining
}

// WaitForDrained waits on state transitions rather than polling or sleeping. Cancelling the wait never changes admission state.
func (c *Client) WaitForDrained(ctx context.Context) error {
	for {
		c.drainMu.Lock()
		if c.drainStateLocked() == protocol.DrainStateDrained {
			c.drainMu.Unlock()
			return nil
		}
		changed := c.drainChanged
		c.drainMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (c *Client) signalDrainChanged() {
	c.drainMu.Lock()
	c.signalDrainChangedLocked()
	c.drainMu.Unlock()
}

func (c *Client) signalDrainChangedLocked() {
	close(c.drainChanged)
	c.drainChanged = make(chan struct{})
}

func (c *Client) handleDrain(frame protocol.Frame) {
	var body protocol.DrainBody
	if frame.ID == "" || json.Unmarshal(frame.Body, &body) != nil {
		c.trySendError(frame.ID, "malformed_drain", "invalid drain command")
		return
	}
	switch body.Action {
	case protocol.DrainActionStart:
		c.drainMu.Lock()
		c.drainCommandID = frame.ID
		if !c.draining {
			c.draining = true
			c.signalDrainChangedLocked()
		}
		c.drainMu.Unlock()
	case protocol.DrainActionCancel:
		c.CancelDrain()
	default:
		c.trySendError(frame.ID, "unsupported_drain", "unsupported drain action")
		return
	}
	c.trySendDrainStatus(frame.ID)
}

func (c *Client) reportDrainStatus() {
	c.drainMu.Lock()
	id := c.drainCommandID
	state := c.drainStateLocked()
	if state == protocol.DrainStateDrained {
		c.drainCommandID = ""
	}
	c.drainMu.Unlock()
	if id != "" {
		c.trySendDrainStatus(id)
	}
}

func (c *Client) trySendDrainStatus(id string) {
	body, err := json.Marshal(protocol.DrainStatusBody{State: c.DrainState(), ActiveRequests: int(c.inflight.Load())})
	if err != nil {
		return
	}
	select {
	case c.sendQ <- protocol.Frame{Type: protocol.FrameDrainStatus, ID: id, Body: body}:
	case <-c.done:
	default:
	}
}

func (c *Client) finishActiveRequest(id string, active *activeRequest) {
	c.requestMu.Lock()
	if c.activeRequests[id] == active {
		delete(c.activeRequests, id)
	}
	c.requestMu.Unlock()
}

func (c *Client) cancelRequest(id string) {
	if id == "" {
		return
	}
	c.requestMu.Lock()
	c.dropRequestBodyLocked(id)
	active := c.activeRequests[id]
	c.requestMu.Unlock()
	if active != nil {
		active.cancel()
	}
}

func (c *Client) handleRequestBody(ctx context.Context, id string, body protocol.RequestBody) {
	if c.cfg.Handler == nil {
		c.emitError(id, "no_handler", "agent has no request handler installed")
		return
	}
	send := func(chunk protocol.ChunkBody) error {
		chunkJSON, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		return c.sendFrame(protocol.Frame{Type: protocol.FrameChunk, ID: id, Body: chunkJSON})
	}
	done, errBody := c.cfg.Handler(ctx, body, send)
	if ctx.Err() != nil {
		return
	}
	if errBody != nil {
		c.emitError(id, errBody.Code, errBody.Message)
		return
	}
	doneJSON, _ := json.Marshal(done)
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameDone, ID: id, Body: doneJSON})
}

func (c *Client) startRequestBody(frame protocol.Frame) {
	if frame.ID == "" {
		return
	}
	var start protocol.RequestStartBody
	if err := json.Unmarshal(frame.Body, &start); err != nil || start.BodySize < 0 || start.BodySize > protocol.MaxRequestBodyBytes {
		c.trySendError(frame.ID, "malformed_request", "invalid request start")
		return
	}
	capacity := int(start.BodySize)
	if capacity > protocol.RequestBodyChunkBytes {
		capacity = protocol.RequestBodyChunkBytes
	}
	now := c.currentTime()
	c.requestMu.Lock()
	c.expireRequestBodiesLocked(now)
	if c.requestBodies == nil {
		c.requestBodies = make(map[string]*requestBodyAssembly)
	}
	if _, exists := c.requestBodies[frame.ID]; exists {
		c.dropRequestBodyLocked(frame.ID)
		c.requestMu.Unlock()
		c.trySendError(frame.ID, "malformed_request", "duplicate request start")
		return
	}
	if len(c.requestBodies) >= protocol.MaxPendingRequestBodies {
		c.requestMu.Unlock()
		c.trySendError(frame.ID, errCodeNodeBusy, "too many request bodies are uploading")
		return
	}
	c.requestBodies[frame.ID] = &requestBodyAssembly{start: start, body: make([]byte, 0, capacity), updatedAt: now}
	c.requestMu.Unlock()
}

func (c *Client) appendRequestBody(frame protocol.Frame) {
	var chunk protocol.RequestBodyChunk
	if err := json.Unmarshal(frame.Body, &chunk); err != nil {
		c.requestMu.Lock()
		c.dropRequestBodyLocked(frame.ID)
		c.requestMu.Unlock()
		c.trySendError(frame.ID, "malformed_request", "invalid request body chunk")
		return
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(chunk.Bytes)
	c.requestMu.Lock()
	assembly, ok := c.requestBodies[frame.ID]
	if !ok {
		c.requestMu.Unlock()
		c.trySendError(frame.ID, "malformed_request", "request body chunk arrived before start")
		return
	}
	if err != nil || len(decoded) > protocol.RequestBodyChunkBytes || int64(len(assembly.body)+len(decoded)) > assembly.start.BodySize || c.requestBodyBytes+int64(len(decoded)) > maxBufferedRequestBodyBytes {
		c.dropRequestBodyLocked(frame.ID)
		c.requestMu.Unlock()
		c.trySendError(frame.ID, "malformed_request", "request body chunk exceeds declared limits")
		return
	}
	assembly.body = append(assembly.body, decoded...)
	assembly.updatedAt = c.currentTime()
	c.requestBodyBytes += int64(len(decoded))
	c.requestMu.Unlock()
}

func (c *Client) finishRequestBody(ctx context.Context, frame protocol.Frame) error {
	c.requestMu.Lock()
	assembly, ok := c.requestBodies[frame.ID]
	if !ok {
		c.requestMu.Unlock()
		c.trySendError(frame.ID, "malformed_request", "request end arrived before start")
		return nil
	}
	c.dropRequestBodyLocked(frame.ID)
	c.requestMu.Unlock()
	if int64(len(assembly.body)) != assembly.start.BodySize {
		c.trySendError(frame.ID, "malformed_request", "request body size does not match declaration")
		return nil
	}
	return c.dispatchRequest(ctx, frame.ID, protocol.RequestBody{
		Method: assembly.start.Method, Path: assembly.start.Path, Headers: assembly.start.Headers,
		Body: assembly.body, Stream: assembly.start.Stream, ConsumerRef: assembly.start.ConsumerRef,
	})
}

func (c *Client) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) expireRequestBodies(now time.Time) {
	c.requestMu.Lock()
	c.expireRequestBodiesLocked(now)
	c.requestMu.Unlock()
}

func (c *Client) expireRequestBodiesLocked(now time.Time) {
	for id, assembly := range c.requestBodies {
		if now.Sub(assembly.updatedAt) > requestBodyAssemblyTimeout {
			c.dropRequestBodyLocked(id)
		}
	}
}

func (c *Client) dropRequestBodyLocked(id string) {
	assembly := c.requestBodies[id]
	if assembly == nil {
		return
	}
	c.requestBodyBytes -= int64(len(assembly.body))
	if c.requestBodyBytes < 0 {
		c.requestBodyBytes = 0
	}
	delete(c.requestBodies, id)
}

func (c *Client) handleControl(ctx context.Context, frame protocol.Frame) {
	if c.cfg.ControlHandler == nil {
		c.emitError(frame.ID, "control_unavailable", "this agent does not support remote control")
		return
	}
	if frame.ID == "" {
		return
	}
	var body protocol.ControlRequestBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		c.emitError(frame.ID, "malformed_control_request", "invalid control request")
		return
	}
	chunk, errBody := c.cfg.ControlHandler(ctx, body)
	if ctx.Err() != nil {
		return
	}
	if errBody != nil {
		c.emitError(frame.ID, errBody.Code, errBody.Message)
		return
	}
	chunkJSON, err := json.Marshal(chunk)
	if err != nil {
		c.emitError(frame.ID, "control_response_failed", "could not encode control response")
		return
	}
	if err := c.sendFrame(protocol.Frame{Type: protocol.FrameChunk, ID: frame.ID, Body: chunkJSON}); err != nil {
		return
	}
	doneJSON, _ := json.Marshal(protocol.DoneBody{})
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameDone, ID: frame.ID, Body: doneJSON})
}

// emitError centralises the Error-frame send so all error paths produce the same envelope. Used from handler goroutines, where the sendFrame 5s timeout is acceptable.
func (c *Client) emitError(reqID, code, msg string) {
	errJSON, _ := json.Marshal(protocol.ErrorBody{Code: code, Message: msg})
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameError, ID: reqID, Body: errJSON})
}

// trySendError enqueues an Error frame without ever blocking the caller — the node_busy reject path runs on the reader goroutine, where the sendFrame 5s timeout would stall reads and risk the WS read deadline expiring. Mirrors SendLog's non-blocking enqueue: drop the frame if sendQ is full or the client is closing.
func (c *Client) trySendError(reqID, code, msg string) {
	errJSON, err := json.Marshal(protocol.ErrorBody{Code: code, Message: msg})
	if err != nil {
		return
	}
	frame := protocol.Frame{Type: protocol.FrameError, ID: reqID, Body: errJSON}
	select {
	case c.sendQ <- frame:
	case <-c.done:
	default:
	}
}

// SendLog pushes a log line to the gateway. Non-blocking with a tight timeout — agent log hooks must NEVER stall on a slow gateway link or we deadlock our own log writer (a stalled log call holds the stdlib log package's mutex, blocking every other goroutine that tries to log). On full send-queue we drop the line silently and rely on the supplier's local docker logs as the authoritative copy.
//
// Best-effort by design: this is dashboard convenience, not audit. Lines lost on a flaky WS link don't get retransmitted.
func (c *Client) SendLog(level, msg string) {
	if c == nil {
		return
	}
	body, err := json.Marshal(protocol.LogBody{
		UnixMs: time.Now().UnixMilli(),
		Level:  level,
		Msg:    msg,
	})
	if err != nil {
		return
	}
	frame := protocol.Frame{Type: protocol.FrameLog, Body: body}
	// Non-blocking enqueue — falls back to drop if sendQ is full. We don't use sendFrame's 5s timeout here because logs fire from inside the log package's mutex and a 5s stall is a deadlock vector.
	//
	// The done arm covers the close race: if closeConn fires between the caller's currentClient.Load() and this select, the done channel races against the send arm. Without it, a concurrent closeConn that closed sendQ (older design) would panic the sender with "send on closed channel". Now sendQ is never closed and done is the close signal.
	select {
	case c.sendQ <- frame:
	case <-c.done:
	default:
	}
}

// writerLoop drains sendQ + heartbeat ticker. Single writer goroutine per gorilla/websocket's concurrency rule.
//
// Exits on ctx cancellation or done (closeConn fired). The done arm replaces the older "sendQ closed → ok=false → return" pattern: we no longer close sendQ at all, because senders (SendLog, sendFrame) would panic on a closed channel under reconnect-heavy load.
func (c *Client) writerLoop(ctx context.Context) error {
	ticker := time.NewTicker(protocol.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return nil
		case frame := <-c.sendQ:
			if err := c.writeFrame(frame); err != nil {
				return err
			}
		case <-ticker.C:
			// Carry live requests and resident model memory so the gateway can avoid piling work onto a node whose capacity is already mostly committed. Collection is bounded; liveness still wins when the local runtime is unavailable.
			beat := protocol.Frame{Type: protocol.FrameHeartbeat}
			telemetryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			telemetry := c.heartbeatTelemetry(telemetryCtx)
			cancel()
			if body, err := json.Marshal(telemetry); err == nil {
				beat.Body = body
			}
			if err := c.writeFrame(beat); err != nil {
				return err
			}
		}
	}
}

// heartbeatTelemetry takes a small, bounded snapshot of resident model memory. The local runtime is the authority for this: OS-wide GPU memory can include unrelated processes and would make the scheduler punish the node for work it cannot manage. Failures intentionally report only the static capacity and live request count; the gateway then falls back to GPU load.
func (c *Client) heartbeatTelemetry(ctx context.Context) protocol.HeartbeatBody {
	telemetry := protocol.HeartbeatBody{
		NowUnixMs:   time.Now().UnixMilli(),
		VRAMTotalGB: c.cfg.VRAMTotalGB,
		ActiveReqs:  int(c.inflight.Load()),
		DrainState:  c.DrainState(),
	}
	if c.cfg.Performance != nil {
		telemetry.Performance = sanitizePerformanceSamples(c.cfg.Performance())
	}
	if c.cfg.HTTPClient == nil {
		return telemetry
	}
	usedBytes := c.ollamaVRAMBytes(ctx)
	imageBytes, _ := c.runtimeSnapshot(ctx, c.cfg.DiffusersURL, protocol.RuntimeImage)
	usedBytes = addVRAMBytes(usedBytes, imageBytes)
	speechBytes, _ := c.runtimeSnapshot(ctx, c.cfg.SpeechURL, protocol.RuntimeSpeech)
	usedBytes = addVRAMBytes(usedBytes, speechBytes)
	transcriptionBytes, _ := c.runtimeSnapshot(ctx, c.cfg.TranscriptionURL, protocol.RuntimeSpeech)
	usedBytes = addVRAMBytes(usedBytes, transcriptionBytes)
	videoBytes, _ := c.runtimeSnapshot(ctx, c.cfg.VideoURL, protocol.RuntimeVideo)
	usedBytes = addVRAMBytes(usedBytes, videoBytes)
	renderBytes, _ := c.runtimeSnapshot(ctx, c.cfg.RenderURL, protocol.RuntimeRender)
	usedBytes = addVRAMBytes(usedBytes, renderBytes)
	rerankBytes, _ := c.runtimeSnapshot(ctx, c.cfg.RerankURL, protocol.RuntimeRerank)
	usedBytes = addVRAMBytes(usedBytes, rerankBytes)
	c.vramUsedBytes.Store(usedBytes)
	telemetry.VRAMUsedGB = float64(usedBytes) / float64(1<<30)
	return telemetry
}

func sanitizePerformanceSamples(samples []protocol.RuntimePerformanceSample) []protocol.RuntimePerformanceSample {
	result := make([]protocol.RuntimePerformanceSample, 0, min(len(samples), protocol.MaxPerformanceSamples))
	for _, sample := range samples {
		if len(result) == protocol.MaxPerformanceSamples {
			break
		}
		if sample.Runtime == "" || sample.Capability == "" || sample.DurationMs <= 0 || sample.TTFTMs < 0 || sample.OutputUnits < 0 || sample.UnixMs <= 0 || sample.UnitsPerSecond < 0 || math.IsNaN(sample.UnitsPerSecond) || math.IsInf(sample.UnitsPerSecond, 0) || len(sample.Model) > 256 {
			continue
		}
		result = append(result, sample)
	}
	return result
}

func (c *Client) ollamaVRAMBytes(ctx context.Context) int64 {
	if c.cfg.OllamaURL == "" {
		return 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.OllamaURL, "/")+"/api/ps", nil)
	if err != nil {
		return 0
	}
	response, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0
	}
	var payload struct {
		Models []struct {
			SizeVRAM int64 `json:"size_vram"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload) != nil {
		return 0
	}
	var usedBytes int64
	for _, model := range payload.Models {
		usedBytes = addVRAMBytes(usedBytes, model.SizeVRAM)
	}
	return usedBytes
}

func (c *Client) runtimeSnapshot(ctx context.Context, baseURL string, kind protocol.RuntimeKind) (int64, string) {
	if strings.TrimSpace(baseURL) == "" {
		return 0, "unavailable"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/health", nil)
	if err != nil {
		return 0, "unavailable"
	}
	response, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, "unavailable"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusServiceUnavailable {
		return 0, "unavailable"
	}
	var payload struct {
		Version      string                `json:"version"`
		VRAMBytes    int64                 `json:"vram_bytes"`
		Capabilities []protocol.Capability `json:"capabilities"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload) != nil || payload.VRAMBytes < 0 {
		return 0, "unavailable"
	}
	for index := range payload.Capabilities {
		payload.Capabilities[index].Runtime = kind
		payload.Capabilities[index].Version = payload.Version
	}
	return payload.VRAMBytes, capabilityFingerprint(kind, payload.Capabilities)
}

func (c *Client) probeTextRuntimeFingerprint(ctx context.Context) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.OllamaURL, "/")+"/api/tags", nil)
	if err != nil {
		return "unavailable"
	}
	response, err := c.cfg.HTTPClient.Do(request)
	if err != nil {
		return "unavailable"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "unavailable"
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&tags) != nil {
		return "unavailable"
	}
	models := make([]string, 0, len(tags.Models))
	for _, model := range tags.Models {
		models = append(models, model.Name)
	}
	if len(normalizedFingerprintStrings(models)) == 0 {
		return "unavailable"
	}
	versionRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.OllamaURL, "/")+"/api/version", nil)
	if err != nil {
		return "unavailable"
	}
	versionResponse, err := c.cfg.HTTPClient.Do(versionRequest)
	if err != nil {
		return "unavailable"
	}
	defer versionResponse.Body.Close()
	if versionResponse.StatusCode != http.StatusOK {
		return "unavailable"
	}
	var version struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(io.LimitReader(versionResponse.Body, 2<<20)).Decode(&version) != nil {
		return "unavailable"
	}
	var major, minor, patch int
	_, parseErr := fmt.Sscanf(strings.TrimSpace(version.Version), "%d.%d.%d", &major, &minor, &patch)
	responses := parseErr == nil && (major > 0 || minor > 13 || minor == 13 && patch >= 3)
	return textRuntimeFingerprint(models, responses)
}

const runtimeStateProbeTimeout = 10 * time.Second

type runtimeFingerprintResult struct {
	kind        protocol.RuntimeKind
	fingerprint string
}

func (c *Client) runtimeMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(protocol.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.monitorRuntimeState(ctx)
		}
	}
}

func (c *Client) monitorRuntimeState(ctx context.Context) {
	results := make(chan runtimeFingerprintResult, len(c.monitoredRuntimes))
	pending := 0
	probe := func(kind protocol.RuntimeKind, discover func(context.Context) string) {
		if !c.monitoredRuntimes[kind] {
			return
		}
		pending++
		go func() {
			probeCtx, cancel := context.WithTimeout(ctx, runtimeStateProbeTimeout)
			defer cancel()
			results <- runtimeFingerprintResult{kind: kind, fingerprint: discover(probeCtx)}
		}()
	}
	probe(protocol.RuntimeText, c.probeTextRuntimeFingerprint)
	probe(protocol.RuntimeImage, func(probeCtx context.Context) string {
		_, fingerprint := c.runtimeSnapshot(probeCtx, c.cfg.DiffusersURL, protocol.RuntimeImage)
		return fingerprint
	})
	probe(protocol.RuntimeSpeech, func(probeCtx context.Context) string {
		_, speechFingerprint := c.runtimeSnapshot(probeCtx, c.cfg.SpeechURL, protocol.RuntimeSpeech)
		_, transcriptionFingerprint := c.runtimeSnapshot(probeCtx, c.cfg.TranscriptionURL, protocol.RuntimeSpeech)
		return speechFingerprint + "\n" + transcriptionFingerprint
	})
	probe(protocol.RuntimeVideo, func(probeCtx context.Context) string {
		_, fingerprint := c.runtimeSnapshot(probeCtx, c.cfg.VideoURL, protocol.RuntimeVideo)
		return fingerprint
	})
	probe(protocol.RuntimeRender, func(probeCtx context.Context) string {
		_, fingerprint := c.runtimeSnapshot(probeCtx, c.cfg.RenderURL, protocol.RuntimeRender)
		return fingerprint
	})
	probe(protocol.RuntimeRerank, func(probeCtx context.Context) string {
		_, fingerprint := c.runtimeSnapshot(probeCtx, c.cfg.RerankURL, protocol.RuntimeRerank)
		return fingerprint
	})
	for range pending {
		select {
		case <-ctx.Done():
			return
		case result := <-results:
			c.observeRuntimeFingerprint(result.kind, result.fingerprint)
		}
	}
}

func (c *Client) observeRuntimeFingerprint(kind protocol.RuntimeKind, fingerprint string) {
	c.runtimeStateMu.Lock()
	if c.runtimeFingerprints == nil {
		c.runtimeStateMu.Unlock()
		return
	}
	if c.runtimeRefreshQueued {
		c.runtimeStateMu.Unlock()
		return
	}
	previous := c.runtimeFingerprints[kind]
	if fingerprint == previous {
		c.runtimeMismatches[kind] = 0
		delete(c.runtimeCandidates, kind)
		c.runtimeStateMu.Unlock()
		return
	}
	if c.runtimeCandidates[kind] != fingerprint {
		c.runtimeCandidates[kind] = fingerprint
		c.runtimeMismatches[kind] = 1
	} else {
		c.runtimeMismatches[kind]++
	}
	if c.runtimeMismatches[kind] < 2 {
		c.runtimeStateMu.Unlock()
		return
	}
	c.runtimeRefreshQueued = true
	c.runtimeStateMu.Unlock()
	if runtimeFingerprintDegraded(fingerprint) {
		c.sendDiagnostic("error", "runtime_degraded", kind)
	} else if runtimeFingerprintDegraded(previous) {
		c.sendDiagnostic("info", "runtime_recovered", kind)
	}
	c.RequestMetadataRefresh()
}

func runtimeFingerprintDegraded(fingerprint string) bool {
	return fingerprint == "unavailable" || strings.Contains(fingerprint, "|degraded|") || strings.Contains(fingerprint, "|unavailable|")
}

func (c *Client) sendDiagnostic(level, code string, runtimeKind protocol.RuntimeKind) {
	body, err := json.Marshal(protocol.DiagnosticsBody{Events: []protocol.DiagnosticEvent{{UnixMs: time.Now().UnixMilli(), Level: level, Code: code, Runtime: runtimeKind}}})
	if err != nil {
		return
	}
	frame := protocol.Frame{Type: protocol.FrameDiagnostics, Body: body}
	select {
	case c.sendQ <- frame:
	case <-c.done:
	default:
	}
}

func textRuntimeFingerprint(models []string, responses bool) string {
	normalized := normalizedFingerprintStrings(models)
	if len(normalized) == 0 {
		return "unavailable"
	}
	return fmt.Sprintf("models=%s;responses=%t", strings.Join(normalized, ","), responses)
}

func capabilityFingerprint(kind protocol.RuntimeKind, capabilities []protocol.Capability) string {
	parts := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if capability.Runtime != kind {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d|%s", capability.ID, capability.Status, strings.Join(normalizedFingerprintStrings(capability.Models), ","), strings.Join(normalizedFingerprintStrings(capability.Paths), ","), strings.TrimSpace(capability.Version), capability.Limits.MaxInputBytes, capability.Limits.MaxInputCharacters, strings.Join(normalizedFingerprintStrings(capability.Limits.Formats), ",")))
	}
	if len(parts) == 0 {
		return "unavailable"
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func normalizedFingerprintStrings(values []string) []string {
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

func addVRAMBytes(total, value int64) int64 {
	if value <= 0 {
		return total
	}
	if total > math.MaxInt64-value {
		return math.MaxInt64
	}
	return total + value
}

func (c *Client) writeFrame(frame protocol.Frame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(protocol.HeartbeatInterval))
	return c.conn.WriteMessage(websocket.TextMessage, payload)
}

// sendFrame is the non-blocking enqueue used by handleRequest. Drops the frame on a full queue with a stderr warning — saturation here usually means the gateway-bound link is stalled, which the writer loop will surface as a write error and tear the session down.
//
// The done arm is the close-race guard: a concurrent closeConn must not panic an in-flight send. With sendQ never closed (see the writerLoop comment) done is the canonical "we're shutting down" signal for senders.
func (c *Client) sendFrame(frame protocol.Frame) error {
	select {
	case c.sendQ <- frame:
		return nil
	case <-c.done:
		return errors.New("client closed")
	case <-time.After(5 * time.Second):
		return errors.New("send queue full for 5s; gateway link stalled")
	}
}

// closeConn tears down the WS session exactly once. Idempotent via sync.Once — Run's defer, the reader/writer loop returns, and any external Close() can all race in without double-closing the conn.
//
// Closing the done channel signals every active sender to bail out; sendQ is intentionally NOT closed, because a closed channel + concurrent sender = panic, which under reconnect-heavy load is exactly the failure mode we used to hit.
func (c *Client) closeConn() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			_ = c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(time.Second),
			)
			_ = c.conn.Close()
		}
	})
}

// wsEndpoint derives wss://host/edge/connect from the configured gateway base. Accepts http:// or https:// as the gateway scheme and flips to ws:// / wss:// accordingly so tests can point at localhost over plaintext.
func (c *Client) wsEndpoint() (*url.URL, error) {
	base, err := url.Parse(c.cfg.GatewayURL)
	if err != nil {
		return nil, fmt.Errorf("parse gateway URL: %w", err)
	}
	switch strings.ToLower(base.Scheme) {
	case "https":
		base.Scheme = "wss"
	case "http":
		base.Scheme = "ws"
	case "wss", "ws":
		// already correct
	default:
		return nil, fmt.Errorf("unsupported gateway scheme %q", base.Scheme)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/edge/connect"
	return base, nil
}

// challengeEndpoint mirrors wsEndpoint for the HTTP side.
func (c *Client) challengeEndpoint() (*url.URL, error) {
	base, err := url.Parse(c.cfg.GatewayURL)
	if err != nil {
		return nil, fmt.Errorf("parse gateway URL: %w", err)
	}
	switch strings.ToLower(base.Scheme) {
	case "ws":
		base.Scheme = "http"
	case "wss":
		base.Scheme = "https"
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/edge/handshake/challenge"
	return base, nil
}

func stderr() io.Writer { return os.Stderr }

func (c *Client) log(level, message string) {
	if c.cfg.Log != nil {
		c.cfg.Log(level, message)
		return
	}
	_, _ = fmt.Fprintf(stderr(), "edge-agent: [%s] %s\n", level, message)
}
