package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func TestDiscoverOllamaModelsUsesTagNamesAndDeduplicates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen2.5:7b"},{"name":"llama3.1:8b"},{"name":"qwen2.5:7b"},{"name":""}]}`)
	}))
	defer srv.Close()

	got, err := discoverOllamaModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("discoverOllamaModels: %v", err)
	}
	want := []string{"llama3.1:8b", "qwen2.5:7b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", got, want)
	}
}

func TestDiscoverDiffusersModelsRequiresReadyHealthAndDeduplicates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q, want /health", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"status":"ready","backend":"mps","models":["sana-600m","qwen-edit","sana-600m",""]}`)
	}))
	defer srv.Close()

	got, err := discoverDiffusersModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("discoverDiffusersModels: %v", err)
	}
	want := []string{"qwen-edit", "sana-600m"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", got, want)
	}
}

func TestDiscoverDiffusersModelsRejectsUnavailableRuntime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"unavailable","models":[],"error":"accelerator required"}`)
	}))
	defer srv.Close()

	if _, err := discoverDiffusersModels(context.Background(), srv.URL); err == nil {
		t.Fatal("unavailable runtime should return an error")
	}
}

func TestMergeModelsSortsAndDeduplicatesAcrossRuntimes(t *testing.T) {
	got := mergeModels([]string{"qwen3:8b", "shared"}, []string{"sana-600m", "shared"})
	want := []string{"qwen3:8b", "sana-600m", "shared"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", got, want)
	}
}

func TestAppleSiliconWithoutHostMetadataDoesNotUseContainerMemory(t *testing.T) {
	detected := false
	got := resolvedMemoryGB(0, "Apple Silicon", func() int {
		detected = true
		return 16
	})
	if got != 0 {
		t.Fatalf("memory = %d GiB, want unknown rather than Docker VM memory", got)
	}
	if detected {
		t.Fatal("Apple Silicon fallback queried memory from inside the Linux container")
	}
}

func TestConfiguredAppleSiliconMemoryRemainsAuthoritative(t *testing.T) {
	got := resolvedMemoryGB(48, "Apple Silicon", func() int { return 16 })
	if got != 48 {
		t.Fatalf("memory = %d GiB, want configured host total 48", got)
	}
}

func TestConfiguredHostPlatformOverridesContainerRuntime(t *testing.T) {
	if got := resolvedPlatform("darwin/arm64", "Apple Silicon", "linux", "arm64"); got != "darwin/arm64" {
		t.Fatalf("platform = %q, want macOS host platform", got)
	}
	if got := resolvedPlatform("", "RTX 4090", "linux", "amd64"); got != "linux/amd64" {
		t.Fatalf("fallback platform = %q, want agent runtime", got)
	}
}

func TestAppleSiliconWithoutHostMetadataDoesNotUseContainerPlatform(t *testing.T) {
	if got := resolvedPlatform("", "Apple Silicon", "linux", "arm64"); got != "" {
		t.Fatalf("platform = %q, want unknown rather than Linux container runtime", got)
	}
}

func TestRemoteControlHandlerAllowsOnlyModelManagementEndpoints(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"name":"qwen3"}]`))
	})
	control := remoteControlHandler(handler)
	response, errBody := control(context.Background(), protocol.ControlRequestBody{Method: http.MethodGet, Path: "/api/models"})
	if errBody != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("response = %#v, error = %#v", response, errBody)
	}
	payload, err := base64.StdEncoding.DecodeString(response.Bytes)
	if err != nil || !bytes.Equal(payload, []byte(`[{"name":"qwen3"}]`)) {
		t.Fatalf("payload = %q, error = %v", payload, err)
	}
	_, errBody = control(context.Background(), protocol.ControlRequestBody{Method: http.MethodGet, Path: "/etc/passwd"})
	if errBody == nil || errBody.Code != "control_forbidden" {
		t.Fatalf("forbidden error = %#v", errBody)
	}
}

func TestRemoteControlHandlerAllowsQuickBenchmarkButNotMethodEscalation(t *testing.T) {
	if !isAllowedRemoteControl(http.MethodPost, "/api/models/benchmark") {
		t.Fatal("quick benchmark should be available to the constrained remote management surface")
	}
	if isAllowedRemoteControl(http.MethodGet, "/api/models/benchmark") {
		t.Fatal("benchmark must not be reachable with an unexpected read method")
	}
}

func TestRemoteControlHandlerAllowsImageRuntimeSelectionButNotImageGeneration(t *testing.T) {
	if !isAllowedRemoteControl(http.MethodGet, "/api/image-runtime") {
		t.Fatal("image runtime status should be available to remote model management")
	}
	if !isAllowedRemoteControl(http.MethodPost, "/api/image-runtime/model") {
		t.Fatal("image runtime model selection should be available to remote model management")
	}
	if isAllowedRemoteControl(http.MethodPost, "/api/image/edit") {
		t.Fatal("remote model management must not proxy arbitrary image edits")
	}
}

