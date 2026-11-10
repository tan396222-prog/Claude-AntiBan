"""Windows-native primitives used by the shared CLI algorithms."""
import ctypes
from ctypes import wintypes
import errno
import os
from pathlib import Path
import struct
import time

kernel = ctypes.WinDLL("kernel32.dll", use_last_error=True)
kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
kernel.OpenProcess.restype = wintypes.HANDLE
kernel.CloseHandle.argtypes = [wintypes.HANDLE]
kernel.GetExitCodeProcess.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
kernel.QueryFullProcessImageNameW.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)]
kernel.GetProcessTimes.argtypes = [wintypes.HANDLE, ctypes.POINTER(ctypes.c_ulonglong), ctypes.POINTER(ctypes.c_ulonglong), ctypes.POINTER(ctypes.c_ulonglong), ctypes.POINTER(ctypes.c_ulonglong)]
kernel.TerminateProcess.argtypes = [wintypes.HANDLE, wintypes.UINT]
kernel.ProcessIdToSessionId.argtypes = [wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]

class FileLock:
    LOCK_EX, LOCK_NB = 2, 4
    @staticmethod
    def flock(file, operation):
        import msvcrt
        fd = file if isinstance(file, int) else file.fileno()
        if os.fstat(fd).st_size == 0:
            os.write(fd, b"\0")
        while True:
            os.lseek(fd, 0, os.SEEK_SET)
            try:
                msvcrt.locking(fd, msvcrt.LK_NBLCK, 1)
                return
            except OSError as error:
                if error.errno not in (errno.EACCES, errno.EDEADLK):
                    raise
                if operation & FileLock.LOCK_NB:
                    raise BlockingIOError(errno.EAGAIN, "private state is busy") from error
                time.sleep(.05)

def process_live(pid):
    try:
        handle = kernel.OpenProcess(0x1000, False, int(pid))
        if not handle:
            return False
        try:
            result = wintypes.DWORD()
            return bool(kernel.GetExitCodeProcess(handle, ctypes.byref(result))) and result.value == 259
        finally:
            kernel.CloseHandle(handle)
    except (ValueError, TypeError, OverflowError):
        return False

def process_fingerprint(pid):
    if not process_live(pid):
        return ""
    handle = kernel.OpenProcess(0x1000, False, int(pid))
    if not handle:
        return ""
    try:
        name = ctypes.create_unicode_buffer(32768); size = wintypes.DWORD(len(name))
        created, exited, kern, user = [ctypes.c_ulonglong() for _ in range(4)]
        session = wintypes.DWORD()
        if not kernel.QueryFullProcessImageNameW(handle, 0, name, ctypes.byref(size)) or not kernel.GetProcessTimes(handle, *map(ctypes.byref, (created, exited, kern, user))) or not kernel.ProcessIdToSessionId(int(pid), ctypes.byref(session)):
            return ""
        return (created.value, os.path.normcase(name.value), session.value)
    finally:
        kernel.CloseHandle(handle)

def terminate_cli(pid, force=False):
    fingerprint = process_fingerprint(pid)
    expected = os.environ.get("CAGE_CLI_BINARY")
    own = wintypes.DWORD()
    if not fingerprint or not expected or not kernel.ProcessIdToSessionId(os.getpid(), ctypes.byref(own)) or fingerprint[2] != own.value or fingerprint[1] != os.path.normcase(str(Path(expected).resolve())):
        raise ValueError("原生 Claude 进程身份不匹配，未停止其他程序")
    handle = kernel.OpenProcess(0x1001, False, int(pid))
    if not handle:
        raise ProcessLookupError("CLI process no longer exists")
    try:
        if process_fingerprint(pid) != fingerprint or not kernel.TerminateProcess(handle, 130):
            raise ValueError("无法停止已核验的原生 Claude 进程")
    finally:
        kernel.CloseHandle(handle)

