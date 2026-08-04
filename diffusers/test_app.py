import base64
import io
import os
import unittest
from unittest.mock import patch

from fastapi.testclient import TestClient
from PIL import Image

import app
from model_config import DEFAULT_GENERATION_MODEL, DEFAULT_MODEL
from runtime import Device, DeviceUnavailableError


class _PipelineResult:
    def __init__(self, images):
        self.images = images


class _GenerationPipeline:
    def __init__(self):
        self.calls = []

    def __call__(self, **kwargs):
        self.calls.append(kwargs)
        return _PipelineResult([Image.new("RGB", (8, 8), "purple")])


class ImageRuntimeAPITests(unittest.TestCase):
    def setUp(self):
        self.client = TestClient(app.app)

    def test_preload_marks_generation_model_ready_only_after_pipeline_load(self):
        previous_ready = app.generation_ready
        previous_error = app.generation_error
        try:
            app.generation_ready = False
            app.generation_error = "loading"
            with patch("app.generation_pipeline", return_value=object()) as load:
                app.preload_generation_model()

            load.assert_called_once_with(DEFAULT_GENERATION_MODEL)
            self.assertTrue(app.generation_ready)
            self.assertEqual(app.generation_error, "")
        finally:
            app.generation_ready = previous_ready
            app.generation_error = previous_error

    def test_runtime_uses_lifespan_instead_of_deprecated_event_registry(self):
        self.assertEqual(app.app.router.on_startup, [])
        with patch("app.preload_generation_model") as preload:
            with TestClient(app.app):
                pass
        preload.assert_called_once_with()

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    @patch("app.generation_ready", True)
    def test_health_reports_backend_and_all_ready_models(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 200)
        payload = response.json()
        self.assertEqual(payload["status"], "ready")
        self.assertEqual(payload["device"], "mps")
        self.assertEqual(payload["backend"], "mps")
        self.assertEqual(
            payload["models"], sorted([DEFAULT_GENERATION_MODEL, DEFAULT_MODEL])
        )

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "16"})
    @patch("app.generation_ready", True)
    def test_health_does_not_advertise_large_editor_on_small_nodes(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.json()["models"], [DEFAULT_GENERATION_MODEL])

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch("app.generation_ready", False)
    @patch("app.generation_error", "model load failed")
    def test_health_is_non_success_until_generation_model_is_loaded(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 503)
        self.assertEqual(response.json()["status"], "unavailable")
        self.assertIn("model load failed", response.json()["error"])

    @patch("app.select_device", side_effect=DeviceUnavailableError("accelerator required"))
    def test_health_is_non_success_without_supported_accelerator(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 503)
        self.assertEqual(response.json()["models"], [])

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch("app.generation_ready", True)
    def test_generation_returns_openai_compatible_base64_data(self, _select):
        fake = _GenerationPipeline()
        with patch("app.generation_pipeline", return_value=fake):
            response = self.client.post(
                "/v1/images/generations",
                json={
                    "model": DEFAULT_GENERATION_MODEL,
                    "prompt": "a tiny robot",
                    "size": "512x512",
                    "response_format": "b64_json",
                },
            )

        self.assertEqual(response.status_code, 200, response.text)
        payload = response.json()
        self.assertEqual(payload["model"], DEFAULT_GENERATION_MODEL)
        self.assertEqual(len(payload["data"]), 1)
        decoded = base64.b64decode(payload["data"][0]["b64_json"])
        self.assertEqual(Image.open(io.BytesIO(decoded)).format, "PNG")
        self.assertEqual(fake.calls[0]["prompt"], "a tiny robot")
        self.assertEqual(fake.calls[0]["width"], 512)
        self.assertEqual(fake.calls[0]["height"], 512)

    def test_generation_rejects_an_unlisted_model(self):
        response = self.client.post(
            "/v1/images/generations",
            json={"model": "publisher/arbitrary-code", "prompt": "no"},
        )

        self.assertEqual(response.status_code, 404)

    def test_generation_validates_supported_size(self):
        response = self.client.post(
            "/v1/images/generations",
            json={
                "model": DEFAULT_GENERATION_MODEL,
                "prompt": "no",
                "size": "4096x4096",
            },
        )

        self.assertEqual(response.status_code, 422)

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_edit_response_keeps_console_field_and_adds_openai_data(self):
        source = io.BytesIO()
        Image.new("RGB", (8, 8), "green").save(source, format="PNG")
        fake = _GenerationPipeline()
        with patch("app.edit_pipeline", return_value=fake):
            response = self.client.post(
                "/v1/images/edits",
                data={"model": DEFAULT_MODEL, "prompt": "make it blue"},
                files={"image": ("source.png", source.getvalue(), "image/png")},
            )

        self.assertEqual(response.status_code, 200, response.text)
        payload = response.json()
        self.assertEqual(payload["b64_json"], payload["data"][0]["b64_json"])


if __name__ == "__main__":
    unittest.main()
