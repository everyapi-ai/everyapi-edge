"""Accelerator selection for the Edge speech runtime.

Unlike the image runtime, CPU is a supported target. Kokoro-82M is small enough that CPU synthesis stays comfortably faster than realtime, so refusing to start without an accelerator would reject hosts that can serve speech perfectly well.
"""

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
