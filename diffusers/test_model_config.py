import tempfile
import unittest
from pathlib import Path

from model_config import (
    DEFAULT_GENERATION_MODEL,
    DEFAULT_MODEL,
    SUPPORTED_GENERATION_MODELS,
    SUPPORTED_IMAGE_EDITORS,
    active_model,
    select_model,
)


class ModelConfigTests(unittest.TestCase):
    def test_small_sana_generator_is_separate_from_qwen_editors(self):
        self.assertEqual(
            DEFAULT_GENERATION_MODEL,
            "Efficient-Large-Model/Sana_600M_1024px_diffusers",
        )
        self.assertIn(DEFAULT_GENERATION_MODEL, SUPPORTED_GENERATION_MODELS)
        self.assertNotIn(DEFAULT_GENERATION_MODEL, SUPPORTED_IMAGE_EDITORS)

    def test_selected_image_editor_persists_for_the_next_runtime_start(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image-runtime.json"

            self.assertEqual(active_model(path, DEFAULT_MODEL), DEFAULT_MODEL)
            selected = select_model(path, "Qwen/Qwen-Image-Edit-2509")

            self.assertEqual(selected, "Qwen/Qwen-Image-Edit-2509")
            self.assertEqual(active_model(path, DEFAULT_MODEL), "Qwen/Qwen-Image-Edit-2509")

    def test_invalid_startup_override_falls_back_to_the_supported_default(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image-runtime.json"

            self.assertEqual(active_model(path, "unsupported/image-model"), DEFAULT_MODEL)


if __name__ == "__main__":
    unittest.main()
