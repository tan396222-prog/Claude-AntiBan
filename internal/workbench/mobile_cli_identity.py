"""Bind mobile history to the actual guest provider. Never print raw configuration."""
import hashlib
import hmac
import json
import os
from pathlib import Path
import runpy
import secrets
import shutil
import subprocess
import sys
import tempfile
import uuid
from urllib.parse import urlsplit


class Unverified(Exception):
    pass


class Changed(Exception):
    pass


class Verified(Exception):
    def __init__(self, config):
        self.config = str(config)


def single_turn_notice():
    print(json.dumps({"type": "system", "subtype": "cage_identity_single_turn"}), flush=True)


def read_settings(path):
    with open(path, "rb") as stream:
        payload = stream.read(1024 * 1024 + 1)
    if len(payload) > 1024 * 1024:
        raise Unverified()
    value = json.loads(payload)
    if not isinstance(value, dict):
        raise Unverified()
    return value


# Invoke CCS on a throwaway UUID with empty stdin. The normal settings-profile
# dispatcher (including its continuity/config-dir choices) calls this probe in
# place of Claude. No real conversation identifier or text is passed to CCS.
# Its stdout is captured privately inside the guest, never forwarded over SSH.
CCS_PROBE = """
import json, os, sys
args = sys.argv[1:]
if os.environ['CAGE_CAPTURE_ID'] not in args:
    sys.exit(1)
settings = {}
source = None
if '--settings' in args:
    index = args.index('--settings')
    source = args[index + 1]
    if source.lstrip().startswith('{'):
        settings = json.loads(source)
    else:
        with open(source, 'rb') as f:
            content = f.read(1048577)
        if len(content) > 1048576: sys.exit(1)
        settings = json.loads(content)
    del args[index:index + 2]
print('CAGE_CCS_CAPTURE:' + json.dumps([args, settings, source, dict(os.environ)]))
"""


def ccs_launch(profile, args, id_index):
    probe_id = str(uuid.uuid4())
    probe_args = args.copy()
    probe_args[id_index] = probe_id
    with tempfile.TemporaryDirectory(prefix="cage-ccs-launch-") as directory:
        probe = Path(directory) / "claude"
        probe.write_text("#!" + sys.executable + " -I\n" + CCS_PROBE)
        probe.chmod(0o700)
        env = dict(os.environ, CCS_CLAUDE_PATH=str(probe), CAGE_CAPTURE_ID=probe_id)
        result = subprocess.run(["ccs", profile, *probe_args], env=env, stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=20)
    records = [line.removeprefix(b"CAGE_CCS_CAPTURE:") for line in result.stdout.splitlines()
               if line.startswith(b"CAGE_CCS_CAPTURE:")]
    if result.returncode or len(records) != 1 or len(records[0]) > 2 << 20:
        raise Unverified()
    passed, settings, source, env = json.loads(records[0])
    if (not isinstance(passed, list) or not isinstance(env, dict) or
            not isinstance(settings, dict) or env.get("CCS_PROFILE_TYPE") != "settings" or
            not isinstance(source, str) or not os.path.isabs(source) or not os.path.isfile(source)):
        # An OpenAI-compat/CLIProxy dispatcher may allocate a temporary token,
        # proxy and settings file for each launch and remove them at child exit.
        raise Unverified()
    # Settings profiles append WebSearch restrictions and guidance. Other
    # rewrites could select a different session, backend, or permission mode.
    extra = passed[len(probe_args):]
    if passed[:len(probe_args)] != probe_args or len(extra) % 2:
        raise Unverified()
    if any(flag not in ("--disallowedTools", "--disallowed-tools", "--append-system-prompt", "--append-system-prompt-file")
           or not value or value.startswith("-") for flag, value in zip(extra[::2], extra[1::2])):
        raise Unverified()
    env.pop("CAGE_CAPTURE_ID", None)
    env.pop("CCS_CLAUDE_PATH", None)
    return args + extra, settings, env


