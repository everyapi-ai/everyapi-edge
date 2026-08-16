package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerNeverRecursivelyDeletesCallerPath(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), `rm -rf "$INSTALL_DIR"`) {
		t.Fatal("installer recursively deletes caller-controlled --dir")
	}
}

func runInstallerExpectFailure(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"install.sh"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("installer unexpectedly succeeded: %s", output)
	}
	return string(output)
}

func TestInstallerRejectsUnsafeDirectoryBeforeSideEffects(t *testing.T) {
	output := runInstallerExpectFailure(t,
		"--node-id", "1",
		"--token", "edgert_test",
		"--name", "test-node",
		"--dir", "/",
	)
	if !strings.Contains(output, "refusing unsafe install directory") {
		t.Fatalf("unexpected failure: %s", output)
	}
}

func TestInstallerRejectsDotenvNewlineInjectionBeforeSideEffects(t *testing.T) {
	output := runInstallerExpectFailure(t,
		"--node-id", "1",
		"--token", "edgert_test\nCOMPOSE_FILE=evil.yml",
		"--name", "test-node",
	)
	if !strings.Contains(output, "registration token must not contain line breaks") {
		t.Fatalf("unexpected failure: %s", output)
	}
}

func TestInstallerRejectsEnvAndPathInjection(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, invariant := range []string{
		"validate_no_newlines",
		"refusing unsafe install directory",
		"refusing existing non-EveryAPI directory",
	} {
		if !strings.Contains(string(script), invariant) {
			t.Errorf("installer is missing security invariant %q", invariant)
		}
	}
}

func TestInstallerDocumentationUsesCDN(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "https://dl.everyapi.ai/edge/install.sh") {
		t.Fatal("installer documentation must direct suppliers to the CDN")
	}
}

func TestInstallerPreparesAndVerifiesAutoSelectedModel(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"--model)",
		"detect_model_memory_gb",
		"model_candidates_for_memory",
		"ensure_macos_ollama",
		"ollama_command pull",
		"/api/generate",
		"--since",
	} {
		if !strings.Contains(string(script), required) {
			t.Errorf("installer is missing model onboarding step %q", required)
		}
	}
}

func TestInstallerDoesNotAdvertiseImageSupportWithoutAnAccelerator(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	if strings.Contains(script, "falling back to nvidia config (will run CPU-only)") {
		t.Fatal("installer silently selects an image profile without a supported accelerator")
	}
	if !strings.Contains(script, "no supported GPU detected") {
		t.Fatal("installer must explain the supported accelerator requirement")
	}
}

func TestGPUComposeLimitsOllamaToOneResidentModel(t *testing.T) {
	for _, filename := range []string{"docker-compose.yml", "docker-compose.rocm.yml"} {
		contents, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "OLLAMA_MAX_LOADED_MODELS: ${OLLAMA_MAX_LOADED_MODELS:-1}") {
			t.Errorf("%s must limit Ollama to one resident model", filename)
		}
	}
}

func TestMacOSOllamaStarterUsesTheSameResidentModelLimit(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "OLLAMA_MAX_LOADED_MODELS=\"${OLLAMA_MAX_LOADED_MODELS:-1}\"") {
		t.Fatal("native Ollama starter must limit resident models")
	}
}

func TestMacOSHomebrewStarterAppliesResidentModelLimit(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`launchctl setenv OLLAMA_MAX_LOADED_MODELS`,
		`launchctl setenv OLLAMA_KEEP_ALIVE`,
		`brew services restart ollama`,
	} {
		if !strings.Contains(string(contents), required) {
			t.Errorf("Homebrew Ollama starter is missing %q", required)
		}
	}
}

