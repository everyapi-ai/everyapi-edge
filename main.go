// EveryAPI edge agent — the supplier-side daemon that connects to the EveryAPI gateway over a reverse WebSocket and serves inference requests by forwarding them to a local Ollama. Protocol contract lives in internal/protocol (mirror of backend/pkg/edge from the gateway repo).
//
// main wires config + identity + WS client + forwarder, then runs the reconnect loop until SIGINT/SIGTERM.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/everyapi-ai/everyapi-edge/internal/client"
	"github.com/everyapi-ai/everyapi-edge/internal/config"
	"github.com/everyapi-ai/everyapi-edge/internal/console"
	"github.com/everyapi-ai/everyapi-edge/internal/forward"
	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
	edgeruntime "github.com/everyapi-ai/everyapi-edge/internal/runtime"
	edgeupdate "github.com/everyapi-ai/everyapi-edge/internal/update"
)

// Version is patched at build time via -ldflags "-X main.Version=...".
var Version = "dev"

// logTee writes to the underlying writer (stderr) AND, when a WS client is live, fires a FrameLog through it. The send is async + drops on full queue so the standard log package's mutex is held only for the duration of the underlying stderr write — a stalled gateway link doesn't back up local logging.
type logTee struct {
	underlying io.Writer
	client     atomic.Pointer[client.Client]
	store      atomic.Pointer[console.Store]
}

func (t *logTee) Write(p []byte) (int, error) {
	msg := string(bytes.TrimRight(p, "\n"))
	if store := t.store.Load(); store != nil && msg != "" {
		store.Log("info", msg)
	}
	if cli := t.client.Load(); cli != nil {
		// Strip the trailing newline the log package adds — the dashboard renders one line per LogBody and would otherwise show double-spaced lines.
		if msg != "" {
			cli.SendLog("info", msg)
		}
	}
	return t.underlying.Write(p)
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC | log.Lmicroseconds)
	log.SetPrefix("[edge-agent] ")
	// Tee log output to the gateway so the seller's dashboard can stream agent logs without the supplier exposing docker logs. The underlying writer stays stderr so `docker compose logs agent` still works on the supplier's machine.
	logSink := &logTee{underlying: os.Stderr}
	log.SetOutput(logSink)

	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}
	updateManager := edgeupdate.New(edgeupdate.Config{
		CurrentVersion: Version,
		StateDir:       filepath.Join(filepath.Dir(cfg.IdentityPath), "updates"),
		Exec: func(path string) error {
			return syscall.Exec(path, append([]string{path}, os.Args[1:]...), os.Environ())
		},
	})
	if err := updateManager.Bootstrap(); err != nil {
		log.Fatalf("update bootstrap: %v", err)
	}
	// The host installer/CLI records accelerator memory in EVERYAPI_VRAM_GB. For runtimes that share the host kernel, retain automatic discovery; a Linux container on Apple Silicon must use the explicit host measurement.
	consoleMemoryGB := resolvedMemoryGB(cfg.VRAMTotalGB, cfg.GPUModel, detectedMemoryGB)
	hostPlatform := resolvedPlatform(os.Getenv("EVERYAPI_PLATFORM"), cfg.GPUModel, runtime.GOOS, runtime.GOARCH)
	log.Printf("starting %s — %s", Version, cfg.String())

	// Early-exit if a previous session received a terminal Disconnect frame (node_revoked) and persisted the sentinel. Without this, docker compose's restart policy ("unless-stopped") would respawn the container after the agent exited, and the new instance would just spin on auth-rejected reconnects until the cap. The sentinel lives alongside the identity file so it survives container restarts without polluting other paths.
	if reason, revoked := readRevokedSentinel(cfg.IdentityPath); revoked {
		log.Printf("node revoked server-side (%s) — agent will not start. "+
			"Run `everyapi edge remove` on the supplier host to clean up.", reason)
		return
	}

	id, err := identity.LoadOrGenerate(cfg.IdentityPath)
	if err != nil {
		log.Fatalf("identity: %v", err)
	}
	log.Printf("identity loaded; pubkey=%s", id.EncodedPubkey())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store := console.NewStore(200)
	logSink.store.Store(store)
	defer logSink.store.Store(nil)
	consoleHandlers := console.NewHandlers(console.Config{
		OllamaURL:    cfg.OllamaURL,
		DiffusersURL: cfg.DiffusersURL,
		StoragePath:  cfg.OllamaStoragePath,
		VRAMTotalGB:  consoleMemoryGB,
		NodeName:     cfg.NodeName,
		AgentVersion: Version,
		Version:      Version,
		GPUModel:     cfg.GPUModel,
		Platform:     hostPlatform,
		CountryISO2:  cfg.CountryISO2,
		Update: func(updateCtx context.Context, report func(console.UpdateStatus)) error {
			return updateManager.RunLatest(updateCtx, func(status edgeupdate.Status) {
				report(console.UpdateStatus{State: status.State, Version: status.Version, Error: status.Error})
			})
		},
	}, store)
	consoleServer, err := startConsole(ctx, cfg.ConsoleAddr, consoleHandlers.Browser)
	if err != nil {
		log.Fatalf("console: %v", err)
	}
	defer shutdownConsole(consoleServer)
	log.Printf("local control room: http://%s", cfg.ConsoleAddr)
	if cfg.LocalPreview {
		store.SetGatewayState("preview", "")
		log.Print("local preview enabled; upstream gateway connection is disabled")
		<-ctx.Done()
		log.Print("shutting down cleanly")
		return
	}

	fwd := forward.New(cfg.OllamaURL, cfg.DiffusersURL)
	var requests sync.Map
	fwd.Observer = forward.ObserverFuncs{
		StartedFunc: func(event forward.RequestEvent) {
			requests.Store(event.ID, store.Start(console.RequestStart{ID: event.ID, Consumer: event.Consumer, Model: event.Model, Path: event.Path, StartedAt: event.StartedAt}))
		},
		FinishedFunc: func(event forward.RequestEvent) {
			if handle, ok := requests.LoadAndDelete(event.ID); ok {
				store.Finish(handle.(console.RequestHandle), console.RequestFinish{CompletedAt: time.Now().UTC(), PromptTokens: event.PromptTokens, CompletionTokens: event.CompletionTokens, Duration: event.Duration, Error: event.Error})
			}
		},
	}
	meta := protocol.NodeMeta{
		Name:      cfg.NodeName,
		AgentVer:  Version,
		Workloads: cfg.Workloads,
		Hardware: protocol.Hardware{
			GPUModel:    cfg.GPUModel,
			VRAMTotalGB: consoleMemoryGB,
			Platform:    hostPlatform,
		},
		Location: protocol.Location{CountryISO2: cfg.CountryISO2},
	}

	if err := runWithReconnect(ctx, cfg, id, meta, fwd, updateManager, consoleHandlers.Control, store, logSink); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("fatal: %v", err)
	}
	log.Print("shutting down cleanly")
}

