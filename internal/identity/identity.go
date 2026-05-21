// Package identity owns the agent's Ed25519 keypair: generate on first
// run, persist to disk with 0600 mode, load on subsequent runs. The
// private key never leaves the supplier's machine — the gateway only
// ever sees the public key (handed over in the first WS Auth frame)
// and signatures over server-issued challenges.
//
// Storage format is plain JSON with base64-encoded keys; the file
// lives at ${EVERYAPI_EDGE_HOME}/identity.json (default
// $HOME/.everyapi-edge/identity.json). The reasons for not using
// PEM / OS keychain / etc:
//
//   - The agent runs unattended in a docker container; OS keychain
//     APIs (Keychain Services on macOS, Secret Service on Linux)
//     would force a daemon dependency or block on a desktop login
//     session that doesn't exist in containers.
//
//   - PEM is overkill for a 32-byte secret. The JSON envelope lets a
//     future schema bump (e.g. wrapping the key in a passphrase-
//     derived KEK) extend cleanly without breaking older agents.
//
//   - Linux file permissions (0600 file, 0700 directory) is the
//     same trust model the docker socket and most agent secrets use.
//     If the supplier's machine is compromised root-equivalent,
//     escalating to "read the agent's identity" was always trivial
//     regardless of the storage backend.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Identity is the on-disk representation. Versioned so a future
// schema change (e.g. encrypted private key) has a migration hook.
type Identity struct {
	Version    int    `json:"version"`
	PublicKey  string `json:"public_key"`  // base64 std
	PrivateKey string `json:"private_key"` // base64 std
}

// CurrentVersion is the schema version any freshly-generated
// identity carries. Bump when the on-disk format changes.
const CurrentVersion = 1

// Decoded unwraps the on-disk identity into the runtime types
// callers want — base64 to []byte to ed25519.* — with the length
// checks the std lib would otherwise panic on.
type Decoded struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// EncodedPubkey is the base64 form the WS Auth frame carries.
func (d Decoded) EncodedPubkey() string {
	return base64.StdEncoding.EncodeToString(d.Public)
}

// Sign produces an Ed25519 signature over msg using the loaded key.
// Callers base64-encode the result themselves (the protocol field
// is a string).
func (d Decoded) Sign(msg []byte) []byte {
	return ed25519.Sign(d.Private, msg)
}

// LoadOrGenerate is the only entry point: returns the identity at
// path, generating + persisting a fresh keypair if the file doesn't
// exist. A corrupt file (parse failure, wrong-size keys) is treated
// as a hard error — silently regenerating would orphan the gateway-
// side row that already trusts the original pubkey.
func LoadOrGenerate(path string) (Decoded, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return Decoded{}, err
	}
	if data, readErr := os.ReadFile(expanded); readErr == nil {
		return decode(data)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Decoded{}, fmt.Errorf("read identity %s: %w", expanded, readErr)
	}
	return generateAndPersist(expanded)
}

func decode(data []byte) (Decoded, error) {
	var stored Identity
	if err := json.Unmarshal(data, &stored); err != nil {
		return Decoded{}, fmt.Errorf("identity file is corrupt: %w", err)
	}
	if stored.Version > CurrentVersion {
		return Decoded{}, fmt.Errorf("identity file version %d unsupported (agent expects ≤ %d)", stored.Version, CurrentVersion)
	}
	pub, err := base64.StdEncoding.DecodeString(stored.PublicKey)
	if err != nil {
		return Decoded{}, fmt.Errorf("identity public key corrupt: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return Decoded{}, fmt.Errorf("identity public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	priv, err := base64.StdEncoding.DecodeString(stored.PrivateKey)
	if err != nil {
		return Decoded{}, fmt.Errorf("identity private key corrupt: %w", err)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return Decoded{}, fmt.Errorf("identity private key is %d bytes, want %d", len(priv), ed25519.PrivateKeySize)
	}
	return Decoded{Public: pub, Private: priv}, nil
}

func generateAndPersist(path string) (Decoded, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Decoded{}, fmt.Errorf("generate keypair: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Decoded{}, fmt.Errorf("create identity dir: %w", err)
	}
	payload, err := json.MarshalIndent(Identity{
		Version:    CurrentVersion,
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}, "", "  ")
	if err != nil {
		return Decoded{}, fmt.Errorf("encode identity: %w", err)
	}
	// O_EXCL guards against a concurrent agent racing on the same
	// path. The supplier almost never runs two agents but the race
	// is recoverable (one wins, the other restarts and reads).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		// If we lost the race, fall back to reading what the winner
		// just wrote rather than failing the startup. Two restarts
		// in a single boot cycle is wasted work, not a fault.
		if errors.Is(err, os.ErrExist) {
			data, readErr := os.ReadFile(path)
			if readErr == nil {
				return decode(data)
			}
		}
		return Decoded{}, fmt.Errorf("open identity for write: %w", err)
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return Decoded{}, fmt.Errorf("write identity: %w", err)
	}
	if err := f.Close(); err != nil {
		return Decoded{}, fmt.Errorf("close identity: %w", err)
	}
	return Decoded{Public: pub, Private: priv}, nil
}

// expandPath rewrites a leading "~" to the user's home so the
// default in main.go ("~/.everyapi-edge/identity.json") works in
// docker (where $HOME is /root) and in operator shells alike.
func expandPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("identity path is empty")
	}
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
