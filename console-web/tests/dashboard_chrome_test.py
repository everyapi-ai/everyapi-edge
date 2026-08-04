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

    assert body_background == "rgb(11, 12, 10)"
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


def test_console_groups_navigation_by_operator_intent() -> None:
    """The rail mirrors an operator's workflow instead of presenting one flat menu."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="networkidle")
        navigation = page.locator("aside nav")
        groups = navigation.locator("[data-navigation-group]")
        labels = groups.locator("[data-navigation-group-label]").all_inner_texts()
        group_count = groups.count()
        browser.close()

    assert group_count == 3
    assert labels == ["OPERATE", "MAINTAIN", "OBSERVE"]


def test_command_center_looks_and_behaves_like_a_system_instrument() -> None:
    """The first screen leads with readiness and capacity, not equal-weight SaaS cards."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    overview = json.dumps({
        "active_requests": 2,
        "completed_requests": 1842,
        "failed_requests": 7,
        "prompt_tokens": 927114,
        "completion_tokens": 384221,
        "loaded_vram_bytes": 9 * 1024 ** 3,
        "vram_total_gb": 24,
        "reserved_vram_bytes": 4 * 1024 ** 3,
        "available_vram_bytes": 11 * 1024 ** 3,
        "settled_earnings_micros": 42870000,
        "settled_earnings_available": True,
        "gateway_state": "online",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.goto(base, wait_until="networkidle")
        status_rail = page.locator("[data-system-status-rail]")
        capacity = page.locator("[data-capacity-rail]")
        command_center = page.locator("[data-command-center]")
        accent = page.locator("html").evaluate("node => getComputedStyle(node).getPropertyValue('--color-accent').trim()")
        status_rail_count = status_rail.count()
        capacity_count = capacity.count()
        capacity_text = capacity.inner_text() if capacity_count else ""
        command_center_count = command_center.count()
        browser.close()

    assert status_rail_count == 1
    assert capacity_count == 1
    assert "9.0 GB" in capacity_text
    assert "24 GB" in capacity_text
    assert command_center_count == 1
    assert accent == "#ff6b2c"


def test_mobile_console_keeps_live_node_status_in_view() -> None:
    """A phone operator sees gateway and capacity state without opening navigation."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 390, "height": 844})
        page.goto(base, wait_until="networkidle")
        mobile_status = page.locator("[data-mobile-system-status]")
        mobile_status_count = mobile_status.count()
        browser.close()

    assert mobile_status_count == 1


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


def test_gateway_session_reports_the_real_scheduled_retry() -> None:
    """A disconnect message must show the actual retry plan, not a generic promise."""
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
        "gateway_reconnect_attempt": 3,
        "gateway_next_reconnect_at": "2026-08-03T10:30:00Z",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.goto(base, wait_until="domcontentloaded")
        retry = page.locator("[data-gateway-reconnect]")
        retry.wait_for(timeout=2_000)
        retry_text = retry.inner_text()
        sidebar_text = page.locator("aside").inner_text()
        browser.close()

    assert "Reconnect attempt 3" in retry_text
    assert "Next retry" in retry_text
    assert "Reconnect attempt 3" in sidebar_text


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


def test_overview_identifies_the_actual_local_node_and_hardware() -> None:
    """A local control room must say which configured Edge machine it represents."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    profile = json.dumps({
        "name": "studio-gpu",
        "agent_version": "v1.2.3",
        "gpu_model": "RTX 4090",
        "platform": "linux/amd64",
        "country_iso2": "JP",
        "vram_total_gb": 24,
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/node", lambda route: route.fulfill(status=200, content_type="application/json", body=profile))
        page.goto(base, wait_until="domcontentloaded")
        node_profile = page.locator("[data-node-profile]")
        node_profile.wait_for(timeout=2_000)
        profile_text = node_profile.inner_text()
        browser.close()

    assert "studio-gpu" in profile_text
    assert "RTX 4090" in profile_text
    assert "24 GB" in profile_text
    assert "v1.2.3" in profile_text


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


