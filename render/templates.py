import copy
import json
import math
import re
from dataclasses import dataclass
from pathlib import Path

TEMPLATE_ID = re.compile(r"^[a-z][a-z0-9_-]{0,63}$")
PARAMETER_ID = re.compile(r"^[A-Za-z][A-Za-z0-9_]{0,63}$")
WORKFLOW_KEY = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
MAX_TEMPLATE_BYTES = 1 << 20
MAX_TEMPLATES = 32


class TemplateError(ValueError):
    pass


@dataclass(frozen=True)
class Template:
    id: str
    workflow: dict
    parameters: dict
    outputs: list

    def render(self, supplied: dict) -> dict:
        unknown = set(supplied) - set(self.parameters)
        if unknown:
            raise TemplateError(f"unknown parameters: {', '.join(sorted(unknown))}")
        rendered = copy.deepcopy(self.workflow)
        for name, definition in self.parameters.items():
            required = bool(definition.get("required", False))
            value = supplied.get(name, definition.get("default"))
            if value is None:
                if required:
                    raise TemplateError(f"parameter {name} is required")
                continue
            value = validate_value(name, definition, value)
            node_id = definition["node"]
            input_name = definition["input"]
            try:
                rendered[node_id]["inputs"][input_name] = value
            except (KeyError, TypeError) as error:
                raise TemplateError(f"template parameter {name} targets an invalid workflow input") from error
        return rendered


def validate_value(name: str, definition: dict, value):
    kind = definition.get("type")
    if kind == "string":
        if not isinstance(value, str):
            raise TemplateError(f"parameter {name} must be a string")
        if len(value) > int(definition.get("max_length", 2000)):
            raise TemplateError(f"parameter {name} is too long")
        if "\x00" in value:
            raise TemplateError(f"parameter {name} contains invalid characters")
    elif kind == "integer":
        if isinstance(value, bool) or not isinstance(value, int):
            raise TemplateError(f"parameter {name} must be an integer")
        if value < int(definition.get("minimum", -2147483648)) or value > int(definition.get("maximum", 2147483647)):
            raise TemplateError(f"parameter {name} is outside its allowed range")
    elif kind == "number":
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TemplateError(f"parameter {name} must be a number")
        if not math.isfinite(float(value)):
            raise TemplateError(f"parameter {name} must be finite")
        if float(value) < float(definition.get("minimum", -1e9)) or float(value) > float(definition.get("maximum", 1e9)):
            raise TemplateError(f"parameter {name} is outside its allowed range")
    elif kind == "boolean":
        if not isinstance(value, bool):
            raise TemplateError(f"parameter {name} must be a boolean")
    elif kind == "enum":
        if value not in definition.get("values", []):
            raise TemplateError(f"parameter {name} is not an allowed value")
    else:
        raise TemplateError(f"parameter {name} has an unsupported type")
    return value


def load_templates(root: Path) -> dict[str, Template]:
    templates = {}
    if not root.is_dir():
        return templates
    for path in sorted(root.glob("*.json")):
        if len(templates) == MAX_TEMPLATES:
            break
        try:
            if path.is_symlink() or path.stat().st_size > MAX_TEMPLATE_BYTES:
                continue
            payload = json.loads(path.read_text(encoding="utf-8"))
            template = parse_template(payload)
        except (OSError, UnicodeError, json.JSONDecodeError, TemplateError):
            continue
        if template.id in templates:
            continue
        templates[template.id] = template
    return templates


def parse_template(payload: dict) -> Template:
    if not isinstance(payload, dict) or set(payload) - {"id", "version", "workflow", "parameters", "outputs"}:
        raise TemplateError("template has unknown top-level fields")
    template_id = payload.get("id")
    if not isinstance(template_id, str) or not TEMPLATE_ID.fullmatch(template_id):
        raise TemplateError("template id is invalid")
    workflow = payload.get("workflow")
    parameters = payload.get("parameters", {})
    outputs = payload.get("outputs", [])
    if payload.get("version") != 1 or not isinstance(workflow, dict) or not workflow or not isinstance(parameters, dict) or not isinstance(outputs, list) or not outputs:
        raise TemplateError("template structure is invalid")
    for name, definition in parameters.items():
        if not PARAMETER_ID.fullmatch(name) or not isinstance(definition, dict) or set(definition) - {"type", "node", "input", "required", "default", "minimum", "maximum", "max_length", "values"}:
            raise TemplateError("template parameter definition is invalid")
        if not isinstance(definition.get("node"), str) or not isinstance(definition.get("input"), str):
            raise TemplateError("template parameter target is invalid")
        node = workflow.get(definition["node"])
        if not isinstance(node, dict) or not isinstance(node.get("inputs"), dict) or definition["input"] not in node["inputs"]:
            raise TemplateError(f"template parameter {name} targets an invalid workflow input")
        validate_value(name, definition, definition["default"]) if "default" in definition else None
    for output in outputs:
        if not isinstance(output, dict) or set(output) != {"node", "key"} or not isinstance(output["node"], str) or not isinstance(output["key"], str) or not WORKFLOW_KEY.fullmatch(output["node"]) or not WORKFLOW_KEY.fullmatch(output["key"]) or output["node"] not in workflow:
            raise TemplateError("template output definition is invalid")
    return Template(template_id, workflow, parameters, outputs)