func resolvedMemoryGB(configured int, gpuModel string, detect func() int) int {
	if configured > 0 {
		return configured
	}
	// The macOS bundle runs this Linux binary inside Docker. /proc/meminfo is therefore the Docker VM limit, not Apple Silicon's unified host memory. Only the host installer/CLI can measure that total accurately. Until an older node runs the standard host upgrade, report unknown instead of advertising the VM's usually much smaller allocation as physical memory.
	if strings.Contains(strings.ToLower(gpuModel), "apple silicon") {
		return 0
	}
	return detect()
}

func resolvedPlatform(configured, gpuModel, runtimeOS, runtimeArch string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	if strings.Contains(strings.ToLower(gpuModel), "apple silicon") {
		return ""
	}
	return runtimeOS + "/" + runtimeArch
}

func detectedMemoryGB() int {
	const gib = int64(1024 * 1024 * 1024)
	var totalBytes int64
	if runtime.GOOS == "darwin" {
		// Apple Silicon exposes GPU memory as part of unified system memory.
		if output, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			totalBytes, _ = strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
		}
	} else if gpuGB := detectedNVIDIAMemoryGB(); gpuGB > 0 {
		return gpuGB
	} else if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		// CPU-only Ollama is supported too. The system-memory fallback prevents offering a model that cannot be resident at all; the individual model requirements still leave headroom for runtime and context.
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				if kib, parseErr := strconv.ParseInt(fields[1], 10, 64); parseErr == nil {
					totalBytes = kib * 1024
				}
				break
			}
		}
	}
	if totalBytes <= 0 {
		return 0
	}
	return int((totalBytes + gib - 1) / gib)
}

func detectedNVIDIAMemoryGB() int {
	output, err := exec.Command("nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0
	}
	var totalMiB int64
	for _, line := range strings.Split(string(output), "\n") {
		if mib, parseErr := strconv.ParseInt(strings.TrimSpace(line), 10, 64); parseErr == nil && mib > 0 {
			totalMiB += mib
		}
	}
	return int((totalMiB + 1023) / 1024)
}

func startConsole(ctx context.Context, addr string, handler http.Handler) (*http.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("local control room stopped: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownConsole(server)
	}()
	return server, nil
}