def merged_settings(user, profile):
    result = dict(user)
    for key, value in profile.items():
        if key == "env":
            result[key] = {**result.get(key, {}), **value}
        elif key not in result or result[key] == value:
            result[key] = value
        else:
            # Avoid guessing how the CLI merges two conflicting permission or
            # tool configurations. The original launcher can still run one turn.
            raise Unverified()
    return result


def route_values(env):
    return {k: v for k, v in env.items() if k.startswith(("ANTHROPIC_", "CLAUDE_"))
            or k.lower() in ("https_proxy", "http_proxy", "all_proxy", "no_proxy", "node_extra_ca_certs",
                             "ssl_cert_file", "ssl_cert_dir", "node_tls_reject_unauthorized",
                             "home", "xdg_config_home", "appdata", "userprofile")}


def windows_registry_settings():
    if os.name != "nt":
        return []
    import winreg
    result = []
    for hive in (winreg.HKEY_LOCAL_MACHINE, winreg.HKEY_CURRENT_USER):
        for view in (winreg.KEY_WOW64_64KEY, winreg.KEY_WOW64_32KEY):
            try:
                with winreg.OpenKey(hive, r"SOFTWARE\Policies\ClaudeCode", 0, winreg.KEY_READ | view) as key:
                    text, kind = winreg.QueryValueEx(key, "Settings")
                if kind not in (winreg.REG_SZ, winreg.REG_EXPAND_SZ) or not isinstance(text, str) or len(text.encode()) > 1 << 20:
                    raise Unverified()
                if kind == winreg.REG_EXPAND_SZ:
                    text = winreg.ExpandEnvironmentStrings(text)
                policy = json.loads(text)
                if not isinstance(policy, dict):
                    raise Unverified()
                if policy not in result:
                    result.append(policy)
            except FileNotFoundError:
                pass
            except (OSError, ValueError):
                raise Unverified()
    return result


def validate_shared_mcp(args, extra_paths=(), config_env=None, workspace=None):
    # Preserve every existing server. Cage's own name/channels may not silently
    # replace an existing launch configuration.
    sources = []
    index = 0
    while index < len(args):
        value = args[index]
        if value in ("--channels", "--dangerously-load-development-channels") or value.startswith(("--channels=", "--dangerously-load-development-channels=")):
            raise Unverified()
        if value == "--mcp-config":
            index += 1
            start = index
            while index < len(args) and not args[index].startswith("-"):
                sources.append(args[index]); index += 1
            if index == start:
                raise Unverified()
            continue
        if value.startswith("--mcp-config="):
            sources.append(value.split("=", 1)[1])
        index += 1
    for item in sources:
        data = json.loads(item) if item.lstrip().startswith("{") else read_settings(Path(item))
        if not isinstance(data, dict) or not isinstance(data.get("mcpServers", {}), dict) or "cage" in data.get("mcpServers", {}):
            raise Unverified()
    for filename in extra_paths:
        if Path(filename).exists():
            data = read_settings(Path(filename))
            if not isinstance(data.get("mcpServers", {}), dict) or "cage" in data.get("mcpServers", {}):
                raise Unverified()
    if config_env is not None:
        # Claude stores user and per-project local MCP entries in its global
        # JSON. Read only the actual child's configuration, never host data.
        directory = config_env.get("CLAUDE_CONFIG_DIR")
        filename = Path(directory) / ".claude.json" if directory else Path.home() / ".claude.json"
        if filename.exists():
            data = read_settings(filename)
            servers = data.get("mcpServers", {})
            if not isinstance(servers, dict) or "cage" in servers:
                raise Unverified()
            projects = data.get("projects", {})
            if not isinstance(projects, dict):
                raise Unverified()
            if workspace is not None:
                target = os.path.normcase(str(Path(workspace).resolve()))
                for path, project in projects.items():
                    if os.path.normcase(str(Path(path).resolve())) != target:
                        continue
                    if not isinstance(project, dict):
                        raise Unverified()
                    servers = project.get("mcpServers", {})
                    if not isinstance(servers, dict) or "cage" in servers:
                        raise Unverified()


