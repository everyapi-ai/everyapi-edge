package client

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
