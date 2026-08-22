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

func composeServiceBlock(t *testing.T, contents, service string) string {
	t.Helper()
	marker := "\n  " + service + ":\n"
	start := strings.Index(contents, marker)
	if start == -1 {
		t.Fatalf("compose file is missing the %s service", service)
	}
	var lines []string
	for _, line := range strings.Split(contents[start+len(marker):], "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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

func TestAgentMountsConfiguredOllamaStorageInContainer(t *testing.T) {
	tests := []struct {
		file  string
		mount string
	}{
		{"docker-compose.yml", "${EVERYAPI_MODEL_PATH:-${HOME}/.everyapi/edge}:/models"},
		{"docker-compose.rocm.yml", "${EVERYAPI_MODEL_PATH:-${HOME}/.everyapi/edge}:/models"},
		{"docker-compose.macos.yml", "${EVERYAPI_MODEL_PATH:-${HOME}/.everyapi/edge}:/models"},
		{"docker-compose.windows.yml", "${EVERYAPI_MODEL_PATH:?model path required}:/models"},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			contents := readPackagingFile(t, test.file)
			agentBlock := composeServiceBlock(t, contents, "agent")
			if !strings.Contains(agentBlock, "EVERYAPI_OLLAMA_STORAGE_PATH: /models") {
				t.Errorf("%s does not configure the agent storage path", test.file)
			}
			if !strings.Contains(agentBlock, test.mount) {
				t.Errorf("%s does not mount the configured Ollama storage into the agent", test.file)
			}
		})
	}
}

