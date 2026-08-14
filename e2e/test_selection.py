"""
E2E UI tests for FileBrowser selection behaviour.

All tests share one logged-in browser session (module scope).
Between tests, selection state is reset via JS without a full page reload.

The session-scoped `selection_dir` fixture pre-creates /selection-test-dir
with 5 files via the REST API so these tests are independent from test_ui.py.
"""

import requests as _requests
import pytest
from playwright.sync_api import expect, Page

# ──────────────────────────────────────────────
# Constants
# ──────────────────────────────────────────────

DIR = "selection-test-dir"
FILES = ["file1.txt", "file2.txt", "file3.txt", "file4.txt", "file5.txt"]


# ──────────────────────────────────────────────
# Session fixture: create test data once via API
# ──────────────────────────────────────────────

@pytest.fixture(scope="session")
def selection_dir(go_server, admin_password):
    """Create /selection-test-dir with 5 numbered files via the REST API."""
    resp = _requests.post(
        f"{go_server}/_/api/login",
        json={"username": "admin", "password": admin_password},
        timeout=10,
    )
    assert resp.ok, f"Login failed: {resp.text}"
    token = resp.json()["token"]
    headers = {"Authorization": f"Bearer {token}"}

    for filename in FILES:
        r = _requests.put(
            f"{go_server}/{DIR}/{filename}",
            data=f"content of {filename}",
            headers=headers,
            timeout=10,
        )
        assert r.ok, f"Failed to create {filename}: {r.text}"

    return DIR


# ──────────────────────────────────────────────
# Module fixture: one browser page, logged in once
# ──────────────────────────────────────────────

@pytest.fixture(scope="module")
def sel_page(browser, go_server, selection_dir, admin_password):
    """
    Shared browser page for the entire module.
    Logs in as admin and navigates to the selection dir once.
    """
    ctx = browser.new_context()
    page = ctx.new_page()
    page.on("pageerror", lambda err: print(f"JS ERROR: {err}"))

    # Login
    page.goto(go_server)
    page.click("#nav-login-btn")
    expect(page.locator("dialog#login-dialog")).to_be_visible()
    page.fill('dialog#login-dialog input[name="username"]', "admin")
    page.fill('dialog#login-dialog input[name="password"]', admin_password)
    page.click('dialog#login-dialog button[type="submit"]')
    page.wait_for_load_state("networkidle")
    expect(page.locator("#user-menu-trigger")).to_contain_text("admin")
    toast = page.locator('.af-toast:has-text("Welcome back")')
    if toast.is_visible():
        page.locator('.af-toast:has-text("Welcome back") button:has-text("×")').click()
        expect(toast).not_to_be_visible()

    # Navigate to the selection dir
    _goto_dir(page, go_server)

    yield page
    ctx.close()


# ──────────────────────────────────────────────
# Per-test reset (autouse)
# ──────────────────────────────────────────────

@pytest.fixture(autouse=True)
def reset_selection(sel_page, go_server):
    """
    Before each test: clear selection state via JS (fast, no page reload).
    If the page has somehow drifted off the selection dir, navigate back.
    """
    if f"/{DIR}" not in sel_page.url:
        _goto_dir(sel_page, go_server)
    else:
        sel_page.evaluate(
            "typeof window.exitSelectionMode === 'function' && window.exitSelectionMode()"
        )
    yield


# ──────────────────────────────────────────────
# Helpers
# ──────────────────────────────────────────────

def _goto_dir(page: Page, go_server: str) -> None:
    page.goto(f"{go_server}/{DIR}")
    expect(page.locator(".af-breadcrumb-current")).to_contain_text(DIR)
    for f in FILES:
        expect(page.locator(f'.af-file-row:has-text("{f}")')).to_be_visible()


def file_row(page: Page, filename: str):
    return page.locator(f'.af-file-row:has-text("{filename}")')


def is_selected(page: Page, filename: str) -> bool:
    return file_row(page, filename).evaluate(
        "el => el.classList.contains('is-selected')"
    )


