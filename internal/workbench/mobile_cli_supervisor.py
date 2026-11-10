# The first frame selects text or stream-json input. SSH stdin then acts as
# a lease: an explicit finish closes CLI input, EOF disposes the process group.
import json
import ctypes
import os
import selectors
import signal
import subprocess
import sys
import time

def enable_subreaper():
    # Debian guests run on all host platforms. Claude tools can start another
    # process group. Adopt those descendants if the CLI exits unexpectedly.
    if not sys.platform.startswith("linux"):
        return False
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(36, 1, 0, 0, 0) != 0:  # PR_SET_CHILD_SUBREAPER
        raise OSError(ctypes.get_errno(), "cannot contain guest CLI descendants")
    with open("/proc/thread-self/children", "rb") as children:
        children.read()
    return True

def reap_adopted():
    deadline = time.monotonic() + 3
    while True:
        try:
            while os.waitpid(-1, os.WNOHANG)[0]:
                pass
        except ChildProcessError:
            return
        with open("/proc/thread-self/children", "rb") as children:
            pids = [int(value) for value in children.read().split()]
        # Only this supervisor reaps its children. A PID read here cannot be
        # recycled before our next waitpid, including when a child exits.
        for pid in pids:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        if time.monotonic() >= deadline:
            raise RuntimeError("guest CLI descendants still await termination")
        time.sleep(0.02)

FRAME_LIMIT = 256 * 1024

def terminate(child):
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    deadline = time.monotonic() + 0.8
    while time.monotonic() < deadline:
        child.poll()
        try:
            os.killpg(child.pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.04)
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass

def run():
    incoming = bytearray()
    while b"\n" not in incoming:
        block = os.read(0, 8192)
        if not block or len(incoming) + len(block) > FRAME_LIMIT:
            return 2
        incoming.extend(block)
    first, _, incoming = incoming.partition(b"\n")
    mode = json.loads(first)
    streaming = mode == {"stream": True}
    if not streaming and (not isinstance(mode, str) or not 0 < len(mode.encode()) <= 32768):
        return 2
    if not os.path.isabs(sys.argv[1]):
        return 2
    subreaper = enable_subreaper()
    pending = bytearray() if streaming else bytearray(mode.encode())
    finished = not streaming
    child = subprocess.Popen(sys.argv[2:], cwd=sys.argv[1], stdin=subprocess.PIPE,
                             bufsize=0, start_new_session=True)
    def interrupted(_signal, _frame):
        raise SystemExit(130)
    completed = False
    try:
        for signum in (signal.SIGHUP, signal.SIGTERM, signal.SIGINT):
            signal.signal(signum, interrupted)
        os.set_blocking(child.stdin.fileno(), False)
        with selectors.DefaultSelector() as selector:
            selector.register(0, selectors.EVENT_READ)
            watching_write = False
            while child.poll() is None:
                while b"\n" in incoming:
                    raw, _, incoming = incoming.partition(b"\n")
                    if finished or len(raw) > FRAME_LIMIT:
                        return 2
                    frame = json.loads(raw)
                    if frame == {"finish": True}:
                        finished = True
                    elif isinstance(frame, dict) and set(frame) == {"input"} and isinstance(frame["input"], dict):
                        pending.extend(json.dumps(frame["input"], ensure_ascii=False, separators=(",", ":")).encode() + b"\n")
                    else:
                        return 2
                if len(incoming) > FRAME_LIMIT or len(pending) > FRAME_LIMIT * 2:
                    return 2
                if pending and not watching_write:
                    selector.register(child.stdin, selectors.EVENT_WRITE)
                    watching_write = True
                elif not pending:
                    if watching_write:
                        selector.unregister(child.stdin)
                        watching_write = False
                    if finished and not child.stdin.closed:
                        child.stdin.close()
                for key, _ in selector.select(0.15):
                    if key.fd == 0:
                        block = os.read(0, 64 * 1024)
                        if not block:
                            return 130
                        incoming.extend(block)
                    elif pending:
                        try:
                            count = os.write(key.fd, pending)
                            del pending[:count]
                        except BlockingIOError:
                            pass
                        except BrokenPipeError:
                            code = child.wait(timeout=1)
                            completed = code == 0
                            return code
        completed = child.returncode == 0
        return child.returncode
    finally:
        if child.poll() is None:
            if subreaper:
                child.terminate()
                try:
                    child.wait(timeout=0.8)
                except subprocess.TimeoutExpired:
                    child.kill()
            else:
                terminate(child)
        child.wait()
        if subreaper and not completed:
            reap_adopted()

if __name__ == "__main__":
    sys.exit(run())
