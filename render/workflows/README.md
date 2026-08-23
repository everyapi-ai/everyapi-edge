# Edge render workflow templates

Place operator-reviewed ComfyUI API workflow templates in this directory as JSON. Each file must declare `version: 1`, a stable `id`, the trusted `workflow`, typed buyer-facing `parameters`, and allow-listed `outputs`. The runtime mounts this directory read-only and never accepts workflow JSON from a buyer.