func TestMacOSInstallerKeepsModelsInEveryAPIHome(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{
		`local model_root="$HOME/.everyapi/edge"`,
		`launchctl setenv OLLAMA_MODELS "$model_root"`,
		`storage_path="$HOME/.everyapi/edge"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("macOS installer is missing %q", required)
		}
	}
	if strings.Index(script, `launchctl setenv OLLAMA_MODELS "$model_root"`) > strings.Index(script, `if ! ollama_api_ready; then`) {
		t.Fatal("macOS installer must configure the model directory before reusing an existing local service")
	}
}

func TestMacOSInstallerPersistsHostUnifiedMemory(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{
		"detect_macos_memory_gb",
		`VRAM_GB="$(detect_macos_memory_gb)"`,
		`physical_gb=$(detect_macos_memory_gb)`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("macOS installer does not persist host unified memory: missing %q", required)
		}
	}
}

func TestExistingNodeUpgradeReusesIdentityWithoutRegistrationToken(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "everyapi-edge")
	if err := os.MkdirAll(filepath.Join(installDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installDir, "data", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "scripts", "install-macos-diffusers.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "data", "agent", "identity.json"), []byte(`{"private_key":"persisted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldEnv := "EVERYAPI_GATEWAY=https://gateway.example.test\n" +
		"EVERYAPI_NODE_ID=842\n" +
		"EVERYAPI_REGISTRATION_TOKEN=edgert_stale_consumed\n" +
		"EVERYAPI_NODE_NAME=existing-mac\n" +
		"EVERYAPI_COUNTRY=jp\n" +
		"EVERYAPI_WORKLOADS=chat, embedding\n" +
		"EVERYAPI_CONSOLE_PORT=9842\n"
	if err := os.WriteFile(filepath.Join(installDir, ".env"), []byte(oldEnv), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable("git", `
if [ "$1" = "-C" ] && [ "$3" = "remote" ]; then
  echo https://github.com/everyapi-ai/everyapi-edge
fi
exit 0
`)
	writeExecutable("docker", `
case "$*" in
  "--version") echo "Docker version 27.0.0, build test" ;;
  *"ps -q agent"*) echo "existing-agent" ;;
  "logs --since "*) echo "connected to gateway with 1 models" ;;
  *" up -d"*)
    if grep -q '^EVERYAPI_REGISTRATION_TOKEN=edgert_' .env; then
      echo "upgrade attempted stale token registration" >&2
      exit 42
    fi
    ;;
esac
exit 0
`)
	writeExecutable("curl", "exit 0\n")
	writeExecutable("ollama", "exit 0\n")
	writeExecutable("brew", "exit 1\n")
	writeExecutable("sysctl", "echo 51539607552\n")
	writeExecutable("uname", `
case "$1" in
  -s) echo Darwin ;;
  -m) echo arm64 ;;
esac
`)

	cmd := exec.Command("bash", "install.sh", "--dir", installDir, "--gpu", "macos")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+binDir+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("existing-node upgrade failed or prompted for a new token: %v\n%s", err, output)
	}
	updated, err := os.ReadFile(filepath.Join(installDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(updated)
	for _, required := range []string{
		"EVERYAPI_GATEWAY=https://gateway.example.test",
		"EVERYAPI_NODE_ID=842",
		"EVERYAPI_NODE_NAME=existing-mac",
		"EVERYAPI_REGISTRATION_TOKEN=",
		"EVERYAPI_GPU_MODEL=Apple Silicon",
		"EVERYAPI_VRAM_GB=48",
		"EVERYAPI_PLATFORM=darwin/arm64",
		"EVERYAPI_COUNTRY=JP",
		"EVERYAPI_WORKLOADS=chat,embedding",
		"EVERYAPI_CONSOLE_PORT=9842",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("upgraded env is missing %q:\n%s", required, contents)
		}
	}
	if strings.Contains(contents, "EVERYAPI_REGISTRATION_TOKEN=edgert_") {
		t.Fatalf("upgrade retained a consumed registration token:\n%s", contents)
	}
}

func TestMacOSComposeDocumentsEveryAPIModelRoot(t *testing.T) {
	contents, err := os.ReadFile("docker-compose.macos.yml")
	if err != nil {
		t.Fatal(err)
	}
	compose := string(contents)
	if strings.Contains(compose, "~/.ollama") || !strings.Contains(compose, "${HOME}/.everyapi/edge") {
		t.Fatalf("macOS Compose documents the wrong model root: %s", compose)
	}
	if !strings.Contains(compose, "EVERYAPI_PLATFORM: ${EVERYAPI_PLATFORM:-darwin/arm64}") {
		t.Fatal("macOS Compose must publish the host platform instead of the Linux container runtime")
	}
}

func TestComposePublishesTheControlRoomToTheTrustedLAN(t *testing.T) {
	for _, filename := range []string{"docker-compose.yml", "docker-compose.rocm.yml", "docker-compose.macos.yml"} {
		contents, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), `"${EVERYAPI_CONSOLE_PORT:-8421}:8421"`) {
			t.Errorf("%s must publish the local Control Room on the configured LAN port", filename)
		}
	}
}

func TestInstallerClearsConsumedTokenOnlyAfterGatewayConnection(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	contents := string(script)
	for _, required := range []string{
		"clear_consumed_registration_token",
		"connected to gateway",
		"EVERYAPI_REGISTRATION_TOKEN=",
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("installer is missing post-connection credential handling %q", required)
		}
	}
}

