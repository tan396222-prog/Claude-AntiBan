#!/usr/bin/python3
"""Launch the official guest CLI and export only its subscription status fields."""
import fcntl
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time


SHIM = "/usr/local/lib/cage-claude-bin"
OPEN = "/usr/local/bin/cage-open"
HELPER = "/usr/local/bin/cage-claude-account"
DEFAULT = "default"


def profile(account_id):
    if account_id != DEFAULT and not re.fullmatch("[0-9a-f]{24}", account_id):
        raise ValueError("invalid account ID")
    return Path.home() / ".local/share/cage/claude-accounts" / account_id


def config_directory(base):
    return Path.home() / ".claude" if base.name == DEFAULT else base / "config"


def cli_environment(base):
    env = os.environ.copy()
    # Executing the real binary must never recurse into the terminal's claude shim.
    path = [p for p in env.get("PATH", os.defpath).split(os.pathsep) if p and p != SHIM]
    env["PATH"] = os.pathsep.join([str(Path.home() / ".local/bin"), *path])
    if base.name == DEFAULT:
        # Let Claude resolve its normal ~/.claude.json and ~/.claude itself.
        # Preserve provider variables: an API-authenticated shell is not a
        # signed-in default subscription, even if a previous login exists.
        return env
    env["CLAUDE_CONFIG_DIR"] = str(config_directory(base))
    for name in ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
                 "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
                 "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_GATEWAY"):
        env.pop(name, None)
    return env