def resource_link(path, target):
    path, target = Path(path), Path(target)
    if not target.is_dir():
        os.link(target, path)
        return
    # An owned NTFS junction needs no developer-mode symlink privilege.
    substitute = ("\\??\\" + str(target.resolve())).encode("utf-16le")
    display = str(target.resolve()).encode("utf-16le")
    names = substitute + b"\0\0" + display + b"\0\0"
    value = struct.pack("<IHHHHHH", 0xA0000003, 8 + len(names), 0, 0, len(substitute), len(substitute) + 2, len(display)) + names
    kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
    kernel.CreateFileW.restype = wintypes.HANDLE
    kernel.DeviceIoControl.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD), wintypes.LPVOID]
    path.mkdir()
    handle = kernel.CreateFileW(str(path), 0x40000000, 7, None, 3, 0x02200000, None)
    try:
        if handle == wintypes.HANDLE(-1).value:
            raise ctypes.WinError(ctypes.get_last_error())
        data = ctypes.create_string_buffer(value); returned = wintypes.DWORD()
        if not kernel.DeviceIoControl(handle, 0x900A4, data, len(value), None, 0, ctypes.byref(returned), None):
            raise ctypes.WinError(ctypes.get_last_error())
    except Exception:
        if handle != wintypes.HANDLE(-1).value:
            kernel.CloseHandle(handle); handle = wintypes.HANDLE(-1).value
        path.rmdir()
        raise
    finally:
        if handle != wintypes.HANDLE(-1).value:
            kernel.CloseHandle(handle)


def launch_cli(args, request, connection, state_file):
    # Keep the ConPTY owner alive; state owns the actual native Claude PID.
    import runpy
    import subprocess
    account = runpy.run_path(os.environ["CAGE_CLI_ACCOUNT_HELPER"])
    base = account["profile"](request.get("account") or "default")
    env = account["cli_environment"](base)
    account["shared_profile_resources"](base)
    args = [args[0], *account["shared_launch_arguments"](base, args[1:], None)]
    if request.get("subject"):
        reading = account["identity"](args[0], env, Path.cwd(), account["settings_flags"](args[1:]))
        if account["subject"](reading) != request["subject"]:
            raise ValueError("项目与所选 Claude 账号身份不一致")
    child = CliProcess(args, env)
    with state_file(connection) as state:
        state["pid"] = child.pid
    try:
        code = child.wait()
    finally:
        child.close()
        with state_file(connection) as state:
            state["closed"] = True
            state["busy"] = False
    shell = os.environ.get("CAGE_CLI_SHELL")
    if not shell:
        return code
    # As on other guests, exiting Claude keeps the existing terminal usable.
    # The fixed function scopes later invocations to the same verified account.
    setup = 'function global:claude { & $env:CAGE_CLI_PYTHON -X utf8 -I $env:CAGE_CLI_ACCOUNT_HELPER run $env:CAGE_CLAUDE_ACCOUNT_ID @args }'
    return subprocess.call([shell, "-NoLogo", "-NoExit", "-Command", setup], env=env)


