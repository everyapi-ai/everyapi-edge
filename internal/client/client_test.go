package client

import (
	"context"
	"crypto/ed25519"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func testFrame() protocol.Frame {
	return protocol.Frame{Type: protocol.FrameLog, Body: []byte(`{"msg":"x"}`)}
}

// shortBudget is the "this should have returned by now" budget used
// across the close-race tests. Has to be well under sendFrame's 5s
// timeout to distinguish "done arm fired" from "timeout fired", but
// generous enough that a heavily-contended CI runner (GOMAXPROCS=1
// under -race) still completes the scheduling round-trip.
const shortBudget = 1 * time.Second

// awaitGoroutineParked is the best-effort barrier the close-race
// tests use to give the sender goroutine time to land in its
// blocking select. The second-round review flagged that observing
// `len(sendQ) == cap(sendQ)` alone proves nothing about the sender
// — the caller pre-filled the buffer, so the condition holds from
// the instant we spawn the worker.
//
// Without a code-side seam (a chan-state hook we explicitly don't
// want in production), the most reliable signal we have is "yield
// many times, then sleep generously". 200ms is empirically enough
// on a 96-core x86 runner under -race -count=10; bump if a future
// runner flakes.
//
// Important: even if the sender hasn't yet entered the select when
// closeConn fires, the test STILL passes correctly. The sender's
// next select sees `<-c.done` already closed and returns via that
// arm. The barrier exists to maximise the probability that the
// path under test is "select wakes a blocked sender" rather than
// "select sees done closed on entry" — both are real-world paths
// the regression we guard against would break.
func awaitGoroutineParked() {
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	time.Sleep(200 * time.Millisecond)
}

// TestSendLogDoesNotPanicAfterClose is the regression guard for the
// SendLog↔closeConn race: the old implementation closed sendQ inside
// closeConn while SendLog wrote to the same channel without
// synchronisation, producing "send on closed channel" panics under
// reconnect-heavy load. The fix replaced close(sendQ) with close(done)
// so the canonical "shutting down" signal is read-only and panic-free.
//
// This test fans out 50 SendLog goroutines while a separate goroutine
// closes the conn. Pre-fix, this reliably panics under `go test -race`;
// post-fix, every SendLog either lands in the queue or selects the
// done arm and returns silently.
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

	// Close mid-fanout. The senders that were already past the select's
	// decision point land in sendQ harmlessly; the rest race against
	// done and bail. Either path is panic-free under the new design.
	c.closeConn()

	wg.Wait()

	// Idempotent close — second call must not panic the sync.Once.
	c.closeConn()
}

// TestSendLogAfterCloseIsSilent pins the "close strictly precedes
// the send" ordering — the most-stressed ordering in the race test
// happens stochastically, this exercise nails it deterministically:
// closeConn fires first, then SendLog runs against a known-closed
// client and must return without panic and without writing anywhere.
func TestSendLogAfterCloseIsSilent(t *testing.T) {
	c := newTestClient(t)
	c.closeConn()

	// Must not panic. Must not block (the done arm wins
	// immediately when the buffer is empty and the select sees
	// done already closed; the default arm wins when sendQ is
	// somehow ready — either is fine for SendLog's
	// best-effort contract).
	c.SendLog("info", "after close")
	c.SendLog("warn", "still no panic")
}

// TestSendFrameUnblocksOnClose pins the sendFrame side of the same
// concern: a sendFrame caller waiting for buffer space (writerLoop
// has stalled or exited) must not sit on the 5s timeout if the client
// is being torn down. The done arm wakes the caller in microseconds
// instead.
//
// Setup: fill sendQ to capacity so the next sendFrame blocks on the
// queue-send arm. Close the client mid-block. Assert sendFrame
// returns promptly (well under the 5s timeout) with a non-nil error.
func TestSendFrameUnblocksOnClose(t *testing.T) {
	c := newTestClient(t)

	// Fill the buffer. writerLoop never started in this test, so
	// nothing drains — every send lands in the buffer until capacity.
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

// TestWriterLoopExitsOnDone pins the writerLoop's done-arm exit —
// the old design relied on `case frame, ok := <-c.sendQ; if !ok` to
// signal "channel closed → return". The new design never closes
// sendQ; closeConn closes c.done instead and writerLoop must pick
// that up. A regression that drops the done arm would leak the
// writer goroutine across every reconnect, eventually exhausting
// stack memory on a long-running supplier.
//
// We don't run a real conn here — by closing done before sendQ ever
// sees activity, writerLoop's done arm wins the select before
// writeFrame can reach c.conn (which is nil in this test client).
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
		// Done arm returns nil; ctx.Done() returns ctx.Err(). We
		// expect the done arm because we fired closeConn, not
		// cancel.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("writerLoop returned unexpected error: %v", err)
		}
	case <-time.After(shortBudget):
		t.Fatal("writerLoop did not exit after closeConn — done arm not wired or sendQ-still-closed regression")
	}
}

// TestTerminalDisconnectError_AsRoundTrip pins the type's behavior
// as an error sentinel: main.go's runWithReconnect calls
// `errors.As(err, &*TerminalDisconnectError{})` to decide whether to
// exit the reconnect loop. A future refactor that changes this to a
// non-pointer receiver or breaks the Error() string would silently
// turn the terminal path into a transient retry.
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

	// And the human-readable string carries both fields so docker
	// logs make the failure mode self-explanatory.
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