def test_agent_logs_filter_by_level_and_message() -> None:
    """Connection failures stay findable when the bounded local log has many entries."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    logs = json.dumps([
        {"at": "2026-08-03T10:00:00Z", "level": "info", "message": "gateway connected"},
        {"at": "2026-08-03T10:00:01Z", "level": "error", "message": "gateway handshake timed out"},
        {"at": "2026-08-03T10:00:02Z", "level": "warn", "message": "model discovery was delayed"},
    ])

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/logs", lambda route: route.fulfill(status=200, content_type="application/json", body=logs))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Logs").click()
        page.locator("[data-log-entry]").first.wait_for(timeout=2_000)
        page.get_by_label("Log level").select_option("error")
        level_matches = page.locator("[data-log-entry]").all_inner_texts()
        page.get_by_label("Search logs").fill("handshake")
        filtered_matches = page.locator("[data-log-entry]").all_inner_texts()
        browser.close()

    assert len(level_matches) == 1
    assert "handshake timed out" in level_matches[0]
    assert len(filtered_matches) == 1
    assert "gateway handshake timed out" in filtered_matches[0]


def test_recent_traffic_filters_failed_requests_by_model_and_metadata() -> None:
    """The traffic audit stays useful without revealing request bodies."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    requests = json.dumps([
        {"id": "request-1", "model": "llama3.1:8b", "path": "/api/chat", "consumer": "gateway customer", "started_at": "2026-08-03T10:00:00Z", "completed_at": "2026-08-03T10:00:01Z", "duration_ms": 1000, "prompt_tokens": 5, "completion_tokens": 3},
        {"id": "request-2", "model": "qwen3:8b", "path": "/api/chat", "consumer": "gateway customer", "started_at": "2026-08-03T10:01:00Z", "completed_at": "2026-08-03T10:01:01Z", "duration_ms": 1000, "prompt_tokens": 5, "completion_tokens": 0, "error": "upstream timed out"},
        {"id": "request-3", "model": "qwen3:8b", "path": "/api/embeddings", "consumer": "gateway customer", "started_at": "2026-08-03T10:02:00Z", "completed_at": "2026-08-03T10:02:01Z", "duration_ms": 1000, "prompt_tokens": 5, "completion_tokens": 3},
    ])

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/requests", lambda route: route.fulfill(status=200, content_type="application/json", body=requests))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Traffic").click()
        page.locator("[data-traffic-row]").first.wait_for(timeout=2_000)
        page.get_by_label("Traffic model").select_option("qwen3:8b")
        page.get_by_label("Traffic result").select_option("error")
        failed_matches = page.locator("[data-traffic-row]").all_inner_texts()
        page.get_by_label("Search traffic").fill("timed out")
        filtered_matches = page.locator("[data-traffic-row]").all_inner_texts()
        browser.close()

    assert len(failed_matches) == 1
    assert "upstream timed out" in failed_matches[0]
    assert len(filtered_matches) == 1
    assert "qwen3:8b" in filtered_matches[0]


def test_mobile_navigation_is_a_keyboard_accessible_modal() -> None:
    """The mobile drawer owns focus, closes with Escape, and restores the trigger."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 390, "height": 844})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        trigger = page.get_by_role("button", name="Open navigation")
        trigger.click()
        dialog = page.get_by_role("dialog")
        mobile_navigation = dialog.locator("nav")
        assert mobile_navigation.get_by_role("link", name="Overview").count() == 1
        assert mobile_navigation.get_by_role("link", name="Models").count() == 1
        assert mobile_navigation.get_by_role("link", name="Traffic").count() == 1
        assert mobile_navigation.get_by_role("link", name="Logs").count() == 1
        assert dialog.locator(":focus").count() == 1
        page.keyboard.press("Escape")
        assert dialog.count() == 0
        assert trigger.evaluate("node => node === document.activeElement")
        browser.close()


def test_mobile_status_distinguishes_offline_from_connecting() -> None:
    """An offline gateway must not be announced as an in-progress connection."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 16,
        "reserved_vram_bytes": 0,
        "available_vram_bytes": 0,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "offline",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 390, "height": 844})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.goto(base, wait_until="networkidle")
        status = page.locator("[data-mobile-system-status]").inner_text()
        browser.close()

    assert status == "GATEWAY DISCONNECTED"


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
    assert sidebar_width == 252
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
        labels = page.locator("aside nav a > span.flex-1").all_inner_texts()
        browser.close()

    assert labels == ["节点总览", "本地推理", "模型管理", "本地对话", "图像编辑", "存储迁移", "请求流量", "运行日志"]


def test_storage_page_offers_a_native_folder_picker() -> None:
    """Migration uses a native directory choice rather than raw path fields."""
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
        raw_path_input_count = page.locator('#storage-source, #storage-destination').count()

        browser.close()

    assert folder_picker_count == 2
    assert uses_agent_picker
    assert raw_path_input_count == 0


