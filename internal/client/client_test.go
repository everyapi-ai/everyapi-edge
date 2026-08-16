package client

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func testFrame() protocol.Frame {
	return protocol.Frame{Type: protocol.FrameLog, Body: []byte(`{"msg":"x"}`)}
}

func TestFetchChallengeRejectsOversizedSuccessResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, endlessSpaces{}, maxChallengeResponseBytes+1)
		_, _ = io.WriteString(w, `{"success":true,"data":{"challenge":"late"}}`)
	}))
	defer srv.Close()

	c := &Client{cfg: Config{GatewayURL: srv.URL, NodeID: 1, HTTPClient: srv.Client()}}
	_, err := c.fetchChallenge(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v, want response-size error", err)
	}
}

func TestFetchChallengeRejectsOversizedErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.CopyN(w, endlessSpaces{}, maxChallengeResponseBytes+1)
	}))
	defer srv.Close()

	c := &Client{cfg: Config{GatewayURL: srv.URL, NodeID: 1, HTTPClient: srv.Client()}}
	_, err := c.fetchChallenge(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v, want response-size error", err)
	}
}

func TestFetchChallengeErrorDoesNotIncludeRemoteBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"api-key-should-never-reach-the-console"}`)
	}))
	defer srv.Close()

	c := &Client{cfg: Config{GatewayURL: srv.URL, NodeID: 1, HTTPClient: srv.Client()}}
	_, err := c.fetchChallenge(context.Background())
	if err == nil || strings.Contains(err.Error(), "api-key-should-never-reach-the-console") {
		t.Fatalf("error leaked remote response body: %v", err)
	}
}

func TestUnexpectedHandshakeFrameErrorIsFixedText(t *testing.T) {
	if got := unexpectedHandshakeFrameError().Error(); got != "unexpected gateway handshake frame" {
		t.Fatalf("error = %q", got)
	}
}

func TestClientUsesConfiguredLogSink(t *testing.T) {
	var gotLevel, gotMessage string
	c := &Client{cfg: Config{Log: func(level, message string) { gotLevel, gotMessage = level, message }}}
	c.log("warn", "malformed gateway frame")
	if gotLevel != "warn" || gotMessage != "malformed gateway frame" {
		t.Fatalf("log sink received %q/%q", gotLevel, gotMessage)
	}
}

func TestHeartbeatTelemetryReportsResidentModelMemory(t *testing.T) {
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Fatalf("runtime path = %q, want /api/ps", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[{"size_vram":2147483648},{"size_vram":1073741824}]}`)
	}))
	defer runtimeServer.Close()

	c := &Client{cfg: Config{OllamaURL: runtimeServer.URL, VRAMTotalGB: 24, HTTPClient: runtimeServer.Client()}}
	got := c.heartbeatTelemetry(context.Background())
	if got.VRAMTotalGB != 24 {
		t.Fatalf("VRAM total = %d, want 24", got.VRAMTotalGB)
	}
	if got.VRAMUsedGB != 3 {
		t.Fatalf("VRAM used = %v, want 3", got.VRAMUsedGB)
	}
}

func TestSettlementReceiptIsParsedAndMalformedInputIsDropped(t *testing.T) {
	var receipts []protocol.SettlementBody
	var warnings int
	c := &Client{cfg: Config{
		Settlement: func(receipt protocol.SettlementBody) { receipts = append(receipts, receipt) },
		Log: func(level, _ string) {
			if level == "warn" {
				warnings++
			}
		},
	}}
	c.handleSettlement(json.RawMessage(`{"request_id":"receipt-1","seller_amount_micros":125000,"settled_at_unix_ms":1700000000000}`))
	c.handleSettlement(json.RawMessage(`{"seller_amount_micros":125000}`))
	if len(receipts) != 1 || receipts[0].RequestID != "receipt-1" || receipts[0].SellerAmountMicros != 125000 {
		t.Fatalf("receipts = %+v", receipts)
	}
	if warnings != 1 {
		t.Fatalf("warnings = %d", warnings)
	}
}

type endlessSpaces struct{}

func (endlessSpaces) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

