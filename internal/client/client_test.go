package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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

type readDeadlineRecordingConn struct {
	net.Conn
	events chan time.Time
}

func (c *readDeadlineRecordingConn) SetReadDeadline(deadline time.Time) error {
	if err := c.Conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	c.events <- deadline
	return nil
}

func TestReaderLoopRefreshesDeadlineOnlyAfterValidEnvelope(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverConnReady := make(chan *websocket.Conn, 1)
	releaseServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnReady <- conn
		<-releaseServer
	}))

	deadlineEvents := make(chan time.Time, 16)
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &readDeadlineRecordingConn{Conn: conn, events: deadlineEvents}, nil
	}}
	clientConn, _, err := dialer.Dial(strings.Replace(srv.URL, "http://", "ws://", 1), nil)
	if err != nil {
		close(releaseServer)
		srv.Close()
		t.Fatalf("dial websocket: %v", err)
	}
	serverConn := <-serverConnReady
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
		close(releaseServer)
		srv.Close()
	}()

	for len(deadlineEvents) > 0 {
		<-deadlineEvents
	}
	client := newTestClient(t)
	client.conn = clientConn
	client.cfg.Log = func(_, _ string) {}
	readerDone := make(chan error, 1)
	go func() { readerDone <- client.readerLoop(ctx) }()
	select {
	case <-deadlineEvents:
	case <-time.After(time.Second):
		t.Fatal("reader loop did not install its initial read deadline")
	}

	pongReceived := make(chan string, 2)
	serverConn.SetPongHandler(func(message string) error {
		pongReceived <- message
		return nil
	})
	go func() {
		_, _, _ = serverConn.ReadMessage()
	}()
	requireWrite := func(messageType int, payload []byte) {
		t.Helper()
		if err := serverConn.WriteMessage(messageType, payload); err != nil {
			t.Fatalf("write websocket message: %v", err)
		}
	}
	requireWrite(websocket.TextMessage, []byte(`{"type":`))
	requireWrite(websocket.PingMessage, []byte("malformed-agent-reader-barrier"))
	select {
	case message := <-pongReceived:
		if message != "malformed-agent-reader-barrier" {
			t.Fatalf("pong barrier = %q", message)
		}
	case err := <-readerDone:
		t.Fatalf("reader exited after one malformed envelope: %v", err)
	case <-time.After(time.Second):
		t.Fatal("reader did not continue after the malformed envelope")
	}
	select {
	case deadline := <-deadlineEvents:
		t.Fatalf("malformed envelope refreshed read deadline to %s", deadline)
	default:
	}
	requireWrite(websocket.TextMessage, []byte(`{}`))
	requireWrite(websocket.PingMessage, []byte("missing-type-agent-reader-barrier"))
	select {
	case message := <-pongReceived:
		if message != "missing-type-agent-reader-barrier" {
			t.Fatalf("pong barrier = %q", message)
		}
	case err := <-readerDone:
		t.Fatalf("reader exited after an envelope without a type: %v", err)
	case <-time.After(time.Second):
		t.Fatal("reader did not continue after the envelope without a type")
	}
	select {
	case deadline := <-deadlineEvents:
		t.Fatalf("envelope without a type refreshed read deadline to %s", deadline)
	default:
	}

	requireWrite(websocket.TextMessage, []byte(`{"type":"future_gateway_frame"}`))
	select {
	case <-deadlineEvents:
	case err := <-readerDone:
		t.Fatalf("reader exited after a valid future envelope: %v", err)
	case <-time.After(time.Second):
		t.Fatal("valid future envelope did not refresh the read deadline")
	}
}

