// EveryAPI edge agent — the supplier-side daemon that connects to
// the EveryAPI gateway over a reverse WebSocket and serves inference
// requests by forwarding them to a local Ollama. Protocol contract
// lives in internal/protocol (mirror of backend/pkg/edge from the
// gateway repo).
//
// main wires config + identity + WS client + forwarder, then runs
// the reconnect loop until SIGINT/SIGTERM.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/everyapi-ai/everyapi-edge/internal/client"
	"github.com/everyapi-ai/everyapi-edge/internal/config"
	"github.com/everyapi-ai/everyapi-edge/internal/forward"
	"github.com/everyapi-ai/everyapi-edge/internal/identity"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// Version is patched at build time via -ldflags "-X main.Version=...".
var Version = "dev"

// currentClient holds the WS client active inside the reconnect loop.
// The log-tee writer reads it on every Write and forwards the line to
// the gateway as a FrameLog. atomic.Pointer keeps the swap lock-free
// — log.Printf is called from many goroutines, the reconnect loop
// swaps from one — so there's no mutex contention.
var currentClient atomic.Pointer[client.Client]

// logTee writes to the underlying writer (stderr) AND, when a WS
// client is live, fires a FrameLog through it. The send is async +
// drops on full queue so the standard log package's mutex is held
// only for the duration of the underlying stderr write — a stalled
// gateway link doesn't back up local logging.
type logTee struct{ underlying io.Writer }

func (t *logTee) Write(p []byte) (int, error) {
	if cli := currentClient.Load(); cli != nil {
		// Strip the trailing newline the log package adds — the
		// dashboard renders one line per LogBody and would
		// otherwise show double-spaced lines.
		msg := string(bytes.TrimRight(p, "\n"))
		if msg != "" {
			cli.SendLog("info", msg)
		}
	}
	return t.underlying.Write(p)
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC | log.Lmicroseconds)
	log.SetPrefix("[edge-agent] ")
	// Tee log output to the gateway so the seller's dashboard
	// can stream agent logs without the supplier exposing docker
	// logs. The underlying writer stays stderr so `docker compose
	// logs agent` still works on the supplier's machine.
	log.SetOutput(&logTee{underlying: os.Stderr})

	cfg := config.FromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("starting %s — %s", Version, cfg.String())

	id, err := identity.LoadOrGenerate(cfg.IdentityPath)
	if err != nil {
		log.Fatalf("identity: %v", err)
	}
	log.Printf("identity loaded; pubkey=%s", id.EncodedPubkey())

	fwd := forward.New(cfg.OllamaURL)
	meta := protocol.NodeMeta{
		Name:     cfg.NodeName,
		AgentVer: Version,
		Hardware: protocol.Hardware{
			GPUModel:    cfg.GPUModel,
			VRAMTotalGB: cfg.VRAMTotalGB,
			Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		},
		Location: protocol.Location{CountryISO2: cfg.CountryISO2},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runWithReconnect(ctx, cfg, id, meta, fwd); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("fatal: %v", err)
	}
	log.Print("shutting down cleanly")
}

// runWithReconnect drives one client lifecycle after another with
// exponential backoff capped at 30s. The reconnect loop is here (not
// inside Client) so a future test can stub the client without also
// stubbing the backoff behavior.
//
// First connect uses the RegistrationToken from config. After a
// successful Welcome the token is cleared from the client's config
// so subsequent reconnects fall through to the Ed25519 signature
// path — the token is one-shot on the server side and reusing it
// would just produce "registration token not recognised" errors.
func runWithReconnect(
	ctx context.Context,
	cfg config.Config,
	id identity.Decoded,
	meta protocol.NodeMeta,
	fwd *forward.Forwarder,
) error {
	registrationToken := cfg.RegistrationToken
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cli, err := client.New(client.Config{
			GatewayURL:        cfg.GatewayURL,
			NodeID:            cfg.NodeID,
			RegistrationToken: registrationToken,
			Identity:          id,
			Meta:              meta,
			Handler:           fwd.Handle,
		})
		if err != nil {
			return fmt.Errorf("client.New: %w", err)
		}
		log.Print("connecting")
		// Publish the active client so the log tee can forward
		// lines through it. Cleared after Run returns so a
		// between-reconnects log line writes only to stderr and
		// not to a stale send queue.
		currentClient.Store(cli)
		runErr := cli.Run(ctx)
		currentClient.Store(nil)
		// Burn the registration token ONLY if the gateway accepted
		// the Auth frame and we got a Welcome back. Without this
		// gate, an Auth rejection (token already consumed by an
		// earlier run, wrong node id, signature path with no stored
		// pubkey) would zero the in-process token in this outer
		// loop, and the next reconnect would dial with an empty
		// token AND no Ed25519 pubkey on file server-side — an
		// unrecoverable state that requires manual operator
		// intervention.
		if cli.WelcomeReceived() {
			registrationToken = ""
		}

		if runErr == nil || errors.Is(runErr, context.Canceled) {
			return runErr
		}
		log.Printf("session ended: %v (reconnecting in %s)", runErr, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff)
	}
}

// nextBackoff is the conventional doubling-with-cap. Stays linear in
// the cap window so a persistent gateway outage doesn't drift to
// minute-long wait times for a transient blip.
func nextBackoff(b time.Duration) time.Duration {
	const max = 30 * time.Second
	doubled := b * 2
	if doubled > max {
		return max
	}
	return doubled
}