def identity(binary, env, cwd, settings=(), timeout=10):
    result = subprocess.run([binary, *settings, "auth", "status"], cwd=cwd, env=env, timeout=timeout,
                            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    # Exit code 1 with JSON is the CLI's documented logged-out response. Any
    # other failure (including missing JSON) must not sign a known user out.
    data = json.loads(result.stdout)
    reported_dir = data.get("configDirectory")
    correct_dir = isinstance(reported_dir, str) and Path(reported_dir) == Path(env.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude")
    if result.returncode not in (0, 1) or not correct_dir:
        raise ValueError("unexpected auth status")
    ok = (result.returncode == 0 and data.get("loggedIn") is True and data.get("authMethod") == "claude.ai"
          and data.get("apiProvider") == "firstParty")
    if not ok:
        return {"logged_in": False}
    return {"logged_in": True, "email": data.get("email"), "organization": data.get("orgId") or data.get("orgName"),
            "plan": data.get("subscriptionType")}


def subject(summary):
    if not summary.get("logged_in") or not summary.get("email"):
        return None
    email = summary["email"].strip().lower()
    organization = (summary.get("organization") or "").strip()
    return hashlib.sha256(f"{email}\n{organization}".encode()).hexdigest()


def json_file(path):
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        return {}


def valid_window(data, now):
    if not isinstance(data, dict):
        return None
    used, reset = data.get("used_percentage"), data.get("resets_at")
    if type(used) not in (int, float) or not 0 <= used <= 100:
        return None
    if type(reset) != int or (reset != 0 and reset <= now):
        return None
    return {"used_percentage": used, "resets_at": reset}


def read_quota(base):
    try:
        stored = json_file(base / "quota.json")
    except (ValueError, OSError):
        return None
    if not isinstance(stored, dict):
        return None
    digest = stored.get("identity", "")
    seen = stored.get("observed_at")
    now = int(time.time())
    if not isinstance(digest, str) or not re.fullmatch("[a-f0-9]{64}", digest) or type(seen) != int or not 0 < seen <= now:
        return None
    result = {}
    for key in ("five_hour", "seven_day"):
        window = valid_window(stored.get(key), now)
        if window:
            window_seen = stored[key].get("observed_at", seen)
            if type(window_seen) == int and 0 < window_seen <= now:
                result[key] = {**window, "observed_at": window_seen}
    if result:
        return {**result, "identity": digest, "observed_at": seen}
    return None


def save_usage(base, data, expected, confirm_subject, query_started=None, deadline=None):
    rate = data.get("rate_limits")
    if not isinstance(rate, dict):
        return
    now = int(time.time())
    with (base / "quota.lock").open("a") as lock:
        if deadline is None:
            fcntl.flock(lock, fcntl.LOCK_EX)
        else:
            while True:
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    if time.monotonic() >= deadline:
                        raise TimeoutError("usage cache busy")
                    time.sleep(.05)
        path = base / "quota.json"
        previous = read_quota(base) or {}
        if previous.get("identity") != expected:
            previous = {}
        windows = {}
        changed = False
        for key in ("five_hour", "seven_day"):
            old = previous.get(key)
            new = valid_window(rate.get(key), now)
            # A redraw of an older terminal must not roll back the more recent
            # account window observed in another conversation.
            redraw = query_started is None and old and new and all(old[field] == new[field] for field in ("resets_at", "used_percentage"))
            older = old and new and (0 < new["resets_at"] < old["resets_at"] or
                    (query_started is None and old["resets_at"] == new["resets_at"] and new["used_percentage"] < old["used_percentage"]))
            if old and (not new or redraw or older or (query_started is not None and old["observed_at"] > query_started)):
                windows[key] = old
            elif new:
                windows[key] = {**new, "observed_at": now}
                changed = True
        # A user can run /login in an open Claude process. Ask the CLI for
        # its identity on changed readings only; never infer it from a path.
        if changed and expected and confirm_subject() == expected:
            windows["observed_at"] = now
            windows["identity"] = expected
            with tempfile.NamedTemporaryFile(mode="w", dir=base, delete=False) as tmp:
                json.dump(windows, tmp, separators=(",", ":"))
            os.replace(tmp.name, path)


def reported_window(percent, stamp, now):
    reset = 0
    if stamp is not None:
        try:
            dt = datetime.fromisoformat(stamp.replace("Z", "+00:00"))
            if dt.tzinfo is None:
                return None
            reset = int(dt.timestamp())
        except (AttributeError, ValueError, OverflowError):
            return None
    return valid_window({"used_percentage": percent, "resets_at": reset}, now)


def usage_windows(raw, now):
    # SDKControlGetUsageResponse is the structured /usage report. The input
    # contains one control request and no user prompt or generate request.
    report = None
    for line in raw.splitlines():
        message = json.loads(line)
        if message.get("type") in ("user", "assistant"):
            raise ValueError("unexpected conversation during usage query")
        if message.get("type") == "result" and (message.get("is_error") or message.get("num_turns") != 0):
            raise ValueError("unexpected inference result")
        if message.get("type") == "control_response":
            response = message.get("response") or {}
            if response.get("request_id") == "quota":
                if report is not None or response.get("subtype") != "success":
                    raise ValueError("failed usage response")
                report = response.get("response")
    if not isinstance(report, dict) or type(report.get("rate_limits_available")) is not bool:
        raise ValueError("missing structured usage response")
    session = report.get("session")
    if not isinstance(session, dict) or session.get("total_cost_usd") != 0 or session.get("model_usage"):
        raise ValueError("unexpected model use")
    if not report["rate_limits_available"]:
        return False, {}
    rate = report.get("rate_limits") or {}
    windows = {}
    for key in ("five_hour", "seven_day"):
        meter = rate.get(key)
        if isinstance(meter, dict):
            reading = reported_window(meter.get("utilization"), meter.get("resets_at"), now)
            if reading:
                windows[key] = reading
    # Some servers supply the unscoped windows in limits[] instead. A
    # model-specific quota never substitutes for an account's weekly window.
    candidates = {"session": "five_hour", "weekly_all": "seven_day"}
    rows = {}
    ambiguous = set()
    for row in rate.get("limits") or ():
        if not isinstance(row, dict):
            continue
        key = candidates.get(row.get("kind"))
        if not key or key in windows or key in ambiguous:
            continue
        reading = reported_window(row.get("percent"), row.get("resets_at"), now)
        if reading and key in rows and rows[key] != reading:
            ambiguous.add(key)
            del rows[key]
        elif reading:
            rows[key] = reading
    return True, {**rows, **windows}


def changed_identity():
    return {"logged_in": False, "usage_status": "identity_changed", "usage_checked_at": int(time.time())}


def invoke_usage(binary, env, cwd, options, timeout):
    command = [binary, *options, "-p", "--input-format", "stream-json", "--verbose", "--output-format", "stream-json",
               "--no-session-persistence", "--strict-mcp-config", "--tools", ""]
    request = {"type": "control_request", "request_id": "quota", "request": {"subtype": "get_usage", "skip_behaviors": True}}
    result = subprocess.run(command, cwd=cwd, env=env, timeout=timeout, input=(json.dumps(request) + "\n").encode(),
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    if result.returncode != 0 or len(result.stdout) > (2 << 20):
        raise ValueError("usage command failed")
    return usage_windows(result.stdout, int(time.time()))


def query_usage(binary, env, base, expected):
    if not expected or not re.fullmatch("[a-f0-9]{64}", expected):
        raise ValueError("missing expected account")
    deadline = time.monotonic() + 12
    started = int(time.time())
    status = "error"
    summary = {}
    options = ["--setting-sources", "user", "--settings", '{"disableAllHooks":true}']
    with tempfile.TemporaryDirectory(prefix="cage-claude-usage-") as cwd:
        try:
            summary = identity(binary, env, cwd, options, timeout=4)
            if subject(summary) != expected:
                return changed_identity()
            # Reserve time to recheck the actual identity after the command.
            timeout = min(8, deadline - time.monotonic() - 2)
            try:
                available, windows = invoke_usage(binary, env, cwd, options, max(.1, timeout))
            except (OSError, ValueError, TypeError, AttributeError, subprocess.SubprocessError):
                windows = None
            after = identity(binary, env, cwd, options, timeout=max(.1, min(4, deadline - time.monotonic())))
            if subject(after) != expected:
                return changed_identity()
            summary = after
            if windows:
                base.mkdir(mode=0o700, parents=True, exist_ok=True)
                save_usage(base, {"rate_limits": windows}, expected, lambda: expected, started, deadline)
            if windows is not None:
                status = {0: "unavailable", 1: "partial", 2: "reported"}[len(windows)] if available else "not_applicable"
        except (OSError, ValueError, TypeError, AttributeError, subprocess.SubprocessError):
            # The CLI can print project/account data even on failure.
            # The host needs a reason category and only verified window data.
            pass
    if subject(summary) == expected:
        previous = read_quota(base)
        if previous and previous["identity"] == expected:
            summary["quota"] = previous
            if status == "unavailable":
                status = "cached"
    summary.update(usage_status=status, usage_checked_at=int(time.time()))
    return summary


def has_status_line(base):
    # Preserve existing user, project and managed status lines, including an
    # explicit disable. If a settings file is unreadable, defer to Claude.
    paths = [config_directory(base) / "settings.json", Path("/etc/claude-code/managed-settings.json")]
    paths.extend(Path("/etc/claude-code/managed-settings.d").glob("*.json"))
    for folder in (Path.cwd(), *Path.cwd().parents):
        paths += [folder / ".claude/settings.json", folder / ".claude/settings.local.json"]
    for path in paths:
        try:
            settings = json_file(path)
            if not isinstance(settings, dict) or "statusLine" in settings or settings.get("allowManagedHooksOnly"):
                return True
        except (OSError, ValueError):
            return True
    return False


def settings_flags(args):
    flags = []
    iterator = iter(args)
    for arg in iterator:
        if arg == "--":
            break
        key, equals, _ = arg.partition("=")
        if key in ("--settings", "--setting-sources"):
            flags.append(arg)
            if not equals:
                value = next(iterator, None)
                if value is None:
                    raise ValueError("missing CLI setting")
                flags.append(value)
    return flags


def main(args):
    mode, account_id, *rest = args
    if mode == "quotas":
        result = {}
        for candidate in [account_id, *rest]:
            quota = read_quota(profile(candidate))
            if quota:
                result[candidate] = quota
        print(json.dumps(result))
        return 0
    base = profile(account_id)
    if account_id == DEFAULT and mode == "login":
        raise ValueError("new logins require a private profile")
    if mode in ("usage", "snapshot") and account_id == DEFAULT and not config_directory(base).is_dir():
        print(json.dumps(changed_identity() if mode == "usage" else {"logged_in": False}))
        return 0
    original_umask = os.umask(0o077)
    if account_id != DEFAULT:
        config_directory(base).mkdir(mode=0o700, parents=True, exist_ok=True)
    elif mode == "statusline":
        base.mkdir(mode=0o700, parents=True, exist_ok=True)
    env = cli_environment(base)
    if account_id == DEFAULT:
        directory = env.get("CLAUDE_CONFIG_DIR") or str(config_directory(base))
        endpoint = env.get("ANTHROPIC_BASE_URL", "").rstrip("/")
        if Path(directory) != config_directory(base) or endpoint not in ("", "https://api.anthropic.com"):
            if mode in ("snapshot", "usage"):
                print(json.dumps(changed_identity() if mode == "usage" else {"logged_in": False}))
                return 0
            raise ValueError("not the default subscription configuration")
    binary = shutil.which("claude", path=env["PATH"])
    if not binary:
        raise ValueError("Claude is not installed")
    if mode == "usage":
        if len(rest) != 1:
            raise ValueError("usage requires an expected account")
        print(json.dumps(query_usage(binary, env, base, rest[0])))
        return 0
    expected = env.get("CAGE_CLAUDE_SUBJECT")
    if mode == "statusline":
        raw = sys.stdin.buffer.read((1 << 20) + 1)
        if len(raw) <= 1 << 20:
            save_usage(base, json.loads(raw), expected, lambda: subject(identity(binary, env, Path.cwd())))
        print("Cage · Claude")
        return 0
    if mode == "snapshot":
        summary = identity(binary, env, Path.home())
        quota = read_quota(base)
        if quota and quota["identity"] == subject(summary):
            summary["quota"] = quota
        print(json.dumps(summary))
        return 0
    if mode == "login":
        env.update(BROWSER=OPEN, CAGE_CLAUDE_LOGIN=account_id)
        # Set the handler before spawning so SSH/window closure also disposes
        # the ephemeral browser and its cookies.
        def interrupted(_signum, _frame):
            raise KeyboardInterrupt()
        signal.signal(signal.SIGHUP, interrupted)
        signal.signal(signal.SIGTERM, interrupted)
        try:
            (base / "quota.json").unlink(missing_ok=True)
            return subprocess.call([binary, "auth", "login", "--claudeai"], cwd=Path.home(), env=env)
        finally:
            subprocess.run([OPEN, "--finish-login"], env=env, timeout=8, check=False,
                           stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if mode == "run":
        # Profile/project settings can override inherited authentication. Check
        # the actual CLI method in the project before launching the conversation.
        options = settings_flags(rest)
        if not expected or subject(identity(binary, env, Path.cwd(), options)) != expected:
            print("此项目未使用所选 Claude 订阅。请检查项目配置和账号登录状态。", file=sys.stderr)
            return 1
        flags = []
        if not options and not has_status_line(base):
            settings = {"statusLine": {"type": "command", "command": f"{HELPER} statusline {account_id}"}}
            flags = ["--settings", json.dumps(settings, separators=(",", ":"))]
        # The private profile is ready. Claude and its tools must create files
        # in the shared project using the user's usual umask.
        os.umask(original_umask)
        os.execve(binary, [binary, *flags, *rest], env)
    raise ValueError("unknown operation")


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except KeyboardInterrupt:
        sys.exit(130)
    except (OSError, ValueError, subprocess.SubprocessError, TypeError):
        # No raw CLI output, paths, credentials or callback URLs go to logs.
        print("无法读取 Claude 订阅状态或启动官方 CLI，请检查环境组件与登录状态。", file=sys.stderr)
        sys.exit(1)
