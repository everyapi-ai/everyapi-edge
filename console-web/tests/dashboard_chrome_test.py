import json
import os

from playwright.sync_api import sync_playwright

def test_console_opens_directly_with_dashboard_chrome() -> None:
    """The local console is available to its loopback host without a token."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")

        body_background = page.locator("body").evaluate("node => getComputedStyle(node).backgroundColor")
        heading_transform = page.locator("main h1").evaluate("node => getComputedStyle(node).textTransform")
        token_field_count = page.get_by_label("Local console token").count()

        browser.close()

    assert body_background == "rgb(8, 8, 11)"
    assert heading_transform == "none"
    assert token_field_count == 0


def test_supplier_ui_never_exposes_the_underlying_text_runtime_brand() -> None:
    """Local controls describe capabilities, not the implementation beneath them."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        model_copy = page.locator("main").inner_text()
        page.locator("aside").get_by_role("link", name="Playground").click()
        playground_copy = page.locator("main").inner_text()
        browser.close()

    assert "ollama" not in model_copy.lower()
    assert "ollama" not in playground_copy.lower()


def test_console_uses_compact_dashboard_navigation() -> None:
    """A local node needs usable navigation, not a terminal-style billboard."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        topbar_height = page.locator("header").evaluate("node => node.getBoundingClientRect().height")
        has_sidebar = page.locator("aside").count()
        heading_count = page.locator("h1").count()
        manage_nodes_count = page.get_by_role("link", name="Manage all nodes").count()

        browser.close()

    assert topbar_height == 48
    assert has_sidebar == 1
    assert heading_count == 1
    assert manage_nodes_count == 0


def test_sidebar_reports_the_real_gateway_session_state() -> None:
    """The local page being reachable must not masquerade as a connected node."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 24,
        "reserved_vram_bytes": 0,
        "available_vram_bytes": 0,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "offline",
        "gateway_last_error": "gateway refused the session",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        label = page.locator("aside").inner_text()
        browser.close()

    assert "Gateway disconnected" in label


def test_sidebar_distinguishes_a_local_preview_from_a_disconnected_node() -> None:
    """A LAN preview must not pretend to be retrying a production gateway."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 24,
        "reserved_vram_bytes": 0,
        "available_vram_bytes": 0,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "preview",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        label = page.locator("aside").inner_text()
        browser.close()

    assert "Local preview" in label


def test_overview_surfaces_node_readiness_and_model_management_actions() -> None:
    """The first screen makes the local node's usable state and next actions explicit."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 24,
        "reserved_vram_bytes": 4 * 1024 ** 3,
        "available_vram_bytes": 20 * 1024 ** 3,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "online",
    })
    models = json.dumps({"models": [{"name": "qwen3:8b", "size": 5 * 1024 ** 3}, {"name": "gemma3:4b", "size": 3 * 1024 ** 3}]})
    runtime = json.dumps({"version": "0.0.0", "models": [{"name": "qwen3:8b", "size_vram": 5 * 1024 ** 3}]})
    storage = json.dumps({"path": "/Users/example/.everyapi/edge", "accessible": True, "used_bytes": 0, "error": ""})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=models))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body=runtime))
        page.route("**/api/storage", lambda route: route.fulfill(status=200, content_type="application/json", body=storage))
        page.goto(base, wait_until="domcontentloaded")
        readiness = page.locator("[data-node-readiness]")
        readiness.wait_for()
        readiness_text = readiness.inner_text()
        page.get_by_role("button", name="Open model library").click()
        page.wait_for_selector('main h1:text-is("Model library")')
        browser.close()

    assert "Connected and ready for work" in readiness_text
    assert "2 installed" in readiness_text
    assert "1 loaded in memory" in readiness_text
    assert "Installed models are outside this Edge directory" in readiness_text