// shortBudget is the "this should have returned by now" budget used across the close-race tests. Has to be well under sendFrame's 5s timeout to distinguish "done arm fired" from "timeout fired", but generous enough that a heavily-contended CI runner (GOMAXPROCS=1 under -race) still completes the scheduling round-trip.
const shortBudget = 1 * time.Second

// awaitGoroutineParked is the best-effort barrier the close-race tests use to give the sender goroutine time to land in its blocking select. The second-round review flagged that observing `len(sendQ) == cap(sendQ)` alone proves nothing about the sender — the caller pre-filled the buffer, so the condition holds from the instant we spawn the worker.
//
// Without a code-side seam (a chan-state hook we explicitly don't want in production), the most reliable signal we have is "yield many times, then sleep generously". 200ms is empirically enough on a 96-core x86 runner under -race -count=10; bump if a future runner flakes.
//
// Important: even if the sender hasn't yet entered the select when closeConn fires, the test STILL passes correctly. The sender's next select sees `<-c.done` already closed and returns via that arm. The barrier exists to maximise the probability that the path under test is "select wakes a blocked sender" rather than "select sees done closed on entry" — both are real-world paths the regression we guard against would break.
func awaitGoroutineParked() {
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	time.Sleep(200 * time.Millisecond)
}

// TestSendLogDoesNotPanicAfterClose is the regression guard for the SendLog↔closeConn race: the old implementation closed sendQ inside closeConn while SendLog wrote to the same channel without synchronisation, producing "send on closed channel" panics under reconnect-heavy load. The fix replaced close(sendQ) with close(done) so the canonical "shutting down" signal is read-only and panic-free.
//
// This test fans out 50 SendLog goroutines while a separate goroutine closes the conn. Pre-fix, this reliably panics under `go test -race`; post-fix, every SendLog either lands in the queue or selects the done arm and returns silently.
func TestSendLogDoesNotPanicAfterClose(t *testing.T) {
	c := newTestClient(t)

	var wg sync.WaitGroup
	const senders = 50
	const perSender = 100

	wg.Add(senders)
	for i := 0; i < senders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perSender; j++ {
				c.SendLog("info", "race")
			}
		}()
	}

	// Close mid-fanout. The senders that were already past the select's decision point land in sendQ harmlessly; the rest race against done and bail. Either path is panic-free under the new design.
	c.closeConn()

	wg.Wait()

	// Idempotent close — second call must not panic the sync.Once.
	c.closeConn()
}

// TestSendLogAfterCloseIsSilent pins the "close strictly precedes the send" ordering — the most-stressed ordering in the race test happens stochastically, this exercise nails it deterministically: closeConn fires first, then SendLog runs against a known-closed client and must return without panic and without writing anywhere.
func TestSendLogAfterCloseIsSilent(t *testing.T) {
	c := newTestClient(t)
	c.closeConn()

	// Must not panic. Must not block (the done arm wins immediately when the buffer is empty and the select sees done already closed; the default arm wins when sendQ is somehow ready — either is fine for SendLog's best-effort contract).
	c.SendLog("info", "after close")
	c.SendLog("warn", "still no panic")
}

// TestSendFrameUnblocksOnClose pins the sendFrame side of the same concern: a sendFrame caller waiting for buffer space (writerLoop has stalled or exited) must not sit on the 5s timeout if the client is being torn down. The done arm wakes the caller in microseconds instead.
//
// Setup: fill sendQ to capacity so the next sendFrame blocks on the queue-send arm. Close the client mid-block. Assert sendFrame returns promptly (well under the 5s timeout) with a non-nil error.
func TestSendFrameUnblocksOnClose(t *testing.T) {
	c := newTestClient(t)

	// Fill the buffer. writerLoop never started in this test, so nothing drains — every send lands in the buffer until capacity.
	for i := 0; i < cap(c.sendQ); i++ {
		c.sendQ <- testFrame()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sendFrame(testFrame())
	}()

	awaitGoroutineParked()
	c.closeConn()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil error after close, got nil")
		}
	case <-time.After(shortBudget):
		t.Fatal("sendFrame did not return after closeConn — done arm not wired")
	}
}