def test_storage_page_reports_real_filesystem_capacity() -> None:
    """Storage management distinguishes model bytes from free space on the backing disk."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    storage = json.dumps({
        "path": "/Users/example/.everyapi/edge",
        "accessible": True,
        "used_bytes": 5 * 1024 ** 3,
        "total_bytes": 100 * 1024 ** 3,
        "available_bytes": 60 * 1024 ** 3,
        "error": "",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/storage", lambda route: route.fulfill(status=200, content_type="application/json", body=storage))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Storage").click()
        capacity = page.locator("[data-storage-capacity]")
        capacity.wait_for(timeout=2_000)
        capacity_text = capacity.inner_text()
        browser.close()

    assert "Disk capacity" in capacity_text
    assert "Available on disk" in capacity_text
    assert "64.4 GB" in capacity_text


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
        page.route("**/api/storage/pick", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": destination})))
        page.locator('[data-native-storage-picker="destination"]').click()
        page.wait_for_function(f"() => document.querySelector('[data-storage-destination]').textContent === {destination!r}")
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
        page.route("**/api/storage/pick", lambda route: route.fulfill(status=200, content_type="application/json", body=json.dumps({"path": source})))
        page.locator('[data-native-storage-picker="source"]').click()
        page.locator('[data-storage-source]').wait_for()
        page.wait_for_function(f"() => document.querySelector('[data-storage-source]').textContent === {source!r}")
        page.wait_for_function(f"() => document.querySelector('[data-storage-destination]').textContent === {destination!r}")
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
        page.wait_for_function(f"() => document.querySelector('[data-storage-source]').textContent === {source!r}")
        selected_destination = page.locator('[data-storage-destination]').text_content()
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


def test_local_playground_keeps_private_browser_conversation_history() -> None:
    """Chat sessions survive a reload without sending their contents to Edge."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    history = json.dumps({
        "version": 1,
        "active_id": "saved-session",
        "conversations": [{
            "id": "saved-session",
            "title": "Plan an offline workflow",
            "model": "llama3.1:8b",
            "system": "",
            "temperature": 0.7,
            "messages": [
                {"role": "user", "content": "Keep this conversation on this browser."},
                {"role": "assistant", "content": "It stays local."},
            ],
        }],
    })
    models = json.dumps({"models": [{"name": "llama3.1:8b", "size": 5 * 1024 ** 3}]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.add_init_script("localStorage.setItem('everyapi.edge.playground.v1', " + json.dumps(history) + ");")
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=models))
        page.goto(base + "/playground", wait_until="domcontentloaded")
        page.locator("[data-playground-history]").wait_for(timeout=2_000)
        saved = page.get_by_role("button", name="Plan an offline workflow", exact=True)
        saved.wait_for()
        transcript = page.locator("[data-playground-transcript]").inner_text()
        page.get_by_role("button", name="New conversation").click()
        count = page.locator("[data-playground-session]").count()
        page.get_by_role("button", name="Delete New conversation", exact=True).click()
        count_after_delete = page.locator("[data-playground-session]").count()
        browser.close()

    assert "Keep this conversation on this browser." in transcript
    assert count == 2
    assert count_after_delete == 1


