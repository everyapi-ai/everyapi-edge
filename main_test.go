package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRevokedSentinelRoundTrip pins the write + read cycle for the
// terminal-disconnect sentinel: a `node_revoked` Disconnect causes
// the agent to persist a reason next to its identity, and the next
// container start reads it back to exit before the WS dial.
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

// TestRevokedSentinelPathLivesNextToIdentity pins the location:
// reviewers and operators look at the identity dir; the sentinel
// must surface there rather than buried under XDG / TempDir / etc.
func TestRevokedSentinelPathLivesNextToIdentity(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")

	got := revokedSentinelPath(identityPath)
	want := filepath.Join(dir, ".revoked")
	if got != want {
		t.Fatalf("sentinel path drifted from identity dir:\n got  %q\n want %q", got, want)
	}
}

// TestRevokedSentinelPermissions confirms 0600 — the file
// shouldn't be world-readable since it lives in the same private
// dir as the Ed25519 private key.
//
// Skipped on Windows: NTFS file modes from os.Stat aren't POSIX bits,
// and the surrounding identity package has its own Windows handling.
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

// TestRevokedSentinelEmptyFileNotRevoked pins the empty-file
// handling: writeRevokedSentinel always appends a newline, so a
// zero-byte file is either a write that failed mid-flight or a
// hand-touch. Either way, blocking startup on a reasonless sentinel
// would be a worse failure mode than letting the agent retry once,
// so readRevokedSentinel reports it as not-revoked.
func TestRevokedSentinelEmptyFileNotRevoked(t *testing.T) {
	dir := t.TempDir()
	identityPath := filepath.Join(dir, "identity.json")
	sentinelPath := revokedSentinelPath(identityPath)

	// Touch a zero-byte file. `touch` would do the same thing in
	// a recovery shell — this is the failure mode we're covering.
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
