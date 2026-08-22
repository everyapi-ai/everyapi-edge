import unittest

from runtime import select_device, select_tts_device


class DeviceSelectionTests(unittest.TestCase):
    def test_tts_defaults_to_cpu_even_when_cuda_is_available(self):
        selected = select_tts_device(configured="", cuda_available=lambda: True, mps_available=lambda: True)
        self.assertEqual(selected.name, "cpu")

    def test_cuda_is_preferred_when_available(self):
        selected = select_device(cuda_available=lambda: True, mps_available=lambda: True)

        self.assertEqual(selected.name, "cuda")
        self.assertEqual(selected.backend, "cuda")

    def test_mps_is_used_when_cuda_is_unavailable(self):
        selected = select_device(cuda_available=lambda: False, mps_available=lambda: True)

        self.assertEqual(selected.name, "mps")
        self.assertEqual(selected.backend, "mps")

    def test_rocm_build_reports_rocm_even_though_pytorch_uses_cuda_api(self):
        selected = select_device(
            cuda_available=lambda: True,
            mps_available=lambda: False,
            cuda_backend=lambda: "rocm",
        )

        self.assertEqual(selected.name, "cuda")
        self.assertEqual(selected.backend, "rocm")

    # The image runtime refuses to start without an accelerator. Speech must not: Kokoro-82M synthesises faster than realtime on CPU, so a CPU-only host is a legitimate speech supplier.
    def test_cpu_is_a_supported_target_rather_than_a_failure(self):
        selected = select_device(cuda_available=lambda: False, mps_available=lambda: False)

        self.assertEqual(selected.name, "cpu")
        self.assertEqual(selected.backend, "cpu")


if __name__ == "__main__":
    unittest.main()