def managed_settings():
    # Cage installs terminal presentation and a default permission mode here.
    # These values do not select a provider. Check every policy fragment before
    # allowing it, and bind its contents along with the user configuration.
    directory = Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "ClaudeCode" if os.name == "nt" else Path("/etc/claude-code")
    paths = [directory / "managed-settings.json"]
    paths.extend(sorted((directory / "managed-settings.d").glob("*.json")))
    policies = []
    values = windows_registry_settings()
    values.extend(read_settings(path) for path in paths if path.exists())
    for policy in values:
        permissions = policy.get("permissions", {})
        if (policy.keys() - {"permissions", "tui", "channelsEnabled"} or not isinstance(permissions, dict)
                or permissions.keys() - {"defaultMode"}):
            raise Unverified()
        # Channel availability does not change the upstream of saved history.
        policies.append({key: value for key, value in policy.items() if key != "channelsEnabled"})
    return policies


def shared_channels_disabled():
    directory = Path(os.environ.get("ProgramFiles", "C:/Program Files")) / "ClaudeCode" if os.name == "nt" else Path("/etc/claude-code")
    policies = windows_registry_settings()
    for path in [directory / "managed-settings.json", *sorted((directory / "managed-settings.d").glob("*.json"))]:
        if path.exists():
            policies.append(read_settings(path))
    return any(policy.get("channelsEnabled") is False for policy in policies)