func shutdownConsole(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// remoteControlResponseBytes deliberately leaves headroom below the websocket envelope limit. A control operation is management metadata, never a model artifact or image; refusing oversized output prevents a gateway from using an administrator link as a bulk exfiltration channel.
const remoteControlResponseBytes = 768 << 10

// remoteControlHandler executes only model-management Control Room endpoints through the agent's existing outbound gateway session. The allowlist is intentionally checked here as well as in the gateway: a compromised or old gateway must not turn a dashboard administrator into arbitrary localhost access on supplier hardware.
func remoteControlHandler(handler http.Handler) client.ControlHandler {
	return func(ctx context.Context, operation protocol.ControlRequestBody) (protocol.ChunkBody, *protocol.ErrorBody) {
		if handler == nil || !isAllowedRemoteControl(operation.Method, operation.Path) {
			return protocol.ChunkBody{}, &protocol.ErrorBody{Code: "control_forbidden", Message: "unsupported remote control operation"}
		}
		req, err := http.NewRequestWithContext(ctx, operation.Method, "http://edge-control"+operation.Path, bytes.NewReader(operation.Body))
		if err != nil {
			return protocol.ChunkBody{}, &protocol.ErrorBody{Code: "malformed_control_request", Message: "invalid control request"}
		}
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		payload := recorder.Body.Bytes()
		if len(payload) > remoteControlResponseBytes {
			return protocol.ChunkBody{}, &protocol.ErrorBody{Code: "control_response_too_large", Message: "control response exceeds the safe limit"}
		}
		headers := map[string]string{}
		if contentType := recorder.Header().Get("Content-Type"); contentType != "" {
			headers["Content-Type"] = contentType
		}
		return protocol.ChunkBody{
			StatusCode: recorder.Code,
			Headers:    headers,
			Bytes:      base64.StdEncoding.EncodeToString(payload),
		}, nil
	}
}

func isAllowedRemoteControl(method, rawPath string) bool {
	parsed, err := url.ParseRequestURI(rawPath)
	if err != nil || parsed.Path == "" || parsed.Host != "" {
		return false
	}
	switch method {
	case http.MethodGet:
		switch parsed.Path {
		case "/api/overview", "/api/models", "/api/models/capabilities", "/api/runtime", "/api/image-runtime", "/api/storage", "/api/storage/migrate", "/api/models/pull":
			return true
		}
	case http.MethodPost:
		switch parsed.Path {
		case "/api/models/pull", "/api/models/benchmark", "/api/image-runtime/model", "/api/runtime/unload", "/api/runtime/unload-all", "/api/storage/pick", "/api/storage/plan", "/api/storage/migrate":
			return true
		}
	case http.MethodDelete:
		return parsed.Path == "/api/models" || parsed.Path == "/api/models/pull"
	}
	return false
}

// discoverOllamaModels reads the native Ollama tag list immediately before a gateway handshake. The gateway uses this list to create routable channel abilities, so reporting a static or stale list would make an online node appear healthy while accepting no buyer traffic.
func discoverOllamaModels(ctx context.Context, ollamaURL string) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	return edgeruntime.NewTextClient(ollamaURL, client).Models(ctx)
}

func discoverDiffusersModels(ctx context.Context, diffusersURL string) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	health, err := edgeruntime.NewImageClient(diffusersURL, client).Health(ctx)
	if err != nil {
		return nil, err
	}
	if health.Status != edgeruntime.StatusReady {
		return nil, fmt.Errorf("local image runtime is %s: %s", health.Status, health.Error)
	}
	return health.Models, nil
}

