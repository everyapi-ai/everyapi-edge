package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

var errExecTest = errors.New("exec test")

func TestRunLatestVerifiesChecksumAndStagesCandidate(t *testing.T) {
	binary := []byte("new edge binary")
	sum := sha256.Sum256(binary)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"edge-v1.2.3","draft":false,"prerelease":false,"assets":[{"name":"everyapi-edge-linux-amd64","browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`, server.URL+"/binary", server.URL+"/checksums")
		case "/binary":
			_, _ = w.Write(binary)
		case "/checksums":
			fmt.Fprintf(w, "%x  everyapi-edge-linux-amd64\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	var executed string
	m := New(Config{CurrentVersion: "1.0.0", StateDir: dir, GOOS: "linux", GOARCH: "amd64", ReleaseAPI: server.URL + "/latest", AllowAssetHost: server.URL, HTTPClient: server.Client(), Exec: func(path string) error { executed = path; return errExecTest }})
	err := m.RunLatest(context.Background(), nil)
	if err != errExecTest {
		t.Fatalf("RunLatest error = %v", err)
	}
	if executed == "" {
		t.Fatal("verified candidate was not executed")
	}
	got, err := os.ReadFile(executed)
	if err != nil || string(got) != string(binary) {
		t.Fatalf("staged candidate = %q, %v", got, err)
	}
	state, err := readState(dir)
	if err != nil || state.Phase != phaseAttempted || state.Version != "1.2.3" {
		t.Fatalf("state = %#v, %v", state, err)
	}
}

func TestRunLatestRejectsChecksumMismatch(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"edge-v1.2.3","assets":[{"name":"everyapi-edge-linux-amd64","browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`, server.URL+"/binary", server.URL+"/checksums")
		case "/binary":
			_, _ = w.Write([]byte("tampered"))
		case "/checksums":
			fmt.Fprintln(w, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  everyapi-edge-linux-amd64")
		}
	}))
	defer server.Close()
	executed := false
	m := New(Config{CurrentVersion: "1.0.0", StateDir: t.TempDir(), GOOS: "linux", GOARCH: "amd64", ReleaseAPI: server.URL + "/latest", AllowAssetHost: server.URL, HTTPClient: server.Client(), Exec: func(string) error { executed = true; return nil }})
	if err := m.RunLatest(context.Background(), nil); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if executed {
		t.Fatal("unverified binary was executed")
	}
}

func TestBootstrapRollsBackAttemptedCandidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "everyapi-edge-1.2.3")
	if err := os.WriteFile(path, []byte("candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeState(dir, state{Version: "1.2.3", Path: path, Phase: phaseAttempted}); err != nil {
		t.Fatal(err)
	}
	called := false
	m := New(Config{CurrentVersion: "1.0.0", StateDir: dir, Exec: func(string) error { called = true; return nil }})
	if err := m.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("failed candidate was executed again")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("candidate still exists: %v", err)
	}
	if _, err := os.Stat(statePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("state still exists: %v", err)
	}
}

func TestPromoteMakesCandidatePersistent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "everyapi-edge-1.2.3")
	if err := writeState(dir, state{Version: "1.2.3", Path: path, Phase: phaseAttempted}); err != nil {
		t.Fatal(err)
	}
	m := New(Config{CurrentVersion: "1.2.3", StateDir: dir})
	if err := m.Promote(); err != nil {
		t.Fatal(err)
	}
	got, err := readState(dir)
	if err != nil || got.Phase != phaseActive {
		t.Fatalf("state = %#v, %v", got, err)
	}
}

func TestBootstrapFallsBackWhenActiveCandidateIsMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing-candidate")
	if err := writeState(dir, state{Version: "1.2.3", Path: missing, Phase: phaseActive}); err != nil {
		t.Fatal(err)
	}
	called := false
	m := New(Config{CurrentVersion: "1.0.0", StateDir: dir, Exec: func(string) error { called = true; return nil }})
	if err := m.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("missing candidate was executed")
	}
	if _, err := os.Stat(statePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("stale state still exists: %v", err)
	}
}