def test_local_playground_exports_a_conversation_without_uploading_it() -> None:
    """A saved local discussion can leave the browser as a Markdown file."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    history = json.dumps({
        "version": 1,
        "active_id": "saved-session",
        "conversations": [{
            "id": "saved-session",
            "title": "Offline notes",
            "model": "llama3.1:8b",
            "system": "",
            "temperature": 0.7,
            "messages": [{"role": "user", "content": "Export this locally."}],
        }],
    })
    models = json.dumps({"models": [{"name": "llama3.1:8b", "size": 5 * 1024 ** 3}]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.add_init_script("localStorage.setItem('everyapi.edge.playground.v1', " + json.dumps(history) + ");")
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=models))
        page.goto(base + "/playground", wait_until="domcontentloaded")
        with page.expect_download() as downloaded:
            page.get_by_role("button", name="Export conversation").click()
        filename = downloaded.value.suggested_filename
        browser.close()

    assert filename == "everyapi-local-conversation.md"


def test_local_playground_persists_new_completed_conversations_across_reload() -> None:
    """A completed local reply is written back to browser-only history."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    models = json.dumps({"models": [{"name": "llama3.1:8b", "size": 5 * 1024 ** 3}]})
    stream = "data: {\"type\":\"delta\",\"content\":\"Stored locally.\"}\n\ndata: {\"type\":\"done\",\"model\":\"llama3.1:8b\",\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\n"

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=models))
        page.route("**/api/playground/chat", lambda route: route.fulfill(status=200, content_type="text/event-stream", body=stream))
        page.goto(base + "/playground", wait_until="domcontentloaded")
        composer = page.locator('textarea[name="playground-message"]')
        composer.wait_for()
        composer.fill("Remember this reply")
        page.get_by_role("button", name="Send").click()
        page.get_by_text("Stored locally.", exact=True).wait_for(timeout=2_000)
        page.reload(wait_until="domcontentloaded")
        transcript = page.locator("[data-playground-transcript]")
        transcript.get_by_text("Remember this reply", exact=True).wait_for(timeout=2_000)
        restored = transcript.get_by_text("Stored locally.", exact=True).count()
        browser.close()

    assert restored == 1


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
        page.wait_for_function("""() => {
            const input = document.querySelector('input[aria-label="Attach image"]')
            return input instanceof HTMLInputElement && !input.disabled
        }""")
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
        page.wait_for_function(
            """() => document.querySelector('[data-model-capability]')?.textContent === 'Text model · Tools'"""
        )
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
    """Image editing shows the chosen source and active model before any work starts."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.set_default_timeout(1_500)
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Image edit").click()
        file_input = page.get_by_label("Source image")
        file_input.wait_for()
        prompt = page.get_by_label("Edit instruction")
        prompt.wait_for()
        file_input.set_input_files({"name": "source.png", "mimeType": "image/png", "buffer": b"\x89PNG\r\n\x1a\n"})
        preview = page.locator("[data-image-source-preview]")
        preview.wait_for(timeout=2_000)
        file_count = file_input.count()
        prompt_count = prompt.count()
        preview_name = preview.get_attribute("alt")
        active_model = page.locator("[data-image-editor-model]").inner_text()
        browser.close()

    assert file_count == 1
    assert prompt_count == 1
    assert preview_name == "source.png"
    assert active_model == "Qwen/Qwen-Image-Edit-2511"


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


def test_runtime_page_can_release_all_resident_models() -> None:
    """One confirmed control releases every model currently occupying memory."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    runtime = json.dumps({
        "version": "0.0.0",
        "models": [
            {"name": "llama3.1:8b", "size_vram": 5 * 1024 ** 3},
            {"name": "gemma3:27b", "size_vram": 17 * 1024 ** 3},
        ],
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body=runtime))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Local runtime").click()
        release_all = page.get_by_role("button", name="Unload all models")
        release_all.wait_for(timeout=2_000)
        dialog_messages = []
        page.once("dialog", lambda dialog: (dialog_messages.append(dialog.message), dialog.dismiss()))
        release_all.click()
        browser.close()

    assert dialog_messages == ["Unload all 2 models from memory? Active work may be interrupted."]


def test_runtime_page_explains_the_live_gpu_memory_budget() -> None:
    """Loaded models, safety reserve, and remaining room stay visible together."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    gib = 1024 ** 3
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 15 * gib,
        "vram_total_gb": 48,
        "reserved_vram_bytes": 10 * gib,
        "available_vram_bytes": 23 * gib,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "online",
    })
    runtime = json.dumps({"version": "0.0.0", "models": [{"name": "gemma3:27b", "size_vram": 15 * gib}]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body=runtime))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Local runtime").click()
        budget = page.locator("[data-runtime-memory-budget]")
        budget.wait_for(timeout=2_000)
        budget_text = budget.inner_text()
        usage_percent = budget.get_by_role("progressbar").get_attribute("aria-valuenow")
        browser.close()

    assert "GPU memory budget" in budget_text
    assert "48.0 GB" in budget_text
    assert "15.0 GB" in budget_text
    assert "10.0 GB" in budget_text
    assert "23.0 GB" in budget_text
    assert "In use" in budget_text
    assert "Reserved" in budget_text
    assert "Available" in budget_text
    assert usage_percent == "31"


def test_model_library_runs_a_real_quick_benchmark() -> None:
    """An installed text model can report its runtime-measured generation speed."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    gib = 1024 ** 3
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 48,
        "reserved_vram_bytes": 10 * gib,
        "available_vram_bytes": 38 * gib,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "online",
    })
    models = json.dumps({"models": [{"name": "llama3.1:8b", "size": 5 * gib, "details": {}}]})
    benchmark = json.dumps({
        "model": "llama3.1:8b",
        "eval_count": 4,
        "eval_duration_ns": 200000000,
        "total_duration_ns": 1200000000,
        "tokens_per_second": 20,
    })
    bodies = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=models))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"version":"0.0.0","models":[]}'))

        def benchmark_route(route):
            bodies.append(route.request.post_data_json)
            route.fulfill(status=200, content_type="application/json", body=benchmark)

        page.route("**/api/models/benchmark", benchmark_route)
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        benchmark_button = page.get_by_role("button", name="Run quick benchmark")
        benchmark_button.wait_for(timeout=2_000)
        benchmark_button.click()
        result = page.locator("[data-model-benchmark]")
        result.wait_for(timeout=2_000)
        result_text = result.inner_text()
        browser.close()

    assert bodies == [{"model": "llama3.1:8b", "release_loaded": False}]
    assert "Quick benchmark" in result_text
    assert "20.0 tokens/s" in result_text