// TestWriterLoopExitsOnDone pins the writerLoop's done-arm exit — the old design relied on `case frame, ok := <-c.sendQ; if !ok` to signal "channel closed → return". The new design never closes sendQ; closeConn closes c.done instead and writerLoop must pick that up. A regression that drops the done arm would leak the writer goroutine across every reconnect, eventually exhausting stack memory on a long-running supplier.
//
// We don't run a real conn here — by closing done before sendQ ever sees activity, writerLoop's done arm wins the select before writeFrame can reach c.conn (which is nil in this test client).
func TestWriterLoopExitsOnDone(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.writerLoop(ctx)
	}()

	c.closeConn()

	select {
	case err := <-errCh:
		// Done arm returns nil; ctx.Done() returns ctx.Err(). We expect the done arm because we fired closeConn, not cancel.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("writerLoop returned unexpected error: %v", err)
		}
	case <-time.After(shortBudget):
		t.Fatal("writerLoop did not exit after closeConn — done arm not wired or sendQ-still-closed regression")
	}
}

// TestTerminalDisconnectError_AsRoundTrip pins the type's behavior as an error sentinel: main.go's runWithReconnect calls `errors.As(err, &*TerminalDisconnectError{})` to decide whether to exit the reconnect loop. A future refactor that changes this to a non-pointer receiver or breaks the Error() string would silently turn the terminal path into a transient retry.
func TestTerminalDisconnectError_AsRoundTrip(t *testing.T) {
	const wantCode = "node_revoked"
	const wantReason = "node deleted via /api/seller/edge/nodes"

	wrapped := errors.Join(
		errors.New("ws read context"),
		&TerminalDisconnectError{Code: wantCode, Reason: wantReason},
	)

	var got *TerminalDisconnectError
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As should unwrap a TerminalDisconnectError; got nil")
	}
	if got.Code != wantCode || got.Reason != wantReason {
		t.Fatalf("unwrapped fields drifted: code=%q reason=%q", got.Code, got.Reason)
	}

	// And the human-readable string carries both fields so docker logs make the failure mode self-explanatory.
	msg := got.Error()
	for _, want := range []string{wantCode, wantReason, "terminal"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Error() missing %q: %q", want, msg)
		}
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	c, err := New(Config{
		GatewayURL: "https://localhost",
		NodeID:     1,
		Identity:   identity.Decoded{Public: pub, Private: priv},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// newCappedClient builds a client with MaxConcurrentRequests = cap and a handler that signals `started` then parks until `release` closes — the shared fixture for the saturation tests.
func newCappedClient(t *testing.T, cap int, started chan struct{}, release chan struct{}) *Client {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	c, err := New(Config{
		GatewayURL:            "https://localhost",
		NodeID:                1,
		Identity:              identity.Decoded{Public: pub, Private: priv},
		MaxConcurrentRequests: cap,
		Handler: func(_ context.Context, _ protocol.RequestBody, _ func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			started <- struct{}{}
			<-release
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestDispatchBoundsConcurrency proves a Request-frame flood never runs more than MaxConcurrentRequests handlers at once — the cap that stops an unbounded goroutine + upstream-call leak when a buggy or hostile gateway streams requests faster than the GPU drains them. Requests past the cap are rejected with a node_busy Error frame instead of queueing (see TestDispatchRejectsWhenSaturated for the reject path itself).
func TestDispatchBoundsConcurrency(t *testing.T) {
	const cap = 2
	started := make(chan struct{}, 16)
	release := make(chan struct{})
	c := newCappedClient(t, cap, started, release)

	ctx := context.Background()
	for i := 0; i < cap+2; i++ {
		if dErr := c.dispatch(ctx, protocol.Frame{Type: protocol.FrameRequest, ID: "req", Body: []byte("{}")}); dErr != nil {
			t.Fatalf("dispatch: %v", dErr)
		}
	}

	// Exactly cap handlers run; the two dispatches past the cap were rejected (not queued), so no further handler may ever start.
	for i := 0; i < cap; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("handler %d did not start", i)
		}
	}
	select {
	case <-started:
		t.Fatal("a handler started beyond MaxConcurrentRequests")
	case <-time.After(200 * time.Millisecond):
	}

	if got := c.inflight.Load(); got != cap {
		t.Fatalf("inflight = %d, want %d", got, cap)
	}
	close(release)

	c.wg.Wait()
	if got := c.inflight.Load(); got != 0 {
		t.Fatalf("inflight after drain = %d, want 0", got)
	}
}

// TestDispatchRejectsWhenSaturated pins the non-blocking saturation contract that keeps the session alive under load: with every slot busy, dispatch must (1) return promptly instead of parking the reader — a parked reader stops refreshing the WS read deadline (HeartbeatTimeout = 30s) while forwarded requests run for minutes, so the next ReadMessage after a slot freed would hit an expired deadline and tear down the whole session — and (2) emit a node_busy Error frame carrying the request's ID so the gateway can retry the buyer request on another node.
func TestDispatchRejectsWhenSaturated(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	c := newCappedClient(t, 1, started, release)

	ctx := context.Background()
	if err := c.dispatch(ctx, protocol.Frame{Type: protocol.FrameRequest, ID: "req-held", Body: []byte("{}")}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Once the handler signalled, its slot is held until `release` closes — the pool is deterministically full from here on.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("slot-holding handler did not start")
	}

	// Saturated dispatch must return promptly, not block until a slot frees. Run it in a goroutine so a regression to blocking semantics fails the budget below instead of hanging the test.
	dispatched := make(chan error, 1)
	go func() {
		dispatched <- c.dispatch(ctx, protocol.Frame{Type: protocol.FrameRequest, ID: "req-busy", Body: []byte("{}")})
	}()
	select {
	case err := <-dispatched:
		if err != nil {
			t.Fatalf("saturated dispatch returned error: %v", err)
		}
	case <-time.After(shortBudget):
		t.Fatal("dispatch blocked on a full pool — reader would miss its read deadline")
	}

	// The rejected request must have produced a node_busy Error frame addressed to its request ID. writerLoop isn't running in this test, so the frame is still sitting in sendQ.
	select {
	case frame := <-c.sendQ:
		if frame.Type != protocol.FrameError {
			t.Fatalf("frame type = %q, want %q", frame.Type, protocol.FrameError)
		}
		if frame.ID != "req-busy" {
			t.Fatalf("frame ID = %q, want %q", frame.ID, "req-busy")
		}
		var body protocol.ErrorBody
		if err := json.Unmarshal(frame.Body, &body); err != nil {
			t.Fatalf("unmarshal error body: %v", err)
		}
		if body.Code != errCodeNodeBusy {
			t.Fatalf("error code = %q, want %q", body.Code, errCodeNodeBusy)
		}
	default:
		t.Fatal("no Error frame enqueued for the rejected request")
	}
}

// TestDispatchAfterCloseDoesNotSpawnHandler pins the post-acquire shutdown re-check: when done is closed AND a semaphore slot is free, both select arms are ready and Go picks one at random — the sem arm can win mid-shutdown. dispatch must still notice done, release the slot, and return errReaderClosed WITHOUT calling wg.Add or spawning a handler. Looped so both arms get exercised across iterations.
func TestDispatchAfterCloseDoesNotSpawnHandler(t *testing.T) {
	for i := 0; i < 200; i++ {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		c := newCappedClient(t, 1, started, release)
		c.closeConn()

		err := c.dispatch(context.Background(), protocol.Frame{Type: protocol.FrameRequest, ID: "req", Body: []byte("{}")})
		if !errors.Is(err, errReaderClosed) {
			t.Fatalf("iter %d: dispatch after close = %v, want errReaderClosed", i, err)
		}
		// No handler may have spawned, and a slot grabbed by the sem arm must have been released again.
		select {
		case <-started:
			t.Fatalf("iter %d: handler spawned after close", i)
		default:
		}
		if got := len(c.sem); got != 0 {
			t.Fatalf("iter %d: semaphore slot leaked: len(sem) = %d", i, got)
		}
		if got := c.inflight.Load(); got != 0 {
			t.Fatalf("iter %d: inflight = %d, want 0", i, got)
		}
		// wg must be at zero — Wait returns immediately, no Add leaked.
		c.wg.Wait()
		close(release)
	}
}

// TestRunSaturationAndShutdown is the end-to-end guard for both session-stability fixes, against a real (in-process) WS gateway:
//
//  1. Saturation must not kill the session: with every slot held by a parked handler, over-cap Request frames are rejected with node_busy Error frames while the reader keeps draining (the old blocking dispatch would park the reader, let the read deadline expire, and tear the session down once a slot freed).
//  2. Shutdown while saturated must neither panic nor leak handlers: Run's reader-exit barrier guarantees no wg.Add races wg.Wait ("sync: WaitGroup misuse"), and wg.Wait guarantees every handler exited before Run returns.
//
// Sequencing is condition-based (welcome → first node_busy at the gateway → cancel → Run return); the timeouts are failure budgets, not synchronization.
func TestRunSaturationAndShutdown(t *testing.T) {
	const maxConcurrent = 2
	const floodFrames = 50 // > maxConcurrent ⇒ guarantees rejects

	upgrader := websocket.Upgrader{}
	busySeen := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Auth → Welcome handshake.
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		welcome, _ := json.Marshal(protocol.Frame{
			Type: protocol.FrameWelcome,
			Body: []byte(`{"session_id":"test","protocol_version":"1.0"}`),
		})
		if err := conn.WriteMessage(websocket.TextMessage, welcome); err != nil {
			return
		}
		// Drain inbound (heartbeats, chunk/done frames, rejects) and flag the first node_busy Error frame. Exits on read error, i.e. when the agent closes the conn during shutdown.
		drainDone := make(chan struct{})
		go func() {
			defer close(drainDone)
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var f protocol.Frame
				if json.Unmarshal(raw, &f) != nil || f.Type != protocol.FrameError {
					continue
				}
				var body protocol.ErrorBody
				if json.Unmarshal(f.Body, &body) == nil && body.Code == errCodeNodeBusy {
					select {
					case busySeen <- struct{}{}:
					default:
					}
				}
			}
		}()
		// Flood more Request frames than the agent has slots.
		for i := 0; i < floodFrames; i++ {
			frame, _ := json.Marshal(protocol.Frame{
				Type: protocol.FrameRequest,
				ID:   fmt.Sprintf("req-%d", i),
				Body: []byte(`{"method":"POST","path":"/v1/chat/completions"}`),
			})
			if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				return
			}
		}
		// Keep the session open until the agent disconnects.
		<-drainDone
	}))
	defer srv.Close()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	var live atomic.Int64
	connected := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL:            srv.URL,
		NodeID:                1,
		RegistrationToken:     "tok", // skip the challenge HTTP round-trip
		Identity:              identity.Decoded{Public: pub, Private: priv},
		MaxConcurrentRequests: maxConcurrent,
		OnConnected: func() {
			connected <- struct{}{}
		},
		Handler: func(ctx context.Context, _ protocol.RequestBody, _ func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			// Simulate a long Ollama forward: park until the session context aborts it. Keeps every slot held so the flood deterministically saturates the pool.
			live.Add(1)
			defer live.Add(-1)
			<-ctx.Done()
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()

	// A node_busy frame landing at the gateway proves: handshake completed, maxConcurrent handlers hold their slots, the reader processed an over-cap frame past them, and the session is still alive while saturated.
	select {
	case <-busySeen:
	case <-time.After(5 * time.Second):
		t.Fatal("no node_busy Error frame reached the gateway — reader blocked or session died under saturation")
	}
	select {
	case <-connected:
	case <-time.After(shortBudget):
		t.Fatal("OnConnected was not called after the gateway Welcome frame")
	}
	if got := c.inflight.Load(); got != maxConcurrent {
		t.Fatalf("inflight = %d, want %d", got, maxConcurrent)
	}

	// Shutdown mid-saturation: Run must return promptly (reader-exit barrier, then cancel unparks the handlers, then wg.Wait drains them) without a WaitGroup panic.
	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel — shutdown ordering deadlocked")
	}
	// Run's deferred wg.Wait ran before it returned, so no handler may still be live and no in-flight count may linger.
	if got := live.Load(); got != 0 {
		t.Fatalf("%d handler(s) still live after Run returned", got)
	}
	if got := c.inflight.Load(); got != 0 {
		t.Fatalf("inflight = %d after Run returned, want 0", got)
	}
}