func TestMetadataRefreshEndsAnAuthenticatedSession(t *testing.T) {
	upgrader := websocket.Upgrader{}
	connected := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		welcome, _ := json.Marshal(protocol.Frame{Type: protocol.FrameWelcome, Body: []byte(`{"session_id":"refresh","protocol_version":"1.1"}`)})
		if err := conn.WriteMessage(websocket.TextMessage, welcome); err != nil {
			return
		}
		connected <- struct{}{}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{GatewayURL: srv.URL, NodeID: 1, RegistrationToken: "tok", Identity: identity.Decoded{Public: pub, Private: priv}})
	if err != nil {
		t.Fatal(err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(context.Background()) }()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not authenticate")
	}
	c.RequestMetadataRefresh()
	select {
	case err := <-runErr:
		if !errors.Is(err, ErrMetadataChanged) {
			t.Fatalf("Run error = %v, want ErrMetadataChanged", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metadata refresh did not end the session")
	}
}

func TestMetadataRefreshSignalSurvivesBetweenClientInstances(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	shared := make(chan struct{}, 1)
	first, err := New(Config{GatewayURL: "http://gateway.example", NodeID: 1, Identity: identity.Decoded{Public: pub}, MetadataChanged: shared})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(Config{GatewayURL: "http://gateway.example", NodeID: 1, Identity: identity.Decoded{Public: pub}, MetadataChanged: shared})
	if err != nil {
		t.Fatal(err)
	}

	first.RequestMetadataRefresh()
	select {
	case <-second.metadataChanged:
	default:
		t.Fatal("metadata refresh signal was not retained for the next client instance")
	}
}

func TestRunAssemblesChunkedMultipartRequest(t *testing.T) {
	multipartBody := []byte("--edge-boundary\r\nContent-Disposition: form-data; name=\"prompt\"\r\n\r\nmake it blue\r\n--edge-boundary--\r\n")
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		welcome, _ := json.Marshal(protocol.Frame{Type: protocol.FrameWelcome, Body: []byte(`{"session_id":"test","protocol_version":"1.1"}`)})
		if err := conn.WriteMessage(websocket.TextMessage, welcome); err != nil {
			return
		}
		frames := []protocol.Frame{
			{
				Type: protocol.FrameType("request_start"),
				ID:   "image-edit",
				Body: []byte(fmt.Sprintf(`{"method":"POST","path":"/v1/images/edits","headers":{"Content-Type":"multipart/form-data; boundary=edge-boundary"},"body_size":%d}`, len(multipartBody))),
			},
			{
				Type: protocol.FrameType("request_body"),
				ID:   "image-edit",
				Body: []byte(fmt.Sprintf(`{"bytes":%q}`, base64.StdEncoding.EncodeToString(multipartBody))),
			},
			{Type: protocol.FrameType("request_end"), ID: "image-edit", Body: []byte(`{}`)},
		}
		for _, frame := range frames {
			payload, _ := json.Marshal(frame)
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	received := make(chan protocol.RequestBody, 1)
	client, err := New(Config{
		GatewayURL:        srv.URL,
		NodeID:            1,
		RegistrationToken: "tok",
		Identity:          identity.Decoded{Public: pub, Private: priv},
		Handler: func(_ context.Context, request protocol.RequestBody, _ func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			received <- request
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- client.Run(ctx) }()

	select {
	case request := <-received:
		if request.Method != http.MethodPost || request.Path != "/v1/images/edits" {
			t.Fatalf("request = %+v", request)
		}
		if string(request.Body) != string(multipartBody) {
			t.Fatalf("request body = %q, want %q", request.Body, multipartBody)
		}
		if request.Headers["Content-Type"] != "multipart/form-data; boundary=edge-boundary" {
			t.Fatalf("content type = %q", request.Headers["Content-Type"])
		}
		cancel()
	case err := <-runErr:
		t.Fatalf("Run returned before request assembly: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("chunked multipart request was not assembled")
	}
}

func TestRunDropsRequestsCanceledBeforeTheirQueuedFramesArrive(t *testing.T) {
	upgrader := websocket.Upgrader{}
	pongReceived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		welcomeBody, err := json.Marshal(protocol.WelcomeBody{SessionID: "cancel-overtake", ProtocolVersion: protocol.ProtocolVersion})
		if err != nil || conn.WriteJSON(protocol.Frame{Type: protocol.FrameWelcome, Body: welcomeBody}) != nil {
			return
		}
		inlineBody, err := json.Marshal(protocol.RequestBody{Method: http.MethodPost, Path: "/v1/chat/completions", Body: []byte(`{"model":"qwen3"}`)})
		if err != nil {
			return
		}
		chunkedBody := []byte("binary-image-edit")
		startBody, err := json.Marshal(protocol.RequestStartBody{Method: http.MethodPost, Path: "/v1/images/edits", Headers: map[string]string{"Content-Type": "application/octet-stream"}, BodySize: int64(len(chunkedBody))})
		if err != nil {
			return
		}
		bodyChunk, err := json.Marshal(protocol.RequestBodyChunk{Bytes: base64.StdEncoding.EncodeToString(chunkedBody)})
		if err != nil {
			return
		}
		frames := []protocol.Frame{
			{Type: protocol.FrameRequestCancel, ID: "inline-canceled"},
			{Type: protocol.FrameRequest, ID: "inline-canceled", Body: inlineBody},
			{Type: protocol.FrameRequestCancel, ID: "chunked-canceled"},
			{Type: protocol.FrameRequestStart, ID: "chunked-canceled", Body: startBody},
			{Type: protocol.FrameRequestBody, ID: "chunked-canceled", Body: bodyChunk},
			{Type: protocol.FrameRequestEnd, ID: "chunked-canceled", Body: []byte(`{}`)},
		}
		for _, frame := range frames {
			if err := conn.WriteJSON(frame); err != nil {
				return
			}
		}
		conn.SetPongHandler(func(message string) error {
			if message == "cancel-overtake-barrier" {
				select {
				case pongReceived <- struct{}{}:
				default:
				}
			}
			return nil
		})
		if err := conn.WriteControl(websocket.PingMessage, []byte("cancel-overtake-barrier"), time.Now().Add(time.Second)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	handlerCalls := make(chan string, 2)
	client, err := New(Config{
		GatewayURL:        srv.URL,
		NodeID:            1,
		RegistrationToken: "tok",
		Identity:          identity.Decoded{Public: pub, Private: priv},
		Handler: func(_ context.Context, request protocol.RequestBody, _ func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			handlerCalls <- request.Path
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- client.Run(ctx) }()

	select {
	case <-pongReceived:
	case err := <-runErr:
		t.Fatalf("Run returned before the ordered frame barrier: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not process the ordered frame barrier")
	}
	client.wg.Wait()
	select {
	case path := <-handlerCalls:
		t.Fatalf("request canceled before arrival still reached handler: %s", path)
	default:
	}
	cancel()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after test cancellation")
	}
}

func TestExpiredRequestBodiesDoNotExhaustUploadSlots(t *testing.T) {
	c := newTestClient(t)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	for i := 0; i < protocol.MaxPendingRequestBodies; i++ {
		c.startRequestBody(protocol.Frame{Type: protocol.FrameRequestStart, ID: fmt.Sprintf("stale-%d", i), Body: []byte(`{"method":"POST","path":"/v1/images/edits","body_size":1}`)})
	}
	if got := len(c.requestBodies); got != protocol.MaxPendingRequestBodies {
		t.Fatalf("request bodies = %d", got)
	}

	now = now.Add(requestBodyAssemblyTimeout + time.Second)
	c.startRequestBody(protocol.Frame{Type: protocol.FrameRequestStart, ID: "fresh", Body: []byte(`{"method":"POST","path":"/v1/images/edits","body_size":1}`)})
	if len(c.requestBodies) != 1 || c.requestBodies["fresh"] == nil {
		t.Fatalf("expired uploads were not reclaimed: %#v", c.requestBodies)
	}
}

func TestCanceledRequestTombstonesCoverGatewayQueueReorderingWindow(t *testing.T) {
	c := newTestClient(t)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	c.cancelRequest("delayed-cancel")
	now = now.Add(requestCancellationReorderWindow + time.Second)
	c.expireRequestBodies(now)
	if err := c.dispatch(context.Background(), protocol.Frame{Type: protocol.FrameRequest, ID: "delayed-cancel", Body: []byte(`{"method":"POST","path":"/v1/chat/completions"}`)}); err != nil {
		t.Fatalf("dispatch inside gateway reordering window: %v", err)
	}
	c.requestMu.Lock()
	active := c.activeRequests["delayed-cancel"] != nil
	c.requestMu.Unlock()
	if active {
		t.Fatal("cancellation tombstone expired while its request could still be queued at the gateway")
	}
}

func TestCanceledRequestTombstonesExpireAfterGatewayQueueReorderingWindow(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	c := newCappedClient(t, 1, started, release)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	c.cancelRequest("expired-cancel")
	now = now.Add(requestCancellationTombstoneTTL + time.Second)
	c.expireRequestBodies(now)
	if err := c.dispatch(context.Background(), protocol.Frame{Type: protocol.FrameRequest, ID: "expired-cancel", Body: []byte(`{"method":"POST","path":"/v1/chat/completions"}`)}); err != nil {
		t.Fatalf("dispatch after tombstone expiry: %v", err)
	}
	c.requestMu.Lock()
	active := c.activeRequests["expired-cancel"] != nil
	c.requestMu.Unlock()
	if !active {
		t.Fatal("expired cancellation tombstone still suppressed a later request")
	}
	close(release)
	c.wg.Wait()
}

func TestCanceledRequestTombstonesFailClosedAtCapacity(t *testing.T) {
	c := newTestClient(t)
	for i := 0; i <= maxCanceledRequestTombstones; i++ {
		c.cancelRequest(fmt.Sprintf("early-cancel-%d", i))
	}
	select {
	case <-c.done:
	default:
		t.Fatal("session remained open after cancellation tombstones exceeded their safe bound")
	}
}

func TestCanceledRequestTombstonesRejectOversizedIDs(t *testing.T) {
	c := newTestClient(t)
	c.cancelRequest(strings.Repeat("x", maxCanceledRequestIDBytes+1))
	select {
	case <-c.done:
	default:
		t.Fatal("session remained open after an oversized cancellation id")
	}
}

func TestRequestBodyAssemblyRejectsAggregateBufferOverflow(t *testing.T) {
	c := newTestClient(t)
	c.startRequestBody(protocol.Frame{Type: protocol.FrameRequestStart, ID: "overflow", Body: []byte(fmt.Sprintf(`{"method":"POST","path":"/v1/images/edits","body_size":%d}`, protocol.MaxRequestBodyBytes))})
	c.requestBodyBytes = maxBufferedRequestBodyBytes - 1
	chunk, _ := json.Marshal(protocol.RequestBodyChunk{Bytes: base64.StdEncoding.EncodeToString([]byte("xx"))})
	c.appendRequestBody(protocol.Frame{Type: protocol.FrameRequestBody, ID: "overflow", Body: chunk})
	if c.requestBodies["overflow"] != nil {
		t.Fatal("aggregate buffer overflow did not discard the upload")
	}
}

func TestRunCancelsOnlyTheRequestedInference(t *testing.T) {
	upgrader := websocket.Upgrader{}
	handlerStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	serverDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		welcome, _ := json.Marshal(protocol.Frame{Type: protocol.FrameWelcome, Body: []byte(`{"session_id":"cancel","protocol_version":"1.1"}`)})
		if err := conn.WriteMessage(websocket.TextMessage, welcome); err != nil {
			return
		}
		request, _ := json.Marshal(protocol.Frame{Type: protocol.FrameRequest, ID: "cancel-me", Body: []byte(`{"method":"POST","path":"/v1/chat/completions"}`)})
		if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
			return
		}
		select {
		case <-handlerStarted:
		case <-r.Context().Done():
			return
		}
		cancelFrame, _ := json.Marshal(protocol.Frame{Type: protocol.FrameType("request_cancel"), ID: "cancel-me"})
		if err := conn.WriteMessage(websocket.TextMessage, cancelFrame); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{
		GatewayURL: srv.URL, NodeID: 1, RegistrationToken: "tok", Identity: identity.Decoded{Public: pub, Private: priv},
		Handler: func(ctx context.Context, _ protocol.RequestBody, _ func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			close(handlerStarted)
			<-ctx.Done()
			close(requestCanceled)
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run(ctx) }()
	select {
	case <-requestCanceled:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("request_cancel did not cancel the inference context")
	}
	cancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not stop")
	}
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("gateway server did not stop")
	}
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

func TestConnectAndAuthValidatesWelcomeBody(t *testing.T) {
	tests := []struct {
		name          string
		frameType     protocol.FrameType
		body          json.RawMessage
		wantError     string
		wantConnected bool
		wantWelcomed  bool
	}{
		{name: "unexpected frame", frameType: protocol.FrameHeartbeat, wantError: "unexpected gateway handshake frame"},
		{name: "malformed body", frameType: protocol.FrameWelcome, body: json.RawMessage(`"not-an-object"`), wantError: "decode welcome body"},
		{name: "missing session id", frameType: protocol.FrameWelcome, body: json.RawMessage(`{"protocol_version":"1.3"}`), wantError: "welcome missing required fields"},
		{name: "incompatible major", frameType: protocol.FrameWelcome, body: json.RawMessage(`{"session_id":"session-2","protocol_version":"2.0"}`), wantError: "gateway protocol version"},
		{name: "future minor", frameType: protocol.FrameWelcome, body: json.RawMessage(`{"session_id":"session-1","protocol_version":"1.99"}`), wantConnected: true, wantWelcomed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				welcome, err := json.Marshal(protocol.Frame{Type: test.frameType, Body: test.body})
				if err != nil {
					return
				}
				_ = conn.WriteMessage(websocket.TextMessage, welcome)
			}))
			defer srv.Close()

			pub, priv, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatalf("generate identity: %v", err)
			}
			connected := false
			client, err := New(Config{GatewayURL: srv.URL, NodeID: 1, RegistrationToken: "one-shot", Identity: identity.Decoded{Public: pub, Private: priv}, OnConnected: func() { connected = true }})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(client.closeConn)
			err = client.connectAndAuth(context.Background())
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("connectAndAuth: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("connectAndAuth error = %v, want %q", err, test.wantError)
			}
			if connected != test.wantConnected {
				t.Fatalf("OnConnected called = %t, want %t", connected, test.wantConnected)
			}
			if client.WelcomeReceived() != test.wantWelcomed {
				t.Fatalf("WelcomeReceived = %t, want %t", client.WelcomeReceived(), test.wantWelcomed)
			}
		})
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

func TestHeartbeatTelemetrySumsAllManagedRuntimeMemory(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"models":[{"size_vram":2147483648}]}`)
	}))
	defer ollama.Close()
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","vram_bytes":1073741824}`)
	}))
	defer image.Close()
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","vram_bytes":536870912}`)
	}))
	defer speech.Close()

	c := &Client{cfg: Config{OllamaURL: ollama.URL, DiffusersURL: image.URL, SpeechURL: speech.URL, VRAMTotalGB: 24, HTTPClient: ollama.Client()}}
	got := c.heartbeatTelemetry(context.Background())
	if got.VRAMUsedGB != 3.5 {
		t.Fatalf("VRAM used = %v, want 3.5", got.VRAMUsedGB)
	}
}

