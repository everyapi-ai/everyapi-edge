import io
import os
from threading import Event

import pytest
from fastapi.testclient import TestClient

import app as runtime


@pytest.fixture(autouse=True)
def ready_runtime(monkeypatch):
    monkeypatch.setattr(runtime, "transcription_ready", True)
    monkeypatch.setattr(runtime, "transcription_status", "ready")
    monkeypatch.setattr(runtime, "transcription_error", "")
    monkeypatch.setattr(runtime, "select_device", lambda: ("cpu", -1))
    monkeypatch.setattr(runtime, "allocated_memory_bytes", lambda: 0)


def upload(client, path="/v1/audio/transcriptions", data=b"RIFFtest", filename="sample.wav", **fields):
    form = {"model": runtime.DEFAULT_MODEL, **fields}
    return client.post(path, data=form, files={"file": (filename, io.BytesIO(data), "audio/wav")})


def test_health_is_not_ready_until_warmup(monkeypatch):
    monkeypatch.setattr(runtime, "transcription_ready", False)
    monkeypatch.setattr(runtime, "transcription_status", "warming")
    monkeypatch.setattr(runtime, "transcription_error", "loading")
    response = TestClient(runtime.app).get("/health")
    assert response.status_code == 503
    assert {item["id"] for item in response.json()["capabilities"]} == {"audio.transcription", "audio.translation"}


def test_transcription_and_translation_forward_bounded_options_and_cleanup(monkeypatch):
    calls = []

    def fake_infer(path, task, language, prompt, temperature, cancelled):
        assert os.path.exists(path)
        calls.append((path, task, language, prompt, temperature, cancelled))
        return {"text": "hello", "chunks": [{"text": "hello", "timestamp": (0.0, 0.5)}]}

    monkeypatch.setattr(runtime, "infer", fake_infer)
    client = TestClient(runtime.app)
    transcription = upload(client, language="en", prompt="names", response_format="verbose_json", temperature="0.2")
    assert transcription.status_code == 200
    assert transcription.json()["segments"][0]["end"] == 0.5
    translation = upload(client, path="/v1/audio/translations", response_format="text")
    assert translation.status_code == 200
    assert translation.text == "hello"
    assert calls[0][1:5] == ("transcribe", "en", "names", 0.2)
    assert calls[1][1] == "translate"
    assert all(not os.path.exists(call[0]) for call in calls)


@pytest.mark.parametrize("filename", ["audio.exe", "audio"])
def test_rejects_unknown_audio_formats(filename):
    response = upload(TestClient(runtime.app), filename=filename)
    assert response.status_code == 422


def test_rejects_oversize_audio(monkeypatch):
    monkeypatch.setattr(runtime, "MAX_AUDIO_BYTES", 8)
    response = upload(TestClient(runtime.app), data=b"123456789")
    assert response.status_code == 413


def test_rejects_prompt_over_limit():
    response = upload(TestClient(runtime.app), prompt="x" * (runtime.MAX_PROMPT_CHARACTERS + 1))
    assert response.status_code == 422


def test_cancellation_criteria_reads_request_event():
    cancelled = Event()
    criteria = runtime.cancellation_criteria(cancelled)[0]
    assert criteria(None, None) is False
    cancelled.set()
    assert criteria(None, None) is True