func TestReleasePublishesInstallerToCDN(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/edge-release.yml")
	if os.IsNotExist(err) {
		t.Skip("release workflow exists only in the monorepo")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), `clients/edge/install.sh  "oss://${OSS_BUCKET}/edge/install.sh"`) {
		t.Fatal("edge release must publish its installer to the CDN")
	}
}

// A node is a long-lived service holding credentials, so where it lands must not depend on which directory the operator happened to be standing in when they ran the documented `curl … | bash` one-liner.
func TestInstallerDefaultsToHomeDirectory(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), `INSTALL_DIR="./everyapi-edge"`) {
		t.Fatal("installer still defaults the install directory to a cwd-relative path")
	}
	if !strings.Contains(string(script), `INSTALL_DIR="${HOME:+$HOME/everyapi-edge}"`) {
		t.Fatal("installer does not default the install directory to $HOME/everyapi-edge")
	}
}

// Installing into a source checkout leaves .env (node credentials) and data/ (the Ed25519 identity) in a working tree, where they show up in git status and are one careless `git add` from being committed. The earlier guards only rejected the cwd itself, so a subdirectory of a repository slipped through.
//
// This test runs from clients/edge, which is inside this repository's working tree, so the target below is exactly the case under test.
func TestInstallerRefusesToInstallInsideGitWorktree(t *testing.T) {
	// Belt and braces: if the guard ever regresses, the installer really does clone into this path, and a failing test should not also leave a bundle sitting in the working tree.
	t.Cleanup(func() { _ = os.RemoveAll("./everyapi-edge-guard-target") })
	output := runInstallerExpectFailure(t,
		"--node-id", "1",
		"--token", "edgert_test",
		"--name", "test-node",
		"--dir", "./everyapi-edge-guard-target",
	)
	if !strings.Contains(output, "refusing to install inside the git working tree") {
		t.Fatalf("unexpected failure: %s", output)
	}
	if _, err := os.Stat("./everyapi-edge-guard-target"); !os.IsNotExist(err) {
		t.Fatal("installer created the target directory before refusing")
	}
}

// A git-managed HOME is the dotfiles pattern, not a source checkout. Refusing there would break the new default for everyone who versions their home directory, so the guard has to exempt it.
func TestInstallerAllowsGitManagedHome(t *testing.T) {
	home := t.TempDir()
	if out, err := exec.Command("git", "-C", home, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// No --node-id: the run fails later, at the required-argument check, which is well past the guard. We assert only on which failure it was.
	cmd := exec.Command("bash", "install.sh", "--dir", home+"/everyapi-edge")
	cmd.Env = append(os.Environ(), "HOME="+home)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("installer unexpectedly ran to completion: %s", output)
	}
	if strings.Contains(string(output), "refusing to install inside the git working tree") {
		t.Fatalf("guard misfired on a git-managed HOME: %s", output)
	}
}