class CliProcess:
    """A suspended native Claude process and its own nested job tree."""
    def __init__(self, args, env):
        import _winapi
        import subprocess
        class Basic(ctypes.Structure):
            _fields_ = [('process_time', ctypes.c_longlong), ('job_time', ctypes.c_longlong),
                        ('flags', wintypes.DWORD), ('minimum', ctypes.c_size_t), ('maximum', ctypes.c_size_t),
                        ('active', wintypes.DWORD), ('affinity', ctypes.c_size_t),
                        ('priority', wintypes.DWORD), ('scheduling', wintypes.DWORD)]
        class IO(ctypes.Structure):
            _fields_ = [('counters', ctypes.c_ulonglong * 6)]
        class Limits(ctypes.Structure):
            _fields_ = [('basic', Basic), ('io', IO), ('process_memory', ctypes.c_size_t),
                        ('job_memory', ctypes.c_size_t), ('peak_process', ctypes.c_size_t), ('peak_job', ctypes.c_size_t)]
        kernel.CreateJobObjectW.argtypes = [wintypes.LPVOID, wintypes.LPCWSTR]
        kernel.CreateJobObjectW.restype = wintypes.HANDLE
        kernel.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, wintypes.LPVOID, wintypes.DWORD]
        kernel.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
        kernel.ResumeThread.argtypes = [wintypes.HANDLE]
        kernel.ResumeThread.restype = wintypes.DWORD
        self.job = kernel.CreateJobObjectW(None, None)
        self.handle = None
        if not self.job:
            raise ctypes.WinError(ctypes.get_last_error())
        try:
            limits = Limits(); limits.basic.flags = 0x2000
            if not kernel.SetInformationJobObject(self.job, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
                raise ctypes.WinError(ctypes.get_last_error())
            process, thread, self.pid, _ = _winapi.CreateProcess(args[0], subprocess.list2cmdline(args), None, None,
                True, 0x00000004 | 0x00000400, env, None, subprocess.STARTUPINFO())
            self.handle = process
            try:
                if not kernel.AssignProcessToJobObject(self.job, process) or kernel.ResumeThread(thread) == 0xffffffff:
                    raise ctypes.WinError(ctypes.get_last_error())
            except BaseException:
                _winapi.TerminateProcess(process, 1)
                raise
            finally:
                _winapi.CloseHandle(thread)
        except BaseException:
            self.close()
            raise

    def wait(self):
        import _winapi
        _winapi.WaitForSingleObject(self.handle, _winapi.INFINITE)
        return _winapi.GetExitCodeProcess(self.handle)

    def close(self):
        import _winapi
        if self.job:
            kernel.CloseHandle(self.job); self.job = None
        if self.handle:
            _winapi.WaitForSingleObject(self.handle, _winapi.INFINITE)
            _winapi.CloseHandle(self.handle); self.handle = None


def launch_ccs_parent(request, channel):
    import json
    import subprocess
    import sys
    connection = channel['identifier'](request['id'])
    sid = channel['identifier'](request.get('resume') or request['session_id'])
    profile = request['ccs_profile']
    root = channel['ROOT'] / connection
    root.mkdir(parents=True, exist_ok=True)
    context = {'id':connection, 'session_id':sid, 'profile':profile, 'folder':str(root),
               'binary':os.environ['CAGE_CLI_BINARY'], 'python':sys.executable,
               'helper':str(Path(channel['__file__']).resolve()), 'main':os.environ['CAGE_CCS_MAIN'],
               'resume':bool(request.get('resume'))}
    channel['save'](root / 'ccs-launch.json', context)
    env = dict(os.environ, CAGE_CCS_LAUNCH_FILE=str(root / 'ccs-launch.json'))
    args = [env['CAGE_CCS_NODE'], '--require', env['CAGE_CCS_PRELOAD'], env['CAGE_CCS_MAIN'], profile,
            '--resume' if request.get('resume') else '--session-id', sid, '--setting-sources', 'user']
    with channel['state_file'](connection) as state:
        if channel['live'](state.get('pid')) and not state.get('closed'):
            raise ValueError('目标会话已有进程，未重复启动')
        state.update(cli_id=sid, config=str(Path(env.get('CLAUDE_CONFIG_DIR') or Path.home()/'.claude')),
                     workspace=str(Path.cwd()), closed=False, busy=False, channel_pid=None,
                     pid=os.getpid(), updated_at=__import__('time').time(), native_ccs_pending=True)
    child = CliProcess(args, env)
    try:
        child.wait()
    finally:
        child.close()
        with channel['state_file'](connection) as state:
            state['closed'] = True
            state['busy'] = False
    shell = env.get('CAGE_CLI_SHELL')
    if not shell:
        return 0
    setup = 'function global:ccs { & $env:CAGE_CCS_NODE $env:CAGE_CCS_MAIN @args }; function global:claude { & $env:CAGE_CLI_BINARY @args }'
    return subprocess.call([shell, '-NoLogo', '-NoExit', '-Command', setup], env=env)


def launch_ccs_live(filename, channel):
    import json
    import runpy
    import subprocess
    import tempfile
    import time
    import sys
    source = Path(filename)
    if source.is_symlink() or source.stat().st_size > 1 << 20:
        raise ValueError('CCS 实际启动记录无效')
    metadata = json.loads(source.read_text(encoding='utf-8'))
    connection = channel['identifier'](metadata['id'])
    sid = channel['identifier'](metadata['session_id'])
    folder = channel['ROOT'] / connection
    if source.parent.resolve() != folder.resolve() or not source.name.startswith('ccs-actual-'):
        raise ValueError('CCS 实际启动记录不属于此会话')
    source.unlink()
    context = channel['read_json'](folder/'ccs-launch.json')
    if context['id'] != connection or context['session_id'] != sid or context['profile'] != metadata['profile']:
        raise ValueError('CCS 实际启动记录已变化')
    original = metadata['actual_args']
    if not isinstance(original, list) or any(not isinstance(arg, str) for arg in original):
        raise ValueError('CCS 参数无效')
    binary = os.environ['CAGE_CLI_BINARY']
    args = original.copy()
    identity = runpy.run_path(os.environ['CAGE_CLI_IDENTITY_HELPER'])
    snapshot = tempfile.NamedTemporaryFile(dir=folder, suffix='.settings.json', delete=False)
    verified = False
    notice = '此 CCS 配置尚未通过共享身份核验，终端仍可使用。'
    try:
        try:
            settings_file = Path(metadata['settings_source'])
            expected_file = Path(metadata['expected_settings_source'])
            if metadata['profile_type'] != 'settings' or not settings_file.is_absolute() or settings_file.resolve() != expected_file.resolve():
                raise identity['Unverified']()
            # Retain the existing shared-session policy. The real CCS child
            # supplied this stable file; its parent/proxy remains alive here.
            settings = identity['read_settings'](settings_file)
            position = args.index('--settings')
            if Path(args[position + 1]).resolve() != settings_file.resolve():
                raise identity['Unverified']()
            del args[position:position + 2]
            _, args, env, material = identity['resolve_actual_context'](binary, ['ccs',metadata['profile']], args, dict(os.environ), settings, snapshot)
            identity['validate_shared_mcp'](args, [Path.cwd()/'.mcp.json', Path(os.environ.get('ProgramFiles','C:/Program Files'))/'ClaudeCode/managed-mcp.json'], config_env=env, workspace=Path.cwd())
            snapshot.seek(0); settings = json.load(snapshot)
            hooks = settings.setdefault('hooks', {})
            if not isinstance(hooks, dict): raise identity['Unverified']()
            handler = {'type':'command', 'command':sys.executable, 'args':['-X','utf8','-I',str(Path(channel['__file__']).resolve()),'hook',connection], 'timeout':5}
            for event in ('SessionStart','UserPromptSubmit','MessageDisplay','PreToolUse','PostToolUse','PostToolUseFailure','Stop','StopFailure','SessionEnd'):
                entries = hooks.setdefault(event, [])
                if not isinstance(entries, list): raise identity['Unverified']()
                entries.append({'hooks':[handler.copy()]})
            snapshot.seek(0); snapshot.truncate(); snapshot.write(json.dumps(settings,ensure_ascii=False).encode('utf-8'))
            snapshot.flush()
            channel['save'](folder/'mcp.json', {'mcpServers':{'cage':{'command':sys.executable,'args':['-X','utf8','-I',str(Path(channel['__file__']).resolve()),'serve',connection]}}})
            args += ['--mcp-config',str(folder/'mcp.json'),'--dangerously-load-development-channels','server:cage']
            disabled = identity['shared_channels_disabled']()
            identity['bind'](sid, context['resume'], material)
            verified = True; notice = ''
        except identity['Changed']:
            raise ValueError('CCS 账号或配置身份已变化，聊天记录已保留，请新建会话。')
        except (identity['Unverified'], OSError, ValueError, KeyError, TypeError, IndexError, subprocess.SubprocessError):
            if context['resume']:
                raise ValueError('CCS 原会话身份暂时无法核验，聊天记录已保留。')
            # Unverified identity already prevents channel use. Do not label
            # it as a managed policy that actually disabled Channels.
            args, env, disabled = original, dict(os.environ), False
        child = CliProcess([binary,*args], env)
        with channel['state_file'](connection) as state:
            state.update(pid=child.pid, native_ccs_pending=False, native_ccs_unverified=not verified,
                         native_ccs_notice=notice, blocked=disabled,
                         config=str(Path(env.get('CLAUDE_CONFIG_DIR') or Path.home()/'.claude').resolve()),
                         cli_id=sid, workspace=str(Path.cwd()), closed=False, updated_at=time.time())
        try:
            return child.wait()
        finally:
            child.close()
            with channel['state_file'](connection) as state:
                state['closed'] = True; state['busy'] = False
    finally:
        snapshot.close(); Path(snapshot.name).unlink(missing_ok=True)