def freeze_settings(env, settings, snapshot):
    config = Path(env.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude")
    if not config.is_absolute():
        raise Unverified()
    user_file = config / "settings.json"
    settings = merged_settings(read_settings(user_file) if user_file.exists() else {}, settings)
    settings_env = settings.get("env", {})
    if not isinstance(settings_env, dict) or not all(isinstance(v, str) for v in settings_env.values()):
        raise Unverified()
    # Managed policies and dynamically minted credentials can change the
    # destination without a stable identity that this launcher can observe.
    if settings.get("apiKeyHelper") or (config / "remote-settings.json").exists():
        raise Unverified()
    policies = managed_settings()
    route_env, route_settings = route_values(env), route_values(settings_env)
    effective = {**route_env, **route_settings}
    if (any(v and k.endswith("_FILE_DESCRIPTOR") for k, v in effective.items()) or
            effective.get("NODE_TLS_REJECT_UNAUTHORIZED") == "0" or
            any(effective.get(k) for k in ("ANTHROPIC_PROFILE", "ANTHROPIC_CONFIG_DIR", "ANTHROPIC_FEDERATION_RULE_ID")) or
            any(k in route_settings and route_settings[k] != env.get(k) for k in
                ("HOME", "XDG_CONFIG_HOME", "APPDATA", "USERPROFILE", "CLAUDE_CONFIG_DIR"))):
        raise Unverified()
    # User and CCS settings are captured once. Both auth status and the real
    # turn read the identical anonymous file rather than reopening mutable
    # profile settings between the check and the first provider request.
    snapshot.write(json.dumps(settings).encode())
    snapshot.flush()
    snapshot.seek(0)
    fd = snapshot.fileno()
    # Unix reopens this inherited descriptor. Windows reads the private path;
    # it must not inherit the file handle into later CLI/proxy descendants.
    os.set_inheritable(fd, os.name != "nt")
    source = str(Path(snapshot.name).resolve()) if os.name == "nt" else "/dev/fd/" + str(fd)
    flags = ["--setting-sources", "", "--settings", source]
    return config, settings, route_env, route_settings, flags, policies


def replace_settings(args, flags):
    position = args.index("--setting-sources")
    if args[position + 1] != "user":
        raise Unverified()
    return [*args[:position], *flags, *args[position + 2:]]


def resolve(prefix, args, id_index, snapshot):
    binary = shutil.which(os.environ.get("CCS_CLAUDE_PATH", "claude") if prefix[0] == "ccs" else "claude")
    if not binary:
        raise Unverified()
    env, settings = dict(os.environ), {}
    if prefix[0] == "ccs":
        args, settings, env = ccs_launch(prefix[1], args, id_index)
    return resolve_actual_context(binary, prefix, args, env, settings, snapshot)


def resolve_actual_context(binary, prefix, args, env, settings, snapshot):
    config, settings, route_env, route_settings, flags, policies = freeze_settings(env, settings, snapshot)
    effective = {**route_env, **route_settings}
    fd = snapshot.fileno()
    status = subprocess.run([binary, *flags, "auth", "status"], env=env, pass_fds=() if os.name == "nt" else (fd,),
                            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=12)
    snapshot.seek(0)
    info = json.loads(status.stdout)
    if not isinstance(info, dict) or status.returncode or not info.get("loggedIn") or info.get("apiProvider") != "firstParty":
        raise Unverified()
    reported_config = info.get("configDirectory")
    if not isinstance(reported_config, str) or Path(reported_config).resolve() != config.resolve():
        raise Unverified()
    method = info.get("authMethod")
    explicit_key = method == "api_key" and info.get("apiKeySource") == "ANTHROPIC_API_KEY" and effective.get("ANTHROPIC_API_KEY")
    explicit_token = method == "oauth_token" and (effective.get("ANTHROPIC_AUTH_TOKEN") or effective.get("CLAUDE_CODE_OAUTH_TOKEN"))
    # Native OAuth credential stores are mutable between auth status and the
    # next CLI process. Only the managed-account launcher can bind those.
    if not explicit_key and not explicit_token:
        raise Unverified()
    endpoint = urlsplit(effective.get("ANTHROPIC_BASE_URL") or "https://api.anthropic.com")
    if endpoint.scheme not in ("http", "https") or not endpoint.hostname or endpoint.hostname.rstrip(".") == "api.anthropic.com":
        # A directly configured key or environment OAuth token can fetch
        # server-managed policy, which outranks the pinned --settings file.
        raise Unverified()
    # Include unknown user/profile settings conservatively: a future CLI can
    # introduce a routing key before Cage knows its name.
    material = [prefix, str(config.resolve()), method, route_env, route_settings, settings]
    if policies:
        material.append(policies)
    return binary, replace_settings(args, flags), env, material


def official_turn(helper_path, account, args, snapshot, sid, resume, verify_only=False):
    # Use the installed helper's account directory, sanitized environment and
    # subject check for both the ordinary subscription and managed profiles.
    helper = runpy.run_path(helper_path)
    env = helper["cli_environment"](helper["profile"](account))
    config, settings, route_env, route_settings, flags, policies = freeze_settings(env, {}, snapshot)
    harmless_env = {"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
                    "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "CLAUDE_CODE_MAX_OUTPUT_TOKENS",
                    "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
                    "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}
    ordinary_settings = {"$schema", "env", "model", "permissions", "hooks", "statusLine", "attribution",
                         "language", "effortLevel", "alwaysThinkingEnabled", "includeCoAuthoredBy",
                         "respectGitignore", "spinnerTipsEnabled", "enabledPlugins", "outputStyle",
                         "disableAllHooks", "includeGitInstructions", "autoUpdatesChannel",
                         "theme", "skipDangerousModePermissionPrompt", "tui", "autoMemoryDirectory", "autoMemoryEnabled"}
    familiar_settings = not (settings.keys() - ordinary_settings)
    for route in (route_env, route_settings):
        for key, value in route.items():
            if not value:
                continue
            if key == "ANTHROPIC_BASE_URL":
                if value.rstrip("/") != "https://api.anthropic.com":
                    raise Changed()
            elif key.startswith(("ANTHROPIC_", "CLAUDE_")) and key not in harmless_env:
                raise Unverified()
    subject = env.get("CAGE_CLAUDE_SUBJECT")
    if not subject:
        raise Unverified()
    def bind_subscription(reading):
        # Team/Enterprise can receive new server-managed routing in a headless
        # turn even if a managed-policy cache did not yet exist at launch.
        proven = familiar_settings and str(reading.get("plan") or "").lower() in ("pro", "max")
        material = ["official", account, str(config.resolve()), subject, route_env, route_settings, settings] if proven else None
        if material is not None and policies:
            material.append(policies)
        bind(sid, resume, material)
        if verify_only:
            raise Verified(config)
        if not proven:
            single_turn_notice()
    result = helper["main"](["run", account, *replace_settings(args, flags)], mobile_settings_fd=snapshot.fileno(),
                            verify_mobile=bind_subscription)
    if result != 0:
        raise Changed()


def bind(session_id, resume, material):
    directory = Path(os.environ["CAGE_CLI_STATE_ROOT"]).parent / "mobile-cli-identities" if os.name == "nt" else Path.home() / ".local/share/cage/mobile-cli-identities"
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    path = directory / session_id
    serialized = json.dumps(material, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    def digest(salt):
        return hmac.new(bytes.fromhex(salt), serialized, hashlib.sha256).hexdigest() if material is not None else ""
    if not path.exists():
        if resume:
            raise Changed()
        salt = secrets.token_hex(32)
        # Publish the complete binding before a process can see the prompt.
        with tempfile.NamedTemporaryFile(dir=directory, delete=False) as f:
            name = f.name
            f.write(json.dumps([salt, digest(salt)]).encode())
            f.flush()
            os.fsync(f.fileno())
        try:
            try:
                os.link(name, path)
            except FileExistsError:
                pass
        finally:
            os.unlink(name)
    salt, expected = json.loads(path.read_bytes())
    if not expected and resume:
        raise Unverified()
    if not hmac.compare_digest(expected, digest(salt)):
        raise Changed()


def main():
    prefix_len = {"ccs": 2, "official": 3}.get(sys.argv[1], 1)
    prefix, args = sys.argv[1:1 + prefix_len], sys.argv[1 + prefix_len:]
    verify_only = "--cage-verify" in args
    if verify_only:
        args.remove("--cage-verify")
    resume = "--resume" in args
    index = args.index("--resume" if resume else "--session-id") + 1
    sid = str(uuid.UUID(args[index]))
    if sid != args[index]:
        raise Unverified()
    with tempfile.TemporaryFile() as snapshot:
        if prefix[0] == "official":
            return official_turn(prefix[1], prefix[2], args, snapshot, sid, resume, verify_only)
        try:
            binary, flags, env, material = resolve(prefix, args, index, snapshot)
        except (Unverified, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
            if resume:
                raise Unverified()
            bind(sid, False, None)
            single_turn_notice()
            os.execvp(prefix[0], [*prefix, *args])
        bind(sid, resume, material)
        if verify_only:
            raise Verified(Path(env.get("CLAUDE_CONFIG_DIR") or Path.home() / ".claude").resolve())
        os.execve(binary, [binary, *flags], env)


if __name__ == "__main__":
    try:
        main()
    except Verified as result:
        print(json.dumps({"config": result.config}), flush=True)
    except (Changed, Unverified, OSError, ValueError, KeyError, TypeError, IndexError, subprocess.SubprocessError) as error:
        subtype = "cage_identity_changed" if isinstance(error, Changed) else "cage_identity_unverified"
        if subtype == "cage_identity_unverified" and "--resume" not in sys.argv:
            subtype = "cage_identity_unverified_start"
        print(json.dumps({"type": "system", "subtype": subtype}), flush=True)
        sys.exit(2)