def test_every_console_route_has_a_dashboard_page_heading() -> None:
    """Each local task needs an explicit page context after the shell redesign."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")
    routes = [
        ("Overview", "Node workspace"),
        ("Local runtime", "Local runtime"),
        ("Models", "Model library"),
        ("Storage", "Storage & migration"),
        ("Traffic", "Recent traffic"),
        ("Logs", "Agent logs"),
    ]

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")

        navigation = page.locator("aside")
        for label, heading in routes:
            navigation.get_by_role("link", name=label).click()
            page.wait_for_timeout(120)
            assert page.locator("main h1").inner_text() == heading

        browser.close()


def test_mobile_navigation_remains_named() -> None:
    """Mobile uses the Dashboard drawer, keeping navigation fully named."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 390, "height": 844})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.get_by_role("button", name="Open navigation").click()
        mobile_navigation = page.locator("[role='dialog'] nav")
        assert mobile_navigation.get_by_role("link", name="Overview").count() == 1
        assert mobile_navigation.get_by_role("link", name="Models").count() == 1
        assert mobile_navigation.get_by_role("link", name="Traffic").count() == 1
        assert mobile_navigation.get_by_role("link", name="Logs").count() == 1
        browser.close()


def test_navigation_uses_dashboard_app_shell_dimensions() -> None:
    """Navigation follows the same desktop grid and mobile drawer model as Dashboard."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        desktop = browser.new_page(viewport={"width": 1440, "height": 960})
        desktop.goto(base, wait_until="domcontentloaded")
        desktop.wait_for_selector("main h1")
        shell_display = desktop.locator("#root > div").evaluate("node => getComputedStyle(node).display")
        sidebar_width = desktop.locator("aside").evaluate("node => node.getBoundingClientRect().width")
        overview_height = desktop.locator("aside").get_by_role("link", name="Overview").evaluate(
            "node => node.getBoundingClientRect().height"
        )

        browser.close()

    assert shell_display == "grid"
    assert sidebar_width == 232
    assert overview_height >= 36


def test_chinese_navigation_uses_four_character_labels() -> None:
    """The compact Chinese sidebar has a consistent four-character rhythm."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("#console-locale").select_option("zh")
        labels = page.locator("aside nav a").all_inner_texts()
        browser.close()

    assert labels == ["节点总览", "本地推理", "模型管理", "本地对话", "图像编辑", "存储迁移", "请求流量", "运行日志"]


def test_storage_page_offers_a_native_folder_picker() -> None:
    """Migration asks the local Edge agent to show its native folder dialog."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Storage").click()
        page.wait_for_selector('main h1:text-is("Storage & migration")')

        picker = page.locator('[data-native-storage-picker]')
        picker.first.wait_for()
        folder_picker_count = picker.count()
        uses_agent_picker = picker.evaluate_all("nodes => nodes.every(node => node.tagName === 'BUTTON')")

        browser.close()

    assert folder_picker_count == 2
    assert uses_agent_picker


def test_storage_page_surfaces_an_import_action_for_models_outside_edge_storage() -> None:
    """An empty Edge directory with installed models needs an obvious import path."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    destination = "/Users/example/.everyapi/edge"

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/storage", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": destination, "accessible": True, "used_bytes": 0, "error": ""})))
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"models": [{"name": "gemma3:27b", "size": 17 * 1024 ** 3}]})))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Storage").click()
        action = page.locator("[data-import-existing-models]")
        action.wait_for(timeout=2_000)
        action_text = action.inner_text()
        browser.close()

    assert action_text == "Import existing library"


