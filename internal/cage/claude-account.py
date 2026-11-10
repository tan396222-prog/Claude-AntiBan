#!/usr/bin/python3
"""Launch the official guest CLI and export only its subscription status fields."""
import runpy
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import tempfile
import time


if os.name == "nt":
    platform_path = Path(__file__).with_name("windows_cli_platform.py")
    if not platform_path.exists():
        platform_path = Path(__file__).parent.parent / "workbench/windows_cli_platform.py"
    _platform = runpy.run_path(platform_path)
    fcntl = _platform["FileLock"]
else:
    import fcntl

SHIM = "/usr/local/lib/cage-claude-bin"
OPEN = "/usr/local/libexec/cage-cli/bin/cage-open" if sys.platform == "darwin" else "/usr/local/bin/cage-open"
HELPER = "/usr/local/bin/cage-claude-account"
DEFAULT = "default"


def shared_root():
    return Path(os.environ["CAGE_CLI_SHARED_ROOT"]) if os.name == "nt" else Path.home() / ".local/share/cage/claude-shared"


class SharedResourceConflict(ValueError):
    pass


def profile(account_id):
    if account_id != DEFAULT and not re.fullmatch("[0-9a-f]{24}", account_id):
        raise ValueError("invalid account ID")
    root = Path(os.environ["CAGE_CLI_ACCOUNT_ROOT"]) if os.name == "nt" else Path.home() / ".local/share/cage/claude-accounts"
    return root / account_id


def config_directory(base):
    return Path.home() / ".claude" if base.name == DEFAULT else base / "config"


def cli_environment(base):
    env = os.environ.copy()
    # Executing the real binary must never recurse into the terminal's claude shim.
    path = [p for p in env.get("PATH", os.defpath).split(os.pathsep) if p and p != SHIM]
    env["PATH"] = os.pathsep.join([str(Path.home() / ".local/bin"), *path])
    if sys.platform == "darwin" and Path("/usr/local/libexec/cage-cli/bin/claude").is_file():
        env["PATH"] = "/usr/local/libexec/cage-cli/bin:/usr/local/bin:" + env["PATH"]
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


def identity(binary, env, cwd, settings=(), timeout=10, pass_fds=()):
    result = subprocess.run([binary, *settings, "auth", "status"], cwd=cwd, env=env, timeout=timeout,
                            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, pass_fds=pass_fds)
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
    if os.name == "nt":
        policy = Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "ClaudeCode"
    elif sys.platform == "darwin":
        policy = Path("/Library/Application Support/ClaudeCode")
    else:
        policy = Path("/etc/claude-code")
    paths = [config_directory(base) / "settings.json", policy / "managed-settings.json"]
    if os.name != "nt":
        paths.extend((policy / "managed-settings.d").glob("*.json"))
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


