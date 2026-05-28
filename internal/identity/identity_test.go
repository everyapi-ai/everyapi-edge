package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrGenerateCreatesOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	id, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if len(id.Public) != ed25519.PublicKeySize {
		t.Fatalf("pub len: got %d, want %d", len(id.Public), ed25519.PublicKeySize)
	}
	if len(id.Private) != ed25519.PrivateKeySize {
		t.Fatalf("priv len: got %d, want %d", len(id.Private), ed25519.PrivateKeySize)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file mode: got %o, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrGenerateReusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	first, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("first LoadOrGenerate: %v", err)
	}
	second, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("second LoadOrGenerate: %v", err)
	}
	if string(first.Public) != string(second.Public) {
		t.Fatal("reload produced a different public key")
	}
	if string(first.Private) != string(second.Private) {
		t.Fatal("reload produced a different private key")
	}
}

func TestSignRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	id, _ := LoadOrGenerate(path)
	msg := []byte("ping")
	sig := id.Sign(msg)
	if !ed25519.Verify(id.Public, msg, sig) {
		t.Fatal("Verify against locally produced signature failed")
	}
}

func TestCorruptFileFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	_, err := LoadOrGenerate(path)
	if err == nil {
		t.Fatal("expected error on corrupt identity, got nil")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("expected 'corrupt' in error, got %q", err.Error())
	}
}

func TestWrongLengthKeyFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	// 16-byte key — half what Ed25519 wants.
	junk := make([]byte, 16)
	_, _ = rand.Read(junk)
	doc := Identity{
		Version:    CurrentVersion,
		PublicKey:  base64.StdEncoding.EncodeToString(junk),
		PrivateKey: base64.StdEncoding.EncodeToString(junk),
	}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := LoadOrGenerate(path)
	if err == nil {
		t.Fatal("expected error on wrong-length key, got nil")
	}
}

func TestFutureVersionRejected(t *testing.T) {
	// Forward-incompatible: a newer agent wrote the file, an older
	// agent shouldn't pretend to understand it.
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	doc := Identity{
		Version:    CurrentVersion + 99,
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(path, b, 0600)
	_, err := LoadOrGenerate(path)
	if err == nil {
		t.Fatal("expected error on future version, got nil")
	}
}

func TestExpandTildePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this runner")
	}
	got, err := expandPath("~/foo/bar")
	if err != nil {
		t.Fatalf("expandPath: %v", err)
	}
	want := filepath.Join(home, "foo", "bar")
	if got != want {
		t.Fatalf("expandPath: got %q, want %q", got, want)
	}
}

func TestEncodedPubkey(t *testing.T) {
	dir := t.TempDir()
	id, _ := LoadOrGenerate(filepath.Join(dir, "id.json"))
	encoded := id.EncodedPubkey()
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode round-trip: %v", err)
	}
	if string(decoded) != string(id.Public) {
		t.Fatal("EncodedPubkey doesn't round-trip")
	}
}

// TestLoadRefusesSymlink pins the symlink guard added with the perm
// check. Lstat (not Stat) is what makes this work: a symlink pointing
// at a 0600 file owned by another user would otherwise pass the perm
// check by proxy.
func TestLoadRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows symlink semantics differ + perm check is skipped
		// there; the guard is Unix-only by design.
		t.Skip("symlink guard is Unix-only by design")
	}
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.json")
	linkPath := filepath.Join(dir, "id.json")

	// Generate the real file at 0600, then symlink id.json → real.json.
	_, err := LoadOrGenerate(realPath)
	if err != nil {
		t.Fatalf("seed real identity: %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := LoadOrGenerate(linkPath); err == nil {
		t.Fatal("expected error loading via symlink, got nil")
	}
}

// TestLoadRefusesWorldReadableFile pins the perm-validation gate:
// `cp -p`, a manual chmod, or a sloppy backup restore can widen the
// 0600 the package wrote on first generate. The agent must refuse to
// load a key file that other users on the host can read — the
// private key is the only thing standing between a co-tenant and
// gateway impersonation of this node.
func TestLoadRefusesWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	// Generate a valid file first.
	_, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	// Widen the perms behind the agent's back.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod 0644: %v", err)
	}
	_, err = LoadOrGenerate(path)
	if err == nil {
		t.Fatal("expected error loading 0644 identity file, got nil")
	}
	// Loosen again to group-readable only — also a fail per the
	// `mode & 0o077 != 0` rule.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod 0640: %v", err)
	}
	_, err = LoadOrGenerate(path)
	if err == nil {
		t.Fatal("expected error loading 0640 identity file, got nil")
	}
	// Tighten back to 0600 — must load successfully again.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod 0600: %v", err)
	}
	if _, err := LoadOrGenerate(path); err != nil {
		t.Fatalf("0600 file should load: %v", err)
	}
}