func TestHeartbeatTelemetryIncludesBoundedPerformanceSamples(t *testing.T) {
	want := []protocol.RuntimePerformanceSample{{Runtime: protocol.RuntimeText, Capability: protocol.CapabilityTextChat, Model: "qwen3:8b", DurationMs: 500, OutputUnits: 20, UnitsPerSecond: 40, Succeeded: true, UnixMs: time.Now().UnixMilli()}}
	c := &Client{cfg: Config{Performance: func() []protocol.RuntimePerformanceSample { return want }}, drainChanged: make(chan struct{})}
	got := c.heartbeatTelemetry(context.Background())
	if len(got.Performance) != 1 || got.Performance[0] != want[0] {
		t.Fatalf("performance = %+v, want %+v", got.Performance, want)
	}
}

func TestRuntimeMonitorRefreshesMetadataAfterStableStateChange(t *testing.T) {
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","version":"1.0.0","vram_bytes":0,"capabilities":[{"id":"image.generate","status":"ready","models":["sana"],"paths":["/v1/images/generations"]}]}`)
	}))
	defer image.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL: "https://localhost", NodeID: 1, Identity: identity.Decoded{Public: pub, Private: priv}, HTTPClient: image.Client(), DiffusersURL: image.URL, MetadataChanged: changes,
		Meta: protocol.NodeMeta{Capabilities: []protocol.Capability{{ID: protocol.CapabilityImageGenerate, Runtime: protocol.RuntimeImage, Status: protocol.CapabilityWarming, Models: []string{"sana"}, Paths: []string{"/v1/images/generations"}, Version: "1.0.0"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
		t.Fatal("one mismatching probe should be debounced")
	default:
	}
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("stable capability change did not request metadata refresh")
	}
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
		t.Fatal("one client instance requested the same metadata refresh more than once")
	default:
	}
}

