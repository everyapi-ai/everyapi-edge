package main

import (
	"os"
	"strings"
	"testing"
)

func readPackagingFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestImageRuntimeComposeProfilesMatchHostAccelerators(t *testing.T) {
	tests := []struct {
		file     string
		required []string
	}{
		{"docker-compose.yml", []string{"diffusers:", "capabilities: [gpu]", "EVERYAPI_DIFFUSERS_URL: http://diffusers:8188", "start_period: 20m"}},
		{"docker-compose.rocm.yml", []string{"diffusers:", "dockerfile: Dockerfile.rocm", "/dev/kfd", "EVERYAPI_DIFFUSERS_URL: http://diffusers:8188", "start_period: 20m"}},
		{"docker-compose.macos.yml", []string{"EVERYAPI_DIFFUSERS_URL: http://host.docker.internal:8188", "EVERYAPI_PLATFORM: ${EVERYAPI_PLATFORM:-darwin/arm64}"}},
		{"docker-compose.windows.yml", []string{"diffusers:", "capabilities: [gpu]", "EVERYAPI_PLATFORM: ${EVERYAPI_PLATFORM:-windows/amd64}", "start_period: 20m"}},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			contents := readPackagingFile(t, test.file)
			for _, required := range test.required {
				if !strings.Contains(contents, required) {
					t.Errorf("%s is missing %q", test.file, required)
				}
			}
		})
	}
}

func TestDiffusersImagesIncludeSharedRuntimeModule(t *testing.T) {
	for _, filename := range []string{"diffusers/Dockerfile", "diffusers/Dockerfile.rocm"} {
		contents := readPackagingFile(t, filename)
		if !strings.Contains(contents, "COPY app.py model_config.py runtime.py .") {
			t.Errorf("%s does not include runtime.py", filename)
		}
	}
}

func TestMacOSInstallerStartsNativeMPSImageRuntime(t *testing.T) {
	installer := readPackagingFile(t, "install.sh")
	for _, required := range []string{"ensure_macos_diffusers", "install-macos-diffusers.sh"} {
		if !strings.Contains(installer, required) {
			t.Errorf("macOS installer is missing %q", required)
		}
	}
	helper := readPackagingFile(t, "scripts/install-macos-diffusers.sh")
	if !strings.Contains(helper, "+ 1200") {
		t.Fatal("macOS installer must allow the first image model download to finish")
	}
}

func TestEdgeReleaseBuildsWindowsAgentBinary(t *testing.T) {
	workflow := readPackagingFile(t, "../../.github/workflows/edge-release.yml")
	if !strings.Contains(workflow, "windows/amd64") {
		t.Fatal("Edge release does not build a windows/amd64 agent")
	}
}

func TestEdgeReleaseSmokeBuildsDiffusersImages(t *testing.T) {
	workflow := readPackagingFile(t, "../../.github/workflows/edge-release.yml")
	for _, required := range []string{
		"Smoke build CUDA Diffusers runtime",
		"file: clients/edge/diffusers/Dockerfile",
		"Smoke build ROCm Diffusers runtime",
		"file: clients/edge/diffusers/Dockerfile.rocm",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("Edge release is missing %q", required)
		}
	}
}
