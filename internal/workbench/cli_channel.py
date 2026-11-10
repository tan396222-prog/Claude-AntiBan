"""Guest-side Claude terminal discovery and authenticated local channel bridge."""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import shlex
import shutil
import signal
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import uuid


if os.name == "nt":
    _platform = runpy.run_path(Path(__file__).with_name("windows_cli_platform.py"))
    fcntl = _platform["FileLock"]
else:
    import fcntl

def process_fingerprint(pid):
    if os.name == "nt":
        return _platform["process_fingerprint"](pid)
    return subprocess.run(["ps", "-o", "lstart=,comm=", "-p", str(pid)], capture_output=True, text=True, timeout=2).stdout

def terminate_cli(pid, force=False):
    if os.name == "nt":
        return _platform["terminate_cli"](pid, force)
    return os.kill(int(pid), signal.SIGKILL if force else signal.SIGTERM)

ROOT = Path(os.environ["CAGE_CLI_STATE_ROOT"]) if os.name == "nt" else Path.home() / ".local/share/cage/cli-channels"
PROMPT_LIMIT = 32 << 10
REPLY_LIMIT = 128 << 10
HISTORY_LIMIT = 448 << 10


def identifier(value):
    return str(uuid.UUID(value))


def read_json(path, default=None):
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        return default


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as stream:
        json.dump(value, stream, ensure_ascii=False)
        temporary = stream.name
    os.replace(temporary, path)