def batch_count_text(page: Page) -> str:
    return page.locator(".af-batch-count").inner_text().strip()


def is_selection_mode(page: Page) -> bool:
    return page.locator(".af-table").evaluate(
        "el => el.classList.contains('af-table-selection-mode')"
    )


def select_via_checkbox(page: Page, filename: str) -> None:
    """Hover the row to reveal the checkbox, then click it."""
    r = file_row(page, filename)
    r.hover()
    r.locator("input.af-selection-checkbox").click()


# ──────────────────────────────────────────────
# Tests
# ──────────────────────────────────────────────

def test_checkbox_enters_selection_mode(sel_page):
    """Clicking a row checkbox enters selection mode and marks only that row."""
    assert not is_selection_mode(sel_page), "Should not be in selection mode initially"

    select_via_checkbox(sel_page, "file1.txt")

    assert is_selection_mode(sel_page), "Should be in selection mode after checkbox click"
    assert is_selected(sel_page, "file1.txt")
    assert not is_selected(sel_page, "file2.txt")
    assert batch_count_text(sel_page) == "1 selected"


def test_shift_click_selects_range_from_anchor(sel_page):
    """SHIFT+click selects all rows between the anchor and the clicked row."""
    select_via_checkbox(sel_page, "file1.txt")

    file_row(sel_page, "file4.txt").click(modifiers=["Shift"])

    for f in ["file1.txt", "file2.txt", "file3.txt", "file4.txt"]:
        assert is_selected(sel_page, f), f"{f} should be selected"
    assert not is_selected(sel_page, "file5.txt")
    assert batch_count_text(sel_page) == "4 selected"


def test_shift_click_without_prior_selection_mode(sel_page):
    """SHIFT+click on a row without prior selection mode enters the mode."""
    file_row(sel_page, "file3.txt").click(modifiers=["Shift"])

    assert is_selection_mode(sel_page)
    assert is_selected(sel_page, "file3.txt")
    assert batch_count_text(sel_page) == "1 selected"

    # Second SHIFT+click extends from the new anchor (file3) to file5
    file_row(sel_page, "file5.txt").click(modifiers=["Shift"])

    assert not is_selected(sel_page, "file1.txt")
    assert not is_selected(sel_page, "file2.txt")
    assert is_selected(sel_page, "file3.txt")
    assert is_selected(sel_page, "file4.txt")
    assert is_selected(sel_page, "file5.txt")
    assert batch_count_text(sel_page) == "3 selected"


def test_ctrl_click_adds_individual_item(sel_page):
    """CTRL+click adds a non-adjacent item without disturbing the rest."""
    select_via_checkbox(sel_page, "file1.txt")

    file_row(sel_page, "file3.txt").click(modifiers=["Control"])

    assert is_selected(sel_page, "file1.txt")
    assert not is_selected(sel_page, "file2.txt")
    assert is_selected(sel_page, "file3.txt")
    assert batch_count_text(sel_page) == "2 selected"


def test_ctrl_click_removes_individual_item(sel_page):
    """CTRL+click on an already-selected item deselects only that item."""
    select_via_checkbox(sel_page, "file1.txt")
    file_row(sel_page, "file3.txt").click(modifiers=["Shift"])
    assert batch_count_text(sel_page) == "3 selected"

    file_row(sel_page, "file2.txt").click(modifiers=["Control"])

    assert is_selected(sel_page, "file1.txt")
    assert not is_selected(sel_page, "file2.txt")
    assert is_selected(sel_page, "file3.txt")
    assert batch_count_text(sel_page) == "2 selected"


def test_shift_click_after_ctrl_deselect_does_not_reinclude(sel_page):
    """
    Regression: after CTRL+deselecting the last item, a SHIFT+click toward the
    start should NOT re-select the just-removed item.
    """
    select_via_checkbox(sel_page, "file1.txt")
    file_row(sel_page, "file4.txt").click(modifiers=["Shift"])
    assert batch_count_text(sel_page) == "4 selected"

    # CTRL+deselect file4
    file_row(sel_page, "file4.txt").click(modifiers=["Control"])
    assert not is_selected(sel_page, "file4.txt")
    assert batch_count_text(sel_page) == "3 selected"

    # SHIFT+click file1 — anchor should now be at file3, so file4 must stay out
    file_row(sel_page, "file1.txt").click(modifiers=["Shift"])

    assert not is_selected(sel_page, "file4.txt"), (
        "file4 was explicitly deselected; SHIFT+click should not re-include it"
    )
    assert is_selected(sel_page, "file1.txt")


