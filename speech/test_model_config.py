import unittest

from model_config import (
    CONTENT_TYPES,
    DEFAULT_VOICE,
    LANGUAGE_BY_PREFIX,
    SAMPLE_RATE,
    SUPPORTED_RESPONSE_FORMATS,
    SUPPORTED_VOICES,
    VOICE_ALIASES,
    WARMUP_PHRASES,
    language_for_voice,
    resolve_voice,
    voice_for_language,
)


class VoiceContractTests(unittest.TestCase):
    def test_openai_stock_voice_names_resolve(self):
        for stock in ("alloy", "echo", "fable", "onyx", "nova", "shimmer"):
            self.assertIn(resolve_voice(stock), SUPPORTED_VOICES)

    def test_resolution_is_case_and_whitespace_tolerant(self):
        self.assertEqual(resolve_voice("  Alloy "), "af_alloy")

    def test_empty_voice_falls_back_to_the_default(self):
        self.assertEqual(resolve_voice(""), DEFAULT_VOICE)

    # Kokoro loads a voice by fetching `voices/<name>.pt` from the model repository, so an unchecked name is a buyer-controlled download path. The allow-list is the control; these are the shapes an attacker would try.
    def test_unknown_voices_are_rejected_before_reaching_the_downloader(self):
        for hostile in (
            "../../../etc/passwd",
            "af_alloy/../../config",
            "https://example.invalid/payload",
            "af_notreal",
        ):
            with self.assertRaises(ValueError):
                resolve_voice(hostile)

    def test_every_advertised_voice_has_a_language(self):
        for voice in SUPPORTED_VOICES:
            self.assertIn(language_for_voice(voice), set(LANGUAGE_BY_PREFIX.values()))

    def test_every_alias_targets_an_advertised_voice(self):
        for target in VOICE_ALIASES.values():
            self.assertIn(target, SUPPORTED_VOICES)

    # Startup warms one voice per advertised language to build that locale's G2P frontend. A language with no phrase would reach a buyer request cold.
    def test_every_advertised_language_has_a_warm_up_phrase(self):
        self.assertEqual(set(WARMUP_PHRASES), set(LANGUAGE_BY_PREFIX.values()))
        for language in WARMUP_PHRASES:
            voice = voice_for_language(language)
            self.assertIn(voice, SUPPORTED_VOICES)
            self.assertEqual(language_for_voice(voice), language)


class OutputFormatContractTests(unittest.TestCase):
    def test_every_supported_format_has_a_content_type(self):
        self.assertEqual(set(CONTENT_TYPES), set(SUPPORTED_RESPONSE_FORMATS))

    # The gateway's TTS handler bills raw PCM by dividing the byte count by 24000 * 2 * 1. Changing the rate here silently changes what buyers are charged.
    def test_pcm_sample_rate_matches_the_gateway_billing_assumption(self):
        self.assertEqual(SAMPLE_RATE, 24000)


if __name__ == "__main__":
    unittest.main()