def test_model_library_inspects_actual_local_model_capabilities() -> None:
    """Model type labels can be verified against the local runtime, not only inferred from names."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    models = json.dumps({"models": [{"name": "qwen3-vl:8b", "size": 10 * 1024 ** 3, "details": {}}]})
    capabilities = json.dumps({"model": "qwen3-vl:8b", "capabilities": ["completion", "vision", "tools"]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=models))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"version":"0.0.0","models":[]}'))
        page.route("**/api/models/capabilities*", lambda route: route.fulfill(status=200, content_type="application/json", body=capabilities))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        inspect_button = page.get_by_role("button", name="Inspect capabilities")
        inspect_button.wait_for(timeout=2_000)
        inspect_button.click()
        detail = page.locator("[data-model-capabilities]")
        detail.wait_for(timeout=2_000)
        detail.get_by_text("Multimodal", exact=True).wait_for(timeout=2_000)
        detail_text = detail.inner_text()
        browser.close()

    assert "Model capabilities" in detail_text
    assert "Multimodal" in detail_text
    assert "Tool calls" in detail_text


def test_model_library_uses_a_catalog_not_a_freeform_model_name_input() -> None:
    """Downloading a model starts from known, runnable catalog choices."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Model", exact=True).wait_for()
        catalog_count = page.get_by_label("Model", exact=True).count()
        freeform_count = page.locator("#model-name").count()
        browser.close()

    assert catalog_count == 1
    assert freeform_count == 0


def test_empty_model_library_points_to_the_catalog_workflow() -> None:
    """An empty library must not instruct suppliers to use a removed freeform field."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body='{"models":[]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        empty_message = page.get_by_text("No local models yet. Choose a catalog model to download.")
        empty_message.wait_for(timeout=2_000)
        empty_count = empty_message.count()
        browser.close()

    assert empty_count == 1


def test_model_catalog_groups_choices_by_provider_and_shows_model_type() -> None:
    """Provider, workload type, and model selection are separate."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5176")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Provider", exact=True).wait_for()
        providers = page.get_by_label("Provider", exact=True).locator("option").all_text_contents()
        page.get_by_label("Type", exact=True).wait_for()
        types = page.get_by_label("Type", exact=True).locator("option").all_text_contents()
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
        page.get_by_label("Type", exact=True).select_option(label="Multimodal")
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
        page.get_by_label("Provider", exact=True).select_option(label="DeepSeek")
        page.wait_for_function("() => document.querySelector('#model-provider').value === 'DeepSeek'")
        page.get_by_label("Type", exact=True).select_option(label="Code")
        page.wait_for_function("""() =>
            document.querySelector('#model-type').value === 'code' &&
            [...document.querySelectorAll('#model-catalog option')].every((option) => option.textContent.includes('Code'))
        """)
        models = page.get_by_label("Model", exact=True).locator("option").all_text_contents()
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
        provider_width = page.get_by_label("Provider", exact=True).evaluate("node => node.getBoundingClientRect().width")
        type_width = page.get_by_label("Type", exact=True).evaluate("node => node.getBoundingClientRect().width")
        model_width = page.get_by_label("Model", exact=True).evaluate("node => node.getBoundingClientRect().width")
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