def test_clearing_selection_resets_anchor(sel_page):
    """
    Regression: after clearing the selection completely and reselecting from a
    new anchor, only the new range should be selected.
    """
    select_via_checkbox(sel_page, "file1.txt")
    file_row(sel_page, "file3.txt").click(modifiers=["Shift"])
    assert batch_count_text(sel_page) == "3 selected"

    # Deselect each item to exit selection mode
    select_via_checkbox(sel_page, "file1.txt")
    select_via_checkbox(sel_page, "file2.txt")
    select_via_checkbox(sel_page, "file3.txt")
    assert not is_selection_mode(sel_page)

    # New anchor at file4
    select_via_checkbox(sel_page, "file4.txt")
    file_row(sel_page, "file5.txt").click(modifiers=["Shift"])

    assert not is_selected(sel_page, "file1.txt"), "file1 should not be selected after anchor reset"
    assert not is_selected(sel_page, "file2.txt")
    assert not is_selected(sel_page, "file3.txt")
    assert is_selected(sel_page, "file4.txt")
    assert is_selected(sel_page, "file5.txt")
    assert batch_count_text(sel_page) == "2 selected"


def test_row_area_click_toggles_in_selection_mode(sel_page):
    """Clicking the size/time cell of a row while in selection mode toggles it."""
    select_via_checkbox(sel_page, "file1.txt")

    # Click the size-cell (not the link, not the checkbox)
    file_row(sel_page, "file3.txt").locator(".size-cell").click()
    assert is_selected(sel_page, "file3.txt")
    assert batch_count_text(sel_page) == "2 selected"

    file_row(sel_page, "file3.txt").locator(".size-cell").click()
    assert not is_selected(sel_page, "file3.txt")
    assert batch_count_text(sel_page) == "1 selected"


def test_deselect_last_item_exits_selection_mode(sel_page):
    """Removing the last selected item automatically exits selection mode."""
    select_via_checkbox(sel_page, "file2.txt")
    assert is_selection_mode(sel_page)

    select_via_checkbox(sel_page, "file2.txt")

    assert not is_selection_mode(sel_page)
    assert not is_selected(sel_page, "file2.txt")


def test_select_all_checkbox_selects_and_deselects(sel_page):
    """Header select-all checkbox selects all rows; clicking again deselects all."""
    sel_page.locator("#af-select-all-checkbox").click()

    for f in FILES:
        assert is_selected(sel_page, f), f"{f} should be selected by select-all"
    assert batch_count_text(sel_page) == f"{len(FILES)} selected"

    sel_page.locator("#af-select-all-checkbox").click()

    for f in FILES:
        assert not is_selected(sel_page, f)
    assert not is_selection_mode(sel_page)


def test_shift_click_anchor_stays_fixed(sel_page):
    """
    Repeated SHIFT+clicks must all extend from the original anchor, not drift
    to the most recent SHIFT+click target.
    Anchor = file2. SHIFT→file5 selects file2–file5.
    SHIFT→file3 adds file2–file3 (anchor still file2).
    file1 must remain unselected because it is before the anchor.
    """
    select_via_checkbox(sel_page, "file2.txt")

    file_row(sel_page, "file5.txt").click(modifiers=["Shift"])
    assert batch_count_text(sel_page) == "4 selected"  # file2–file5

    file_row(sel_page, "file3.txt").click(modifiers=["Shift"])

    assert not is_selected(sel_page, "file1.txt"), (
        "file1 is before the anchor (file2); SHIFT+click must not reach it"
    )
    assert is_selected(sel_page, "file2.txt"), "Anchor (file2) must remain selected"
