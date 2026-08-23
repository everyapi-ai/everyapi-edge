package client

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// A receipt must never delay the download it is reporting on.
//
// reportModelPull first used sendFrame, which waits up to 5s for room in the send queue. That is the right tolerance for a frame that matters, but this runs inside the pull loop at two receipts per model, so a full Welcome set on a stalled link spent 20 x 2 x 5s doing nothing — enough to push the cap test above past the 2-minute CI timeout.
//
// Both cases below leave the queue unusable in the two ways production can: a client with no live session at all, and one whose link has backed up.
func TestReportModelPullNeverBlocks(t *testing.T) {
	cases := map[string]*Client{
		// sendQ and done are both nil, as in the cap test. A nil channel blocks forever, so a waiting implementation hangs outright.
		"no session": {cfg: Config{Log: func(string, string) {}}},
		"queue full": func() *Client {
			c := &Client{cfg: Config{Log: func(string, string) {}}, sendQ: make(chan protocol.Frame, 2)}
			for range cap(c.sendQ) {
				c.sendQ <- protocol.Frame{Type: protocol.FrameLog}
			}
			return c
		}(),
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			returned := make(chan struct{})
			go func() {
				defer close(returned)
				c.reportModelPull("llama3", protocol.ModelPullPending, "")
			}()
			select {
			case <-returned:
			case <-time.After(2 * time.Second):
				t.Fatal("reportModelPull blocked instead of dropping the receipt")
			}
		})
	}
}

// TestPullOllamaModelDrainsStream covers the happy path: a streaming pull is drained to completion and reported as success.
func TestPullOllamaModelDrainsStream(t *testing.T) {
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody.Store(string(buf[:n]))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pulling"}` + "\n" + `{"status":"success"}` + "\n"))
	}))
	defer srv.Close()

	if err := pullOllamaModel(context.Background(), srv.URL, "qwen3"); err != nil {
		t.Fatalf("pullOllamaModel: %v", err)
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, `"name":"qwen3"`) {
		t.Fatalf("model name not forwarded to ollama, body=%q", body)
	}
}

// TestPullOllamaModelSurfacesHTTPError pins that a non-200 from ollama is an error rather than a silent no-op — otherwise a node would report success for a model it never downloaded.
func TestPullOllamaModelSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such model", http.StatusNotFound)
	}))
	defer srv.Close()

	if err := pullOllamaModel(context.Background(), srv.URL, "ghost"); err == nil {
		t.Fatal("expected an error for a 404 from ollama")
	}
}

func TestPullOllamaModelSurfacesStreamingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":"proxyconnect tcp: connection refused"}` + "\n"))
	}))
	defer srv.Close()

	err := pullOllamaModel(context.Background(), srv.URL, "qwen3")
	if err == nil || !strings.Contains(err.Error(), "proxyconnect tcp: connection refused") {
		t.Fatalf("streaming pull error = %v", err)
	}
}

func TestPullOllamaModelRequiresTerminalSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
	}))
	defer srv.Close()

	err := pullOllamaModel(context.Background(), srv.URL, "qwen3")
	if err == nil || !strings.Contains(err.Error(), "without success") {
		t.Fatalf("truncated pull error = %v", err)
	}
}

// TestPullRecommendedModelsCapsAndContinues pins the two safety properties of the batch path: the gateway cannot make the agent pull an unbounded number of models onto someone else's disk, and one failing model does not abort the rest.
func TestPullRecommendedModelsCapsAndContinues(t *testing.T) {
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := pulls.Add(1)
		if n == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
	}))
	defer srv.Close()

	c := &Client{cfg: Config{OllamaURL: srv.URL, Log: func(string, string) {}}}

	oversized := make([]string, maxAutoPullModels+5)
	for i := range oversized {
		oversized[i] = "m"
	}
	c.pullRecommendedModels(context.Background(), oversized)
	if got := int(pulls.Load()); got != maxAutoPullModels {
		t.Fatalf("pulled %d models, want the cap %d", got, maxAutoPullModels)
	}
}

// TestPullRecommendedModelsNoOllamaURL pins that auto-pull stays inert when no local runtime is configured, rather than issuing requests to "".
func TestPullRecommendedModelsNoOllamaURL(t *testing.T) {
	c := &Client{cfg: Config{Log: func(string, string) {}}}
	c.pullRecommendedModels(context.Background(), []string{"qwen3"})
}

func TestRunCancelsAutoPullBeforeReturningAfterGatewayDisconnect(t *testing.T) {
	pullStarted := make(chan struct{})
	releasePull := make(chan struct{})
	ollama := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(pullStarted)
		<-releasePull
	}))

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
		body, err := json.Marshal(protocol.WelcomeBody{SessionID: "auto-pull-session", ProtocolVersion: protocol.ProtocolVersion, RecommendedModels: []string{"qwen3"}})
		if err != nil {
			return
		}
		if err := conn.WriteJSON(protocol.Frame{Type: protocol.FrameWelcome, Body: body}); err != nil {
			return
		}
		<-disconnect
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	}))

	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancelRun()
		disconnectGateway()
		close(releasePull)
		gateway.CloseClientConnections()
		gateway.Close()
		ollama.CloseClientConnections()
		ollama.Close()
	})
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	pullStopped := make(chan struct{})
	var pullStoppedOnce sync.Once
	client, err := New(Config{GatewayURL: gateway.URL, OllamaURL: ollama.URL, NodeID: 1, RegistrationToken: "one-shot", Identity: identity.Decoded{Public: public, Private: private}, Log: func(_, message string) {
		if strings.Contains(message, "auto-pull: qwen3 failed:") {
			pullStoppedOnce.Do(func() { close(pullStopped) })
		}
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- client.Run(runCtx) }()

	select {
	case <-pullStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("auto-pull did not start")
	}
	disconnectGateway()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after gateway disconnect")
	}
	select {
	case <-pullStopped:
	default:
		t.Fatal("Run returned before its session auto-pull stopped")
	}
}