def test_storage_page_can_start_a_safe_model_copy_after_preflight() -> None:
    """A ready migration plan must expose the action that actually transfers models."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    source = "/Users/example/.everyapi/edge"
    destination = "/Volumes/models/everyapi"
    plan = json.dumps({
        "source": {"path": source, "accessible": True, "used_bytes": 1024, "error": ""},
        "destination": {"path": destination, "accessible": True, "used_bytes": 0, "error": ""},
        "ready": True,
        "blockers": [],
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/storage", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": source, "accessible": True, "used_bytes": 1024, "error": ""})))
        page.route("**/api/storage/plan", lambda route: route.fulfill(status=200, content_type="application/json", body=plan))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Storage").click()
        page.get_by_label("New model directory").fill(destination)
        page.get_by_role("button", name="Prepare migration").click()
        copy = page.locator("[data-start-storage-migration]")
        copy.wait_for()
        copy_count = copy.count()
        browser.close()

    assert copy_count == 1


def test_storage_migration_can_import_a_user_selected_existing_library() -> None:
    """A model library outside the current Edge directory can be selected as the copy source."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    source = "/Volumes/legacy-models"
    destination = "/Users/example/.everyapi/edge"
    plan = json.dumps({
        "source": {"path": source, "accessible": True, "used_bytes": 1024, "error": ""},
        "destination": {"path": destination, "accessible": True, "used_bytes": 0, "error": ""},
        "ready": True,
        "blockers": [],
    })
    request_body = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/storage", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": destination, "accessible": True, "used_bytes": 0, "error": ""})))
        def preflight(route):
            request_body.append(route.request.post_data_json)
            route.fulfill(status=200, content_type="application/json", body=plan)
        page.route("**/api/storage/plan", preflight)
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Storage").click()
        page.get_by_label("Existing model directory").fill(source)
        page.get_by_label("New model directory").fill(destination)
        page.get_by_role("button", name="Prepare migration").click()
        page.locator("[data-start-storage-migration]").wait_for()
        browser.close()

    assert request_body == [{"source": source, "destination": destination}]


def test_storage_source_picker_defaults_the_destination_to_edge_storage() -> None:
    """Importing an existing library should need one folder choice, not two."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    source = "/Volumes/legacy-models"
    destination = "/Users/example/.everyapi/edge"

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/storage", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": destination, "accessible": True, "used_bytes": 0, "error": ""})))
        page.route("**/api/storage/pick", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": source})))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Storage").click()
        page.locator('[data-native-storage-picker="source"]').click()
        page.wait_for_function(f"() => document.querySelector('#storage-source').value === {source!r}")
        selected_destination = page.get_by_label("New model directory").input_value()
        browser.close()

    assert selected_destination == destination


def test_local_playground_is_available_from_navigation() -> None:
    """Suppliers can open a local chat playground from the console shell."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Playground").click()
        page.wait_for_selector('main h1:text-is("Local playground")')
        composer = page.locator('textarea[name="playground-message"]')
        composer.wait_for()
        composer_count = composer.count()

        browser.close()

    assert composer_count == 1


