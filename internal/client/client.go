// Package client owns the agent's WebSocket connection to the
// EveryAPI gateway: opens the connection, completes Auth, runs the
// read/write loops until terminated, and surfaces inbound Request
// frames to a Handler (the forwarder in internal/forward, wired by
// main).
//
// One Client = one logical "connect or die"; main.go wraps it in the
// reconnect loop so a transient network blip doesn't take the agent
// down.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// Config is what main.go passes in. Everything is required except
// HTTPClient (defaulted) and Handler (allowed nil for tests).
type Config struct {
	// GatewayURL — base URL of the EveryAPI gateway, e.g.
	// https://api.everyapi.ai. The agent derives WS / HTTP endpoints
	// off this base (wss host for /edge/connect, https for the
	// challenge endpoint).
	GatewayURL string
	// NodeID is the EdgeNode primary key the gateway minted when
	// the seller registered through the dashboard.
	NodeID int64
	// RegistrationToken is the one-time secret returned with the
	// node row. Used on FIRST connect only; subsequent connects
	// rely on the Ed25519 identity. Empty after the first
	// successful Welcome.
	RegistrationToken string
	// Identity is the loaded Ed25519 keypair (see internal/identity).
	Identity identity.Decoded
	// Meta is the snapshot the agent reports on every connect —
	// hardware, location, currently-installed models, agent version.
	Meta protocol.NodeMeta
	// Handler is invoked for every inbound Request frame. The
	// returned io.Reader streams response chunks; closing it signals
	// the agent that the request is done. A nil Handler drops
	// Request frames on the floor (test-only).
	Handler RequestHandler
	// HTTPClient is used for the challenge endpoint. Defaulted in
	// New() if nil; injectable for tests.
	HTTPClient *http.Client
}

// RequestHandler is what main.go installs to forward inbound
// requests. The agent gives it the parsed RequestBody and a sender
// that emits Chunk frames; the handler returns a Done frame body
// when finished or an Error if forwarding failed. Implementations
// run in their own goroutine so concurrent requests don't serialise.
type RequestHandler func(req protocol.RequestBody, send func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody)

// Client is a single WebSocket session. New() returns one not yet
// connected; Run() connects, authenticates, and blocks until the
// session ends.
type Client struct {
	cfg Config

	// Mutex protects sendQ access during Close() — the send method
	// itself uses the channel for ordering. We need the mutex only
	// to close the channel exactly once.
	mu       sync.Mutex
	conn     *websocket.Conn
	sendQ    chan protocol.Frame
	closed   bool
	closeErr error

	// welcomeReceived flips true after the gateway accepts the Auth
	// frame and we successfully parse a Welcome back. The reconnect
	// loop in main.go reads this via WelcomeReceived() to decide
	// whether the in-process RegistrationToken has been consumed
	// server-side — without that gate, an Auth rejection (token
	// already used, wrong node id, signature failure) would still
	// burn the token in main's outer loop and brick the agent.
	welcomeReceived atomic.Bool
}

// WelcomeReceived reports whether this Client successfully completed
// the WS handshake. False until the gateway's Welcome frame is read;
// stays false if the connection terminated during/before Auth.
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
	return &Client{
		cfg:   cfg,
		sendQ: make(chan protocol.Frame, 32),
	}, nil
}