@contextlib.contextmanager
def state_file(connection):
    folder = ROOT / identifier(connection)
    folder.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (folder / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        path = folder / "state.json"
        state = read_json(path, {})
        yield state
        save(path, state)


def live(pid):
    if os.name == "nt":
        return _platform["process_live"](pid)
    try:
        os.kill(int(pid), 0)
        return True
    except (OSError, ValueError, TypeError):
        return False


def config_directory(account):
    if account == "default":
        return Path(os.environ.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude").resolve()
    if not re.fullmatch(r"[0-9a-f]{24}", account):
        raise ValueError("invalid account")
    root = Path(os.environ["CAGE_CLI_ACCOUNT_ROOT"]) if os.name == "nt" else Path.home() / ".local/share/cage/claude-accounts"
    return root / account / "config"


def workspace_within(path, root):
    if os.name == "nt":
        path, root = path.replace("\\", "/").lower(), root.replace("\\", "/").lower()
    return path == root or path.startswith(root.rstrip("/") + "/")


def text_content(message):
    content = message.get("content", [])
    if isinstance(content, str):
        return content
    return "\n".join(item.get("text", "") for item in content if isinstance(item, dict) and item.get("type") == "text")


def clip_text(text, limit):
    return text.encode("utf-8")[:limit].decode("utf-8", errors="ignore")


def channel_message(text):
    match = re.match(r'^<channel\s+[^>]*cage_request="([0-9a-f-]{36})"[^>]*>\n?', text)
    if not match or not text.endswith("</channel>"):
        return None
    # Only remove the framing newline. The user's text can itself contain XML
    # tags, including </channel>, and must remain an opaque message body.
    body = text[match.end():-len("</channel>")]
    return match.group(1), body.removesuffix("\n")


def update_tools(turn, role, content):
    if not isinstance(content, list):
        return
    tools = turn.get("tools", [])
    by_id = {tool["id"]: tool for tool in tools}
    for block in content:
        if not isinstance(block, dict):
            continue
        kind = block.get("type")
        if role == "user" and kind == "tool_result":
            result_id = block.get("tool_use_id")
            tool = by_id.get(result_id) if isinstance(result_id, str) else None
            if tool:
                tool["status"] = "failed" if block.get("is_error") else "completed"
        if role != "assistant" or kind not in ("tool_use", "server_tool_use"):
            continue
        key, name = block.get("id"), block.get("name")
        if not isinstance(key, str) or not 0 < len(key.encode()) <= 128 or key in by_id or len(tools) >= 64:
            continue
        if not isinstance(name, str) or not re.fullmatch(r"[a-zA-Z0-9_.:-]{1,128}", name):
            continue
        tool = {"id": key, "name": name, "status": "called"}
        args = block.get("input")
        if isinstance(args, dict):
            for field in ("description", "subject", "file_path", "pattern", "query", "skill", "subagent_type"):
                value = args.get(field)
                if isinstance(value, str) and value.strip():
                    tool["detail"] = value.strip()[:160]
                    break
        by_id[key] = tool
        tools.append(tool)
    if tools:
        turn["tools"] = tools


def transcript(path):
    turns, seen, workspace, title = [], set(), "", ""
    if not path or not path.is_file() or path.is_symlink():
        return turns, workspace, title, seen
    with path.open() as stream:
        for line in stream:
            if len(line) > 4 * 1024 * 1024:
                continue
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("isSidechain"):
                continue
            workspace = row.get("cwd") or workspace
            if row.get("type") == "custom-title":
                title = row.get("customTitle") or title
            key = row.get("uuid")
            if not key or key in seen:
                continue
            seen.add(key)
            message = row.get("message") or {}
            if turns:
                update_tools(turns[-1], row.get("type"), message.get("content"))
            text = text_content(message)
            if not text:
                continue
            if row.get("type") == "assistant" and message.get("model") == "<synthetic>" and text == "No response requested.":
                continue
            if row.get("type") == "user":
                channel = channel_message(text) if row.get("isMeta") else None
                if channel:
                    key, text = channel
                    seen.add(key)
                elif row.get("isCompactSummary") or (row.get("isMeta") and not text.lstrip().startswith("<task-notification>")):
                    continue
                turns.append({"id": key, "prompt": clip_text(text, PROMPT_LIMIT), "prompt_truncated": len(text.encode()) > PROMPT_LIMIT, "text": "", "status": "completed"})
                turns = turns[-256:]
            elif row.get("type") == "assistant" and turns:
                turn = turns[-1]
                value = turn["text"] + ("\n\n" if turn["text"] else "") + text
                turn["text"], turn["truncated"] = clip_text(value, REPLY_LIMIT), len(value.encode()) > REPLY_LIMIT
    return turns, workspace, title, seen


def connection_for(session_id, config):
    matches = []
    for path in ROOT.glob("*/state.json"):
        state = read_json(path, {})
        if state.get("cli_id") == session_id and state.get("config") == str(config):
            matches.append((path.parent.name, state))
    return max(matches, key=lambda item: (live(item[1].get("pid")) and not item[1].get("closed"), live(item[1].get("channel_pid")), item[1].get("updated_at", 0)), default=("", {}))


def view(config, session_id, path=None):
    connection, state = connection_for(session_id, config)
    path = path or next((config / "projects").glob("*/" + session_id + ".jsonl"), None)
    native_history = path is not None
    if path is None and state.get("resume_transcript"):
        frozen = Path(state["resume_transcript"])
        if frozen.parent.parent == ROOT.parent / "cli-account-switches" and frozen.name == "history.jsonl":
            path = frozen
    turns, workspace, title, received = transcript(path)
    has_history = bool(turns)
    if connection and any(sent["id"] in received for sent in state.get("sent", [])):
        with state_file(connection) as latest:
            latest["sent"] = [sent for sent in latest.get("sent", []) if sent["id"] not in received]
            state = dict(latest)
    running = live(state.get("pid")) and not state.get("closed")
    channel = running and live(state.get("channel_pid")) and not state.get("blocked") and not state.get("policy_unavailable") and not state.get("native_ccs_unverified")
    known = {turn["id"] for turn in turns}
    for sent in state.get("sent", []):
        if sent["id"] not in known:
            turns.append({"id": sent["id"], "prompt": sent["text"], "text": "", "status": "interrupted" if sent.get("interrupted") else "queued"})
    display = state.get("display")
    if turns and display and state.get("busy") and display.get("text"):
        text = display["text"]
        if not turns[-1]["text"].endswith(text):
            turns[-1]["text"] = clip_text((turns[-1]["text"] + "\n\n" + text).strip(), REPLY_LIMIT)
    if turns:
        if state.get("account_failure"):
            turns[-1]["status"] = "failed"
        elif state.get("stopped"):
            turns[-1]["status"] = "stopped"
        elif running and state.get("busy"):
            turns[-1]["status"] = "running"
        elif not running and turns[-1]["status"] == "queued":
            turns[-1]["status"] = "interrupted"
        approvals = state.get("approvals", [])
        if running and approvals:
            turns[-1]["status"] = "running"
            turns[-1]["approval"] = {key: value for key, value in approvals[0].items() if key != "relay_id"}
    shortened = any(turn.get("prompt_truncated") for turn in turns)
    while len(turns) > 1 and len(json.dumps(turns, ensure_ascii=False).encode()) > HISTORY_LIMIT:
        turns.pop(0)
        shortened = True
    modified = max(path.stat().st_mtime if path else 0, float(state.get("updated_at", 0)))
    notice = state.get("native_ccs_notice", "")
    if state.get("blocked"):
        notice = "这台主机的 Claude Code 管理策略关闭了 Channels，请由主机管理员处理。"
    elif state.get("policy_unavailable"):
        notice = "Cage 默认环境的通道配置未能更新，请在电脑更新环境组件。"
    return {"cli_id": session_id, "config": str(config), "connection": connection, "native_ccs_unverified": bool(state.get("native_ccs_unverified")),
            "workspace": workspace or state.get("workspace", ""), "title": title or (turns[0]["prompt"][:40] if turns else "Claude Code"),
            "updated_at": modified, "turns": turns, "running": bool(running), "active": bool(running and (state.get("busy") or state.get("approvals"))), "channel_ready": bool(channel), "can_send": bool(channel and not state.get("resume_task")), "has_history": has_history, "history_truncated": shortened,
            "connection_notice": "" if channel else notice, "account_failure": state.get("account_failure", ""),
            "resume_pending": bool(state.get("resume_task")), "native_history": native_history}


def history_summary(path):
    workspace, title = "", ""
    with path.open() as stream:
        for _ in range(64):
            line = stream.readline(4 << 20)
            if not line:
                break
            try:
                row = json.loads(line)
            except ValueError:
                continue
            workspace = row.get("cwd") or workspace
            if row.get("type") == "user":
                text = text_content(row.get("message") or {})
                channel = channel_message(text) if row.get("isMeta") else None
                if channel:
                    title = channel[1][:40]
                elif not row.get("isMeta"):
                    title = text[:40]
            if workspace and title:
                break
    # Names and directory changes are appended to the transcript. Read a
    # bounded tail for the list, and load full message history only on opening.
    with path.open("rb") as stream:
        stream.seek(max(0, path.stat().st_size - (64 << 10)))
        lines = stream.read().splitlines()
    for line in lines:
        try:
            row = json.loads(line)
        except (ValueError, UnicodeDecodeError):
            continue
        workspace = row.get("cwd") or workspace
        if row.get("type") == "custom-title":
            title = row.get("customTitle") or title
    return workspace, title


def discover(request):
    allowed = request["workspaces"]
    sessions = []
    configs = {str(config_directory(account)) for account in request["accounts"]}
    for state_path in ROOT.glob("*/state.json"):
        state = read_json(state_path, {})
        if state.get("config"):
            configs.add(state["config"])
    for config_name in configs:
        config = Path(config_name)
        running_ids = None
        def observed_running():
            nonlocal running_ids
            if running_ids is None:
                running_ids = {row.get("sessionId") for row in running_sessions(config_name)}
            return running_ids
        paths = sorted((config / "projects").glob("*/*.jsonl"), key=lambda p: p.stat().st_mtime, reverse=True)
        found = set()
        for path in paths:
            if path.is_symlink():
                continue
            try:
                session_id = identifier(path.stem)
            except ValueError:
                continue
            workspace, title = history_summary(path)
            if not any(workspace_within(workspace, root) for root in allowed):
                continue
            connection, state = connection_for(session_id, config)
            item = {"cli_id": session_id, "config": config_name, "connection": connection,
                    "workspace": workspace, "title": title or "Claude Code", "updated_at": path.stat().st_mtime,
                    "running": session_id in observed_running() or bool(live(state.get("pid")) and not state.get("closed")),
                    "active": bool(live(state.get("pid")) and not state.get("closed") and (state.get("busy") or state.get("approvals")))}
            found.add(session_id)
            sessions.append(item)
        for state_path in ROOT.glob("*/state.json"):
            state = read_json(state_path, {})
            session_id = state.get("cli_id")
            workspace = state.get("workspace", "")
            if state.get("config") != config_name or not session_id or session_id in found or not any(workspace_within(workspace, root) for root in allowed):
                continue
            item = view(config, session_id)
            item["running"] = item["running"] or session_id in observed_running()
            item.pop("turns")
            sessions.append(item)
    return sessions


def hook(connection):
    payload = json.load(sys.stdin)
    event = payload.get("hook_event_name")
    with state_file(connection) as state:
        if state.get("account_switch"):
            if event == "UserPromptSubmit":
                return {"decision": "block", "reason": "Cage 正在切换账号，请在目标会话中继续。"}
            if event == "PreToolUse":
                return {"hookSpecificOutput": {"hookEventName": event, "permissionDecision": "deny", "permissionDecisionReason": "Cage 正在切换账号。"}}
        state["updated_at"] = time.time()
        state["cli_id"] = identifier(payload["session_id"])
        state["workspace"] = payload.get("cwd", state.get("workspace", ""))
        state["config"] = str(config_directory("default"))
        if event == "SessionStart":
            state["closed"] = False
        if event in ("UserPromptSubmit", "PreToolUse"):
            state["busy"] = True
        if event == "UserPromptSubmit":
            state.pop("account_failure", None)
            state.pop("stopped", None)
            state.pop("pending_send", None)
        if event in ("UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd"):
            state["approvals"] = []
        if event in ("Stop", "StopFailure", "SessionEnd"):
            state["busy"] = False
            state.pop("display", None)
        if event == "StopFailure":
            state["account_failure"] = payload.get("error", "unknown")
        if event == "SessionEnd":
            state["closed"] = True
        if event == "MessageDisplay":
            current = state.get("display") or {}
            if current.get("id") != payload.get("message_id"):
                current = {"id": payload.get("message_id"), "text": "", "index": -1}
            if payload.get("index", 0) > current["index"]:
                current["text"] = clip_text(current["text"] + payload.get("delta", ""), REPLY_LIMIT)
                current["index"] = payload.get("index", 0)
            state["display"] = current
    return {}


def migrate_cage_policy():
    if os.name == "nt":
        # Organizational Windows policy is never rewritten by a GUI user.
        return None
    path = Path("/etc/claude-code/managed-settings.json")
    original = {"permissions": {"defaultMode": "bypassPermissions"}, "tui": "fullscreen"}
    if sys.platform != "linux":
        return
    try:
        if read_json(path) != original:
            return
    except (OSError, ValueError):
        return False
    # Upgrade only the exact policy shipped by Cage. An administrator's
    # explicit settings, including channelsEnabled=false, take precedence.
    migration = '''import json, pathlib, tempfile, os
p = pathlib.Path('/etc/claude-code/managed-settings.json')
old = p.read_bytes()
value = json.loads(old)
if value == {"permissions": {"defaultMode": "bypassPermissions"}, "tui": "fullscreen"}:
    backup = p.with_name('managed-settings.json.cage-before-channels')
    if not backup.exists():
        backup.write_bytes(old)
        backup.chmod(0o600)
    value['channelsEnabled'] = True
    with tempfile.NamedTemporaryFile(dir=p.parent, mode='w', delete=False) as f:
        json.dump(value, f)
        name = f.name
    os.chmod(name, 0o644)
    os.replace(name, p)
'''
    try:
        subprocess.run(["/usr/bin/sudo", "-n", "python3", "-I", "-c", migration], timeout=10,
                       stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    except (OSError, subprocess.SubprocessError):
        return False
    return True


def active_session(session_id, config=""):
    return bool(active_sessions(session_id, config))


def active_sessions(session_id, config=""):
    return [row for row in running_sessions(config) if row.get("sessionId") == session_id]


def running_sessions(config=""):
    env = dict(os.environ)
    if config:
        env["CLAUDE_CONFIG_DIR"] = config
    listing = subprocess.run(["claude", "agents", "--json"], env=env, capture_output=True, text=True, timeout=10)
    if listing.returncode != 0:
        raise ValueError("无法检查原会话是否仍在运行")
    return [row for row in json.loads(listing.stdout) if live(row.get("pid"))]


def handoff_session(request):
    session_id = identifier(request["cli_id"])
    config = request["config"]
    workspace = request["workspace"]
    rows = active_sessions(session_id, config)
    # A live shared channel is reusable even after the host service restarts.
    _, state = connection_for(session_id, Path(config))
    if live(state.get("pid")) and live(state.get("channel_pid")) and not state.get("closed"):
        return {"running": True, "channel_ready": True}
    for row in rows:
        if row.get("cwd") != workspace:
            raise ValueError("原会话的项目已变化，请重新读取会话列表")
        # A waiting CLI has no executing tool. Resume its task on the shared
        # channel so any permission decision is requested again on the phone.
        if row.get("status") not in ("idle", "waiting") or row.get("kind") != "interactive":
            return {"running": True}
    for row in rows:
        # Refresh the supported CLI registry immediately before signalling so
        # stale discovery data cannot select a different process or workspace.
        current = active_sessions(session_id, config)
        matched = next((item for item in current if item.get("pid") == row["pid"] and item.get("cwd") == workspace and item.get("kind") == "interactive" and item.get("status") in ("idle", "waiting")), None)
        if matched is None:
            return {"running": bool(current)}
        if matched["status"] == "waiting":
            save(handoff_receipt(config, session_id), {"resume_task": True, "permission_mode": "manual" if matched.get("waitingFor") != "input needed" else ""})
        try:
            terminate_cli(row["pid"])
        except ProcessLookupError:
            pass
    deadline = time.monotonic() + 5
    while rows and time.monotonic() < deadline:
        if not any(live(row["pid"]) for row in rows):
            return {"running": False}
        time.sleep(0.05)
    return {"running": bool(rows)}


def handoff_receipt(config, session_id):
    key = hashlib.sha256((str(config) + "\n" + session_id).encode()).hexdigest()
    return ROOT.parent / "cli-handoffs" / (key + ".json")


def prepare_account_switch(request):
    # Freeze only an idle source. Never resubmit prompts or tools here.
    sid = identifier(request["cli_id"])
    switch_id = identifier(request["switch_id"])
    config = Path(request["config"]).resolve()
    connection = identifier(request["connection"])
    workspace = request["workspace"]
    folder = ROOT.parent / "cli-account-switches" / switch_id
    receipt = folder / "receipt.json"
    binding = {key: request[key] for key in ("cli_id", "config", "connection", "workspace", "target_cli_id")}
    if receipt.exists():
        saved = read_json(receipt)
        if saved["binding"] != binding:
            raise ValueError("账号交接编号已用于另一项操作")
        return {"transcript": str(folder / "history.jsonl")}
    with state_file(connection) as state:
        if state.get("cli_id") != sid or state.get("config") != str(config) or state.get("workspace") != workspace:
            raise ValueError("原会话身份或项目已变化，请刷新后重试")
        if state.get("busy") or state.get("approvals") or state.get("pending_send") or state.get("sent"):
            raise ValueError("原会话仍有任务、待发消息或审批，请结束后再切换账号")
        existing = state.get("account_switch")
        if existing and existing != switch_id:
            raise ValueError("此会话已有账号交接尚未完成")
        state["account_switch"] = switch_id
        save(ROOT / connection / "state.json", state)
    rows = active_sessions(sid, str(config))
    # Any other live worker in this account can represent a background
    # child of the source. The registry does not expose a complete ancestry.
    listing = subprocess.run(["claude", "agents", "--json"], env={**os.environ, "CLAUDE_CONFIG_DIR": str(config)}, capture_output=True, text=True, timeout=10)
    if listing.returncode != 0:
        raise ValueError("无法确认后台任务，未切换账号")
    if any(row.get("sessionId") != sid and live(row.get("pid")) for row in json.loads(listing.stdout)):
        raise ValueError("原账号仍有其他执行任务，请结束后台任务后再交接")
    for row in rows:
        if row.get("cwd") != workspace or row.get("status") != "idle" or row.get("kind") != "interactive":
            raise ValueError("原 Claude Code 尚未安全停止，请先结束本轮任务")
    pid = state.get("pid")
    fingerprint = process_fingerprint(pid) if live(pid) else ""
    def executing():
        if not live(pid):
            return False
        if os.name == "nt":
            return True
        status = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True, timeout=2)
        return bool(status.stdout.strip()) and not status.stdout.lstrip().startswith("Z")
    if executing() and not any(row.get("pid") == pid for row in rows):
        raise ValueError("无法核对原进程状态，请先退出原 Claude Code 后重试")
    for row in rows:
        terminate_cli(row["pid"])
    deadline = time.monotonic() + 5
    while executing() and time.monotonic() < deadline:
        time.sleep(.05)
    if executing():
        latest = read_json(ROOT / connection / "state.json", {})
        current = process_fingerprint(pid)
        # The CLI can finish its SessionEnd hook but hang while shutting down
        # its MCP child. Terminate only that same, already idle and fenced CLI.
        if latest.get("closed") and latest.get("account_switch") == switch_id and fingerprint and current == fingerprint and not active_sessions(sid, str(config)):
            terminate_cli(pid, True)
            deadline = time.monotonic() + 1
            while executing() and time.monotonic() < deadline:
                time.sleep(.05)
    if executing():
        raise ValueError("原进程尚未退出，未启动目标账号")
    path = next((config / "projects").glob("*/" + sid + ".jsonl"), None)
    if path is None or path.is_symlink():
        raise ValueError("原会话历史不可用，未切换账号")
    pending = set()
    for line in path.read_text().splitlines():
        row = json.loads(line)
        if row.get("isSidechain"):
            continue
        if row.get("cwd") and row["cwd"] != workspace:
            raise ValueError("历史项目与当前项目不一致")
        blocks = (row.get("message") or {}).get("content", [])
        if not isinstance(blocks, list):
            continue
        for block in blocks:
            if block.get("type") == "tool_use":
                pending.add(block["id"])
            elif block.get("type") == "tool_result":
                pending.discard(block.get("tool_use_id"))
    if pending:
        raise ValueError("历史仍有结果未确认的工具，不能自动接续")
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    target = folder / "history.jsonl"
    with tempfile.NamedTemporaryFile(dir=folder, delete=False) as stream:
        stream.write(path.read_bytes())
        temporary = stream.name
    os.replace(temporary, target)
    save(receipt, {"binding": binding, "requests": state.get("requests", {})})
    with state_file(connection) as state:
        state["closed"] = True
    return {"transcript": str(target)}


def cancel_account_switch(request):
    connection = identifier(request["connection"])
    switch_id = identifier(request["switch_id"])
    with state_file(connection) as state:
        if state.get("cli_id") != identifier(request["cli_id"]) or state.get("account_switch") not in (None, switch_id):
            raise ValueError("原会话交接已变化")
        state.pop("account_switch", None)
    # The source may already be stopped, but it remains resumable. Remove only
    # this operation's frozen copy so a later retry cannot use stale history.
    folder = ROOT.parent / "cli-account-switches" / switch_id
    if folder.exists():
        shutil.rmtree(folder)
    return {"ok": True}


def purge_session(request):
    session_id = identifier(request["cli_id"])
    config = Path(request["config"]) if request.get("config") else config_directory(request.get("account") or "default")
    config = config.resolve()
    workspace = request["workspace"].rstrip("/")
    if active_session(session_id, str(config)):
        raise ValueError("Claude Code 仍在运行这段会话，请先结束原终端中的 Claude Code")
    projects = config / "projects"
    if projects.is_symlink():
        raise ValueError("会话目录是符号链接，请检查主机配置")
    paths = list(projects.glob("*/" + session_id + ".jsonl"))
    for path in paths:
        if path.is_symlink() or path.parent.is_symlink():
            raise ValueError("会话历史路径已变化，请检查主机配置")
        directory, _ = history_summary(path)
        if not workspace_within(directory, workspace):
            raise ValueError("会话历史与已授权项目不一致")
    key = hashlib.sha256((str(config) + "\n" + session_id).encode()).hexdigest()
    receipt = ROOT.parent / "cli-deleted" / (key + ".json")
    if not paths and request.get("expect_history") and not receipt.exists():
        raise ValueError("当前账号配置中未找到原始会话，请核对主机配置后重试")
    # The SDK's delete_session removes the transcript and its sibling session
    # directory. Keep a content-free receipt so a lost response can be retried.
    save(receipt, {"cli_id": session_id, "config": str(config), "complete": False})
    for path in paths:
        related = path.parent / session_id
        if related.is_symlink():
            related.unlink()
        elif related.exists():
            shutil.rmtree(related)
        path.unlink(missing_ok=True)
    for path in ROOT.glob("*/state.json"):
        state = read_json(path, {})
        if state.get("cli_id") == session_id and state.get("config") == str(config):
            if live(state.get("pid")) and not state.get("closed"):
                raise ValueError("会话通道尚未退出，请稍后重试")
            shutil.rmtree(path.parent)
    identity = ROOT.parent / "mobile-cli-identities" / session_id
    identity.unlink(missing_ok=True)
    handoff_receipt(config, session_id).unlink(missing_ok=True)
    save(receipt, {"cli_id": session_id, "config": str(config), "complete": True})
    for saved in (ROOT.parent / "cli-account-switches").glob("*/receipt.json"):
        binding = read_json(saved, {}).get("binding", {})
        if binding.get("cli_id") == session_id or binding.get("target_cli_id") == session_id:
            shutil.rmtree(saved.parent)
    return {"ok": True}


def launch(request):
    if os.name == "nt" and request.get("ccs_profile"):
        return _platform["launch_ccs_parent"](request, globals())
    policy_available = migrate_cage_policy()
    connection = identifier(request["id"])
    folder = ROOT / connection
    folder.mkdir(parents=True, exist_ok=True, mode=0o700)
    prefix = request["prefix"]
    if os.name == "nt":
        account_id = request.get("account") or "default"
        os.environ["CLAUDE_CONFIG_DIR"] = str(config_directory(account_id))
        if request.get("subject"):
            os.environ["CAGE_CLAUDE_SUBJECT"] = request["subject"]
        prefix = [os.environ["CAGE_CLI_BINARY"]]
        request["prefix"] = prefix
    elif len(prefix) == 5 and Path(prefix[2]).name == "cage-claude-account" and prefix[3] == "run":
        os.environ["CLAUDE_CONFIG_DIR"] = str(config_directory(prefix[4]))
    elif request.get("config"):
        os.environ["CLAUDE_CONFIG_DIR"] = request["config"]
    session_id = identifier(request.get("resume") or request.get("session_id") or connection)
    _, previous = connection_for(session_id, config_directory("default"))
    if request.get("resume"):
        if active_session(request["resume"]):
            raise ValueError("原会话仍在电脑终端运行，请先结束该终端中的 Claude Code 再接续")
    command = shlex.join([sys.executable, *(["-X", "utf8"] if os.name == "nt" else []), "-I", str(Path(__file__).resolve()), "hook", connection])
    events = ("SessionStart", "UserPromptSubmit", "MessageDisplay", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "StopFailure", "SessionEnd")
    handler = {"type": "command", "command": command, "timeout": 5}
    if os.name == "nt":
        handler = {"type": "command", "command": sys.executable,
                   "args": ["-X", "utf8", "-I", str(Path(__file__).resolve()), "hook", connection], "timeout": 5}
    settings = {"hooks": {event: [{"hooks": [handler.copy()]}] for event in events}}
    prefix = request["prefix"]
    if os.name == "nt":
        account = runpy.run_path(os.environ["CAGE_CLI_ACCOUNT_HELPER"])
        if not account["has_status_line"](account["profile"](account_id)):
            status_command = shlex.join([sys.executable, "-X", "utf8", "-I", os.environ["CAGE_CLI_ACCOUNT_HELPER"], "statusline", account_id])
            settings["statusLine"] = {"type": "command", "command": status_command}
    elif len(prefix) == 5 and Path(prefix[2]).name == "cage-claude-account" and prefix[3] == "run":
        account = runpy.run_path(prefix[2])
        if not account["has_status_line"](account["profile"](prefix[4])):
            settings["statusLine"] = {"type": "command", "command": prefix[2] + " statusline " + prefix[4]}
    save(folder / "settings.json", settings)
    config = {"mcpServers": {"cage": {"command": sys.executable, "args": [*(["-X", "utf8"] if os.name == "nt" else []), "-I", str(Path(__file__).resolve()), "serve", connection]}}}
    save(folder / "mcp.json", config)
    with state_file(connection) as state:
        if live(state.get("pid")) and not state.get("closed"):
            raise ValueError("目标会话已有进程，未重复启动")
        state.update(cli_id=session_id, config=str(config_directory("default")),
                     workspace=str(Path.cwd()), pid=os.getpid(), closed=False, updated_at=time.time(), approvals=[], busy=False,
                     channel_pid=None, pending_send=None, display=None, stopped=False,
                     requests=previous.get("requests", {}), sent=previous.get("sent", []))
        receipt = handoff_receipt(config_directory("default"), session_id)
        transfer = read_json(receipt, {})
        state["resume_task"] = previous.get("resume_task", False) or transfer.get("resume_task", False)
        state["account_resume"] = previous.get("account_resume", False)
        if request.get("resume_transcript"):
            state["resume_transcript"] = request["resume_transcript"]
            state["resume_task"] = bool(request.get("continue_after_switch"))
            state["account_resume"] = True
            transfer = read_json(ROOT.parent / "cli-account-switches" / identifier(request["switch_id"]) / "receipt.json", {})
            state["requests"] = {**transfer.get("requests", {}), **state["requests"]}
        state["handoff_permission_mode"] = transfer.get("permission_mode") or previous.get("handoff_permission_mode", "")
        receipt.unlink(missing_ok=True)
        for sent in state.get("sent", []):
            sent["interrupted"] = True
        try:
            policy = Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "ClaudeCode/managed-settings.json" if os.name == "nt" else Path("/Library/Application Support/ClaudeCode/managed-settings.json") if sys.platform == "darwin" else Path("/etc/claude-code/managed-settings.json")
            managed = read_json(policy, {})
            state["blocked"] = isinstance(managed, dict) and managed.get("channelsEnabled") is False
        except (OSError, ValueError):
            state["blocked"] = False
        state["policy_unavailable"] = policy_available is False
    args = [*request["prefix"], "--settings", str(folder / "settings.json"), "--mcp-config", str(folder / "mcp.json"),
            "--dangerously-load-development-channels", "server:cage"]
    if request.get("resume_transcript"):
        expected = ROOT.parent / "cli-account-switches" / identifier(request["switch_id"]) / "history.jsonl"
        if Path(request["resume_transcript"]) != expected or not expected.is_file():
            raise ValueError("账号交接历史无效")
        args += ["--resume", str(expected), "--fork-session", "--session-id", session_id]
    else:
        args += ["--resume", session_id] if request.get("resume") else ["--session-id", session_id]
    if state.get("handoff_permission_mode") == "manual":
        args += ["--permission-mode", "manual"]
    if os.name == "nt":
        return _platform["launch_cli"](args, request, connection, state_file)
    os.execvpe(args[0], args, os.environ)


def rpc(request):
    connection = identifier(request["connection"])
    os.chdir(ROOT / connection)
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(5)
        client.connect("rpc")
        client.sendall(json.dumps(request).encode() + b"\n")
        with client.makefile("rb") as stream:
            return json.loads(stream.readline(1 << 20))


def serve(connection):
    connection = identifier(connection)
    output_lock = threading.Lock()

    def emit(message):
        with output_lock:
            sys.stdout.write(json.dumps({"jsonrpc": "2.0", **message}) + "\n")
            sys.stdout.flush()

    class Handler(socketserver.StreamRequestHandler):
        def handle(self):
            self.connection.settimeout(5)
            try:
                request = json.loads(self.rfile.readline(72 << 10))
                with state_file(connection) as state:
                    if request["cli_id"] != state.get("cli_id") or state.get("closed"):
                        raise ValueError("终端已切换会话，请重新读取列表")
                    action = request["action"]
                    if state.get("account_switch"):
                        raise ValueError("此会话正在切换账号，请等待交接完成")
                    if action == "send":
                        request_id = identifier(request["request_id"])
                        text = request["text"]
                        if not text.strip() or len(text.encode()) > PROMPT_LIMIT:
                            raise ValueError("消息为空或过长")
                        digest = hashlib.sha256(text.encode()).hexdigest()
                        requests = state.setdefault("requests", {})
                        previous = requests.get(request_id)
                        if previous is not None:
                            if previous != digest:
                                raise ValueError("同一发送编号已用于另一条消息")
                        else:
                            if state.get("busy") or state.get("pending_send"):
                                raise ValueError("这段会话已有消息正在处理，请等待后重试")
                            state.pop("stopped", None)
                            state["pending_send"] = request_id
                            requests[request_id] = digest
                            state.setdefault("sent", []).append({"id": request_id, "text": text})
                            # Persist admission before writing to the CLI. A lost
                            # acknowledgement must never replay a channel event.
                            save(ROOT / connection / "state.json", state)
                            emit({"method": "notifications/claude/channel", "params": {"content": text, "meta": {"cage_request": request_id}}})
                    elif action == "approve":
                        pending = state.get("approvals", [])
                        if not pending or pending[0]["id"] != request["approval_id"] or type(request["allow"]) is not bool:
                            raise ValueError("审批已结束，请重新读取会话")
                        approval = pending.pop(0)
                        emit({"method": "notifications/claude/channel/permission", "params": {
                            "request_id": approval["relay_id"], "behavior": "allow" if request["allow"] else "deny"}})
                    elif action == "stop":
                        state["stopped"] = True
                        state["busy"] = False
                        state["approvals"] = []
                    elif action == "resume":
                        if state.get("resume_task"):
                            state["resume_task"] = False
                            state["busy"] = True
                            # Commit before notification; reconnecting must not
                            # inject the same continuation twice.
                            save(ROOT / connection / "state.json", state)
                            emit({"method": "notifications/claude/channel", "params": {
                                "content": Path(__file__).with_suffix(".account-resume.md" if state.get("account_resume") else ".resume.md").read_text(encoding="utf-8"),
                                "meta": {"cage_resume": "true"}}})
                    else:
                        raise ValueError("unsupported operation")
                    state["updated_at"] = time.time()
                response = {"ok": True}
            except (OSError, ValueError, KeyError, TypeError) as error:
                response = {"error": str(error)}
            self.wfile.write(json.dumps(response).encode() + b"\n")

    os.chdir(ROOT / connection)
    address = "rpc"
    # A connection UUID is created once by the host. A second live server must
    # fail to bind instead of stealing the first server's socket.
    if Path(address).is_socket():
        with socket.socket(socket.AF_UNIX) as probe:
            try:
                probe.connect(address)
            except ConnectionRefusedError:
                Path(address).unlink()
            else:
                raise ValueError("会话通道已在运行")
    with socketserver.ThreadingUnixStreamServer(address, Handler) as server:
        server.daemon_threads = True
        os.chmod(address, 0o600)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            for line in sys.stdin:
                message = json.loads(line)
                method = message.get("method")
                if method == "initialize":
                    emit({"id": message["id"], "result": {"protocolVersion": "2024-11-05", "serverInfo": {"name": "cage", "version": "1"},
                          "capabilities": {"experimental": {"claude/channel": {}, "claude/channel/permission": {}}},
                          "instructions": "The user is viewing this same Claude Code conversation from Cage on a phone. Respond normally in the conversation. Cage synchronizes the transcript."}})
                elif method == "notifications/initialized":
                    with state_file(connection) as state:
                        state["channel_pid"] = os.getpid()
                        state["updated_at"] = time.time()
                elif method == "notifications/claude/channel/permission_request":
                    params = message["params"]
                    preview = params.get("input_preview", "")
                    try:
                        arguments = json.loads(preview)
                    except ValueError:
                        arguments = {"preview": preview}
                    with state_file(connection) as state:
                        state.setdefault("approvals", []).append({"id": str(uuid.uuid4()), "relay_id": params["request_id"],
                            "tool": params["tool_name"], "input": arguments, "preview": True})
                        state["updated_at"] = time.time()
                elif "id" in message:
                    if method == "ping":
                        emit({"id": message["id"], "result": {}})
                    else:
                        emit({"id": message["id"], "error": {"code": -32601, "message": "Method not supported"}})
        finally:
            server.shutdown()
            Path(address).unlink(missing_ok=True)
            with state_file(connection) as state:
                state["channel_pid"] = None
                state["approvals"] = []


def main():
    os.umask(0o077)
    mode, argument = sys.argv[1:3]
    if mode == "ccs-live" and os.name == "nt":
        return _platform["launch_ccs_live"](argument, globals())
    if mode == "serve":
        return serve(argument)
    if mode == "hook":
        result = hook(argument)
    else:
        request = json.load(sys.stdin) if argument == "-" else json.loads(argument)
        if mode == "launch":
            return launch(request)
        if mode == "discover":
            result = discover(request)
        elif mode == "view":
            config = Path(request["config"]) if request.get("config") else config_directory(request.get("account") or "default")
            state = read_json(ROOT / identifier(request.get("connection") or request["cli_id"]) / "state.json", {})
            if state.get("cli_id") == request["cli_id"] and state.get("config"):
                config = Path(state["config"])
            result = view(config, identifier(request["cli_id"]))
        elif mode == "rpc":
            result = rpc(request)
        elif mode == "active":
            config = request.get("config") or str(config_directory(request.get("account") or "default"))
            result = {"running": active_session(identifier(request["cli_id"]), config)}
        elif mode == "handoff":
            try:
                result = handoff_session(request)
            except (OSError, ValueError, subprocess.SubprocessError) as error:
                result = {"error": str(error)}
        elif mode == "purge":
            try:
                result = purge_session(request)
            except (OSError, ValueError, subprocess.SubprocessError) as error:
                result = {"error": str(error)}
        elif mode == "account-switch":
            try:
                result = prepare_account_switch(request)
            except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
                result = {"error": str(error)}
        elif mode == "account-switch-cancel":
            try:
                result = cancel_account_switch(request)
            except (OSError, ValueError, KeyError) as error:
                result = {"error": str(error)}
        else:
            raise ValueError("unsupported operation")
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    sys.exit(main() or 0)
