"""Accelerator selection for the Edge speech runtime.

Unlike the image runtime, CPU is a supported target. Kokoro-82M is small enough that CPU synthesis stays comfortably faster than realtime, so refusing to start without an accelerator would reject hosts that can serve speech perfectly well.
"""

import os
from dataclasses import dataclass
from typing import Callable, Optional


@dataclass(frozen=True)
class Device:
    name: str
    backend: str


def select_device(
    cuda_available: Optional[Callable[[], bool]] = None,
    mps_available: Optional[Callable[[], bool]] = None,
    cuda_backend: Optional[Callable[[], str]] = None,
) -> Device:
    if cuda_available is None or mps_available is None:
        import torch

        cuda_available = cuda_available or torch.cuda.is_available
        mps_available = mps_available or (
            lambda: bool(
                getattr(torch.backends, "mps", None)
                and torch.backends.mps.is_available()
            )
        )
        cuda_backend = cuda_backend or (
            lambda: "rocm" if getattr(torch.version, "hip", None) else "cuda"
        )

    cuda_backend = cuda_backend or (lambda: "cuda")

    if cuda_available():
        return Device(name="cuda", backend=cuda_backend())
    if mps_available():
        return Device(name="mps", backend="mps")
    return Device(name="cpu", backend="cpu")


def select_tts_device(
    configured: Optional[str] = None,
    cuda_available: Optional[Callable[[], bool]] = None,
    mps_available: Optional[Callable[[], bool]] = None,
) -> Device:
    """Keep Kokoro on CPU by default so it never occupies VRAM needed by larger runtimes."""
    requested = (configured if configured is not None else os.getenv("EVERYAPI_TTS_DEVICE", "cpu")).strip().lower() or "cpu"
    if requested == "cpu":
        return Device(name="cpu", backend="cpu")
    available = select_device(cuda_available=cuda_available, mps_available=mps_available)
    if requested == available.name:
        return available
    raise ValueError(f"EVERYAPI_TTS_DEVICE={requested!r} is not available")


def allocated_memory_bytes(device: Device) -> int:
    """Return this runtime process' current accelerator allocation."""
    try:
        import torch

        if device.name == "cuda":
            return int(torch.cuda.memory_reserved())
        if device.name == "mps":
            return int(torch.mps.current_allocated_memory())
    except (ImportError, AttributeError, RuntimeError):
        return 0
    return 0