// Run connects, authenticates, and runs the session. Returns when the
// connection ends — nil for a clean shutdown, non-nil for any error.
// Callers re-invoke after a backoff for the reconnect loop.
func (c *Client) Run(ctx context.Context) error {
	if err := c.connectAndAuth(ctx); err != nil {
		return err
	}
	defer c.closeConn()

	readErr := make(chan error, 1)
	writeErr := make(chan error, 1)

	go func() { readErr <- c.readerLoop(ctx) }()
	go func() { writeErr <- c.writerLoop(ctx) }()

	select {
	case err := <-readErr:
		return err
	case err := <-writeErr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// connectAndAuth dials the WS, sends the first Auth frame, waits for
// Welcome. On reconnect, fetches a challenge first and signs it.
func (c *Client) connectAndAuth(ctx context.Context) error {
	authBody := protocol.AuthBody{
		NodeID:          c.cfg.NodeID,
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    c.cfg.Meta.AgentVer,
		Meta:            c.cfg.Meta,
	}
	if c.cfg.RegistrationToken != "" {
		// First-connect: token + pubkey.
		authBody.RegistrationToken = c.cfg.RegistrationToken
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
		// Likely a close/disconnect frame with a reason. Surface it.
		preview := string(msg)
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		_ = conn.Close()
		return fmt.Errorf("expected welcome, got %q (%s)", welcome.Type, preview)
	}
	// Clear the deadline — heartbeat reset takes over from here.
	_ = conn.SetReadDeadline(time.Time{})

	c.conn = conn
	// Welcome acknowledged — the registration token (if any) has now
	// been consumed server-side. WelcomeReceived() gates the
	// reconnect loop's token burn so an Auth rejection earlier in the
	// handshake (before Welcome) doesn't lose the token for retry.
	c.welcomeReceived.Store(true)
	return nil
}

// fetchChallenge POSTs to /edge/handshake/challenge and returns the
// base64 nonce. Short timeout — if the gateway is slow here we'd
// rather fail fast and let the reconnect loop retry than block the
// agent for minutes.
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
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("challenge endpoint returned %s: %s", resp.Status, string(raw))
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Challenge string `json:"challenge"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode challenge response: %w", err)
	}
	if !payload.Success || payload.Data.Challenge == "" {
		return "", errors.New("challenge endpoint returned empty challenge")
	}
	return payload.Data.Challenge, nil
}

// readerLoop drains inbound frames, routing Request → Handler,
// dropping heartbeats (their only job is keeping the read deadline
// alive).
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
			// Malformed inbound from the gateway is a bug worth
			// surfacing but not session-terminating.
			fmt.Fprintf(stderr(), "edge-agent: malformed inbound frame: %v\n", err)
			continue
		}
		switch frame.Type {
		case protocol.FrameHeartbeat:
			// Liveness only — read deadline already reset above.
			continue
		case protocol.FrameRequest:
			go c.handleRequest(frame)
		case protocol.FrameDisconnect:
			var body protocol.DisconnectBody
			_ = json.Unmarshal(frame.Body, &body)
			return fmt.Errorf("gateway disconnect: %s: %s", body.Code, body.Reason)
		default:
			fmt.Fprintf(stderr(), "edge-agent: unexpected frame type %q from gateway\n", frame.Type)
		}
	}
}

// handleRequest is the per-request goroutine: parse, hand to the
// installed Handler, emit Chunk frames as the handler streams, and
// terminate with Done/Error.
func (c *Client) handleRequest(frame protocol.Frame) {
	if c.cfg.Handler == nil {
		// No handler installed — emit an immediate Error so the
		// gateway doesn't wait HeartbeatTimeout for a dead request.
		c.emitError(frame.ID, "no_handler", "agent has no request handler installed")
		return
	}
	var body protocol.RequestBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		c.emitError(frame.ID, "malformed_request", err.Error())
		return
	}
	send := func(chunk protocol.ChunkBody) error {
		chunkJSON, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		return c.sendFrame(protocol.Frame{Type: protocol.FrameChunk, ID: frame.ID, Body: chunkJSON})
	}
	done, errBody := c.cfg.Handler(body, send)
	if errBody != nil {
		c.emitError(frame.ID, errBody.Code, errBody.Message)
		return
	}
	doneJSON, _ := json.Marshal(done)
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameDone, ID: frame.ID, Body: doneJSON})
}

// emitError centralises the Error-frame send so all error paths
// produce the same envelope.
func (c *Client) emitError(reqID, code, msg string) {
	errJSON, _ := json.Marshal(protocol.ErrorBody{Code: code, Message: msg})
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameError, ID: reqID, Body: errJSON})
}

// SendLog pushes a log line to the gateway. Non-blocking with a tight
// timeout — agent log hooks must NEVER stall on a slow gateway link
// or we deadlock our own log writer (a stalled log call holds the
// stdlib log package's mutex, blocking every other goroutine that
// tries to log). On full send-queue we drop the line silently and
// rely on the supplier's local docker logs as the authoritative copy.
//
// Best-effort by design: this is dashboard convenience, not audit.
// Lines lost on a flaky WS link don't get retransmitted.
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
	// Non-blocking enqueue — falls back to drop if sendQ is full.
	// We don't use sendFrame's 5s timeout here because logs fire
	// from inside the log package's mutex and a 5s stall is a
	// deadlock vector.
	select {
	case c.sendQ <- frame:
	default:
	}
}

// writerLoop drains sendQ + heartbeat ticker. Single writer
// goroutine per gorilla/websocket's concurrency rule.
func (c *Client) writerLoop(ctx context.Context) error {
	ticker := time.NewTicker(protocol.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame, ok := <-c.sendQ:
			if !ok {
				return nil // closed
			}
			if err := c.writeFrame(frame); err != nil {
				return err
			}
		case <-ticker.C:
			beat := protocol.Frame{Type: protocol.FrameHeartbeat}
			if err := c.writeFrame(beat); err != nil {
				return err
			}
		}
	}
}

func (c *Client) writeFrame(frame protocol.Frame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(protocol.HeartbeatInterval))
	return c.conn.WriteMessage(websocket.TextMessage, payload)
}

// sendFrame is the non-blocking enqueue used by handleRequest. Drops
// the frame on a full queue with a stderr warning — saturation here
// usually means the gateway-bound link is stalled, which the writer
// loop will surface as a write error and tear the session down.
func (c *Client) sendFrame(frame protocol.Frame) error {
	select {
	case c.sendQ <- frame:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("send queue full for 5s; gateway link stalled")
	}
}

func (c *Client) closeConn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.sendQ)
	if c.conn != nil {
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		_ = c.conn.Close()
	}
}

// wsEndpoint derives wss://host/edge/connect from the configured
// gateway base. Accepts http:// or https:// as the gateway scheme
// and flips to ws:// / wss:// accordingly so tests can point at
// localhost over plaintext.
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
