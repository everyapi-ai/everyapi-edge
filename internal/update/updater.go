// Package update implements the Edge agent's latest-stable self-update path. It deliberately accepts no caller-supplied version or asset URL.
package update

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultReleaseAPI = "https://api.github.com/repos/everyapi-ai/everyapi-edge/releases/latest"
	defaultAssetHost  = "https://github.com"
	maxMetadataBytes  = 1 << 20
	maxChecksumBytes  = 1 << 20
	maxBinaryBytes    = 128 << 20
	phaseAttempted    = "attempted"
	phaseActive       = "active"
	autoCheckInterval = 24 * time.Hour
	maxInitialJitter  = 30 * time.Minute
)

var strictVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

var ErrUpdateInProgress = errors.New("update already in progress")

type Config struct {
	CurrentVersion string
	StateDir       string
	GOOS           string
	GOARCH         string
	ReleaseAPI     string
	AllowAssetHost string
	HTTPClient     *http.Client
	Exec           func(path string) error
}

type Status struct {
	State   string
	Version string
	Error   string
}

type Manager struct {
	cfg             Config
	mu              sync.Mutex
	settingsMu      sync.Mutex
	settingsVersion uint64
	settingsChanged chan struct{}
	initialDelay    func() time.Duration
	after           func(time.Duration) <-chan time.Time
	runLatest       func(context.Context, func(Status)) error
}

type state struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	Phase   string `json:"phase"`
}

type preferences struct {
	AutoUpdate bool `json:"auto_update"`
}

type Settings struct {
	AutoUpdate    bool
	CheckInterval time.Duration
}

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func New(cfg Config) *Manager {
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.GOARCH == "" {
		cfg.GOARCH = runtime.GOARCH
	}
	if cfg.ReleaseAPI == "" {
		cfg.ReleaseAPI = defaultReleaseAPI
	}
	if cfg.AllowAssetHost == "" {
		cfg.AllowAssetHost = defaultAssetHost
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	}
	manager := &Manager{
		cfg:             cfg,
		settingsChanged: make(chan struct{}, 1),
		initialDelay:    randomInitialDelay,
		after:           time.After,
	}
	manager.runLatest = manager.runLatestRelease
	return manager
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

func preferencesPath(dir string) string { return filepath.Join(dir, "preferences.json") }

func readState(dir string) (state, error) {
	var value state
	b, err := os.ReadFile(statePath(dir))
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(b, &value); err != nil {
		return value, err
	}
	return value, nil
}

func writeState(dir string, value state) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, statePath(dir))
}

func readPreferences(dir string) (preferences, error) {
	var value preferences
	b, err := os.ReadFile(preferencesPath(dir))
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(b, &value); err != nil {
		return value, err
	}
	return value, nil
}

func writePreferences(dir string, value preferences) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".preferences-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(b)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, preferencesPath(dir))
}

func (m *Manager) Settings() (Settings, error) {
	settings, _, err := m.settingsSnapshot()
	return settings, err
}

func (m *Manager) settingsSnapshot() (Settings, uint64, error) {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	value, err := readPreferences(m.cfg.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{CheckInterval: autoCheckInterval}, m.settingsVersion, nil
	}
	if err != nil {
		return Settings{}, m.settingsVersion, fmt.Errorf("read update preferences: %w", err)
	}
	return Settings{AutoUpdate: value.AutoUpdate, CheckInterval: autoCheckInterval}, m.settingsVersion, nil
}

func (m *Manager) SetAutoUpdate(enabled bool) (Settings, error) {
	m.settingsMu.Lock()
	current, readErr := readPreferences(m.cfg.StateDir)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		current = preferences{}
	}
	changed := readErr != nil && !errors.Is(readErr, os.ErrNotExist) || current.AutoUpdate != enabled
	current.AutoUpdate = enabled
	if err := writePreferences(m.cfg.StateDir, current); err != nil {
		m.settingsMu.Unlock()
		return Settings{}, fmt.Errorf("write update preferences: %w", err)
	}
	if changed {
		m.settingsVersion++
	}
	m.settingsMu.Unlock()
	if changed {
		select {
		case m.settingsChanged <- struct{}{}:
		default:
		}
	}
	return Settings{AutoUpdate: enabled, CheckInterval: autoCheckInterval}, nil
}

