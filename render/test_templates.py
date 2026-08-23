import json

import pytest

from templates import TemplateError, load_templates, parse_template


def valid_template():
    return {
        "id": "product-shot",
        "version": 1,
        "workflow": {"6": {"class_type": "CLIPTextEncode", "inputs": {"text": "default", "steps": 20}}, "9": {"class_type": "SaveImage", "inputs": {}}},
        "parameters": {
            "prompt": {"type": "string", "node": "6", "input": "text", "required": True, "max_length": 40},
            "steps": {"type": "integer", "node": "6", "input": "steps", "default": 20, "minimum": 1, "maximum": 50},
        },
        "outputs": [{"node": "9", "key": "images"}],
    }


def test_template_only_changes_declared_inputs():
    template = parse_template(valid_template())
    rendered = template.render({"prompt": "a mug", "steps": 30})
    assert rendered["6"]["inputs"] == {"text": "a mug", "steps": 30}
    assert template.workflow["6"]["inputs"] == {"text": "default", "steps": 20}


def test_template_rejects_unknown_and_invalid_parameters():
    template = parse_template(valid_template())
    with pytest.raises(TemplateError, match="unknown parameters"):
        template.render({"workflow": {"1": {}}, "prompt": "x"})
    with pytest.raises(TemplateError, match="outside its allowed range"):
        template.render({"prompt": "x", "steps": 51})
    with pytest.raises(TemplateError, match="too long"):
        template.render({"prompt": "x" * 41})


def test_template_rejects_invalid_targets_and_output_names():
    payload = valid_template()
    payload["parameters"]["prompt"]["node"] = "missing"
    with pytest.raises(TemplateError, match="invalid workflow input"):
        parse_template(payload)

    payload = valid_template()
    payload["outputs"] = [{"node": "../9", "key": "images"}]
    with pytest.raises(TemplateError, match="output definition"):
        parse_template(payload)


def test_loader_ignores_invalid_and_symlinked_templates(tmp_path):
    good = tmp_path / "good.json"
    good.write_text(json.dumps(valid_template()), encoding="utf-8")
    (tmp_path / "bad.json").write_text("{}", encoding="utf-8")
    (tmp_path / "linked.json").symlink_to(good)
    assert list(load_templates(tmp_path)) == ["product-shot"]
