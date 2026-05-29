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
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

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

	// Early-exit if a previous session received a terminal Disconnect
	// frame (node_revoked) and persisted the sentinel. Without this,
	// docker compose's restart policy ("unless-stopped") would respawn
	// the container after the agent exited, and the new instance would
	// just spin on auth-rejected reconnects until the cap. The sentinel
	// lives alongside the identity file so it survives container
	// restarts without polluting other paths.
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
		welcomed := cli.WelcomeReceived()
		if welcomed {
			registrationToken = ""
		}

		if runErr == nil || errors.Is(runErr, context.Canceled) {
			return runErr
		}

		// Terminal codes (currently only node_revoked) write a
		// sentinel next to the identity file and exit clean. The
		// docker compose restart policy will respawn the container,
		// but the next boot reads the sentinel and exits immediately
		// — so the seller sees one clear "node revoked" line in
		// docker logs instead of a forever-backoff loop. Must run
		// BEFORE the backoff-reset block below since we return.
		var terminal *client.TerminalDisconnectError
		if errors.As(runErr, &terminal) {
			log.Printf("terminal disconnect from gateway: %s (%s) — agent will not retry", terminal.Code, terminal.Reason)
			if writeErr := writeRevokedSentinel(cfg.IdentityPath, terminal.Reason); writeErr != nil {
				log.Printf("warning: failed to persist revoked sentinel: %v", writeErr)
			}
			return nil
		}

		// Reset the backoff after a stable session so the next
		// disconnect retries fast. Without this the agent
		// accumulates exponential delay across the lifetime of the
		// process — a fleet that hit a transient gateway outage
		// would reconnect slowly even after stability returns.
		// "Stable" = the gateway accepted Auth and sent Welcome;
		// failures before Welcome (bad token, signature mismatch,
		// dial refused) keep the backoff growing so we don't hammer
		// a refusing gateway.
		if welcomed {
			backoff = time.Second
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

// revokedSentinelPath sits next to the identity file so it shares
// the same volume mount in docker-compose and survives container
// restarts. Named `.revoked` so a `ls -l` next to identity.json
// makes the failure mode obvious without grepping logs.
func revokedSentinelPath(identityPath string) string {
	return filepath.Join(filepath.Dir(identityPath), ".revoked")
}

// readRevokedSentinel returns the persisted reason text + true when
// the sentinel file exists AND has content. Any read error
// short-circuits to "not revoked" — the worst case is the agent
// tries (and fails) one more reconnect cycle, which is the pre-PR
// behavior and not a regression.
//
// A zero-byte sentinel is treated as not-revoked: writeRevokedSentinel
// always appends a newline, so an empty file is either a write that
// failed mid-flight or someone hand-touched the path. Either way,
// blocking startup on a reasonless sentinel would be a worse failure
// mode than retrying once.
func readRevokedSentinel(identityPath string) (string, bool) {
	b, err := os.ReadFile(revokedSentinelPath(identityPath))
	if err != nil {
		// IsNotExist is the normal "no sentinel here" path. Any
		// OTHER error (EACCES from a maintainer chmod-ing the file
		// to 0000, EIO from a flaky disk) gets surfaced on stderr
		// so the operator sees why the early-exit branch was
		// silently skipped rather than discovering it from a
		// runaway reconnect loop. We still return not-revoked so
		// the agent attempts to start — the alternative (hard-fail
		// on unreadable sentinel) trades one footgun for another.
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

// maxSentinelBytes caps the on-disk reason. The gateway-side reason
// is short today ("node deleted via /api/seller/edge/nodes"), but a
// future protocol bump that lets the gateway send arbitrary operator
// notes would otherwise let a compromised upstream write unbounded
// data into the supplier's identity dir. 4 KiB covers any reasonable
// human-readable reason and stays well under filesystem-block
// thresholds.
const maxSentinelBytes = 4 * 1024

// writeRevokedSentinel persists the reason text so a maintainer can
// `cat .revoked` and see why the agent stopped. Best-effort: a write
// failure means the next boot will retry one more time, which is
// fine. 0600 because the file lives in the identity dir which we
// already enforce as private.
//
// Truncates oversize reasons rather than rejecting — the sentinel's
// presence is what matters for the early-exit decision; the reason
// is operator-facing context.
//
// Truncation walks back to a UTF-8 rune boundary so we never write
// half a rune into .revoked. The operator-facing reason can carry
// multi-byte content from any locale (or "…" itself, which is 3
// bytes), and a malformed-by-truncation file would render as a
// replacement character or worse in `cat`.
func writeRevokedSentinel(identityPath, reason string) error {
	payload := reason + "\n"
	if len(payload) > maxSentinelBytes {
		const trunc = "…(truncated)\n"
		budget := maxSentinelBytes - len(trunc)
		// Walk back from the byte-budget cut until we stand ON a
		// rune-start byte, then slice exclusive at that index so
		// the preserved prefix ends on the last byte of the
		// PREVIOUS complete rune. budget==0 is the degenerate exit
		// (entire payload was continuation bytes, impossible in
		// real UTF-8 input but defensive); payload[:0] + trunc is
		// just the marker, still valid UTF-8 and under cap.
		for budget > 0 && !utf8.RuneStart(payload[budget]) {
			budget--
		}
		payload = payload[:budget] + trunc
	}
	return os.WriteFile(revokedSentinelPath(identityPath), []byte(payload), 0o600)
}

// nextBackoff is the conventional doubling-with-cap WITH ±25%
// jitter. Stays linear in the cap window so a persistent gateway
// outage doesn't drift to minute-long wait times for a transient
// blip.
//
// Jitter is the fleet-coordination concern: without it, 100 agents
// that all lost contact at t=0 would all wake up at t=1s, 2s, 4s,
// 8s, 16s, 30s, ... in lockstep, and every recovery attempt is a
// thundering-herd against the just-recovered gateway. ±25% spreads
// the retries so the gateway sees a smooth load curve as the fleet
// reconnects. 25% is enough to break sync; tighter risks chasing
// hot spots in time; wider distorts the doubling shape too much
// for an operator reading the log to recognise the backoff pattern.
//
// math/rand is fine here — we're not seeding cryptographic decisions,
// just breaking lockstep on a recovering fleet. Go 1.20+'s default
// rand source is per-call seeded; no global state to coordinate.
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