func mergeModels(groups ...[]string) []string {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, model := range group {
			name := strings.TrimSpace(model)
			if name != "" {
				seen[name] = struct{}{}
			}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// runWithReconnect drives one client lifecycle after another with exponential backoff capped at 30s. The reconnect loop is here (not inside Client) so a future test can stub the client without also stubbing the backoff behavior.
//
// First connect uses the RegistrationToken from config. After a successful Welcome the token is cleared from the client's config so subsequent reconnects fall through to the Ed25519 signature path — the token is one-shot on the server side and reusing it would just produce "registration token not recognised" errors.
func runWithReconnect(
	ctx context.Context,
	cfg config.Config,
	id identity.Decoded,
	meta protocol.NodeMeta,
	fwd *forward.Forwarder,
	updateManager *edgeupdate.Manager,
	controlHandler http.Handler,
	store *console.Store,
	logSink *logTee,
) error {
	return runGatewayLifecycle(ctx, cfg, id, meta, fwd, updateManager, controlHandler, store, logSink)
}

// revokedSentinelPath sits next to the identity file so it shares the same volume mount in docker-compose and survives container restarts. Named `.revoked` so a `ls -l` next to identity.json makes the failure mode obvious without grepping logs.
func revokedSentinelPath(identityPath string) string {
	return filepath.Join(filepath.Dir(identityPath), ".revoked")
}

// readRevokedSentinel returns the persisted reason text + true when the sentinel file exists AND has content. Any read error short-circuits to "not revoked" — the worst case is the agent tries (and fails) one more reconnect cycle, which is the pre-PR behavior and not a regression.
//
// A zero-byte sentinel is treated as not-revoked: writeRevokedSentinel always appends a newline, so an empty file is either a write that failed mid-flight or someone hand-touched the path. Either way, blocking startup on a reasonless sentinel would be a worse failure mode than retrying once.
func readRevokedSentinel(identityPath string) (string, bool) {
	b, err := os.ReadFile(revokedSentinelPath(identityPath))
	if err != nil {
		// IsNotExist is the normal "no sentinel here" path. Any OTHER error (EACCES from a maintainer chmod-ing the file to 0000, EIO from a flaky disk) gets surfaced on stderr so the operator sees why the early-exit branch was silently skipped rather than discovering it from a runaway reconnect loop. We still return not-revoked so the agent attempts to start — the alternative (hard-fail on unreadable sentinel) trades one footgun for another.
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "edge-agent: read .revoked sentinel: %v — continuing as not-revoked\n", err)
		}
		return "", false
	}
	reason := string(bytes.TrimSpace(b))
	if reason == "" {
		return "", false
	}
	return reason, true
}

// maxSentinelBytes caps the on-disk reason. The gateway-side reason is short today ("node deleted via /api/seller/edge/nodes"), but a future protocol bump that lets the gateway send arbitrary operator notes would otherwise let a compromised upstream write unbounded data into the supplier's identity dir. 4 KiB covers any reasonable human-readable reason and stays well under filesystem-block thresholds.
const maxSentinelBytes = 4 * 1024

// writeRevokedSentinel persists the reason text so a maintainer can `cat .revoked` and see why the agent stopped. Best-effort: a write failure means the next boot will retry one more time, which is fine. 0600 because the file lives in the identity dir which we already enforce as private.
//
// Truncates oversize reasons rather than rejecting — the sentinel's presence is what matters for the early-exit decision; the reason is operator-facing context.
//
// Truncation walks back to a UTF-8 rune boundary so we never write half a rune into .revoked. The operator-facing reason can carry multi-byte content from any locale (or "…" itself, which is 3 bytes), and a malformed-by-truncation file would render as a replacement character or worse in `cat`.
func writeRevokedSentinel(identityPath, reason string) error {
	payload := reason + "\n"
	if len(payload) > maxSentinelBytes {
		const trunc = "…(truncated)\n"
		budget := maxSentinelBytes - len(trunc)
		// Walk back from the byte-budget cut until we stand ON a rune-start byte, then slice exclusive at that index so the preserved prefix ends on the last byte of the PREVIOUS complete rune. budget==0 is the degenerate exit (entire payload was continuation bytes, impossible in real UTF-8 input but defensive); payload[:0] + trunc is just the marker, still valid UTF-8 and under cap.
		for budget > 0 && !utf8.RuneStart(payload[budget]) {
			budget--
		}
		payload = payload[:budget] + trunc
	}
	return os.WriteFile(revokedSentinelPath(identityPath), []byte(payload), 0o600)
}

// nextBackoff is the conventional doubling-with-cap WITH ±25% jitter. Stays linear in the cap window so a persistent gateway outage doesn't drift to minute-long wait times for a transient blip.
//
// Jitter is the fleet-coordination concern: without it, 100 agents that all lost contact at t=0 would all wake up at t=1s, 2s, 4s, 8s, 16s, 30s, ... in lockstep, and every recovery attempt is a thundering-herd against the just-recovered gateway. ±25% spreads the retries so the gateway sees a smooth load curve as the fleet reconnects. 25% is enough to break sync; tighter risks chasing hot spots in time; wider distorts the doubling shape too much for an operator reading the log to recognise the backoff pattern.
//
// math/rand is fine here — we're not seeding cryptographic decisions, just breaking lockstep on a recovering fleet. Go 1.20+'s default rand source is per-call seeded; no global state to coordinate.
func nextBackoff(b time.Duration) time.Duration {
	const max = 30 * time.Second
	doubled := b * 2
	if doubled > max {
		doubled = max
	}
	// ±25% jitter window around the doubled value.
	jitter := time.Duration((rand.Float64() - 0.5) * 0.5 * float64(doubled))
	out := doubled + jitter
	if out < time.Second {
		out = time.Second // never under the floor
	}
	if out > max {
		out = max // never over the cap
	}
	return out
}
