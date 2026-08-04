import unittest

from runtime import DeviceUnavailableError, select_device


class DeviceSelectionTests(unittest.TestCase):
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

    def test_missing_accelerator_is_explicitly_unavailable(self):
        with self.assertRaisesRegex(DeviceUnavailableError, "CUDA, ROCm, or Apple MPS"):
            select_device(cuda_available=lambda: False, mps_available=lambda: False)


if __name__ == "__main__":
    unittest.main()
