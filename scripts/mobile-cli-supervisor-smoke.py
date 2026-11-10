# Linux guest check. Each fixture creates only its own short-lived descendants.
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

def until(condition, message, timeout=5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(0.025)
    raise AssertionError(message)

def verify(supervisor, crash):
    with tempfile.TemporaryDirectory(prefix="cage-supervisor-") as directory:
        root = Path(directory)
        child = (
            "import os,time,pathlib;"
            "pathlib.Path('tool.pid').write_text(str(os.getpid()));"
            "time.sleep(1.8);pathlib.Path('late-write').write_text('unexpected')"
        )
        middle = (
            "import sys,subprocess; "
            f"p=subprocess.Popen([sys.executable,'-c',{child!r}],start_new_session=True);p.wait()"
        )
        cli = (
            "import os,sys,subprocess,pathlib;sys.stdin.read();"
            "pathlib.Path('cli.pid').write_text(str(os.getpid()));"
            f"p=subprocess.Popen([sys.executable,'-c',{middle!r}],start_new_session=True);p.wait()"
        )
        proc = subprocess.Popen(
            [sys.executable, "-I", supervisor, directory, sys.executable, "-c", cli],
            stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        with proc:
            proc.stdin.write(json.dumps("synthetic approved command").encode() + b"\n")
            proc.stdin.flush()
            until(lambda: (root / "tool.pid").is_file(), "tool did not start")
            if crash:
                os.kill(int((root / "cli.pid").read_text()), signal.SIGKILL)
            else:
                proc.stdin.close()
            code = proc.wait(timeout=6)
            if code == 0:
                raise AssertionError("interrupted CLI reported success")
            until(lambda: not Path("/proc", (root / "tool.pid").read_text()).exists(),
                  "detached tool survived supervision")
            time.sleep(1.9)
            if (root / "late-write").exists():
                raise AssertionError("tool changed project after termination")

if __name__ == "__main__":
    if not sys.platform.startswith("linux"):
        raise SystemExit("run inside an isolated Linux guest")
    for crash in (False, True):
        verify(str(Path(sys.argv[1]).resolve()), crash)
        print("CLI crash: PASS" if crash else "SSH disconnect: PASS")
