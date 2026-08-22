package main

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsInstallerSelectsWSL2NVIDIAProfileAndPersistsHostPlatform(t *testing.T) {
	contents, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{
		"docker-compose.windows.yml",
		"nvidia-smi",
		"windows/amd64",
		"EVERYAPI_DIFFUSERS_URL",
		"Move-Item -LiteralPath $temporaryEnv",
		"Invoke-CheckedNative",
		"New-EdgeConsoleToken",
		"EVERYAPI_CONSOLE_TOKEN=$consoleToken",
		"connected to gateway",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("Windows installer is missing %q", required)
		}
	}
	if _, err := os.Stat("scripts/test-edge-installer.ps1"); err != nil {
		t.Fatalf("PowerShell checked-command behavior test is missing: %v", err)
	}
}

func TestWindowsInstallerRejectsUntrustedDotenvValues(t *testing.T) {
	contents, err := os.ReadFile("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Assert-SingleLine", "registration token", "official Edge checkout"} {
		if !strings.Contains(string(contents), required) {
			t.Errorf("Windows installer is missing security invariant %q", required)
		}
	}
}