func TestRemoteControlHandlerAllowsNativeStorageSelectionAndMigrationOnly(t *testing.T) {
	if !isAllowedRemoteControl(http.MethodPost, "/api/storage/pick") {
		t.Fatal("native storage selection should be available to constrained remote management")
	}
	if !isAllowedRemoteControl(http.MethodGet, "/api/storage/migrate") {
		t.Fatal("storage migration progress should be available to constrained remote management")
	}
	if !isAllowedRemoteControl(http.MethodPost, "/api/storage/migrate") {
		t.Fatal("storage migration start should be available to constrained remote management")
	}
	if isAllowedRemoteControl(http.MethodGet, "/api/storage/pick") {
		t.Fatal("native storage selection must not be reachable with an unexpected read method")
	}
}

// TestNextBackoffJittered pins the jitter spread + cap/floor. Calling nextBackoff(prev) 1000 times from a stable prev should produce values in [prev*1.5, prev*2.5] (the ±25% window around the doubled value), clamped to [1s, 30s]. Without jitter the values were always exactly 2×prev — a thundering-herd risk for a fleet that all lost contact at the same instant. The assertion here is a wide range so the test isn't flaky on RNG seeds.
func TestNextBackoffJittered(t *testing.T) {
	const prev = 4 * time.Second
	const trials = 1000
	distinct := map[time.Duration]bool{}
	for i := 0; i < trials; i++ {
		got := nextBackoff(prev)
		// Doubled is 8s; jitter window ±25% of 8s = ±2s; floor 1s, cap 30s.
		if got < time.Second || got > 30*time.Second {
			t.Fatalf("backoff out of [1s, 30s]: got %v", got)
		}
		if got < 5*time.Second || got > 11*time.Second {
			// Wide-but-not-trivial: catches a regression that drops the jitter (always 8s) OR overshoots the window. 5s..11s comfortably brackets 8s±25%=6..10s with slack so the test isn't seed-flaky.
			t.Fatalf("backoff outside jitter window for prev=4s: got %v", got)
		}
		distinct[got] = true
	}
	if len(distinct) < 10 {
		t.Fatalf("only %d distinct backoff values out of %d — jitter is not breaking lockstep", len(distinct), trials)
	}
}

// TestNextBackoffCapHonoured pins the 30s cap. Doubling 30s with jitter could overshoot if the clamp at the end of nextBackoff fires after the jitter add — verify the cap holds.
//
// Also asserts the lower-bound at saturation: at prev=30s, doubled clamps to 30s and jitter ±25% of doubled = ±7.5s, so the floor after clamping is 22.5s — agents at the cap don't reconnect faster than this. That's a known asymmetry (the ceiling clamp truncates positive jitter while negative jitter passes through); the test pins it so a refactor that breaks the floor surfaces here, and so the asymmetry is documented next to the cap test.
func TestNextBackoffCapHonoured(t *testing.T) {
	const cap = 30 * time.Second
	const expectedFloor = 22500 * time.Millisecond // cap - 25% of cap
	for i := 0; i < 100; i++ {
		got := nextBackoff(cap)
		if got > cap {
			t.Fatalf("backoff exceeded 30s cap: got %v", got)
		}
		if got < expectedFloor {
			t.Fatalf("at cap, backoff dipped below expected floor of %v: got %v", expectedFloor, got)
		}
	}
}

// TestRevokedSentinelRoundTrip pins the write + read cycle for the terminal-disconnect sentinel: a `node_revoked` Disconnect causes the agent to persist a reason next to its identity, and the next container start reads it back to exit before the WS dial.
func TestRevokedSentinelRoundTrip(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")

	// Pre-state: no sentinel — reader reports "not revoked".
	if reason, revoked := readRevokedSentinel(identityPath); revoked {
		t.Fatalf("fresh dir should not look revoked; got reason=%q", reason)
	}

	// Write + read back.
	const wantReason = "node deleted via /api/seller/edge/nodes"
	if err := writeRevokedSentinel(identityPath, wantReason); err != nil {
		t.Fatalf("writeRevokedSentinel: %v", err)
	}

	gotReason, revoked := readRevokedSentinel(identityPath)
	if !revoked {
		t.Fatal("readRevokedSentinel returned !revoked after a successful write")
	}
	if gotReason != wantReason {
		t.Fatalf("reason text not preserved: got %q want %q", gotReason, wantReason)
	}
}

