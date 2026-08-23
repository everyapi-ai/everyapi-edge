package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

func TestResourcePolicyStoreUsesFallbackUntilSaved(t *testing.T) {
	t.Parallel()
	fallback := defaultResourcePolicy()
	fallback.Image.ReserveVRAMMB = 4096
	store := NewResourcePolicyStore(filepath.Join(t.TempDir(), "resource-policy.json"), fallback, 8)

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != fallback {
		t.Fatalf("Load = %#v, want %#v", got, fallback)
	}
}

func TestResourcePolicyStorePersistsAtomicallyWithPrivatePermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state", "resource-policy.json")
	store := NewResourcePolicyStore(path, defaultResourcePolicy(), 16)
	want := protocol.ResourcePolicy{
		Text:   protocol.RuntimeResourcePolicy{MaxConcurrent: 8, ReserveVRAMMB: 2048},
		Image:  protocol.RuntimeResourcePolicy{MaxConcurrent: 2, ReserveVRAMMB: 8192},
		Speech: protocol.RuntimeResourcePolicy{MaxConcurrent: 3, ReserveVRAMMB: 1024},
		Video:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 12288},
		Render: protocol.RuntimeResourcePolicy{MaxConcurrent: 1, ReserveVRAMMB: 4096},
		Rerank: protocol.RuntimeResourcePolicy{MaxConcurrent: 4, ReserveVRAMMB: 2048},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := NewResourcePolicyStore(path, defaultResourcePolicy(), 16).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got != want {
		t.Fatalf("reload = %#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("mode = %o, want 600", gotMode)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".resource-policy-*.tmp")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, err = %v", matches, err)
	}
}

func TestResourcePolicyStoreRejectsInvalidPolicyWithoutReplacingCurrent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "resource-policy.json")
	fallback := defaultResourcePolicy()
	store := NewResourcePolicyStore(path, fallback, 8)
	invalid := fallback
	invalid.Video.ReserveVRAMMB = 9 * 1024

	if err := store.Save(invalid); err == nil {
		t.Fatal("Save invalid policy succeeded")
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != fallback {
		t.Fatalf("Load after rejected save = %#v, want %#v", got, fallback)
	}
}
