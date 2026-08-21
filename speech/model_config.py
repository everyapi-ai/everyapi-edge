"""Voice, language, and output-format contract for the Edge speech runtime.

The advertised model is a Hugging Face repository id, matching how the image runtime advertises Sana. Voices are allow-listed rather than passed through: Kokoro loads a voice by downloading `voices/<name>.pt` from the repository, so an unchecked name would let a buyer make the node fetch arbitrary files.
"""

DEFAULT_MODEL = "hexgrad/Kokoro-82M"
SUPPORTED_MODELS = frozenset({DEFAULT_MODEL})

# Kokoro derives its grapheme-to-phoneme frontend from the voice prefix. Only the three locales whose G2P dependencies ship in the image are advertised: `a`/`b` share misaki[en], `z` uses misaki[zh]. Japanese needs pyopenjtalk and the remaining locales are untested here, so their voices stay unlisted.
LANGUAGE_BY_PREFIX = {
    "af": "a",
    "am": "a",
    "bf": "b",
    "bm": "b",
    "zf": "z",
    "zm": "z",
}

SUPPORTED_VOICES = frozenset(
    {
        "af_alloy",
        "af_aoede",
        "af_bella",
        "af_heart",
        "af_jessica",
        "af_kore",
        "af_nicole",
        "af_nova",
        "af_river",
        "af_sarah",
        "af_sky",
        "am_adam",
        "am_echo",
        "am_eric",
        "am_fenrir",
        "am_liam",
        "am_michael",
        "am_onyx",
        "am_puck",
        "am_santa",
        "bf_alice",
        "bf_emma",
        "bf_isabella",
        "bf_lily",
        "bm_daniel",
        "bm_fable",
        "bm_george",
        "bm_lewis",
        "zf_xiaobei",
        "zf_xiaoni",
        "zf_xiaoxiao",
        "zf_xiaoyi",
        "zm_yunjian",
        "zm_yunxi",
        "zm_yunxia",
        "zm_yunyang",
    }
)

DEFAULT_VOICE = "af_alloy"

# OpenAI clients send the six stock voice names. Kokoro happens to ship voices under most of those names already; `shimmer` has no counterpart and maps to the nearest bright female timbre.
VOICE_ALIASES = {
    "alloy": "af_alloy",
    "echo": "am_echo",
    "fable": "bm_fable",
    "onyx": "am_onyx",
    "nova": "af_nova",
    "shimmer": "af_sky",
}

SAMPLE_RATE = 24000

# `pcm` is raw signed 16-bit little-endian mono at SAMPLE_RATE, with no container. The gateway bills that format by assuming exactly those parameters, so the runtime must not emit PCM at any other rate or width.
SOUNDFILE_FORMATS = {"mp3": "MP3", "wav": "WAV", "flac": "FLAC"}
SUPPORTED_RESPONSE_FORMATS = frozenset(set(SOUNDFILE_FORMATS) | {"pcm"})
DEFAULT_RESPONSE_FORMAT = "mp3"

CONTENT_TYPES = {
    "mp3": "audio/mpeg",
    "wav": "audio/wav",
    "flac": "audio/flac",
    "pcm": "audio/L16",
}

MIN_SPEED = 0.25
MAX_SPEED = 4.0

# One phrase per advertised language, synthesised at startup. Building a KPipeline constructs that locale's grapheme-to-phoneme frontend, and misaki fetches its Chinese assets the first time it phonemises, so a locale that is only exercised by a buyer request pays that cost inside the request.
WARMUP_PHRASES = {"a": "Ready.", "b": "Ready.", "z": "准备就绪。"}


def resolve_voice(voice: str) -> str:
    """Map a requested voice onto an allow-listed Kokoro voice.

    Raises ValueError so callers can turn it into a 422 rather than letting an unknown name reach Kokoro's downloader.
    """
    name = (voice or DEFAULT_VOICE).strip()
    name = VOICE_ALIASES.get(name.lower(), name)
    if name not in SUPPORTED_VOICES:
        raise ValueError(f"voice {voice!r} is not available on this runtime")
    return name


def language_for_voice(voice: str) -> str:
    return LANGUAGE_BY_PREFIX[voice.split("_", 1)[0]]


def voice_for_language(language: str) -> str:
    """A representative advertised voice, used to warm each locale at startup."""
    return min(voice for voice in SUPPORTED_VOICES if language_for_voice(voice) == language)
