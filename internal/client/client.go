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
	// MaxConcurrentRequests caps how many inbound Request frames the
	// agent forwards at once. A buggy or hostile gateway can stream
	// Request frames faster than the local GPU drains them; without a
	// cap each frame would spawn an unbounded goroutine + HTTP call to
	// Ollama (resource exhaustion on the supplier host). Requests past
	// the cap are rejected immediately with a "node_busy" Error frame
	// (the gateway retries them on another channel/node). Defaulted in
	// New() if <= 0.
	MaxConcurrentRequests int
}

// RequestHandler is what main.go installs to forward inbound
// requests. The agent gives it the parsed RequestBody and a sender
// that emits Chunk frames; the handler returns a Done frame body
// when finished or an Error if forwarding failed. Implementations
// run in their own goroutine so concurrent requests don't serialise.
//
// ctx is the session context: it is cancelled when the WS session
// ends (clean shutdown or read error), so a long-running forward can
// abort its in-flight upstream call instead of leaking past the
// connection it belongs to.
type RequestHandler func(ctx context.Context, req protocol.RequestBody, send func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody)

// defaultMaxConcurrentRequests bounds in-flight forwarded requests
// when Config.MaxConcurrentRequests is unset. A single BYO-GPU node
// rarely benefits from deep concurrency (Ollama serialises on the
// GPU anyway); the cap exists to stop a frame flood from spawning
// unbounded goroutines, not to maximise throughput.
const defaultMaxConcurrentRequests = 16

// The handshake challenge is a compact JSON envelope containing one nonce.
// Keep a generous ceiling while preventing a hostile gateway or proxy from
// growing the edge agent without bound before the WebSocket session starts.
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

// TerminalDisconnectError is the typed wrapper for a gateway-side
// Disconnect frame whose Code marks the session as unrecoverable
// (currently: node_revoked). The reconnect loop in main.go checks
// `errors.As(err, &*TerminalDisconnectError{})` and exits cleanly
// instead of backing off — the operator's intent on the server side
// (delete the node row) shouldn't be undone by a tight retry loop
// on the supplier side.
type TerminalDisconnectError struct {
	Code   string
	Reason string
}

func (e *TerminalDisconnectError) Error() string {
	return "gateway disconnect (terminal): " + e.Code + ": " + e.Reason
}

// Client is a single WebSocket session. New() returns one not yet
// connected; Run() connects, authenticates, and blocks until the
// session ends.
type Client struct {
	cfg Config

	// closeOnce wraps the conn-shutdown + done-fire path so multiple
	// callers (Run's defer, the reader/writer loop's own error
	// returns, and any external Close()) can race in without
	// double-closing the conn or done channel.
	closeOnce sync.Once
	conn      *websocket.Conn
	sendQ     chan protocol.Frame
	// done is closed exactly once by closeConn. Senders (SendLog,
	// sendFrame) select on it so a concurrent close doesn't panic
	// them with "send on closed channel" — instead the send arm
	// loses the race and the sender exits via the done arm.
	// writerLoop also selects on it to exit cleanly after closeConn
	// fires from outside its own error path.
	done chan struct{}

	// sem bounds concurrent handleRequest goroutines (buffered to
	// cfg.MaxConcurrentRequests). readerLoop try-acquires a slot before
	// spawning a handler; on a full pool dispatch rejects the request
	// with a node_busy Error frame instead of parking the reader (see
	// dispatch for why blocking here would tear the session down).
	sem chan struct{}
	// wg tracks in-flight handlers so Run can drain them before
	// returning — combined with session-ctx cancellation, no handler
	// (or its upstream HTTP call) outlives the session it belongs to.
	wg sync.WaitGroup
	// inflight is the live handler count, surfaced to the gateway in
	// each heartbeat (HeartbeatBody.ActiveReqs) so it has back-pressure
	// visibility into how loaded this node is.
	inflight atomic.Int64

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
	if cfg.MaxConcurrentRequests <= 0 {
		cfg.MaxConcurrentRequests = defaultMaxConcurrentRequests
	}
	return &Client{
		cfg:   cfg,
		sendQ: make(chan protocol.Frame, 32),
		done:  make(chan struct{}),
		sem:   make(chan struct{}, cfg.MaxConcurrentRequests),
	}, nil
}