// TestRevokedSentinelPathLivesNextToIdentity pins the location: reviewers and operators look at the identity dir; the sentinel must surface there rather than buried under XDG / TempDir / etc.
func TestRevokedSentinelPathLivesNextToIdentity(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")

	got := revokedSentinelPath(identityPath)
	want := filepath.Join(dir, ".revoked")
	if got != want {
		t.Fatalf("sentinel path drifted from identity dir:\n got  %q\n want %q", got, want)
	}
}

// TestRevokedSentinelPermissions confirms 0600 — the file shouldn't be world-readable since it lives in the same private dir as the Ed25519 private key.
//
// Skipped on Windows: NTFS file modes from os.Stat aren't POSIX bits, and the surrounding identity package has its own Windows handling.
func TestRevokedSentinelPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX perm bits don't apply on Windows; identity package handles its own NTFS gate")
	}
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")

	if err := writeRevokedSentinel(identityPath, "x"); err != nil {
		t.Fatalf("writeRevokedSentinel: %v", err)
	}
	info, err := os.Stat(revokedSentinelPath(identityPath))
	if err != nil {
		t.Fatalf("stat sentinel: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("sentinel mode not 0600: got %#o", mode)
	}
}

// TestRevokedSentinelEmptyFileNotRevoked pins the empty-file handling: writeRevokedSentinel always appends a newline, so a zero-byte file is either a write that failed mid-flight or a hand-touch. Either way, blocking startup on a reasonless sentinel would be a worse failure mode than letting the agent retry once, so readRevokedSentinel reports it as not-revoked.
func TestRevokedSentinelEmptyFileNotRevoked(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")
	sentinelPath := revokedSentinelPath(identityPath)

	// Touch a zero-byte file. `touch` would do the same thing in a recovery shell — this is the failure mode we're covering.
	if err := os.WriteFile(sentinelPath, []byte{}, 0o600); err != nil {
		t.Fatalf("touch sentinel: %v", err)
	}
	reason, revoked := readRevokedSentinel(identityPath)
	if revoked {
		t.Fatalf("empty sentinel should NOT be treated as revoked; got reason=%q", reason)
	}

	// Whitespace-only too — TrimSpace catches "\n", "\t", etc.
	if err := os.WriteFile(sentinelPath, []byte("   \n\t"), 0o600); err != nil {
		t.Fatalf("whitespace sentinel: %v", err)
	}
	reason, revoked = readRevokedSentinel(identityPath)
	if revoked {
		t.Fatalf("whitespace-only sentinel should NOT be treated as revoked; got reason=%q", reason)
	}
}

// TestRevokedSentinelTruncatesOnRuneBoundary pins the UTF-8 safety of the truncation path. A reason longer than maxSentinelBytes (e.g. an operator-friendly note in a non-ASCII locale, or one already containing "…") must NOT be sliced mid-rune — the file's human-readable contract is `cat .revoked`, and a half-rune writes a U+FFFD replacement character at the seam or worse.
//
// Runs three subtests with different ASCII prefix lengths so the byte offset `maxSentinelBytes - len(trunc)` lands at each of the three possible positions inside a 3-byte rune (% 3 == 0, 1, 2). A single fixed-length run would only exercise one alignment and a regression that broke the off-by-one cases would slip through — see review of #393 for the specific concern.
func TestRevokedSentinelTruncatesOnRuneBoundary(t *testing.T) {
	for _, prefixLen := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("prefix=%d", prefixLen), func(t *testing.T) {
			dir := t.TempDir()
			identityPath := filepath.Join(dir, "identity.json")

			// "数" is 3 bytes (E6 95 B0). The ASCII prefix shifts every subsequent rune's start by prefixLen bytes, so the cut at maxSentinelBytes - len("…(truncated)\n") hits a different position inside a rune for each run.
			reason := strings.Repeat("A", prefixLen) + strings.Repeat("数", maxSentinelBytes)
			if err := writeRevokedSentinel(identityPath, reason); err != nil {
				t.Fatalf("writeRevokedSentinel: %v", err)
			}

			b, err := os.ReadFile(revokedSentinelPath(identityPath))
			if err != nil {
				t.Fatalf("read sentinel: %v", err)
			}
			if !utf8.Valid(b) {
				t.Fatalf("truncated sentinel contains invalid UTF-8; %d bytes written\nhead=%q\ntail=%q",
					len(b), string(b[:min(40, len(b))]), string(b[max(0, len(b)-40):]))
			}
			// Must end with the truncation marker so an operator running `cat .revoked` knows the reason was cut.
			if !strings.HasSuffix(string(b), "…(truncated)\n") {
				t.Fatalf("expected truncation suffix; got tail=%q", string(b[max(0, len(b)-20):]))
			}
			// And the byte length must respect the cap.
			if len(b) > maxSentinelBytes {
				t.Fatalf("sentinel exceeded maxSentinelBytes=%d; got %d", maxSentinelBytes, len(b))
			}
		})
	}
}
