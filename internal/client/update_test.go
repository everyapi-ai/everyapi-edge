package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func TestHandleUpdateRunsLatestAndReportsCorrelatedStatus(t *testing.T) {
	c := &Client{
		cfg: Config{Update: func(_ context.Context, report func(protocol.UpdateStatusBody)) error {
			report(protocol.UpdateStatusBody{State: protocol.UpdateStateDownloading, Version: "1.2.3"})
			return nil
		}},
		sendQ: make(chan protocol.Frame, 2), done: make(chan struct{}),
	}
	body, _ := json.Marshal(protocol.UpdateBody{Action: protocol.UpdateActionLatest})
	c.handleUpdate(context.Background(), protocol.Frame{Type: protocol.FrameUpdate, ID: "cmd-1", Body: body})
	select {
	case frame := <-c.sendQ:
		if frame.Type != protocol.FrameUpdateStatus || frame.ID != "cmd-1" {
			t.Fatalf("frame = %#v", frame)
		}
		var status protocol.UpdateStatusBody
		if err := json.Unmarshal(frame.Body, &status); err != nil || status.State != protocol.UpdateStateDownloading {
			t.Fatalf("status = %#v, %v", status, err)
		}
	case <-time.After(time.Second):
		t.Fatal("update status was not sent")
	}
}

func TestHandleUpdateRejectsUnsupportedAction(t *testing.T) {
	called := false
	c := &Client{cfg: Config{Update: func(context.Context, func(protocol.UpdateStatusBody)) error { called = true; return nil }}, sendQ: make(chan protocol.Frame, 2), done: make(chan struct{})}
	body, _ := json.Marshal(protocol.UpdateBody{Action: "https://attacker.invalid/payload"})
	c.handleUpdate(context.Background(), protocol.Frame{ID: "cmd-2", Body: body})
	select {
	case frame := <-c.sendQ:
		var status protocol.UpdateStatusBody
		_ = json.Unmarshal(frame.Body, &status)
		if status.State != protocol.UpdateStateFailed {
			t.Fatalf("status = %#v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("failure status was not sent")
	}
	if called {
		t.Fatal("unsupported update action reached updater")
	}
}

func TestRunCancelsRemoteUpdateBeforeReturningAfterGatewayDisconnect(t *testing.T) {
	updateStarted := make(chan struct{})
	updateCanceled := make(chan struct{})
	updateStopped := make(chan struct{})
	releaseUpdate := make(chan struct{})
	var releaseUpdateOnce sync.Once
	release := func() { releaseUpdateOnce.Do(func() { close(releaseUpdate) }) }
	disconnect := make(chan struct{})
	var disconnectOnce sync.Once
	disconnectGateway := func() { disconnectOnce.Do(func() { close(disconnect) }) }

	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(response, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		welcomeBody, err := json.Marshal(protocol.WelcomeBody{SessionID: "remote-update-session", ProtocolVersion: protocol.ProtocolVersion})
		if err != nil {
			return
		}
		if err := conn.WriteJSON(protocol.Frame{Type: protocol.FrameWelcome, Body: welcomeBody}); err != nil {
			return
		}
		updateBody, err := json.Marshal(protocol.UpdateBody{Action: protocol.UpdateActionLatest})
		if err != nil {
			return
		}
		if err := conn.WriteJSON(protocol.Frame{Type: protocol.FrameUpdate, ID: "update-1", Body: updateBody}); err != nil {
			return
		}
		<-disconnect
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}))

	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancelRun()
		disconnectGateway()
		release()
		gateway.CloseClientConnections()
		gateway.Close()
	})
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	client, err := New(Config{
		GatewayURL:        gateway.URL,
		NodeID:            1,
		RegistrationToken: "one-shot",
		Identity:          identity.Decoded{Public: public, Private: private},
		Update: func(ctx context.Context, _ func(protocol.UpdateStatusBody)) error {
			defer close(updateStopped)
			close(updateStarted)
			<-ctx.Done()
			close(updateCanceled)
			<-releaseUpdate
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- client.Run(runCtx) }()

	select {
	case <-updateStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("remote update did not start")
	}
	disconnectGateway()
	select {
	case <-updateCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway disconnect did not cancel remote update")
	}
	select {
	case err := <-runErr:
		t.Fatalf("Run returned before its session remote update stopped: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after remote update stopped")
	}
	select {
	case <-updateStopped:
	default:
		t.Fatal("Run returned before the remote update callback exited")
	}
}

func TestHandleControlReportsOneCorrelatedResponse(t *testing.T) {
	c := &Client{
		cfg: Config{ControlHandler: func(_ context.Context, req protocol.ControlRequestBody) (protocol.ChunkBody, *protocol.ErrorBody) {
			if req.Method != "GET" || req.Path != "/api/models" {
				t.Fatalf("unexpected request: %#v", req)
			}
			return protocol.ChunkBody{StatusCode: 200, Bytes: base64.StdEncoding.EncodeToString([]byte(`[{"name":"qwen3"}]`))}, nil
		}},
		sendQ: make(chan protocol.Frame, 2), done: make(chan struct{}),
	}
	body, _ := json.Marshal(protocol.ControlRequestBody{Method: "GET", Path: "/api/models"})
	c.handleControl(context.Background(), protocol.Frame{Type: protocol.FrameControlRequest, ID: "control-1", Body: body})
	chunk := <-c.sendQ
	if chunk.Type != protocol.FrameChunk || chunk.ID != "control-1" {
		t.Fatalf("chunk = %#v", chunk)
	}
	done := <-c.sendQ
	if done.Type != protocol.FrameDone || done.ID != "control-1" {
		t.Fatalf("done = %#v", done)
	}
}