def test_local_playground_exposes_open_webui_style_chat_controls() -> None:
    """A local chat exposes the controls needed to test model behavior."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Playground").click()
        page.get_by_label("System prompt").wait_for()
        temperature = page.get_by_role("slider", name="Temperature")
        temperature.wait_for()
        controls = [page.get_by_label("System prompt").count(), temperature.count()]
        browser.close()

    assert controls == [1, 1]


def test_local_playground_accepts_an_image_attachment() -> None:
    """Multimodal models need a real local file picker in the chat composer."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models/capabilities*", lambda route: route.fulfill(status=200, content_type="application/json", body='{"model":"llama3.1:8b","capabilities":["completion","vision"]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Playground").click()
        attachment = page.get_by_label("Attach image")
        attachment.wait_for()
        attachment_count = attachment.count()
        attachment_enabled = not attachment.is_disabled()
        attachment.set_input_files({"name": "cat.png", "mimeType": "image/png", "buffer": b"\x89PNG\r\n\x1a\n"})
        page.get_by_text("cat.png", exact=True).wait_for()
        browser.close()

    assert attachment_count == 1
    assert attachment_enabled


def test_local_playground_disables_image_attachment_for_a_text_only_model() -> None:
    """The composer must reject image input before a text-only runtime sees it."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models/capabilities*", lambda route: route.fulfill(status=200, content_type="application/json", body='{"model":"llama3.1:8b","capabilities":["completion","tools"]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Playground").click()
        attachment = page.get_by_label("Attach image")
        attachment.wait_for()
        disabled = attachment.is_disabled()
        capability = page.locator("[data-model-capability]")
        capability.wait_for()
        capability_text = capability.inner_text()
        browser.close()

    assert disabled
    assert capability_text == "Text model · Tools"


def test_local_playground_can_stop_an_in_progress_generation() -> None:
    """A supplier must be able to abort a local stream without reloading the chat."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.add_init_script("""
          (() => {
            const nativeFetch = window.fetch
            window.fetch = (input, init) => {
              const url = typeof input === 'string' ? input : input.url
              if (!url.includes('/api/playground/chat')) return nativeFetch(input, init)
              return new Promise((_resolve, reject) => {
                init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true })
              })
            }
          })()
        """)
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Playground").click()
        composer = page.locator('textarea[name="playground-message"]')
        composer.wait_for()
        composer.fill("Keep generating")
        page.get_by_role("button", name="Send").click()
        stop = page.get_by_role("button", name="Stop generating")
        stop.wait_for(timeout=3_000)
        stop.click()
        page.get_by_role("button", name="Send").wait_for(timeout=3_000)
        browser.close()


def test_image_editing_playground_accepts_a_real_image_file() -> None:
    """Diffusers image editing is a local tool with a real file input, not a text-only Ollama prompt."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Image edit").click()
        file_input = page.get_by_label("Source image")
        file_input.wait_for()
        prompt = page.get_by_label("Edit instruction")
        prompt.wait_for()
        file_count = file_input.count()
        prompt_count = prompt.count()
        browser.close()

    assert file_count == 1
    assert prompt_count == 1


def test_runtime_page_reports_the_current_ollama_residency_state() -> None:
    """Runtime stays useful whether Ollama currently has a resident model or not."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Local runtime").click()
        page.wait_for_function("""() =>
            [...document.querySelectorAll('button')].some((button) => button.textContent?.includes('Unload')) ||
            document.body.innerText.includes('No model is loaded in memory right now.')
        """)
        unload_count = page.get_by_role("button", name="Unload").count()
        empty_count = page.get_by_text("No model is loaded in memory right now.").count()
        browser.close()

    assert unload_count >= 1 or empty_count == 1


def test_model_library_uses_a_catalog_not_a_freeform_model_name_input() -> None:
    """Downloading a model starts from known, runnable catalog choices."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Model").wait_for()
        catalog_count = page.get_by_label("Model").count()
        freeform_count = page.locator("#model-name").count()
        browser.close()

    assert catalog_count == 1
    assert freeform_count == 0


def test_model_catalog_groups_choices_by_provider_and_shows_model_type() -> None:
    """Provider, workload type, and model selection are separate."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Provider").wait_for()
        providers = page.get_by_label("Provider").locator("option").all_text_contents()
        page.get_by_label("Type").wait_for()
        types = page.get_by_label("Type").locator("option").all_text_contents()
        type_hint = page.locator("[data-model-type]").inner_text()
        browser.close()

    assert "Alibaba / Qwen" in providers
    assert "Meta" in providers
    assert "Chat" in types
    assert "Chat" in type_hint


def test_model_catalog_calls_image_capable_models_multimodal() -> None:
    """The type label describes the full image-and-text capability, not only vision."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Type").select_option(label="Multimodal")
        option_text = page.locator('#model-catalog option[value="qwen3-vl:4b"]').inner_text()
        browser.close()

    assert "Multimodal" in option_text