def shared_profile_resources(base):
    """Share local customizations; credentials, synced skills and history stay private."""
    config = config_directory(base)
    common = Path.home() / ".claude"
    common.mkdir(mode=0o700, parents=True, exist_ok=True)
    if config == common:
        return
    lock_dir = shared_root()
    lock_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (lock_dir / "resources.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        conflict = "账号的技能或指令与共用资料同名冲突，请先核对 ~/.claude 中的对应文件；原文件已保留"
        def check(local, shared):
            if (local.is_symlink() or os.name == "nt" and local.is_junction()):
                if local.resolve() != shared.resolve():
                    raise SharedResourceConflict(conflict)
            elif local.exists() and shared.exists():
                if local.is_dir() and shared.is_dir():
                    for child in local.iterdir():
                        check(child, shared / child.name)
                elif not (local.is_file() and shared.is_file() and local.read_bytes() == shared.read_bytes()):
                    raise SharedResourceConflict(conflict)
        def merge(local, shared):
            if (local.is_symlink() or os.name == "nt" and local.is_junction()):
                if local.resolve() == shared.resolve():
                    return
                raise SharedResourceConflict(conflict)
            if local.exists():
                if local.is_dir() and shared.is_dir():
                    for child in list(local.iterdir()):
                        merge(child, shared / child.name)
                        if child.is_symlink():
                            child.unlink()
                    local.rmdir()
                elif not shared.exists():
                    shared.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                    local.rename(shared)
                elif local.is_file() and shared.is_file() and local.read_bytes() == shared.read_bytes():
                    # Keep a recoverable copy before replacing a local resource.
                    backup = lock_dir / "backups" / base.name / local.relative_to(config)
                    backup.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                    if not backup.exists():
                        shutil.copy2(local, backup)
                    local.unlink()
                else:
                    # Never choose which conflicting instruction or skill wins.
                    raise SharedResourceConflict(conflict)
            if not shared.exists():
                if local.name in ("CLAUDE.md", "AGENTS.md"):
                    shared.touch(mode=0o600)
                else:
                    shared.mkdir(mode=0o700, parents=True, exist_ok=True)
            local.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            if os.name == "nt":
                _platform["resource_link"](local, shared)
            else:
                local.symlink_to(shared, target_is_directory=shared.is_dir())
        # claude.ai downloads skills into skills/synced. Keep that subtree
        # account-specific; link only locally installed skill entries.
        local, shared = config / "skills", common / "skills"
        local.mkdir(mode=0o700, parents=True, exist_ok=True)
        shared.mkdir(mode=0o700, parents=True, exist_ok=True)
        names = {p.name for folder in (local, shared) for p in folder.iterdir()} - {"synced"}
        resources = [(config / name, common / name) for name in
                     ("commands", "agents", "rules", "agent-memory", "CLAUDE.md", "AGENTS.md")]
        resources += [(local / name, shared / name) for name in sorted(names)]
        # Validate every resource before moving any of them. A late conflict
        # must leave the account's existing directory layout usable.
        for source, destination in resources:
            check(source, destination)
        for source, destination in resources:
            if source.exists() and not source.is_symlink():
                backup = lock_dir / "backups" / base.name / source.relative_to(config)
                if backup.exists():
                    backup = backup.with_name(backup.name + "-" + str(time.time_ns()))
                backup.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                if source.is_dir():
                    shutil.copytree(source, backup, symlinks=True)
                else:
                    shutil.copy2(source, backup)
            merge(source, destination)


def shared_launch_arguments(base, args, settings_fd=None):
    """Apply a stable project memory location without sharing auth/settings files."""
    lock_dir = shared_root()
    lock_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (lock_dir / "resources.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        return shared_launch_arguments_locked(base, args, settings_fd)


def shared_launch_arguments_locked(base, args, settings_fd):
    directory = Path.cwd().resolve()
    try:
        git = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
                             cwd=directory, capture_output=True, text=True, timeout=2)
        if git.returncode == 0:
            common_git = Path(git.stdout.strip())
            if common_git.name == ".git":
                directory = common_git.parent.resolve()
    except (OSError, subprocess.SubprocessError):
        pass
    key = re.sub(r"[^a-zA-Z0-9]", "-", str(directory))
    if len(key) > 180:
        key = key[:140] + "-" + hashlib.sha256(str(directory).encode()).hexdigest()[:32]
    memory = Path.home() / ".claude/projects" / key / "memory"
    own_memory = config_directory(base) / "projects" / key / "memory"
    memory.mkdir(mode=0o700, parents=True, exist_ok=True)
    # Retain pre-existing account notes. Colliding files remain in their
    # original directory and block automatic consolidation until reviewed.
    if own_memory != memory and own_memory.exists() and not (own_memory.is_symlink() or os.name == "nt" and own_memory.is_junction()):
        sources = [source for source in own_memory.rglob("*") if source.is_file()]
        for source in sources:
            destination = memory / source.relative_to(own_memory)
            if destination.exists() and (not destination.is_file() or destination.read_bytes() != source.read_bytes()):
                raise SharedResourceConflict("账号的项目记忆与共用记忆冲突，原记忆已保留，请先核对")
        for source in sources:
            if source.is_file():
                destination = memory / source.relative_to(own_memory)
                if destination.exists() and destination.read_bytes() != source.read_bytes():
                    raise SharedResourceConflict("账号的项目记忆与共用记忆冲突，原记忆已保留，请先核对")
                destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                if not destination.exists():
                    shutil.copy2(source, destination)
        backup = shared_root() / "backups" / base.name / "projects" / key / "memory"
        backup.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if backup.exists():
            backup = backup.with_name(backup.name + "-" + str(time.time_ns()))
        own_memory.rename(backup)
        if os.name == "nt":
            _platform["resource_link"](own_memory, memory)
        else:
            own_memory.symlink_to(memory, target_is_directory=True)
    settings = {"autoMemoryDirectory": str(memory)}
    # Only local behavior is shared. Never copy env, apiKeyHelper, login/org
    # constraints, OAuth metadata, or account-synced remote configuration.
    common_settings = json_file(Path.home() / ".claude/settings.json")
    shared_keys = {"model", "permissions", "hooks", "statusLine", "attribution", "language",
                   "effortLevel", "alwaysThinkingEnabled", "includeCoAuthoredBy", "respectGitignore",
                   "spinnerTipsEnabled", "outputStyle", "disableAllHooks", "includeGitInstructions",
                   "theme", "skipDangerousModePermissionPrompt", "tui", "autoMemoryEnabled"}
    if isinstance(common_settings, dict):
        settings.update({key: value for key, value in common_settings.items() if key in shared_keys})
    # Respect an explicit memory location in existing user/project settings.
    paths = [Path.home() / ".claude/settings.json", config_directory(base) / "settings.json"]
    for parent in reversed((Path.cwd(), *Path.cwd().parents)):
        paths += [parent / ".claude/settings.json", parent / ".claude/settings.local.json"]
    for path in paths:
        stored = json_file(path)
        if path != Path.home() / ".claude/settings.json" and isinstance(stored, dict):
            # Let the CLI's normal profile/project precedence decide these
            # values. A shared default must not turn into a command-line override.
            for key in shared_keys & stored.keys():
                settings.pop(key, None)
        if isinstance(stored, dict) and "autoMemoryDirectory" in stored:
            settings["autoMemoryDirectory"] = stored["autoMemoryDirectory"]
    result, iterator = [], iter(args)
    for arg in iterator:
        if arg == "--":
            result += [arg, *iterator]
            break
        if arg == "--settings" or arg.startswith("--settings="):
            value = next(iterator) if arg == "--settings" else arg.split("=", 1)[1]
            supplied = json.loads(value) if value.lstrip().startswith("{") else json.loads(Path(value).read_text())
            settings.update(supplied)
        else:
            result.append(arg)
    data = json.dumps(settings, separators=(",", ":")).encode()
    if settings_fd is not None:
        # Mobile launch deliberately keeps settings in an anonymous file.
        # Sharing local defaults must not persist its provider material.
        with os.fdopen(os.dup(settings_fd), "r+b") as snapshot:
            snapshot.seek(0)
            snapshot.truncate()
            snapshot.write(data)
            snapshot.flush()
            snapshot.seek(0)
        return ["--settings", "/dev/fd/" + str(settings_fd), *result]
    folder = base / "launch-settings"
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    path = folder / (hashlib.sha256(data).hexdigest() + ".json")
    if not path.exists():
        with tempfile.NamedTemporaryFile(dir=folder, delete=False) as stream:
            stream.write(data)
            temporary = stream.name
        os.replace(temporary, path)
    return ["--settings", str(path), *result]


def main(args, *, mobile_settings_fd=None, verify_mobile=None):
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
    binary = os.environ.get("CAGE_CLI_BINARY") if os.name == "nt" else shutil.which("claude", path=env["PATH"])
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
        env.update(CAGE_CLAUDE_LOGIN=account_id)
        if os.name != "nt":
            env["BROWSER"] = OPEN
        # Set the handler before spawning so SSH/window closure also disposes
        # the ephemeral browser and its cookies.
        def interrupted(_signum, _frame):
            raise KeyboardInterrupt()
        if hasattr(signal, "SIGHUP"):
            signal.signal(signal.SIGHUP, interrupted)
        signal.signal(signal.SIGTERM, interrupted)
        try:
            (base / "quota.json").unlink(missing_ok=True)
            return subprocess.call([binary, "auth", "login", *([] if os.name == "nt" else ["--claudeai"])], cwd=Path.home(), env=env)
        finally:
            if os.name != "nt":
                subprocess.run([OPEN, "--finish-login"], env=env, timeout=8, check=False,
                               stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if mode == "run":
        shared_profile_resources(base)
        if not settings_flags(rest) and not has_status_line(base):
            rest = ["--settings", json.dumps({"statusLine": {"type": "command", "command": f"{HELPER} statusline {account_id}"}}), *rest]
        rest = shared_launch_arguments(base, rest, mobile_settings_fd)
        # Profile/project settings can override inherited authentication. Check
        # the actual CLI method in the project before launching the conversation.
        options = settings_flags(rest)
        # A mobile turn verifies and executes with the same anonymous settings
        # snapshot. Keep its descriptor open across the auth-status child.
        pass_fds = () if mobile_settings_fd is None else (mobile_settings_fd,)
        reading = identity(binary, env, Path.cwd(), options, pass_fds=pass_fds) if expected else None
        if not expected or subject(reading) != expected:
            print("此项目未使用所选 Claude 订阅。请检查项目配置和账号登录状态。", file=sys.stderr)
            return 1
        if mobile_settings_fd is not None:
            verify_mobile(reading)
            os.lseek(mobile_settings_fd, 0, os.SEEK_SET)
        # The private profile is ready. Claude and its tools must create files
        # in the shared project using the user's usual umask.
        os.umask(original_umask)
        if os.name == "nt":
            return subprocess.call([binary, *rest], env=env)
        os.execve(binary, [binary, *rest], env)
    raise ValueError("unknown operation")


def macos_open(args):
    login = os.environ.get("CAGE_CLAUDE_LOGIN", "")
    if len(args) != 1 or not re.fullmatch("[0-9a-f]{24}", login):
        return 2
    finish = args[0] == "--finish-login"
    data = {"action": "login-finish" if finish else "login-open", "login": login}
    if not finish:
        from urllib.parse import urlsplit
        url = urlsplit(args[0])
        if len(args[0]) > 8192 or url.scheme != "https" or url.username or url.password or url.hostname not in (
                "claude.ai", "claude.com", "console.anthropic.com", "platform.claude.com", "platform.anthropic.com"):
            return 2
        data["url"] = args[0]
    address = Path.home() / "Library/Application Support/Cage/Login/open.sock"
    owner = address.lstat()
    if not stat.S_ISSOCK(owner.st_mode) or owner.st_uid != os.getuid() or owner.st_mode & 0o077:
        return 1
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(12)
        client.connect(str(address))
        client.sendall(json.dumps(data).encode())
        client.shutdown(socket.SHUT_WR)
        with client.makefile("rb") as reply:
            return 0 if reply.read(4) == b"OK" else 1


if __name__ == "__main__":
    try:
        sys.exit(macos_open(sys.argv[1:]) if sys.platform == "darwin" and Path(sys.argv[0]).name == "cage-open" else main(sys.argv[1:]))
    except KeyboardInterrupt:
        sys.exit(130)
    except SharedResourceConflict as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, subprocess.SubprocessError, TypeError):
        # No raw CLI output, paths, credentials or callback URLs go to logs.
        print("无法读取 Claude 订阅状态或启动官方 CLI，请检查环境组件与登录状态。", file=sys.stderr)
        sys.exit(1)
