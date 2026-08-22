import io
import unittest
from threading import Event
from unittest.mock import patch

import numpy as np
import soundfile as sf
from fastapi.testclient import TestClient

import app
from model_config import (
    DEFAULT_MODEL,
    SAMPLE_RATE,
    SUPPORTED_VOICES,
    WARMUP_PHRASES,
    language_for_voice,
)
from runtime import Device


def _tone(seconds: float = 0.25) -> np.ndarray:
    """A deterministic non-silent waveform, so encoders produce real frames."""
    t = np.linspace(0.0, seconds, int(SAMPLE_RATE * seconds), endpoint=False)
    return (0.25 * np.sin(2.0 * np.pi * 440.0 * t)).astype(np.float32)


class SpeechRuntimeAPITests(unittest.TestCase):
    def setUp(self):
        self.client = TestClient(app.app)

    def test_runtime_preloads_through_lifespan(self):
        self.assertEqual(app.app.router.on_startup, [])
        started = Event()
        with patch("app.preload", side_effect=started.set) as preload:
            with TestClient(app.app):
                self.assertTrue(started.wait(timeout=1), "speech warmup did not start")
        preload.assert_called_once_with()

    def test_lifespan_does_not_clear_pipeline_while_warmup_is_running(self):
        started = Event()
        release = Event()

        def blocked_preload():
            started.set()
            release.wait(timeout=1)

        try:
            with patch("app.preload", side_effect=blocked_preload):
                with patch.object(app.pipeline, "cache_clear") as clear_pipeline:
                    with TestClient(app.app):
                        self.assertTrue(started.wait(timeout=1), "speech warmup did not start")
                    clear_pipeline.assert_not_called()
        finally:
            release.set()

    def test_preload_only_marks_ready_after_a_successful_synthesis(self):
        previous_ready, previous_error, previous_status = app.speech_ready, app.speech_error, app.speech_status
        try:
            app.speech_ready = False
            with patch("app.pipeline"):
                with patch("app.synthesize", side_effect=RuntimeError("weights missing")):
                    app.preload()
                self.assertFalse(app.speech_ready)
                self.assertEqual(app.speech_error, "weights missing")

                with patch("app.synthesize", return_value=_tone()):
                    app.preload()
            self.assertTrue(app.speech_ready)
            self.assertEqual(app.speech_error, "")
        finally:
            app.speech_ready, app.speech_error, app.speech_status = previous_ready, previous_error, previous_status

    # Kokoro fetches a voice tensor the first time that voice is asked for. Warming only the default would hand the download latency, and any network failure, to whichever buyer requests another advertised voice first.
    def test_preload_warms_every_advertised_language_and_voice(self):
        previous_ready, previous_error = app.speech_ready, app.speech_error
        try:
            with patch("app.pipeline") as pipeline:
                with patch("app.synthesize", return_value=_tone()) as synth:
                    app.preload()

            warmed_languages = [call.args[0] for call in synth.call_args_list]
            self.assertEqual(
                sorted(map(language_for_voice, warmed_languages)),
                sorted(WARMUP_PHRASES),
            )
            loaded = {
                call.args[0] for call in pipeline.return_value.load_voice.call_args_list
            }
            self.assertEqual(loaded, set(SUPPORTED_VOICES))
            self.assertTrue(app.speech_ready)
        finally:
            app.speech_ready, app.speech_error = previous_ready, previous_error

    def test_preload_stays_unready_when_a_voice_cannot_be_fetched(self):
        previous_ready, previous_error = app.speech_ready, app.speech_error
        try:
            app.speech_ready = True
            with patch("app.pipeline") as pipeline:
                pipeline.return_value.load_voice.side_effect = OSError("network unreachable")
                with patch("app.synthesize", return_value=_tone()):
                    app.preload()

            self.assertFalse(app.speech_ready)
            self.assertIn("network unreachable", app.speech_error)
        finally:
            app.speech_ready, app.speech_error = previous_ready, previous_error

    @patch("app.select_device", return_value=Device(name="cuda", backend="cuda"))
    @patch("app.speech_ready", True)
    def test_health_advertises_the_model_for_agent_discovery(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 200)
        payload = response.json()
        self.assertEqual(payload["status"], "ready")
        self.assertEqual(payload["device"], "cuda")
        self.assertEqual(payload["models"], [DEFAULT_MODEL])
        self.assertEqual(payload["version"], app.RUNTIME_VERSION)
        self.assertIsInstance(payload["vram_bytes"], int)
        self.assertEqual(
            payload["capabilities"],
            [
                {
                    "id": "audio.tts",
                    "status": "ready",
                    "models": [DEFAULT_MODEL],
                    "paths": ["/v1/audio/speech"],
                    "limits": {
                        "max_input_characters": app.MAX_INPUT_CHARACTERS,
                        "formats": sorted(app.SUPPORTED_RESPONSE_FORMATS),
                    },
                }
            ],
        )

    @patch("app.select_device", return_value=Device(name="cpu", backend="cpu"))
    @patch("app.speech_ready", False)
    @patch("app.speech_error", "voice download failed")
    @patch("app.speech_status", "degraded")
    def test_health_is_non_success_until_the_voice_is_warm(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 503)
        self.assertEqual(response.json()["status"], "degraded")
        self.assertEqual(response.json()["models"], [DEFAULT_MODEL])
        self.assertIn("voice download failed", response.json()["error"])

    @patch("app.speech_ready", True)
    def test_speech_returns_decodable_mp3(self):
        with patch("app.synthesize", return_value=_tone()) as synth:
            response = self.client.post(
                "/v1/audio/speech",
                json={"model": DEFAULT_MODEL, "input": "hello", "voice": "af_alloy"},
            )

        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(response.headers["content-type"], "audio/mpeg")
        self.assertGreater(len(response.content), 0)
        synth.assert_called_once_with("af_alloy", "hello", 1.0)

    # The gateway reads the duration back out of these bytes to bill the buyer, so a container the decoder cannot parse would silently fall back to a byte-size estimate.
    @patch("app.speech_ready", True)
    def test_wav_output_carries_the_declared_duration(self):
        samples = _tone(seconds=0.5)
        with patch("app.synthesize", return_value=samples):
            response = self.client.post(
                "/v1/audio/speech",
                json={"model": DEFAULT_MODEL, "input": "hello", "response_format": "wav"},
            )

        self.assertEqual(response.headers["content-type"], "audio/wav")
        decoded, rate = sf.read(io.BytesIO(response.content))
        self.assertEqual(rate, SAMPLE_RATE)
        self.assertAlmostEqual(len(decoded) / rate, 0.5, places=3)

    # PCM has no header; the gateway bills it as 24 kHz, 16-bit, mono. Two bytes per sample at exactly that rate is the whole contract.
    @patch("app.speech_ready", True)
    def test_pcm_output_is_16_bit_mono_at_the_billed_rate(self):
        samples = _tone(seconds=0.5)
        with patch("app.synthesize", return_value=samples):
            response = self.client.post(
                "/v1/audio/speech",
                json={"model": DEFAULT_MODEL, "input": "hello", "response_format": "pcm"},
            )

        self.assertEqual(len(response.content), len(samples) * 2)
        billed = len(response.content) / (SAMPLE_RATE * 2 * 1)
        self.assertAlmostEqual(billed, 0.5, places=3)

    @patch("app.speech_ready", True)
    def test_stock_openai_voice_names_are_translated_for_kokoro(self):
        with patch("app.synthesize", return_value=_tone()) as synth:
            self.client.post(
                "/v1/audio/speech",
                json={"model": DEFAULT_MODEL, "input": "hello", "voice": "shimmer"},
            )

        self.assertEqual(synth.call_args.args[0], "af_sky")

    @patch("app.speech_ready", True)
    def test_unknown_voice_is_rejected_without_synthesising(self):
        with patch("app.synthesize") as synth:
            response = self.client.post(
                "/v1/audio/speech",
                json={"model": DEFAULT_MODEL, "input": "hello", "voice": "../secrets"},
            )

        self.assertEqual(response.status_code, 422)
        synth.assert_not_called()

    def test_unlisted_model_is_rejected(self):
        response = self.client.post(
            "/v1/audio/speech",
            json={"model": "publisher/arbitrary-weights", "input": "hello"},
        )

        self.assertEqual(response.status_code, 404)

    @patch("app.speech_ready", True)
    def test_unsupported_response_format_is_rejected(self):
        response = self.client.post(
            "/v1/audio/speech",
            json={"model": DEFAULT_MODEL, "input": "hello", "response_format": "opus"},
        )

        self.assertEqual(response.status_code, 422)

    @patch("app.speech_ready", True)
    def test_input_and_speed_bounds_are_enforced(self):
        oversized = self.client.post(
            "/v1/audio/speech",
            json={"model": DEFAULT_MODEL, "input": "x" * (app.MAX_INPUT_CHARACTERS + 1)},
        )
        self.assertEqual(oversized.status_code, 422)

        too_fast = self.client.post(
            "/v1/audio/speech",
            json={"model": DEFAULT_MODEL, "input": "hello", "speed": 9.0},
        )
        self.assertEqual(too_fast.status_code, 422)

    @patch("app.speech_ready", False)
    @patch("app.speech_error", "still loading")
    def test_requests_are_refused_while_the_model_is_cold(self):
        response = self.client.post(
            "/v1/audio/speech", json={"model": DEFAULT_MODEL, "input": "hello"}
        )

        self.assertEqual(response.status_code, 503)
        self.assertIn("still loading", response.json()["detail"])

    # This runtime bundles synthesis models only, so transcription must not be advertised accidentally.
    def test_transcription_route_is_absent(self):
        for path in ("/v1/audio/transcriptions", "/v1/audio/translations"):
            self.assertEqual(self.client.post(path).status_code, 404)


if __name__ == "__main__":
    unittest.main()