// Run connects, authenticates, and runs the session. Returns when the
// connection ends — nil for a clean shutdown, non-nil for any error.
// Callers re-invoke after a backoff for the reconnect loop.
func (c *Client) Run(ctx context.Context) error {
	if err := c.connectAndAuth(ctx); err != nil {
		return err
	}
	// sessionCtx cancels when Run returns for ANY reason (read error,
	// write error, or parent cancel). In-flight handlers derive their
	// upstream-call deadline from it, so a gateway-side disconnect
	// aborts their HTTP work instead of leaving it to run against the
	// local Ollama after the session is gone.
	sessionCtx, cancel := context.WithCancel(ctx)
	// readerDone closes only after readerLoop has fully returned.
	// Shutdown MUST wait on it before wg.Wait(): every wg.Add happens
	// on the reader goroutine (inside dispatch), so starting wg.Wait
	// while the reader is still running races Add against Wait —
	// either a "sync: WaitGroup misuse" panic or a Wait that returns
	// while a freshly-spawned handler is still in flight.
	readerDone := make(chan struct{})
	// Defers run LIFO: closeConn first (close done + conn → unblock a
	// parked ReadMessage and any parked sender), then wait for the
	// reader to exit (after which no further wg.Add can happen), then
	// cancel (abort in-flight upstream calls), then wg.Wait (drain
	// handlers) so Run never returns while goroutines it spawned are
	// still touching the connection.
	defer c.wg.Wait()
	defer cancel()
	defer func() { <-readerDone }()
	defer c.closeConn()

	readErr := make(chan error, 1)
	writeErr := make(chan error, 1)

	go func() {
		// readErr is buffered, so the send completes even when Run is
		// already past its select; readerDone then closes strictly
		// after readerLoop (and any dispatch it was inside) returned.
		defer close(readerDone)
		readErr <- c.readerLoop(sessionCtx)
	}()
	go func() { writeErr <- c.writerLoop(sessionCtx) }()

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
	raw, err := readChallengeResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read challenge response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("challenge endpoint returned %s: %s", resp.Status, string(raw))
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
			if err := c.dispatch(ctx, frame); err != nil {
				if errors.Is(err, errReaderClosed) {
					return nil
				}
				return err
			}
		case protocol.FrameDisconnect:
			var body protocol.DisconnectBody
			if uErr := json.Unmarshal(frame.Body, &body); uErr != nil {
				// Surface the parse failure on stderr instead of
				// silently dropping it — a malformed Disconnect body
				// whose Code field doesn't decode would otherwise fall
				// to the transient "gateway disconnect" path with an
				// empty code and the agent would reconnect forever
				// against a gateway that meant to send a terminal
				// signal. Treat unparseable Disconnect as transient
				// (return a generic error) so the next reconnect can
				// fetch a fresh, parseable frame; but at least the
				// operator sees the parse error in docker logs.
				fmt.Fprintf(stderr(), "edge-agent: malformed Disconnect body: %v (frame=%q)\n", uErr, string(frame.Body))
				return fmt.Errorf("malformed gateway disconnect frame: %w", uErr)
			}
			// Terminal codes propagate as typed errors so
			// runWithReconnect in main.go can stop the reconnect
			// loop instead of treating them like a generic blip.
			// Everything else collapses into a generic
			// "gateway disconnect" wrap and the outer loop retries
			// with backoff.
			if body.Code == protocol.DisconnectCodeNodeRevoked {
				return &TerminalDisconnectError{Code: body.Code, Reason: body.Reason}
			}
			return fmt.Errorf("gateway disconnect: %s: %s", body.Code, body.Reason)
		default:
			fmt.Fprintf(stderr(), "edge-agent: unexpected frame type %q from gateway\n", frame.Type)
		}
	}
}

// errReaderClosed is the internal sentinel dispatch returns when the
// session is closing via the done channel (vs ctx cancellation). The
// reader loop maps it to a clean (nil) exit, matching the original
// inline behavior where a done-close meant "stop, no error".
var errReaderClosed = errors.New("reader closed")

// errCodeNodeBusy is the Error-frame code dispatch emits when the
// concurrency pool is full. Gateway-side this surfaces as a pre-chunk
// FrameError: waitForFirstChunk (relay/channel/edge/adaptor.go) fails
// the DoRequest before any byte reaches the buyer, the relay wraps it
// as a retryable 500 (ErrorCodeDoRequestFailed), and shouldRetry
// reroutes the request to another channel/node.
const errCodeNodeBusy = "node_busy"

