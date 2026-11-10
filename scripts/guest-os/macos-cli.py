"""Run the common Claude session protocol in the ordinary macOS guest account."""
import importlib.util
import json
import os
from pathlib import Path
import re
import sys


source = Path(__file__).resolve().with_name("cli_channel.py")
spec = importlib.util.spec_from_file_location("cli_channel", source)
cli_channel = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cli_channel)
os.environ["PATH"] = "/usr/local/libexec/cage-cli/bin:/usr/local/bin:" + os.environ.get("PATH", os.defpath)


def macos_launch(request):
    prefix = request["prefix"]
    managed = (len(prefix) == 5 and prefix[0] == "env" and re.fullmatch(r"CAGE_CLAUDE_SUBJECT=[a-f0-9]{64}", prefix[1])
               and prefix[2:4] == ["/usr/local/bin/cage-claude-account", "run"]
               and (prefix[4] == "default" or re.fullmatch("[a-f0-9]{24}", prefix[4])))
    ccs = len(prefix) == 2 and prefix[0] == "ccs" and re.fullmatch("[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}", prefix[1])
    if prefix != ["claude"] and not managed and not ccs:
        raise ValueError("不支持的客机 Claude 启动命令")
    # Adopt a nested transcript only inside the same approved mount, without
    # traversing symbolic links or interpreting a saved path in a shell.
    subdir = request.get("project_subdir")
    if subdir:
        directory = os.open(".", os.O_RDONLY | os.O_DIRECTORY)
        try:
            device = os.fstat(directory).st_dev
            for part in subdir.split("/"):
                if part in ("", ".", ".."):
                    raise ValueError("会话目录无效")
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
                os.close(directory)
                directory = child
                if os.fstat(directory).st_dev != device:
                    raise ValueError("会话目录不属于当前挂载")
            os.fchdir(directory)
        finally:
            os.close(directory)
    return cli_channel.launch(request)


if __name__ == "__main__":
    if sys.argv[1] == "macos-launch":
        # An ordinary CLI exit leaves an interactive guest shell in this
        # project. Pass the launch JSON as a literal positional argument.
        script = '"$1" -I "$2" macos-exec "$3"; exec /bin/zsh -i'
        os.execv("/bin/zsh", ["zsh", "-c", script, "cage-claude", sys.executable, str(Path(__file__).resolve()), sys.argv[2]])
    if sys.argv[1] == "macos-exec":
        os.umask(0o077)
        sys.exit(macos_launch(json.loads(sys.argv[2])) or 0)
    sys.exit(cli_channel.main() or 0)
