"""Run with Cage's Python inside a prepared, network-authorized test VirtualMac.

Use the ordinary guest account. The test gives Chrome and the official CLI a
fresh temporary home, opens the official sign-in page, then cancels. It never
submits an account, authorization code or model prompt.
"""
import json
import os
from pathlib import Path
import pty
import secrets
import select
import signal
import subprocess
import tempfile
import time
from urllib.parse import urlsplit


def browser_request(browser, route):
    browser.stdin.write(json.dumps({"method": "GET", "path": route}).encode() + b"\n")
    browser.stdin.flush()
    if not select.select([browser.stdout], [], [], 30)[0]:
        raise RuntimeError("Guest Chrome timed out")
    value = json.loads(browser.stdout.readline())
    if value.get("error"):
        raise RuntimeError("The guest Chrome controller could not serve " + route + " (" + str(value.get("code")) + ")")
    return value


def authorized_tab(snapshot):
    for tab in snapshot.get("tabs", []):
        if not tab.get("login") or tab["id"] != snapshot.get("active"):
            continue
        if tab["url"] in ("about:blank", "null"):
            continue
        url = urlsplit(tab["url"])
        if url.query or url.fragment or url.path not in ("", "/"):
            raise RuntimeError("The workbench snapshot exposed a login URL path or parameters")
        return url.scheme == "https" and url.hostname in (
            "claude.ai", "claude.com", "platform.claude.com", "platform.anthropic.com", "console.anthropic.com")
    return False


def check_login(browser, env):
    master, slave = pty.openpty()
    try:
        account = secrets.token_hex(12)
        with subprocess.Popen(["/usr/local/bin/cage-claude-account", "login", account],
                              stdin=slave, stdout=slave, stderr=slave, env=env,
                              start_new_session=True) as cli:
            try:
                deadline = time.monotonic() + 45
                while cli.poll() is None and time.monotonic() < deadline:
                    # Drain without storing or printing the access-bearing CLI output.
                    if select.select([master], [], [], .25)[0]:
                        os.read(master, 65536)
                    if authorized_tab(browser_request(browser, "/tabs")):
                        break
                    time.sleep(.3)
                else:
                    raise RuntimeError("The CLI did not activate an isolated official authorization tab")
            finally:
                if cli.poll() is None:
                    os.killpg(cli.pid, signal.SIGINT)
                    try:
                        cli.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(cli.pid, signal.SIGKILL)
    finally:
        os.close(master)
        os.close(slave)
    if any(tab.get("login") for tab in browser_request(browser, "/tabs").get("tabs", [])):
        raise RuntimeError("Canceling the login left its private tab open")


def main():
    model = subprocess.check_output(["/usr/sbin/sysctl", "-n", "hw.model"], text=True).strip()
    if os.geteuid() == 0 or not model.startswith("VirtualMac") or Path("/var/db/cage-guest-os").read_text().strip() != "macos":
        raise RuntimeError("Run in a prepared test VirtualMac as its ordinary account")
    base = Path("/usr/local/libexec/cage-workbench-browser-4.145.0-pw1.65.0")
    with tempfile.TemporaryDirectory(dir="/tmp", prefix="cgc.") as home:
        env = dict(os.environ, HOME=home, TERM="xterm-256color",
                   PATH="/usr/local/libexec/cage-cli/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
        with subprocess.Popen([base / "editor/lib/node", base / "controller/macos-native-browser.mjs"],
                              env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                              stderr=subprocess.DEVNULL) as browser:
            try:
                deadline = time.monotonic() + 45
                while True:
                    try:
                        if browser_request(browser, "/health").get("ready"):
                            break
                    except RuntimeError as error:
                        if "browser_unavailable" not in str(error) or time.monotonic() >= deadline:
                            raise
                    if time.monotonic() >= deadline:
                        raise RuntimeError("Guest Chrome is unavailable")
                    time.sleep(1)
                check_login(browser, env)
            finally:
                browser.stdin.close()
                try:
                    browser.wait(timeout=12)
                except subprocess.TimeoutExpired:
                    browser.kill()
    print("Official Claude Code authorization opened in guest Chrome and closed on cancellation.")


if __name__ == "__main__":
    main()