func TestRuntimeMonitorDetectsOutOfBandTextModelChange(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			_, _ = io.WriteString(w, `{"models":[]}`)
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"new-model"}]}`)
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"0.13.3"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ollama.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c, err := New(Config{GatewayURL: "https://localhost", NodeID: 1, Identity: identity.Decoded{Public: pub, Private: priv}, HTTPClient: ollama.Client(), OllamaURL: ollama.URL, TextModels: []string{"old-model"}, TextResponsesSupported: true, MetadataChanged: changes})
	if err != nil {
		t.Fatal(err)
	}
	c.monitorRuntimeState(context.Background())
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("out-of-band Ollama model change did not request metadata refresh")
	}
}

func TestRuntimeMonitorKeepsSpeechAndTranscriptionNodeStable(t *testing.T) {
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","version":"speech-1.0.0","vram_bytes":0,"capabilities":[{"id":"audio.tts","status":"ready","models":["kokoro"],"paths":["/v1/audio/speech"],"limits":{"max_input_characters":4096,"formats":["mp3","wav"]}}]}`)
	}))
	defer speech.Close()
	transcription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","version":"whisper-2.0.0","vram_bytes":0,"capabilities":[{"id":"audio.transcription","status":"ready","models":["whisper-large"],"paths":["/v1/audio/transcriptions"],"limits":{"max_input_bytes":26214400,"formats":["mp3","wav"]}},{"id":"audio.translation","status":"ready","models":["whisper-large"],"paths":["/v1/audio/translations"],"limits":{"max_input_bytes":26214400}}]}`)
	}))
	defer transcription.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL: "https://localhost", NodeID: 1, Identity: identity.Decoded{Public: pub, Private: priv}, HTTPClient: speech.Client(), SpeechURL: speech.URL, TranscriptionURL: transcription.URL, MetadataChanged: changes,
		Meta: protocol.NodeMeta{Capabilities: []protocol.Capability{
			{ID: protocol.CapabilityAudioTTS, Runtime: protocol.RuntimeSpeech, Status: protocol.CapabilityReady, Models: []string{"kokoro"}, Paths: []string{"/v1/audio/speech"}, Version: "speech-1.0.0", Limits: protocol.CapabilityLimits{MaxInputCharacters: 4096, Formats: []string{"mp3", "wav"}}},
			{ID: protocol.CapabilityAudioTranscription, Runtime: protocol.RuntimeSpeech, Status: protocol.CapabilityReady, Models: []string{"whisper-large"}, Paths: []string{"/v1/audio/transcriptions"}, Version: "whisper-2.0.0", Limits: protocol.CapabilityLimits{MaxInputBytes: 26214400, Formats: []string{"mp3", "wav"}}},
			{ID: protocol.CapabilityAudioTranslation, Runtime: protocol.RuntimeSpeech, Status: protocol.CapabilityReady, Models: []string{"whisper-large"}, Paths: []string{"/v1/audio/translations"}, Version: "whisper-2.0.0", Limits: protocol.CapabilityLimits{MaxInputBytes: 26214400}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.monitorRuntimeState(context.Background())
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
		t.Fatal("unchanged speech and transcription runtimes requested a metadata refresh")
	default:
	}
}

func TestRuntimeMonitorKeepsWarmingRuntimeStable(t *testing.T) {
	// Every Python runtime reports "starting" until its model finishes loading, and the session baseline normalizes that to "warming". A probe that forwarded the raw value would mismatch its own baseline on every pass for the whole warm-up and re-register the session in a loop.
	video := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"starting","version":"video-1.0.0","vram_bytes":0,"capabilities":[{"id":"video.generate","status":"starting","models":["wan-2.1"],"paths":["/v1/videos"]},{"id":"audio.tts","status":"ready","models":["not-mine"],"paths":["/v1/audio/speech"]}]}`)
	}))
	defer video.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL: "https://localhost", NodeID: 1, Identity: identity.Decoded{Public: pub, Private: priv}, HTTPClient: video.Client(), VideoURL: video.URL, MetadataChanged: changes,
		Meta: protocol.NodeMeta{Capabilities: []protocol.Capability{
			{ID: protocol.CapabilityVideoGenerate, Runtime: protocol.RuntimeVideo, Status: protocol.CapabilityWarming, Models: []string{"wan-2.1"}, Paths: []string{"/v1/videos"}, Version: "video-1.0.0"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.monitorRuntimeState(context.Background())
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
		t.Fatal("a runtime still warming up requested a metadata refresh")
	default:
	}
}

func TestRuntimeMonitorDetectsTranscriptionOnlyChange(t *testing.T) {
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","version":"speech-1.0.0","vram_bytes":0,"capabilities":[{"id":"audio.tts","status":"ready","models":["kokoro"],"paths":["/v1/audio/speech"]}]}`)
	}))
	defer speech.Close()
	transcription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","version":"whisper-2.0.0","vram_bytes":0,"capabilities":[{"id":"audio.transcription","status":"ready","models":["whisper-turbo"],"paths":["/v1/audio/transcriptions"]}]}`)
	}))
	defer transcription.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL: "https://localhost", NodeID: 1, Identity: identity.Decoded{Public: pub, Private: priv}, HTTPClient: speech.Client(), SpeechURL: speech.URL, TranscriptionURL: transcription.URL, MetadataChanged: changes,
		Meta: protocol.NodeMeta{Capabilities: []protocol.Capability{
			{ID: protocol.CapabilityAudioTTS, Runtime: protocol.RuntimeSpeech, Status: protocol.CapabilityReady, Models: []string{"kokoro"}, Paths: []string{"/v1/audio/speech"}, Version: "speech-1.0.0"},
			{ID: protocol.CapabilityAudioTranscription, Runtime: protocol.RuntimeSpeech, Status: protocol.CapabilityReady, Models: []string{"whisper-large"}, Paths: []string{"/v1/audio/transcriptions"}, Version: "whisper-2.0.0"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.monitorRuntimeState(context.Background())
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("out-of-band transcription model change did not request metadata refresh")
	}
}

func TestRuntimeMonitorUsesDiscoverySizedBudgetForSlowHealthyRuntime(t *testing.T) {
	image := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2100 * time.Millisecond)
		_, _ = io.WriteString(w, `{"status":"ready","version":"1.0.0","vram_bytes":0,"capabilities":[{"id":"image.generate","status":"ready","models":["sana"],"paths":["/v1/images/generations"]}]}`)
	}))
	defer image.Close()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL: "https://localhost", NodeID: 1, Identity: identity.Decoded{Public: pub, Private: priv}, HTTPClient: image.Client(), DiffusersURL: image.URL, MetadataChanged: changes,
		Meta: protocol.NodeMeta{Capabilities: []protocol.Capability{{ID: protocol.CapabilityImageGenerate, Runtime: protocol.RuntimeImage, Status: protocol.CapabilityReady, Models: []string{"sana"}, Paths: []string{"/v1/images/generations"}, Version: "1.0.0"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.monitorRuntimeState(context.Background())
	c.monitorRuntimeState(context.Background())
	select {
	case <-changes:
		t.Fatal("slow healthy runtime triggered a metadata refresh loop")
	default:
	}
}

func TestRuntimeMonitorRequiresTheSameCandidateTwice(t *testing.T) {
	c := newTestClient(t)
	c.runtimeFingerprints[protocol.RuntimeImage] = "baseline"
	c.observeRuntimeFingerprint(protocol.RuntimeImage, "candidate-a")
	c.observeRuntimeFingerprint(protocol.RuntimeImage, "candidate-b")
	select {
	case <-c.metadataChanged:
		t.Fatal("two different transient fingerprints triggered refresh")
	default:
	}
}

func TestRuntimeMonitorEmitsRedactedDegradedDiagnostic(t *testing.T) {
	c := newTestClient(t)
	c.runtimeFingerprints[protocol.RuntimeImage] = "ready"
	c.observeRuntimeFingerprint(protocol.RuntimeImage, "unavailable")
	c.observeRuntimeFingerprint(protocol.RuntimeImage, "unavailable")

	select {
	case frame := <-c.sendQ:
		if frame.Type != protocol.FrameDiagnostics {
			t.Fatalf("frame type = %q, want diagnostics", frame.Type)
		}
		var body protocol.DiagnosticsBody
		if err := json.Unmarshal(frame.Body, &body); err != nil {
			t.Fatalf("decode diagnostics: %v", err)
		}
		if len(body.Events) != 1 || body.Events[0].Code != "runtime_degraded" || body.Events[0].Runtime != protocol.RuntimeImage || body.Events[0].Message != "" {
			t.Fatalf("diagnostics = %#v", body.Events)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime degradation did not emit diagnostics")
	}
}

func TestHeartbeatAckReportsGatewayRoundTrip(t *testing.T) {
	var got time.Duration
	c := &Client{cfg: Config{GatewayRoundTrip: func(duration time.Duration) { got = duration }}}
	body, err := json.Marshal(protocol.HeartbeatBody{NowUnixMs: time.Now().Add(-25 * time.Millisecond).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	c.handleHeartbeatAck(body)
	if got < 20*time.Millisecond || got > time.Second {
		t.Fatalf("round trip = %v", got)
	}
	c.handleHeartbeatAck(json.RawMessage(`{"now_unix_ms":-1}`))
	if got < 20*time.Millisecond {
		t.Fatalf("malformed ACK changed round trip to %v", got)
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
		if dErr := c.dispatch(ctx, protocol.Frame{Type: protocol.FrameRequest, ID: fmt.Sprintf("req-%d", i), Body: []byte("{}")}); dErr != nil {
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

func TestDispatchUsesIndependentRuntimePools(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 8)
	release := make(chan struct{})
	c, err := New(Config{
		GatewayURL: "https://localhost",
		NodeID:     1,
		Identity:   identity.Decoded{Public: pub, Private: priv},
		ResourcePolicy: protocol.ResourcePolicy{
			Text: protocol.RuntimeResourcePolicy{MaxConcurrent: 2}, Image: protocol.RuntimeResourcePolicy{MaxConcurrent: 1}, Speech: protocol.RuntimeResourcePolicy{MaxConcurrent: 1}, Video: protocol.RuntimeResourcePolicy{MaxConcurrent: 1}, Render: protocol.RuntimeResourcePolicy{MaxConcurrent: 1}, Rerank: protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		},
		Handler: func(_ context.Context, req protocol.RequestBody, _ func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			started <- req.Path
			<-release
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := []protocol.RequestBody{
		{Path: "/v1/images/generations"},
		{Path: "/v1/chat/completions"},
		{Path: "/v1/embeddings"},
	}
	for i, request := range requests {
		if err := c.dispatchRequest(context.Background(), fmt.Sprintf("request-%d", i), request); err != nil {
			t.Fatal(err)
		}
	}
	for range requests {
		select {
		case <-started:
		case <-time.After(shortBudget):
			t.Fatal("independent runtime request did not start")
		}
	}
	if err := c.dispatchRequest(context.Background(), "second-image", protocol.RequestBody{Path: "/v1/images/edits"}); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-started:
		t.Fatalf("second image request started despite the image pool limit: %s", path)
	case <-time.After(100 * time.Millisecond):
	}
	frame := <-c.sendQ
	var body protocol.ErrorBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatal(err)
	}
	if frame.ID != "second-image" || body.Code != errCodeNodeBusy || !strings.Contains(body.Message, "image") {
		t.Fatalf("busy frame = %#v, body = %#v", frame, body)
	}
	close(release)
	c.wg.Wait()
}

func TestDrainRejectsNewRequestsAndWaitsForInflightWork(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	c := newCappedClient(t, 1, started, release)
	if err := c.dispatchRequest(context.Background(), "active", protocol.RequestBody{Path: "/v1/chat/completions"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(shortBudget):
		t.Fatal("active request did not start")
	}
	c.BeginDrain()
	if got := c.DrainState(); got != protocol.DrainStateDraining {
		t.Fatalf("drain state = %q, want draining", got)
	}
	if err := c.dispatchRequest(context.Background(), "rejected", protocol.RequestBody{Path: "/v1/chat/completions"}); err != nil {
		t.Fatal(err)
	}
	frame := <-c.sendQ
	var body protocol.ErrorBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatal(err)
	}
	if frame.ID != "rejected" || body.Code != "node_draining" {
		t.Fatalf("drain rejection = %#v, body = %#v", frame, body)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- c.WaitForDrained(waitCtx) }()
	select {
	case err := <-drained:
		t.Fatalf("drain completed while a request was active: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-drained; err != nil {
		t.Fatalf("WaitForDrained: %v", err)
	}
	if got := c.DrainState(); got != protocol.DrainStateDrained {
		t.Fatalf("drain state = %q, want drained", got)
	}
	c.CancelDrain()
	if got := c.DrainState(); got != protocol.DrainStateServing {
		t.Fatalf("drain state = %q, want serving", got)
	}
}

// TestDrainDoesNotReportDrainedWhileAdmittingRequest pins the admission/counter atomicity that BeginDrain + WaitForDrained callers running off the reader goroutine depend on (the console drain handler, and the auto-updater hook that follows the wait with syscall.Exec). The test freezes a dispatch inside the window between the drain gate and the point where the handler becomes visible by holding requestMu, then drains: the node must report DRAINING, and WaitForDrained must block, because a request has already been admitted. With the gate and the counter in separate critical sections the frozen request is invisible and the node reports DRAINED with live work about to start.
func TestDrainDoesNotReportDrainedWhileAdmittingRequest(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	c := newCappedClient(t, 1, started, release)
	pool := c.pools[protocol.RuntimeText]

	c.requestMu.Lock()
	dispatched := make(chan error, 1)
	go func() {
		dispatched <- c.dispatchRequest(context.Background(), "admitted", protocol.RequestBody{Path: "/v1/chat/completions"})
	}()
	deadline := time.Now().Add(shortBudget)
	for len(pool) == 0 {
		if time.Now().After(deadline) {
			c.requestMu.Unlock()
			t.Fatal("dispatch never acquired an admission slot")
		}
		time.Sleep(time.Millisecond)
	}
	// The slot is held, so the dispatch has passed the drain gate and is now parked on requestMu. Give it a moment to actually park before drawing conclusions from the drain state.
	time.Sleep(50 * time.Millisecond)

	c.BeginDrain()
	if got := c.DrainState(); got != protocol.DrainStateDraining {
		t.Fatalf("drain state = %q, want draining: an admitted request was reported as drained", got)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- c.WaitForDrained(waitCtx) }()
	select {
	case err := <-drained:
		c.requestMu.Unlock()
		t.Fatalf("WaitForDrained returned while a request was being admitted: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	c.requestMu.Unlock()
	if err := <-dispatched; err != nil {
		t.Fatalf("dispatchRequest: %v", err)
	}
	select {
	case <-started:
	case <-time.After(shortBudget):
		t.Fatal("admitted request did not start")
	}
	close(release)
	if err := <-drained; err != nil {
		t.Fatalf("WaitForDrained: %v", err)
	}
	if got := c.DrainState(); got != protocol.DrainStateDrained {
		t.Fatalf("drain state = %q, want drained", got)
	}
	c.wg.Wait()
}

func TestHeartbeatReportsDrainState(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	c := newCappedClient(t, 1, started, release)
	c.BeginDrain()
	if got := c.heartbeatTelemetry(context.Background()).DrainState; got != protocol.DrainStateDrained {
		t.Fatalf("heartbeat drain state = %q, want drained", got)
	}
	close(release)
}

func TestReserveVRAMRejectsBeforeStartingRuntimeWork(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	c, err := New(Config{
		GatewayURL:  "https://localhost",
		NodeID:      1,
		Identity:    identity.Decoded{Public: pub, Private: priv},
		VRAMTotalGB: 8,
		ResourcePolicy: protocol.ResourcePolicy{
			Image: protocol.RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 4096},
		},
		Handler: func(context.Context, protocol.RequestBody, func(protocol.ChunkBody) error) (protocol.DoneBody, *protocol.ErrorBody) {
			started <- struct{}{}
			return protocol.DoneBody{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.vramUsedBytes.Store(6 << 30)
	if err := c.dispatchRequest(context.Background(), "image", protocol.RequestBody{Path: "/v1/images/generations"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("image runtime started without its configured VRAM reserve")
	case <-time.After(100 * time.Millisecond):
	}
	frame := <-c.sendQ
	var body protocol.ErrorBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "insufficient_vram" || !strings.Contains(body.Message, "4096") {
		t.Fatalf("VRAM rejection = %#v", body)
	}
}

func TestDrainFrameReportsDrainingAndTerminalState(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	c := newCappedClient(t, 1, started, release)
	if err := c.dispatchRequest(context.Background(), "active", protocol.RequestBody{Path: "/v1/chat/completions"}); err != nil {
		t.Fatal(err)
	}
	<-started
	body, _ := json.Marshal(protocol.DrainBody{Action: protocol.DrainActionStart})
	c.handleDrain(protocol.Frame{Type: protocol.FrameDrain, ID: "drain-1", Body: body})
	first := <-c.sendQ
	var firstStatus protocol.DrainStatusBody
	if err := json.Unmarshal(first.Body, &firstStatus); err != nil {
		t.Fatal(err)
	}
	if first.Type != protocol.FrameDrainStatus || first.ID != "drain-1" || firstStatus.State != protocol.DrainStateDraining || firstStatus.ActiveRequests != 1 {
		t.Fatalf("first drain status = %#v, body = %#v", first, firstStatus)
	}
	close(release)
	c.wg.Wait()
	deadline := time.After(shortBudget)
	for {
		select {
		case terminal := <-c.sendQ:
			if terminal.Type != protocol.FrameDrainStatus || terminal.ID != "drain-1" {
				continue
			}
			var terminalStatus protocol.DrainStatusBody
			if err := json.Unmarshal(terminal.Body, &terminalStatus); err != nil {
				t.Fatal(err)
			}
			if terminalStatus.State != protocol.DrainStateDrained || terminalStatus.ActiveRequests != 0 {
				t.Fatalf("terminal drain status = %#v, body = %#v", terminal, terminalStatus)
			}
			return
		case <-deadline:
			t.Fatal("terminal drained status was not reported")
		}
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