func randomInitialDelay() time.Duration {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(maxInitialJitter)))
	if err != nil {
		return maxInitialJitter / 2
	}
	return time.Duration(value.Int64())
}

type autoWaitResult uint8

const (
	autoWaitStopped autoWaitResult = iota
	autoWaitElapsed
	autoWaitSettingsChanged
)

func (m *Manager) RunAuto(ctx context.Context, report func(Status)) {
	firstCheck := true
	for {
		settings, version, err := m.settingsSnapshot()
		if err != nil {
			if report != nil {
				report(Status{State: "failed", Error: err.Error()})
			}
			if m.waitForAutoCheck(ctx, autoCheckInterval, version) == autoWaitStopped {
				return
			}
			firstCheck = true
			continue
		}
		if !settings.AutoUpdate {
			firstCheck = true
			if !m.waitForSettingsChange(ctx, version) {
				return
			}
			continue
		}
		delay := settings.CheckInterval
		if firstCheck {
			delay = m.initialDelay()
			firstCheck = false
		}
		waitResult := m.waitForAutoCheck(ctx, delay, version)
		if waitResult == autoWaitStopped {
			return
		}
		if waitResult == autoWaitSettingsChanged {
			firstCheck = true
			continue
		}
		settings, currentVersion, err := m.settingsSnapshot()
		if err != nil || !settings.AutoUpdate || currentVersion != version {
			firstCheck = true
			continue
		}
		if err := m.runLatest(ctx, report); err != nil && !errors.Is(err, ErrUpdateInProgress) && report != nil {
			report(Status{State: "failed", Error: err.Error()})
		}
	}
}

func (m *Manager) waitForAutoCheck(ctx context.Context, delay time.Duration, version uint64) autoWaitResult {
	timer := m.after(delay)
	for {
		select {
		case <-ctx.Done():
			return autoWaitStopped
		case <-m.settingsChanged:
			if m.currentSettingsVersion() != version {
				return autoWaitSettingsChanged
			}
		case <-timer:
			return autoWaitElapsed
		}
	}
}

func (m *Manager) waitForSettingsChange(ctx context.Context, version uint64) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-m.settingsChanged:
			if m.currentSettingsVersion() != version {
				return true
			}
		}
	}
}

func (m *Manager) currentSettingsVersion() uint64 {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	return m.settingsVersion
}

// Bootstrap runs before normal agent startup. An active candidate remains the preferred binary across container restarts. An attempted candidate means it failed before its first successful gateway Welcome and is rolled back.
func (m *Manager) Bootstrap() error {
	s, err := readState(m.cfg.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read update state: %w", err)
	}
	switch s.Phase {
	case phaseActive:
		if s.Version == m.cfg.CurrentVersion {
			return nil
		}
		if info, statErr := os.Stat(s.Path); statErr != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			_ = os.Remove(s.Path)
			return os.Remove(statePath(m.cfg.StateDir))
		}
		if m.cfg.Exec == nil {
			return errors.New("update exec is not configured")
		}
		return m.cfg.Exec(s.Path)
	case phaseAttempted:
		if s.Version == m.cfg.CurrentVersion {
			return nil
		}
		_ = os.Remove(s.Path)
		return os.Remove(statePath(m.cfg.StateDir))
	default:
		return fmt.Errorf("unknown update phase %q", s.Phase)
	}
}

