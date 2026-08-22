package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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

func TestRunLatestReturnsTheSharedConflictSentinel(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		_, _ = io.WriteString(w, `{"tag_name":"edge-v1.0.0","draft":false,"prerelease":false}`)
	}))
	defer server.Close()
	manager := New(Config{CurrentVersion: "1.0.0", StateDir: t.TempDir(), ReleaseAPI: server.URL, HTTPClient: server.Client()})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- manager.RunLatest(context.Background(), nil)
	}()
	receiveWithin(t, requestStarted, "first update request")

	if err := manager.RunLatest(context.Background(), nil); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("concurrent RunLatest error = %v", err)
	}
	close(releaseRequest)
	if err := receiveWithin(t, firstDone, "first update completion"); err != nil {
		t.Fatalf("first RunLatest: %v", err)
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

func TestAutoUpdateSettingDefaultsOffAndPersists(t *testing.T) {
	dir := t.TempDir()
	manager := New(Config{StateDir: dir})

	settings, err := manager.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AutoUpdate {
		t.Fatal("automatic updates must default off")
	}
	if settings.CheckInterval != 24*time.Hour {
		t.Fatalf("check interval = %s", settings.CheckInterval)
	}

	settings, err = manager.SetAutoUpdate(true)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AutoUpdate {
		t.Fatal("automatic updates were not enabled")
	}

	restarted := New(Config{StateDir: dir})
	settings, err = restarted.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.AutoUpdate {
		t.Fatal("automatic update setting did not survive restart")
	}
}

func TestAutoUpdateSchedulerChecksAfterJitterThenEveryDay(t *testing.T) {
	type scheduledTimer struct {
		delay time.Duration
		fire  chan time.Time
	}
	scheduled := make(chan scheduledTimer, 2)
	checked := make(chan struct{}, 1)
	manager := New(Config{StateDir: t.TempDir()})
	manager.initialDelay = func() time.Duration { return 17 * time.Minute }
	manager.after = func(delay time.Duration) <-chan time.Time {
		timer := scheduledTimer{delay: delay, fire: make(chan time.Time, 1)}
		scheduled <- timer
		return timer.fire
	}
	manager.runLatest = func(context.Context, func(Status)) error {
		checked <- struct{}{}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.RunAuto(ctx, nil)
		close(done)
	}()
	if _, err := manager.SetAutoUpdate(true); err != nil {
		t.Fatal(err)
	}

	initial := receiveWithin(t, scheduled, "initial automatic update timer")
	if initial.delay != 17*time.Minute {
		t.Fatalf("initial delay = %s", initial.delay)
	}
	initial.fire <- time.Now()
	receiveWithin(t, checked, "automatic update check")

	next := receiveWithin(t, scheduled, "daily automatic update timer")
	if next.delay != 24*time.Hour {
		t.Fatalf("next delay = %s", next.delay)
	}
	cancel()
	receiveWithin(t, done, "automatic update scheduler shutdown")
}

func TestDisablingAutoUpdateCancelsAScheduledCheck(t *testing.T) {
	type scheduledTimer struct {
		fire chan time.Time
	}
	scheduled := make(chan scheduledTimer, 1)
	var checks atomic.Int32
	manager := New(Config{StateDir: t.TempDir()})
	manager.initialDelay = func() time.Duration { return 10 * time.Minute }
	manager.after = func(time.Duration) <-chan time.Time {
		timer := scheduledTimer{fire: make(chan time.Time, 1)}
		scheduled <- timer
		return timer.fire
	}
	manager.runLatest = func(context.Context, func(Status)) error {
		checks.Add(1)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.RunAuto(ctx, nil)
		close(done)
	}()
	if _, err := manager.SetAutoUpdate(true); err != nil {
		t.Fatal(err)
	}
	timer := receiveWithin(t, scheduled, "initial automatic update timer")
	if _, err := manager.SetAutoUpdate(false); err != nil {
		t.Fatal(err)
	}
	timer.fire <- time.Now()
	cancel()
	receiveWithin(t, done, "disabled automatic update scheduler shutdown")
	if checks.Load() != 0 {
		t.Fatalf("automatic checks after disable = %d", checks.Load())
	}
}

func TestReenablingAutoUpdateReplacesTheStaleTimer(t *testing.T) {
	type scheduledTimer struct {
		fire chan time.Time
	}
	scheduled := make(chan scheduledTimer, 3)
	checked := make(chan struct{}, 1)
	manager := New(Config{StateDir: t.TempDir()})
	manager.initialDelay = func() time.Duration { return 10 * time.Minute }
	manager.after = func(time.Duration) <-chan time.Time {
		timer := scheduledTimer{fire: make(chan time.Time, 1)}
		scheduled <- timer
		return timer.fire
	}
	manager.runLatest = func(context.Context, func(Status)) error {
		checked <- struct{}{}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.RunAuto(ctx, nil)
		close(done)
	}()
	if _, err := manager.SetAutoUpdate(true); err != nil {
		t.Fatal(err)
	}
	stale := receiveWithin(t, scheduled, "initial automatic update timer")
	if _, err := manager.SetAutoUpdate(false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetAutoUpdate(true); err != nil {
		t.Fatal(err)
	}
	stale.fire <- time.Now()

	var replacement scheduledTimer
	select {
	case <-checked:
		t.Fatal("stale automatic update timer ran after the setting changed")
	case replacement = <-scheduled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for replacement automatic update timer")
	}
	replacement.fire <- time.Now()
	receiveWithin(t, checked, "replacement automatic update check")
	cancel()
	receiveWithin(t, done, "automatic update scheduler shutdown")
}

func TestAutoUpdateDoesNotOverwriteAnUpdateAlreadyInProgress(t *testing.T) {
	type scheduledTimer struct {
		fire chan time.Time
	}
	scheduled := make(chan scheduledTimer, 2)
	checked := make(chan struct{})
	var failures atomic.Int32
	manager := New(Config{StateDir: t.TempDir()})
	manager.initialDelay = func() time.Duration { return 0 }
	manager.after = func(time.Duration) <-chan time.Time {
		timer := scheduledTimer{fire: make(chan time.Time, 1)}
		scheduled <- timer
		return timer.fire
	}
	manager.runLatest = func(context.Context, func(Status)) error {
		close(checked)
		return ErrUpdateInProgress
	}
	if _, err := manager.SetAutoUpdate(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		manager.RunAuto(ctx, func(status Status) {
			if status.State == "failed" {
				failures.Add(1)
			}
		})
		close(done)
	}()
	timer := receiveWithin(t, scheduled, "automatic update timer")
	timer.fire <- time.Now()
	receiveWithin(t, checked, "automatic update attempt")
	cancel()
	receiveWithin(t, done, "automatic update scheduler shutdown")
	if failures.Load() != 0 {
		t.Fatalf("automatic update overwrote active update status %d times", failures.Load())
	}
}

func receiveWithin[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}