def test_installed_model_library_contains_overflow_and_keeps_metadata_on_one_line() -> None:
    """Dense model metadata scrolls inside its panel without wrapping or widening the page."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    installed = json.dumps({"models": [
        {
            "name": "qwen3:14b",
            "size": 9_300_000_000,
            "details": {"parameter_size": "14.8B", "quantization_level": "Q4_K_M"},
        },
        {
            "name": "gemma3:27b",
            "size": 17_400_000_000,
            "details": {"parameter_size": "27.4B", "quantization_level": "Q4_K_M"},
        },
    ]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        captures = []
        for viewport in (
            {"width": 1440, "height": 960},
            {"width": 1536, "height": 960},
            {"width": 1024, "height": 768},
            {"width": 390, "height": 844},
        ):
            context = browser.new_context(viewport=viewport, locale="zh-CN")
            page = context.new_page()
            page.route("**/api/models", lambda route: route.fulfill(
                status=200, content_type="application/json", body=installed,
            ))
            page.route("**/api/runtime", lambda route: route.fulfill(
                status=200, content_type="application/json", body='{"version":"0.0.0","models":[]}',
            ))
            page.goto(f"{base}/models", wait_until="domcontentloaded")
            row = page.locator('[data-installed-model="qwen3:14b"]')
            row.wait_for()
            capture = page.evaluate("""() => {
                const row = document.querySelector('[data-installed-model="qwen3:14b"]')
                const scroller = row.closest('table').parentElement
                return {
                    viewportWidth: window.innerWidth,
                    documentWidth: document.documentElement.scrollWidth,
                    scrollerClientWidth: scroller.clientWidth,
                    scrollerScrollWidth: scroller.scrollWidth,
                    scrollerLabel: scroller.getAttribute('aria-label'),
                    scrollerTabIndex: scroller.tabIndex,
                    whiteSpace: [...row.querySelectorAll('td')]
                        .slice(0, 6)
                        .map((cell) => getComputedStyle(cell).whiteSpace),
                }
            }""")
            capture["hintCount"] = page.get_by_text("左右滑动查看全部模型信息").count()
            captures.append(capture)
            context.close()
        browser.close()

    assert all(capture["documentWidth"] <= capture["viewportWidth"] for capture in captures)
    assert all(
        capture["scrollerScrollWidth"] <= capture["scrollerClientWidth"]
        for capture in captures[:2]
    )
    assert all(
        capture["scrollerScrollWidth"] > capture["scrollerClientWidth"]
        for capture in captures[2:]
    )
    assert all(capture["hintCount"] == 1 for capture in captures[2:])
    assert all(capture["scrollerLabel"] == "已安装模型表格" for capture in captures)
    assert all(capture["scrollerTabIndex"] == 0 for capture in captures)
    assert all(
        white_space == "nowrap"
        for capture in captures
        for white_space in capture["whiteSpace"]
    )


def test_mobile_wide_tables_disclose_and_label_horizontal_scrolling() -> None:
    """Hidden columns stay discoverable to touch and keyboard users."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    installed = json.dumps({"models": [{
        "name": "qwen3:14b",
        "size": 9_300_000_000,
        "details": {"parameter_size": "14.8B", "quantization_level": "Q4_K_M"},
    }]})
    requests = json.dumps([{
        "id": "req-local-001",
        "model": "qwen3:14b",
        "path": "/v1/chat/completions",
        "consumer": "local-verification-consumer-with-a-long-name",
        "started_at": "2026-08-04T07:00:00Z",
        "completed_at": "2026-08-04T07:00:01Z",
    }])

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 390, "height": 844}, locale="zh-CN")
        page = context.new_page()
        page.set_default_timeout(2_000)
        page.route("**/api/models", lambda route: route.fulfill(
            status=200, content_type="application/json", body=installed,
        ))
        page.route("**/api/runtime", lambda route: route.fulfill(
            status=200, content_type="application/json", body='{"version":"0.0.0","models":[]}',
        ))
        page.route("**/api/requests", lambda route: route.fulfill(
            status=200, content_type="application/json", body=requests,
        ))

        page.goto(f"{base}/models", wait_until="domcontentloaded")
        model_hint = page.get_by_text("左右滑动查看全部模型信息")
        model_hint.wait_for()
        model_scroller = page.get_by_role("region", name="已安装模型表格")
        model_tab_index = model_scroller.get_attribute("tabindex")

        page.goto(f"{base}/traffic", wait_until="domcontentloaded")
        traffic_hint = page.get_by_text("左右滑动查看全部请求信息")
        traffic_hint.wait_for()
        traffic_scroller = page.get_by_role("region", name="最近请求表格")
        traffic_tab_index = traffic_scroller.get_attribute("tabindex")
        browser.close()

    assert model_tab_index == "0"
    assert traffic_tab_index == "0"