def test_model_catalog_filters_models_by_provider_and_type() -> None:
    """The final selector only contains models from the chosen provider and type."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Provider").select_option(label="DeepSeek")
        page.wait_for_function("() => document.querySelector('#model-provider').value === 'DeepSeek'")
        page.get_by_label("Type").select_option(label="Code")
        page.wait_for_function("""() =>
            document.querySelector('#model-type').value === 'code' &&
            [...document.querySelectorAll('#model-catalog option')].every((option) => option.textContent.includes('Code'))
        """)
        models = page.get_by_label("Model").locator("option").all_text_contents()
        browser.close()

    assert models == ["deepseek-coder-v2:16b · Code · requires ≥ 14 GB", "deepseek-coder-v2:236b · Code · requires ≥ 160 GB"]


def test_model_type_selector_uses_a_compact_control_width() -> None:
    """The short type selector must not take the same space as the model selector."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        provider_width = page.get_by_label("Provider").evaluate("node => node.getBoundingClientRect().width")
        type_width = page.get_by_label("Type").evaluate("node => node.getBoundingClientRect().width")
        model_width = page.get_by_label("Model").evaluate("node => node.getBoundingClientRect().width")
        browser.close()

    assert provider_width >= 148
    assert type_width <= 96
    assert model_width >= 300


def test_installed_model_library_shows_catalog_provider_and_multimodal_type() -> None:
    """Installed models retain provider and capability metadata instead of name-only guesses."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    installed = json.dumps({
        "models": [{
            "name": "gemma3:27b",
            "size": 17 * 1024 ** 3,
            "details": {"parameter_size": "27.4B", "quantization_level": "Q4_K_M"},
        }],
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=installed))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"version":"0.0.0","models":[]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        row = page.locator('[data-installed-model="gemma3:27b"]')
        row.wait_for()
        cells = row.locator("td").all_inner_texts()
        headings = page.locator("main table thead th").all_inner_texts()
        browser.close()

    assert "Provider" in headings
    assert cells[:4] == ["Google", "gemma3:27b", "Installed", "Multimodal"]


def test_installed_model_can_open_the_playground_and_shows_residency() -> None:
    """The library gives each installed model a direct use path and live memory state."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    installed = json.dumps({"models": [{"name": "gemma3:27b", "size": 17 * 1024 ** 3, "details": {}}]})
    runtime = json.dumps({
        "version": "0.0.0",
        "models": [{"name": "gemma3:27b", "size_vram": 12 * 1024 ** 3, "context_length": 8192}],
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=installed))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body=runtime))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        row = page.locator('[data-installed-model="gemma3:27b"]')
        row.wait_for()
        residency = row.locator('[data-model-residency]').inner_text()
        unload_count = row.get_by_role("button", name="Unload").count()
        remove_disabled = row.get_by_role("button", name="Remove").is_disabled()
        row.get_by_role("button", name="Open in playground").click()
        page.wait_for_selector('main h1:text-is("Local playground")')
        selected = page.locator('main select').first.input_value()
        browser.close()

    assert residency == "Loaded in memory"
    assert unload_count == 1
    assert remove_disabled
    assert selected == "gemma3:27b"


def test_model_catalog_reuses_an_installed_selection_instead_of_downloading_again() -> None:
    """An installed catalog model offers a direct use action rather than another pull."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    installed = json.dumps({"models": [{"name": "qwen3:32b", "size": 20 * 1024 ** 3, "details": {}}]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=installed))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        option_text = page.locator('#model-catalog option[value="qwen3:32b"]').inner_text()
        action = page.locator("[data-model-catalog-action]")
        action.wait_for()
        action_text = action.inner_text()
        action.click()
        page.wait_for_selector('main h1:text-is("Local playground")')
        browser.close()

    assert "Installed" in option_text
    assert action_text == "Open in playground"


def test_qwen_image_edit_models_are_visible_but_not_offered_to_ollama() -> None:
    """Diffusion image-edit weights remain discoverable without pretending Ollama can pull them."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Provider").select_option(label="Alibaba / Qwen")
        page.get_by_label("Type").select_option(label="Image generation")
        model = page.locator('#model-catalog option[value="Qwen/Qwen-Image-Edit-2511"]')
        model.wait_for(state="attached")
        is_disabled = model.is_disabled()
        browser.close()

    assert is_disabled