// The speech runtime rides the same accelerator as ollama and diffusers, so every accelerated bundle gets it. Apple Silicon runs it natively because Docker cannot expose MPS.
func TestSpeechRuntimeIsWiredIntoEveryAcceleratedBundle(t *testing.T) {
	tests := []struct {
		file     string
		required []string
	}{
		{"docker-compose.yml", []string{"speech:", "build: ./speech", "EVERYAPI_TTS_DEVICE: ${EVERYAPI_TTS_DEVICE:-cpu}", "EVERYAPI_SPEECH_URL: http://speech:8189", "EVERYAPI_MAX_CONCURRENT_REQUESTS: ${EVERYAPI_MAX_CONCURRENT_REQUESTS:-4}"}},
		{"docker-compose.rocm.yml", []string{"speech:", "dockerfile: Dockerfile.rocm", "EVERYAPI_TTS_DEVICE: ${EVERYAPI_TTS_DEVICE:-cpu}", "EVERYAPI_SPEECH_URL: http://speech:8189", "EVERYAPI_MAX_CONCURRENT_REQUESTS: ${EVERYAPI_MAX_CONCURRENT_REQUESTS:-4}"}},
		{"docker-compose.macos.yml", []string{"EVERYAPI_SPEECH_URL: http://host.docker.internal:8189", "EVERYAPI_MAX_CONCURRENT_REQUESTS: ${EVERYAPI_MAX_CONCURRENT_REQUESTS:-4}"}},
		{"docker-compose.windows.yml", []string{"speech:", "build: ./speech", "EVERYAPI_TTS_DEVICE: ${EVERYAPI_TTS_DEVICE:-cpu}", "EVERYAPI_SPEECH_URL: http://speech:8189", "EVERYAPI_MAX_CONCURRENT_REQUESTS: ${EVERYAPI_MAX_CONCURRENT_REQUESTS:-4}"}},
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

// Kokoro cannot phonemise out-of-vocabulary words without espeak-ng, Whisper needs ffmpeg to decode uploads, and misaki pip-installs the spaCy pipeline on first use unless it is baked in. Any of these gaps would surface on a live buyer request rather than at build time.
func TestSpeechImagesBundleTheirPhonemiserAssets(t *testing.T) {
	for _, filename := range []string{"speech/Dockerfile", "speech/Dockerfile.rocm"} {
		contents := readPackagingFile(t, filename)
		for _, required := range []string{"espeak-ng", "ffmpeg", "spacy download en_core_web_sm", "COPY app.py model_config.py runtime.py ."} {
			if !strings.Contains(contents, required) {
				t.Errorf("%s is missing %q", filename, required)
			}
		}
	}
	requirements := readPackagingFile(t, "speech/requirements.txt")
	for _, required := range []string{"transformers", "accelerate", "safetensors"} {
		if !strings.Contains(requirements, required) {
			t.Errorf("speech/requirements.txt is missing %q", required)
		}
	}
}

// Suppliers build the speech runtime themselves — docker-compose.yml says `build: ./speech`, and nothing is pushed to a registry — so a Dockerfile that does not compile reaches them as a failed `docker compose up`. CI has to build it on the pull request that changes it.
//
// It has to be CI and not edge-release.yml, which is where these builds used to live. The release workflow publishes clients/edge/ to the public mirror in a job that runs in parallel with the image job, so a broken Dockerfile is already shipped by the time a release-time build proves it broken; all the failure does downstream is block the GitHub Release, since `release` needs `image`. That is how edge-v0.1.27 through v0.1.31 ended up tagged with nothing published.
func TestCIBuildsSpeechRuntimeImages(t *testing.T) {
	workflow := readPackagingFile(t, "../../.github/workflows/ci.yml")
	// The trailing newline keeps the CUDA entry from being satisfied by the ROCm one, whose path has it as a prefix.
	for _, required := range []string{
		"name: speech-cuda",
		"file: clients/edge/speech/Dockerfile\n",
		"name: speech-rocm",
		"file: clients/edge/speech/Dockerfile.rocm\n",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI is missing %q", required)
		}
	}

	release := readPackagingFile(t, "../../.github/workflows/edge-release.yml")
	if strings.Contains(release, "clients/edge/speech/Dockerfile") {
		t.Error("edge-release.yml builds the speech runtime again; that check belongs on the pull request, where it can still stop a broken Dockerfile from shipping")
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

func TestMacOSInstallerStartsNativeMPSSpeechRuntime(t *testing.T) {
	installer := readPackagingFile(t, "install.sh")
	for _, required := range []string{"ensure_macos_speech", "install-macos-speech.sh"} {
		if !strings.Contains(installer, required) {
			t.Errorf("macOS installer is missing %q", required)
		}
	}
	helper := readPackagingFile(t, "scripts/install-macos-speech.sh")
	for _, required := range []string{"speech/requirements-macos.txt", "com.everyapi.edge-speech.plist.in", "brew install ffmpeg", "127.0.0.1:8189/health", "+ 1200"} {
		if !strings.Contains(helper, required) {
			t.Errorf("native speech installer is missing %q", required)
		}
	}
	requirements := readPackagingFile(t, "speech/requirements-macos.txt")
	if !strings.Contains(requirements, "torch>=2.4,<3") {
		t.Fatal("native speech requirements must install an Apple MPS-capable torch build")
	}
	plist := readPackagingFile(t, "scripts/com.everyapi.edge-speech.plist.in")
	if !strings.Contains(plist, "/opt/homebrew/bin") {
		t.Fatal("native speech launchd service must be able to resolve Homebrew espeak-ng")
	}
}

func TestEdgeReleaseBuildsWindowsAgentBinary(t *testing.T) {
	workflow := readPackagingFile(t, "../../.github/workflows/edge-release.yml")
	if !strings.Contains(workflow, "windows/amd64") {
		t.Fatal("Edge release does not build a windows/amd64 agent")
	}
}

// Same reasoning as TestCIBuildsSpeechRuntimeImages: `build: ./diffusers` in the compose bundles means suppliers compile this one too, so the build that catches a broken Dockerfile has to run before the merge rather than during the release.
func TestCIBuildsDiffusersRuntimeImages(t *testing.T) {
	workflow := readPackagingFile(t, "../../.github/workflows/ci.yml")
	for _, required := range []string{
		"name: diffusers-cuda",
		"file: clients/edge/diffusers/Dockerfile\n",
		"name: diffusers-rocm",
		"file: clients/edge/diffusers/Dockerfile.rocm\n",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI is missing %q", required)
		}
	}

	release := readPackagingFile(t, "../../.github/workflows/edge-release.yml")
	if strings.Contains(release, "clients/edge/diffusers/Dockerfile") {
		t.Error("edge-release.yml builds the Diffusers runtime again; that check belongs on the pull request, where it can still stop a broken Dockerfile from shipping")
	}
}