def test_installed_model_library_filters_by_provider_type_and_name() -> None:
    """A busy local library can be narrowed without changing model state."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    installed = json.dumps({"models": [
        {"name": "qwen3-vl:8b", "size": 10 * 1024 ** 3, "details": {}},
        {"name": "llama3.1:8b", "size": 5 * 1024 ** 3, "details": {}},
        {"name": "deepseek-r1:8b", "size": 5 * 1024 ** 3, "details": {}},
    ]})

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=installed))
        page.route("**/api/runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"version":"0.0.0","models":[]}'))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.locator('[data-installed-model="qwen3-vl:8b"]').wait_for()

        page.get_by_label("Installed provider").select_option(label="Alibaba / Qwen")
        provider_matches = page.locator("[data-installed-model]").all_inner_texts()
        page.get_by_label("Installed type").select_option(label="Multimodal")
        page.get_by_label("Search installed models").fill("qwen3-vl")
        filtered_matches = page.locator("[data-installed-model]").all_inner_texts()
        browser.close()

    assert len(provider_matches) == 1
    assert "qwen3-vl:8b" in provider_matches[0]
    assert len(filtered_matches) == 1
    assert "qwen3-vl:8b" in filtered_matches[0]


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
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 48,
        "reserved_vram_bytes": 0,
        "available_vram_bytes": 48 * 1024 ** 3,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
        "gateway_state": "online",
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/models", lambda route: route.fulfill(status=200, content_type="application/json", body=installed))
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        option_text = page.locator('#model-catalog option[value="qwen3:32b"]').inner_text()
        page.get_by_label("Model", exact=True).select_option("qwen3:32b")
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
        page.get_by_label("Provider", exact=True).select_option(label="Alibaba / Qwen")
        page.get_by_label("Type", exact=True).select_option(label="Image generation")
        model = page.locator('#model-catalog option[value="Qwen/Qwen-Image-Edit-2511"]')
        model.wait_for(state="attached")
        is_disabled = model.is_disabled()
        browser.close()

    assert is_disabled


def test_model_catalog_disables_image_models_the_local_editor_cannot_select() -> None:
    """A broad image catalogue must not make unsupported editor weights actionable."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    overview = json.dumps({
        "active_requests": 0,
        "completed_requests": 0,
        "failed_requests": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "loaded_vram_bytes": 0,
        "vram_total_gb": 96,
        "reserved_vram_bytes": 0,
        "available_vram_bytes": 96 * 1024 ** 3,
        "settled_earnings_micros": 0,
        "settled_earnings_available": False,
    })

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/overview", lambda route: route.fulfill(status=200, content_type="application/json", body=overview))
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.goto(base, wait_until="networkidle")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Type", exact=True).select_option("image")
        unsupported = page.locator('#model-catalog option[value="Qwen/Qwen-Image"]')
        supported = page.locator('#model-catalog option[value="Qwen/Qwen-Image-Edit-2511"]')
        unsupported.wait_for(state="attached")
        supported.wait_for(state="attached")
        unsupported_disabled = unsupported.is_disabled()
        supported_disabled = supported.is_disabled()
        browser.close()

    assert unsupported_disabled
    assert not supported_disabled


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
        page.get_by_label("Type", exact=True).select_option("image")
        action = page.locator("[data-select-image-model]")
        action_count = action.count()
        browser.close()

    assert action_count == 1


def test_model_download_queue_exposes_cancellation_for_each_job() -> None:
    """A queued large download must be removable without waiting for the active transfer."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    queue = json.dumps({
        "active": {"name": "qwen3:32b", "status": "downloading", "completed": 3, "total": 10, "rate_bytes_per_second": 104857600, "seconds_remaining": 75, "error": "", "done": False},
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
        cancel.wait_for(timeout=2_000)
        cancel_count = cancel.count()
        speed = page.locator('[data-download-speed]').inner_text()
        eta = page.locator('[data-download-eta]').inner_text()
        browser.close()

    assert cancel_count == 1
    assert speed == "100 MB/s"
    assert eta == "1m 15s left"


def test_model_catalog_disables_models_over_the_detected_memory_budget() -> None:
    """Large models remain visible but cannot be downloaded on an undersized node."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(base, wait_until="domcontentloaded")
        page.wait_for_selector("main h1")
        page.locator("aside").get_by_role("link", name="Models").click()
        page.get_by_label("Provider", exact=True).select_option(label="Meta")
        oversized = page.locator('#model-catalog option[value="llama3.3:70b"]')
        oversized.wait_for(state="attached")
        is_disabled = oversized.is_disabled()
        browser.close()

    assert is_disabled


def test_model_library_supports_a_direct_bookmark_url() -> None:
    """A copied /models URL must open the model library, not silently reset to overview."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.goto(f"{base}/models", wait_until="networkidle")
        title = page.locator("main h1").inner_text()
        browser.close()

    assert title == "Model library"


def test_agent_update_requires_confirmation_before_restarting_the_node() -> None:
    """Dismissing the warning must keep an update request from interrupting the node."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    update_requests = 0
    confirmations: list[str] = []

    def update_route(route) -> None:
        nonlocal update_requests
        update_requests += 1
        route.fulfill(status=202, content_type="application/json", body='{"accepted":true}')

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/update", update_route)
        page.on("dialog", lambda dialog: (confirmations.append(dialog.message), dialog.dismiss()))
        page.goto(base, wait_until="networkidle")
        page.get_by_role("button", name="Check for updates").click()
        page.wait_for_timeout(150)
        browser.close()

    assert confirmations == ["Check for and install the latest agent version? This node will restart when the update is ready."]
    assert update_requests == 0


