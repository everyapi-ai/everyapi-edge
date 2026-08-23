# The 1.3B Diffusers checkpoint is Apache-2.0 and the immutable revision prevents an upstream model mutation from changing supplier runtime behavior between releases.
DEFAULT_MODEL = "Wan-AI/Wan2.1-T2V-1.3B-Diffusers"
DEFAULT_MODEL_REVISION = "0fad780a534b6463e45facd96134c9f345acfa5b"
SUPPORTED_MODELS = frozenset({DEFAULT_MODEL})
SUPPORTED_SIZES = {"832x480": (832, 480), "480x832": (480, 832)}
DEFAULT_SIZE = "832x480"
DEFAULT_SECONDS = 2
MIN_SECONDS = 1
MAX_SECONDS = 3
FRAMES_PER_SECOND = 16
MAX_PROMPT_CHARACTERS = 2000
MAX_OUTPUT_BYTES = 64 * 1024 * 1024
