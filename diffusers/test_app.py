import base64
import io
import os
import unittest
from threading import Event, Lock, Thread, current_thread
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


class _PauseAfterReleaseLock:
    def __init__(self, thread_name):
        self._lock = Lock()
        self._thread_name = thread_name
        self.released = Event()
        self.resume = Event()

    def __enter__(self):
        self._lock.acquire()
        return self

    def __exit__(self, _exception_type, _exception, _traceback):
        self._lock.release()
        if current_thread().name == self._thread_name:
            self.released.set()
            self.resume.wait(timeout=2)


class ImageRuntimeAPITests(unittest.TestCase):
    def setUp(self):
        self.client = TestClient(app.app)

    def test_preload_marks_generation_model_ready_only_after_pipeline_load(self):
        previous_ready = app.generation_ready
        previous_error = app.generation_error
        previous_status = app.generation_status
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
            app.generation_status = previous_status

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_preload_marks_editor_ready_only_after_pipeline_load(self):
        previous_ready = app.editor_ready
        previous_error = app.editor_error
        previous_status = app.editor_status
        previous_model = app.resident_editor_model
        previous_pipeline = app.resident_editor_pipeline
        try:
            app.editor_ready = False
            with patch("app.generation_pipeline", return_value=object()):
                with patch("app.edit_pipeline", return_value=object()) as load:
                    app.preload_generation_model()

            load.assert_called_once_with(DEFAULT_MODEL)
            self.assertTrue(app.editor_ready)
            self.assertEqual(app.editor_error, "")
            self.assertEqual(app.editor_status, "ready")
        finally:
            app.editor_ready = previous_ready
            app.editor_error = previous_error
            app.editor_status = previous_status
            app.resident_editor_model = previous_model
            app.resident_editor_pipeline = previous_pipeline

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_preload_cannot_overwrite_a_concurrent_model_switch(self):
        previous = "Qwen/Qwen-Image-Edit-2509"
        preload_pipeline = object()
        selected_pipeline = object()
        lock = _PauseAfterReleaseLock("preload-editor")

        def load(model):
            return preload_pipeline if model == previous else selected_pipeline

        with patch("app.runtime_lock", lock):
            with patch("app.resident_editor_model", None):
                with patch("app.resident_editor_pipeline", None):
                    with patch("app.generation_pipeline", return_value=object()):
                        with patch("app.selected_model", return_value=previous):
                            with patch("app.edit_pipeline", side_effect=load):
                                with patch("app.select_model", return_value=DEFAULT_MODEL):
                                    preload = Thread(target=app.preload_generation_model, name="preload-editor")
                                    preload.start()
                                    self.assertTrue(lock.released.wait(timeout=1), "preload did not release the runtime lock")
                                    response = app.select_image_editor(app.ModelSelection(model=DEFAULT_MODEL))
                                    lock.resume.set()
                                    preload.join(timeout=2)

                                    self.assertEqual(app.resident_editor_model, DEFAULT_MODEL)
                                    self.assertIs(app.resident_editor_pipeline, selected_pipeline)

        self.assertEqual(response, {"status": "ready", "models": [DEFAULT_MODEL]})

    def test_runtime_uses_lifespan_instead_of_deprecated_event_registry(self):
        self.assertEqual(app.app.router.on_startup, [])
        started = Event()
        with patch("app.preload_generation_model", side_effect=started.set) as preload:
            with TestClient(app.app):
                self.assertTrue(started.wait(timeout=1), "image warmup did not start")
        preload.assert_called_once_with()

    def test_lifespan_does_not_clear_pipeline_while_warmup_is_running(self):
        started = Event()
        release = Event()

        def blocked_preload():
            started.set()
            release.wait(timeout=1)

        try:
            with patch("app.preload_generation_model", side_effect=blocked_preload):
                with patch.object(app.generation_pipeline, "cache_clear") as clear_generation:
                    with TestClient(app.app):
                        self.assertTrue(started.wait(timeout=1), "image warmup did not start")
                    clear_generation.assert_not_called()
        finally:
            release.set()

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    @patch("app.generation_ready", True)
    @patch("app.editor_ready", True, create=True)
    @patch("app.resident_editor_model", DEFAULT_MODEL)
    @patch("app.resident_editor_pipeline", object())
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
        self.assertEqual(payload["version"], app.RUNTIME_VERSION)
        self.assertIsInstance(payload["vram_bytes"], int)
        self.assertEqual(
            payload["capabilities"],
            [
                {
                    "id": "image.generate",
                    "status": "ready",
                    "models": [DEFAULT_GENERATION_MODEL],
                    "paths": ["/v1/images/generations"],
                    "limits": {"max_input_bytes": 33554432},
                },
                {
                    "id": "image.edit",
                    "status": "ready",
                    "models": [DEFAULT_MODEL],
                    "paths": ["/v1/images/edits"],
                    "limits": {"max_input_bytes": 33554432},
                },
            ],
        )

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    @patch("app.generation_ready", True)
    @patch("app.editor_ready", False, create=True)
    @patch("app.editor_status", "warming", create=True)
    @patch("app.editor_error", "editor model is still loading", create=True)
    def test_health_does_not_mark_editor_ready_before_its_pipeline_loads(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 200)
        payload = response.json()
        self.assertEqual(payload["models"], [DEFAULT_GENERATION_MODEL])
        editing = next(capability for capability in payload["capabilities"] if capability["id"] == "image.edit")
        self.assertEqual(editing["status"], "warming")
        self.assertIn("still loading", editing["reason"])

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "16"})
    @patch("app.generation_ready", True)
    def test_health_does_not_advertise_large_editor_on_small_nodes(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.json()["models"], [DEFAULT_GENERATION_MODEL])

    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch("app.generation_ready", False)
    @patch("app.generation_error", "model load failed")
    @patch("app.generation_status", "degraded")
    def test_health_is_non_success_until_generation_model_is_loaded(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 503)
        self.assertEqual(response.json()["status"], "degraded")
        self.assertIn("model load failed", response.json()["error"])
        self.assertEqual(response.json()["capabilities"][0]["status"], "degraded")

    @patch("app.select_device", side_effect=DeviceUnavailableError("accelerator required"))
    def test_health_is_non_success_without_supported_accelerator(self, _select):
        response = self.client.get("/health")

        self.assertEqual(response.status_code, 503)
        self.assertEqual(response.json()["models"], [])

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_select_editor_loads_model_before_persisting_ready_selection(self):
        calls = []
        with patch("app.edit_pipeline", side_effect=lambda model: calls.append(("load", model)) or object()):
            with patch("app.select_model", side_effect=lambda _path, model: calls.append(("persist", model)) or model):
                response = self.client.post("/v1/models/select", json={"model": DEFAULT_MODEL})

        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(calls, [("load", DEFAULT_MODEL), ("persist", DEFAULT_MODEL)])

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_select_editor_does_not_persist_model_when_pipeline_load_fails(self):
        with patch("app.edit_pipeline", side_effect=RuntimeError("out of memory")):
            with patch("app.select_model") as persist:
                response = self.client.post("/v1/models/select", json={"model": DEFAULT_MODEL})

        self.assertEqual(response.status_code, 503)
        persist.assert_not_called()

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_select_editor_restores_previous_resident_when_persistence_fails(self):
        previous = "Qwen/Qwen-Image-Edit-2509"
        previous_pipeline = object()
        candidate_pipeline = object()
        with patch("app.resident_editor_model", previous):
            with patch("app.resident_editor_pipeline", previous_pipeline):
                with patch("app.editor_ready", True):
                    with patch("app.selected_model", return_value=previous):
                        with patch("app.edit_pipeline", return_value=candidate_pipeline) as load:
                            with patch("app.select_model", side_effect=OSError("read-only filesystem")):
                                response = self.client.post("/v1/models/select", json={"model": DEFAULT_MODEL})

                                self.assertIs(app.resident_editor_pipeline, previous_pipeline)
                                self.assertEqual(app.resident_editor_model, previous)
                                self.assertTrue(app.editor_ready)

        self.assertEqual(response.status_code, 503)
        load.assert_called_once_with(DEFAULT_MODEL)
        load.cache_clear.assert_called_once_with()

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_failed_concurrent_switch_preserves_first_resident_identity(self):
        second_model = "Qwen/Qwen-Image-Edit-2509"
        lock = _PauseAfterReleaseLock("first-switch")
        first_result = []
        second_errors = []
        first_pipeline = object()
        second_pipeline = object()
        loads = []

        def load(model):
            loads.append((current_thread().name, model))
            return first_pipeline if model == DEFAULT_MODEL else second_pipeline

        def persist(_path, model):
            if current_thread().name == "second-switch":
                raise OSError("read-only filesystem")
            return model

        with patch("app.runtime_lock", lock):
            with patch("app.resident_editor_model", DEFAULT_MODEL):
                with patch("app.resident_editor_pipeline", object()):
                    with patch("app.editor_ready", True):
                        with patch("app.editor_status", "ready"):
                            with patch("app.editor_error", ""):
                                with patch("app.edit_pipeline", side_effect=load):
                                    with patch("app.select_model", side_effect=persist):
                                        first = Thread(
                                            target=lambda: first_result.append(app.select_image_editor(app.ModelSelection(model=DEFAULT_MODEL))),
                                            name="first-switch",
                                        )
                                        first.start()
                                        self.assertTrue(lock.released.wait(timeout=1), "first switch did not release the runtime lock")

                                        def run_second_switch():
                                            try:
                                                app.select_image_editor(app.ModelSelection(model=second_model))
                                            except Exception as error:
                                                second_errors.append(error)

                                        second = Thread(target=run_second_switch, name="second-switch")
                                        second.start()
                                        second.join(timeout=2)
                                        lock.resume.set()
                                        first.join(timeout=2)

                                        self.assertTrue(app.editor_ready)
                                        self.assertEqual(app.editor_status, "ready")
                                        self.assertEqual(app.resident_editor_model, DEFAULT_MODEL)
                                        self.assertIs(app.resident_editor_pipeline, first_pipeline)

        self.assertEqual(first_result, [{"status": "ready", "models": [DEFAULT_MODEL]}])
        self.assertEqual(len(second_errors), 1)
        self.assertEqual(loads, [("first-switch", DEFAULT_MODEL), ("second-switch", second_model)])

    @patch("app.allocated_memory_bytes", return_value=0)
    @patch("app.select_device", return_value=Device(name="mps", backend="mps"))
    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_health_never_observes_persisted_selection_before_resident_commit(self, _select, _memory):
        previous = "Qwen/Qwen-Image-Edit-2509"
        state = {"selected": previous}
        persisted = Event()
        release_persist = Event()
        health_done = Event()
        health_result = []

        def persist(_path, model):
            state["selected"] = model
            persisted.set()
            release_persist.wait(timeout=2)
            return model

        with patch("app.generation_ready", True):
            with patch("app.resident_editor_model", previous):
                with patch("app.resident_editor_pipeline", object()):
                    with patch("app.editor_ready", True):
                        with patch("app.editor_status", "ready"):
                            with patch("app.editor_error", ""):
                                with patch("app.selected_model", side_effect=lambda: state["selected"]):
                                    with patch("app.edit_pipeline", return_value=object()):
                                        with patch("app.select_model", side_effect=persist):
                                            switch = Thread(
                                                target=lambda: app.select_image_editor(app.ModelSelection(model=DEFAULT_MODEL))
                                            )
                                            switch.start()
                                            self.assertTrue(persisted.wait(timeout=1), "model selection was not persisted")

                                            def read_health():
                                                health_result.append(app.health())
                                                health_done.set()

                                            reader = Thread(target=read_health)
                                            reader.start()
                                            self.assertTrue(health_done.wait(timeout=1), "health did not return the resident snapshot")
                                            editing_during_commit = next(
                                                capability
                                                for capability in health_result[0]["capabilities"]
                                                if capability["id"] == "image.edit"
                                            )
                                            self.assertEqual(editing_during_commit["status"], "ready")
                                            self.assertEqual(editing_during_commit["models"], [previous])
                                            release_persist.set()
                                            switch.join(timeout=2)
                                            reader.join(timeout=2)

                                            committed_health = app.health()
                                            committed_editing = next(
                                                capability
                                                for capability in committed_health["capabilities"]
                                                if capability["id"] == "image.edit"
                                            )
                                            self.assertEqual(committed_editing["status"], "ready")
                                            self.assertEqual(committed_editing["models"], [DEFAULT_MODEL])

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
    @patch("app.editor_ready", True, create=True)
    def test_edit_response_keeps_console_field_and_adds_openai_data(self):
        source = io.BytesIO()
        Image.new("RGB", (8, 8), "green").save(source, format="PNG")
        fake = _GenerationPipeline()
        with patch("app.resident_editor_model", DEFAULT_MODEL):
            with patch("app.resident_editor_pipeline", fake):
                response = self.client.post(
                    "/v1/images/edits",
                    data={"model": DEFAULT_MODEL, "prompt": "make it blue"},
                    files={"image": ("source.png", source.getvalue(), "image/png")},
                )

        self.assertEqual(response.status_code, 200, response.text)
        payload = response.json()
        self.assertEqual(payload["b64_json"], payload["data"][0]["b64_json"])

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    @patch("app.editor_ready", False, create=True)
    @patch("app.editor_error", "editor model is still loading", create=True)
    def test_edit_rejects_requests_until_editor_pipeline_is_ready(self):
        source = io.BytesIO()
        Image.new("RGB", (8, 8), "green").save(source, format="PNG")
        with patch("app.edit_pipeline", return_value=_GenerationPipeline()):
            response = self.client.post(
                "/v1/images/edits",
                data={"model": DEFAULT_MODEL, "prompt": "make it blue"},
                files={"image": ("source.png", source.getvalue(), "image/png")},
            )

        self.assertEqual(response.status_code, 503)
        self.assertIn("still loading", response.json()["detail"])

    @patch.dict(os.environ, {"EVERYAPI_VRAM_GB": "48"})
    def test_model_switch_serializes_with_an_edit_for_the_previous_model(self):
        previous = "Qwen/Qwen-Image-Edit-2509"
        state = {"active": previous}
        load_started = Event()
        release_load = Event()
        switch_response = []
        edit_response = []
        source = io.BytesIO()
        Image.new("RGB", (8, 8), "green").save(source, format="PNG")

        def load(model):
            load_started.set()
            release_load.wait(timeout=2)
            return _GenerationPipeline()

        def persist(_path, model):
            state["active"] = model
            return model

        with patch("app.editor_ready", True):
            with patch("app.resident_editor_model", previous):
                with patch("app.resident_editor_pipeline", _GenerationPipeline()):
                    with patch("app.selected_model", side_effect=lambda: state["active"]):
                        with patch("app.edit_pipeline", side_effect=load):
                            with patch("app.select_model", side_effect=persist):
                                switch = Thread(target=lambda: switch_response.append(self.client.post("/v1/models/select", json={"model": DEFAULT_MODEL})))
                                switch.start()
                                self.assertTrue(load_started.wait(timeout=1), "model switch did not begin loading")
                                edit = Thread(
                                    target=lambda: edit_response.append(
                                        self.client.post(
                                            "/v1/images/edits",
                                            data={"model": previous, "prompt": "make it blue"},
                                            files={"image": ("source.png", source.getvalue(), "image/png")},
                                        )
                                    )
                                )
                                edit.start()
                                release_load.set()
                                switch.join(timeout=2)
                                edit.join(timeout=2)

        self.assertEqual(switch_response[0].status_code, 200)
        self.assertEqual(edit_response[0].status_code, 404)


if __name__ == "__main__":
    unittest.main()
