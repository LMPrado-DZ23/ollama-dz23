#!/usr/bin/env python3
import ipaddress
import json
import os
import pathlib
import re
import socket
import sys
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright


def fail(message):
    raise RuntimeError(message)


def validate_url(value):
    parsed = urlparse(value)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        fail("browser only permits http and https URLs")
    if os.environ.get("OLLAMA_AGENT_BROWSER_ALLOW_PRIVATE", "") == "1":
        return
    try:
        addresses = socket.getaddrinfo(parsed.hostname, parsed.port or 443, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        fail(f"browser DNS resolution failed: {exc}")
    for address in addresses:
        ip = ipaddress.ip_address(address[4][0])
        if ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_reserved or ip.is_multicast or ip.is_unspecified:
            fail("browser URL resolves to a private or local address")


def page_for(context):
    if context.pages:
        return context.pages[0]
    return context.new_page()


def save_session_state(path, page):
    if page.url.startswith(("http://", "https://")):
        path.write_text(json.dumps({"url": page.url}, ensure_ascii=False), encoding="utf-8")


def page_result(session_id, page, state_path, **extra):
    result = {"session_id": session_id, "url": page.url, "title": page.title()}
    result.update(extra)
    save_session_state(state_path, page)
    return result


def main(request):
    action = str(request.get("action", "")).strip()
    session_id = str(request.get("session_id", "")).strip()
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", session_id):
        fail("invalid browser session id")
    root = pathlib.Path(os.environ.get("OLLAMA_AGENT_BROWSER_ROOT", "/tmp/ollama-agent-browser"))
    user_dir = root / "sessions" / session_id
    user_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
    state_path = user_dir / "state.json"
    executable = os.environ.get("OLLAMA_AGENT_BROWSER_EXECUTABLE", "/usr/bin/chromium")
    if not pathlib.Path(executable).exists():
        fail("configured Chromium executable does not exist")

    with sync_playwright() as playwright:
        browser_context = playwright.chromium.launch_persistent_context(
            str(user_dir), headless=True, executable_path=executable,
            accept_downloads=True, args=["--disable-dev-shm-usage"],
        )
        try:
            page = page_for(browser_context)
            timeout = min(max(int(request.get("timeout_ms", 15000)), 1000), 60000)
            page.set_default_timeout(timeout)
            if page.url == "about:blank" and state_path.is_file():
                previous = json.loads(state_path.read_text(encoding="utf-8")).get("url", "")
                if previous:
                    validate_url(previous)
                    page.goto(previous, wait_until="domcontentloaded")
            if action == "navigate":
                url = str(request.get("url", "")).strip()
                validate_url(url)
                page.goto(url, wait_until="domcontentloaded")
                validate_url(page.url)
                return page_result(session_id, page, state_path)
            if action in {"snapshot", "content"}:
                validate_url(page.url)
                content = page.locator("body").inner_text(timeout=timeout)
                return page_result(session_id, page, state_path, content=content[:131072])
            if action == "click":
                page.locator(str(request.get("selector", ""))).click()
                validate_url(page.url)
                return page_result(session_id, page, state_path)
            if action == "fill":
                page.locator(str(request.get("selector", ""))).fill(str(request.get("text", "")))
                return page_result(session_id, page, state_path)
            if action == "press":
                page.locator(str(request.get("selector", ""))).press(str(request.get("key", "Enter")))
                validate_url(page.url)
                return page_result(session_id, page, state_path)
            if action == "upload":
                source = pathlib.Path(str(request.get("path", ""))).resolve()
                if not source.is_file():
                    fail("upload path is not a file")
                page.locator(str(request.get("selector", ""))).set_input_files(str(source))
                return page_result(session_id, page, state_path, uploaded=source.name)
            if action == "download":
                target = pathlib.Path(str(request.get("save_path", ""))).resolve()
                target.parent.mkdir(parents=True, exist_ok=True)
                with page.expect_download(timeout=timeout) as download_info:
                    page.locator(str(request.get("selector", ""))).click()
                download = download_info.value
                download.save_as(str(target))
                return page_result(session_id, page, state_path, path=str(target), suggested_filename=download.suggested_filename)
            if action == "screenshot":
                target = pathlib.Path(str(request.get("save_path", ""))).resolve()
                target.parent.mkdir(parents=True, exist_ok=True)
                page.screenshot(path=str(target), full_page=bool(request.get("full_page", True)))
                return page_result(session_id, page, state_path, path=str(target))
            if action == "takeover":
                return {"session_id": session_id, "status": "approval_required", "reason": "human takeover must be completed by a connected Desktop/Browser surface", "url": page.url}
            fail(f"unsupported browser action: {action}")
        finally:
            browser_context.close()


if __name__ == "__main__":
    try:
        print(json.dumps(main(json.load(sys.stdin)), ensure_ascii=False))
    except Exception as exc:
        print(json.dumps({"error": str(exc)}, ensure_ascii=False))
        sys.exit(1)