// Promote marks the attempted candidate healthy after its first Welcome.
func (m *Manager) Promote() error {
	s, err := readState(m.cfg.StateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.Phase != phaseAttempted || s.Version != m.cfg.CurrentVersion {
		return nil
	}
	s.Phase = phaseActive
	return writeState(m.cfg.StateDir, s)
}

func (m *Manager) RunLatest(ctx context.Context, report func(Status)) error {
	return m.runLatest(ctx, report)
}

func (m *Manager) runLatestRelease(ctx context.Context, report func(Status)) error {
	if !m.mu.TryLock() {
		return ErrUpdateInProgress
	}
	defer m.mu.Unlock()
	emit := func(s Status) {
		if report != nil {
			report(s)
		}
	}
	emit(Status{State: "checking"})
	rel, err := m.fetchRelease(ctx)
	if err != nil {
		emit(Status{State: "failed", Error: err.Error()})
		return err
	}
	version := strings.TrimPrefix(rel.TagName, "edge-v")
	if compareVersions(version, m.cfg.CurrentVersion) <= 0 {
		emit(Status{State: "current", Version: m.cfg.CurrentVersion})
		return nil
	}
	assetName := "everyapi-edge-" + m.cfg.GOOS + "-" + m.cfg.GOARCH
	assetURL, checksumURL, err := selectAssets(rel, assetName)
	if err != nil {
		emit(Status{State: "failed", Version: version, Error: err.Error()})
		return err
	}
	if err = m.validateAssetURL(assetURL); err == nil {
		err = m.validateAssetURL(checksumURL)
	}
	if err != nil {
		emit(Status{State: "failed", Version: version, Error: err.Error()})
		return err
	}
	emit(Status{State: "downloading", Version: version})
	checksums, err := m.get(ctx, checksumURL, maxChecksumBytes)
	if err != nil {
		return err
	}
	want, err := checksumFor(checksums, assetName)
	if err != nil {
		return err
	}
	binary, err := m.get(ctx, assetURL, maxBinaryBytes)
	if err != nil {
		return err
	}
	got := sha256.Sum256(binary)
	if !strings.EqualFold(want, hex.EncodeToString(got[:])) {
		return errors.New("downloaded binary checksum mismatch")
	}
	if err := os.MkdirAll(m.cfg.StateDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(m.cfg.StateDir, assetName+"-"+version)
	if err := atomicWriteExecutable(path, binary); err != nil {
		return err
	}
	if err := writeState(m.cfg.StateDir, state{Version: version, Path: path, Phase: phaseAttempted}); err != nil {
		return err
	}
	emit(Status{State: "restarting", Version: version})
	if m.cfg.Exec == nil {
		return errors.New("update exec is not configured")
	}
	return m.cfg.Exec(path)
}

func (m *Manager) fetchRelease(ctx context.Context) (release, error) {
	var rel release
	b, err := m.get(ctx, m.cfg.ReleaseAPI, maxMetadataBytes)
	if err != nil {
		return rel, err
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		return rel, fmt.Errorf("decode latest release: %w", err)
	}
	version := strings.TrimPrefix(rel.TagName, "edge-v")
	if rel.Draft || rel.Prerelease || !strings.HasPrefix(rel.TagName, "edge-v") || !strictVersion.MatchString(version) {
		return rel, errors.New("latest release is not a stable edge semver tag")
	}
	return rel, nil
}

func (m *Manager) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("download exceeds size limit")
	}
	return b, nil
}

func (m *Manager) validateAssetURL(raw string) error {
	want, err := url.Parse(m.cfg.AllowAssetHost)
	if err != nil {
		return err
	}
	got, err := url.Parse(raw)
	if err != nil || got.Scheme != want.Scheme || got.Host != want.Host || got.User != nil {
		return errors.New("release asset URL is outside the allowed origin")
	}
	return nil
}

func selectAssets(rel release, binaryName string) (string, string, error) {
	var binaryURL, checksumURL string
	for _, asset := range rel.Assets {
		switch asset.Name {
		case binaryName:
			binaryURL = asset.URL
		case "checksums.txt":
			checksumURL = asset.URL
		}
	}
	if binaryURL == "" || checksumURL == "" {
		return "", "", errors.New("release is missing binary or checksums asset")
	}
	return binaryURL, checksumURL, nil
}

func checksumFor(data []byte, name string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return fields[0], nil
		}
	}
	return "", errors.New("binary checksum is missing")
}

func compareVersions(a, b string) int {
	pa, pb := strictVersion.FindStringSubmatch(a), strictVersion.FindStringSubmatch(b)
	if pa == nil {
		return -1
	}
	if pb == nil {
		return 1
	}
	for i := 1; i <= 3; i++ {
		ai, _ := strconv.Atoi(pa[i])
		bi, _ := strconv.Atoi(pb[i])
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func atomicWriteExecutable(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".candidate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o700); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