def test_image_editing_can_be_stopped_while_the_local_request_is_running() -> None:
    """A long local image edit must expose a stop action before a result returns."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")
    pending_requests = []

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.set_default_timeout(1_500)
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.route("**/api/image/edit", lambda route: pending_requests.append(route))
        try:
            page.goto(base, wait_until="networkidle")
            page.locator("aside").get_by_role("link", name="Image edit").click()
            page.locator("#source-image").set_input_files({"name": "source.png", "mimeType": "image/png", "buffer": b"image"})
            page.get_by_label("Edit instruction").fill("make it neon")
            page.get_by_role("button", name="Edit image").click()
            page.get_by_role("button", name="Stop editing").wait_for()
            page.get_by_role("button", name="Stop editing").click()
            page.get_by_role("button", name="Edit image").wait_for()
        finally:
            for request in pending_requests:
                try:
                    request.abort()
                except Exception:
                    pass
            browser.close()

    assert len(pending_requests) == 1


def test_image_edit_uses_a_console_file_picker_with_the_selected_name() -> None:
    """The local image file remains a native input, but it should look like part of the console."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.goto(base, wait_until="networkidle")
        page.locator("aside").get_by_role("link", name="Image edit").click()
        picker = page.locator("[data-source-file-picker]")
        picker.wait_for()
        picker_text = picker.inner_text()
        page.locator("#source-image").set_input_files({"name": "portrait.png", "mimeType": "image/png", "buffer": b"image"})
        chosen_name = page.locator("[data-source-file-name]").inner_text()
        browser.close()

    assert "Choose source image" in picker_text
    assert chosen_name == "portrait.png"


def test_image_edit_keeps_the_source_file_picker_visible_when_runtime_is_unavailable() -> None:
    """A missing image runtime must not make the native source-image picker disappear."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.add_init_script("sessionStorage.setItem('everyapi-edge-console', JSON.stringify({state:{locale:'zh'},version:1}))")
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"unavailable","models":[],"error":"A CUDA-capable GPU is required for image editing."}'))
        page.goto(f"{base}/image-edit", wait_until="networkidle")
        image_error = page.locator("[data-image-runtime-error]").inner_text()
        source_file_inputs = page.locator("#source-image").count()
        browser.close()

    assert image_error == "图像编辑需要兼容 CUDA 的 GPU。"
    assert source_file_inputs == 1


def test_image_edit_result_can_be_downloaded() -> None:
    """An edited image needs a local download action instead of a display-only preview."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.set_default_timeout(1_500)
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.route("**/api/image/edit", lambda route: route.fulfill(status=200, content_type="application/json", body='{"b64_json":"aW1hZ2U="}'))
        page.goto(base, wait_until="networkidle")
        page.locator("aside").get_by_role("link", name="Image edit").click()
        page.locator("#source-image").set_input_files({"name": "source.png", "mimeType": "image/png", "buffer": b"image"})
        page.get_by_label("Edit instruction").fill("make it neon")
        page.get_by_role("button", name="Edit image").click()
        download = page.get_by_role("link", name="Download image")
        download.wait_for()
        href = download.get_attribute("href")
        filename = download.get_attribute("download")
        browser.close()

    assert href == "data:image/png;base64,aW1hZ2U="
    assert filename == "everyapi-image-edit.png"


def test_image_edit_displays_a_structured_api_error() -> None:
    """Image Lab uses the shared structured error envelope instead of stringifying it."""
    base = os.environ.get("EDGE_CONSOLE_WEB", "http://127.0.0.1:5175")

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        page = browser.new_page(viewport={"width": 1440, "height": 960})
        page.route("**/api/image-runtime", lambda route: route.fulfill(status=200, content_type="application/json", body='{"status":"ready","models":["Qwen/Qwen-Image-Edit-2511"]}'))
        page.route("**/api/image/edit", lambda route: route.fulfill(status=503, content_type="application/json", body='{"error":{"code":"runtime_unavailable","message":"The local image runtime is unavailable.","retryable":true}}'))
        page.goto(f"{base}/image-edit", wait_until="networkidle")
        page.locator("#source-image").set_input_files({"name": "source.png", "mimeType": "image/png", "buffer": b"image"})
        page.get_by_label("Edit instruction").fill("make it neon")
        page.get_by_role("button", name="Edit image").click()
        alert = page.get_by_role("alert")
        alert.wait_for()
        message = alert.inner_text()
        browser.close()

    assert message == "The local image runtime is unavailable."


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
