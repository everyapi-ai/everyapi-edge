package main

import (
	"os"
	"os/exec"
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