def test_model_catalog_uses_the_image_runtime_for_qwen_image_editors() -> None:
    """Qwen image editors are activated locally, never sent to the text-model download API."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Type").select_option("image")
        action = page.locator("[data-select-image-model]")
        action_count = action.count()
        browser.close()

    assert action_count == 1


def test_model_download_queue_exposes_cancellation_for_each_job() -> None:
    """A queued large download must be removable without waiting for the active transfer."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    queue = json.dumps({
        "active": {"name": "qwen3:32b", "status": "downloading", "completed": 3, "total": 10, "error": "", "done": False},
        "queued": [{"name": "gemma3:27b", "status": "queued", "completed": 0, "total": 0, "error": "", "done": False}],
        "latest": None,
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models/pull", lambda route: route.fulfill(status=200, content_type="application/json", body=queue))
        page.goto(base, wait_until="networkidle")
        page.locator("aside").get_by_role("link", name="Models").click()
        cancel = page.locator('[data-cancel-download="gemma3:27b"]')
        page.wait_for_timeout(100)
        cancel_count = cancel.count()
        browser.close()

    assert cancel_count == 1


def test_model_catalog_disables_models_over_the_detected_memory_budget() -> None:
    """Large models remain visible but cannot be downloaded on an undersized node."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Provider").select_option(label="Meta")
        oversized = page.locator('#model-catalog option[value="llama3.3:70b"]')
        oversized.wait_for(state="attached")
        is_disabled = oversized.is_disabled()
        browser.close()

    assert is_disabled


def test_model_catalog_reserves_capacity_for_models_already_in_memory() -> None:
    """A model that fits the device total is disabled when loaded models use its budget."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route(
            "**/api/overview",
            lambda route: route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps({
                    "active_requests": 0,
                    "completed_requests": 0,
                    "failed_requests": 0,
                    "prompt_tokens": 0,
                    "completion_tokens": 0,
                    "loaded_vram_bytes": 20 * 1024 ** 3,
                    "vram_total_gb": 48,
                    "reserved_vram_bytes": 10 * 1024 ** 3,
                    "available_vram_bytes": 18 * 1024 ** 3,
                    "settled_earnings_micros": 0,
                    "settled_earnings_available": False,
                }),
            ),
        )
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.locator("[data-model-capacity]").wait_for()
        capacity_text = page.locator("[data-model-capacity]").inner_text()
        model = page.locator('#model-catalog option[value="qwen3:32b"]')
        model.wait_for(state="attached")
        is_disabled = model.is_disabled()
        browser.close()

    assert "18 GB available" in capacity_text
    assert is_disabled


def test_model_catalog_disables_all_pulls_when_loaded_models_exhaust_capacity() -> None:
    """No download choice is presented as runnable after the live budget reaches zero."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route(
            "**/api/overview",
            lambda route: route.fulfill(
                status=200,
                content_type="application/json",
                body=json.dumps({
                    "active_requests": 0,
                    "completed_requests": 0,
                    "failed_requests": 0,
                    "prompt_tokens": 0,
                    "completion_tokens": 0,
                    "loaded_vram_bytes": 44 * 1024 ** 3,
                    "vram_total_gb": 48,
                    "reserved_vram_bytes": 4 * 1024 ** 3,
                    "available_vram_bytes": 0,
                    "settled_earnings_micros": 0,
                    "settled_earnings_available": False,
                }),
            ),
        )
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.locator("[data-model-capacity]").wait_for()
        smallest_model = page.locator('#model-catalog option[value="qwen2.5:0.5b"]')
        smallest_model.wait_for(state="attached")
        is_disabled = smallest_model.is_disabled()
        pull_disabled = page.get_by_role("button", name="Download model").is_disabled()
        browser.close()

    assert is_disabled
    assert pull_disabled
