package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