// dispatch try-acquires a concurrency slot and starts a handler
// goroutine for an inbound Request frame. It MUST NOT block on a full
// pool: the WS read deadline (HeartbeatTimeout, 30s) is only refreshed
// by successful reads, while forwarded requests run for minutes — a
// reader parked here would let the deadline expire, so the next
// ReadMessage after a slot freed would fail with an i/o timeout and
// tear the whole session down (aborting every in-flight handler).
// Parking would also stall Disconnect-frame handling (incl.
// node_revoked) for the duration of the saturation. Instead, a full
// pool rejects the request immediately with a node_busy Error frame
// and the reader keeps draining; the gateway retries the buyer request
// on another node (see errCodeNodeBusy). Returns ctx.Err() if the
// session context was cancelled, errReaderClosed if closeConn fired,
// or nil once the frame was either handed to a handler or rejected.
func (c *Client) dispatch(ctx context.Context, frame protocol.Frame) error {
	select {
	case c.sem <- struct{}{}:
		// Slot acquired — but if done/ctx became ready at the same
		// time, select picks an arm at random and can land here
		// mid-shutdown. Re-check before wg.Add so no handler spawns
		// once the session is closing (Run's reader-exit barrier makes
		// a late Add safe for wg.Wait, but the handler would only burn
		// an upstream call against a dead session).
		select {
		case <-c.done:
			<-c.sem
			return errReaderClosed
		case <-ctx.Done():
			<-c.sem
			return ctx.Err()
		default:
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errReaderClosed
	default:
		// Pool full. Reject without blocking the reader: emitError →
		// sendFrame would park up to 5s if sendQ is saturated, and a
		// parked reader stops refreshing the WS read deadline — the very
		// stall this reject path exists to avoid. Drop the reject if the
		// queue is full; the gateway's first-chunk wait still times out
		// and reroutes, so it degrades gracefully.
		c.trySendError(frame.ID, errCodeNodeBusy,
			fmt.Sprintf("node at max concurrent requests (%d)", c.cfg.MaxConcurrentRequests))
		return nil
	}
	c.wg.Add(1)
	c.inflight.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.inflight.Add(-1)
		defer func() { <-c.sem }()
		c.handleRequest(ctx, frame)
	}()
	return nil
}

// handleRequest is the per-request goroutine: parse, hand to the
// installed Handler, emit Chunk frames as the handler streams, and
// terminate with Done/Error.
func (c *Client) handleRequest(ctx context.Context, frame protocol.Frame) {
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
	done, errBody := c.cfg.Handler(ctx, body, send)
	if errBody != nil {
		c.emitError(frame.ID, errBody.Code, errBody.Message)
		return
	}
	doneJSON, _ := json.Marshal(done)
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameDone, ID: frame.ID, Body: doneJSON})
}

// emitError centralises the Error-frame send so all error paths
// produce the same envelope. Used from handler goroutines, where the
// sendFrame 5s timeout is acceptable.
func (c *Client) emitError(reqID, code, msg string) {
	errJSON, _ := json.Marshal(protocol.ErrorBody{Code: code, Message: msg})
	_ = c.sendFrame(protocol.Frame{Type: protocol.FrameError, ID: reqID, Body: errJSON})
}

// trySendError enqueues an Error frame without ever blocking the
// caller — the node_busy reject path runs on the reader goroutine,
// where the sendFrame 5s timeout would stall reads and risk the WS
// read deadline expiring. Mirrors SendLog's non-blocking enqueue:
// drop the frame if sendQ is full or the client is closing.
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
	//
	// The done arm covers the close race: if closeConn fires between
	// the caller's currentClient.Load() and this select, the done
	// channel races against the send arm. Without it, a concurrent
	// closeConn that closed sendQ (older design) would panic the
	// sender with "send on closed channel". Now sendQ is never
	// closed and done is the close signal.
	select {
	case c.sendQ <- frame:
	case <-c.done:
	default:
	}
}

// writerLoop drains sendQ + heartbeat ticker. Single writer
// goroutine per gorilla/websocket's concurrency rule.
//
// Exits on ctx cancellation or done (closeConn fired). The done arm
// replaces the older "sendQ closed → ok=false → return" pattern: we
// no longer close sendQ at all, because senders (SendLog, sendFrame)
// would panic on a closed channel under reconnect-heavy load.
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
			// Carry the live in-flight count so the gateway has
			// back-pressure visibility; body is best-effort, a marshal
			// failure just sends a bare heartbeat (liveness still works).
			beat := protocol.Frame{Type: protocol.FrameHeartbeat}
			if body, err := json.Marshal(protocol.HeartbeatBody{
				NowUnixMs:  time.Now().UnixMilli(),
				ActiveReqs: int(c.inflight.Load()),
			}); err == nil {
				beat.Body = body
			}
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
//
// The done arm is the close-race guard: a concurrent closeConn must
// not panic an in-flight send. With sendQ never closed (see the
// writerLoop comment) done is the canonical "we're shutting down"
// signal for senders.
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

// closeConn tears down the WS session exactly once. Idempotent via
// sync.Once — Run's defer, the reader/writer loop returns, and any
// external Close() can all race in without double-closing the conn.
//
// Closing the done channel signals every active sender to bail out;
// sendQ is intentionally NOT closed, because a closed channel +
// concurrent sender = panic, which under reconnect-heavy load is
// exactly the failure mode we used to hit.
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
