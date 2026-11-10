"""Check the installed local macOS trial's official app, without signing in.

Run with uv run --no-project python scripts/guest-os/test-macos-claude-trial.py
--profile <trial profile> --frame <new PNG path>. Inspect the final window PNG
to distinguish the online login page from an offline error or another dialog.
"""

import argparse
import base64
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import struct
import time
from urllib import error, parse, request


class LocalOnly(request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        raise RuntimeError("Unexpected redirect from the local desktop gateway")


def main():
    args = argparse.ArgumentParser(description=__doc__)
    args.add_argument("--profile", type=Path, required=True)
    args.add_argument("--frame", type=Path, required=True)
    options = args.parse_args()
    if options.frame.exists():
        args.error("Specify a new PNG path")
    record = json.loads((options.profile / "desktop-entry/control.json").read_text())
    port = record["port"]
    if type(port) is not int or not 17777 <= port < 17808:
        raise RuntimeError("Invalid local desktop gateway port")
    origin = f"http://localhost:{port}"
    client = request.build_opener(
        request.ProxyHandler({}), request.HTTPCookieProcessor(http.cookiejar.CookieJar()), LocalOnly()
    )
    connection = None

    def call(path, data=None):
        headers = {"Origin": origin}
        if connection:
            headers["X-Cage-Connection"] = connection
        body = None
        if data is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(data).encode()
        req = request.Request(f"{origin}/api/{path}", body, headers)
        try:
            with client.open(req, timeout=60) as response:
                return json.load(response)
        except error.HTTPError as exc:
            raise RuntimeError(f"Local {path} returned HTTP {exc.code}") from None

    launch = parse.urlsplit(call("launch", {"token": record["token"]})["url"])
    if f"{launch.scheme}://{launch.netloc}" != origin or not launch.fragment:
        raise RuntimeError("Unexpected local launch URL")
    call("session", {"token": launch.fragment})
    context = call("client/context")
    if context.get("mode") != "local" or not context.get("desktop_gateway"):
        raise RuntimeError("The installed desktop app has not selected this local workload")
    connection = context["connection_id"]
    account, state = call("account"), call("status")
    if account.get("guest_system") != "macos" or not account.get("unlocked") or not state.get("ready"):
        raise RuntimeError("The target is not the unlocked, ready macOS guest workbench")
    safety = state.get("safety") or {}
    checks = [c for c in (safety.get("report") or {}).get("checks", []) if c.get("critical")]
    if (safety.get("gate") or {}).get("mode") != "open" or len(checks) != 11 or any(c["state"] != "passed" for c in checks):
        raise RuntimeError("The trial has not passed all eleven current network and isolation checks")

    def window(action, **fields):
        reply = call("macos/claude/window", {"action": action, **fields})
        if "error" in reply:
            raise RuntimeError("Guest window service: " + reply["error"])
        return reply

    status = window("status")
    if not all(status.get(k) for k in ("installed", "screen", "input")):
        raise RuntimeError("The guest application or its window permissions are unavailable")
    window("launch")
    deadline = time.monotonic() + 12
    while True:
        try:
            before = window("frame")
            break
        except RuntimeError as exc:
            if not any(key in str(exc) for key in ("not_running", "no_visible_window")) or time.monotonic() >= deadline:
                raise
            time.sleep(.4)
    window("key", window=before["window"], pid=before["pid"], key="Tab")
    time.sleep(.4)
    after = window("frame")
    if (before["window"], before["pid"]) != (after["window"], after["pid"]):
        raise RuntimeError("Focused application window changed during the input check")
    png = base64.b64decode(after["image"], validate=True)
    if not png.startswith(b"\x89PNG\r\n\x1a\n") or png[12:16] != b"IHDR":
        raise RuntimeError("Expected a window PNG")
    with os.fdopen(os.open(options.frame, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600), "wb") as stream:
        stream.write(png)
    print(json.dumps({
        "checked": len(checks), "size": struct.unpack(">II", png[16:24]), "bytes": len(png),
        "sha256": hashlib.sha256(png).hexdigest(), "changedAfterTab": before["image"] != after["image"]
    }))


if __name__ == "__main__":
    main()
