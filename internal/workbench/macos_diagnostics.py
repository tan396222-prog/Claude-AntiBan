"""Read only the two Cage GUI helpers' bounded, machine-code diagnostic logs."""
from collections import deque
from datetime import datetime, timedelta, timezone
import heapq
import itertools
import json
import os
from pathlib import Path
import re
import stat

root = Path.home() / "Library/Logs/Cage"
entries = []
order = itertools.count()
cutoff = datetime.now(timezone.utc) - timedelta(days=14)
if root.exists():
    directory = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        info = os.fstat(directory)
        if info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise ValueError("diagnostic_directory_invalid")
        for source in ("window", "clipboard"):
            # Guest timestamps have second precision. Read rotations oldest
            # first so the newest file wins ties within a source.
            for suffix in ("-4", "-3", "-2", "-1", ""):
                try:
                    fd = os.open(source + suffix + ".jsonl", os.O_RDONLY | os.O_NOFOLLOW, dir_fd=directory)
                except FileNotFoundError:
                    continue
                with os.fdopen(fd, "r", encoding="utf-8", errors="replace") as stream:
                    info = os.fstat(stream.fileno())
                    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_nlink != 1 or info.st_size > (1 << 20) + 4096:
                        raise ValueError("diagnostic_file_invalid")
                    for line in stream:
                        if len(line) > 4096:
                            continue
                        try:
                            value = json.loads(line)
                            time = datetime.fromisoformat(value["time"].replace("Z", "+00:00"))
                            reason = value["reason"]
                            if time.tzinfo is None or time < cutoff or value["source"] != source or not re.fullmatch(r"[a-zA-Z][a-zA-Z0-9_]{0,95}", reason):
                                continue
                            entry = (time, next(order), dict(time=value["time"], source=source, reason=reason))
                            heapq.heappush(entries, entry)
                            if len(entries) > 150:
                                heapq.heappop(entries)
                        except (ValueError, KeyError, TypeError, AttributeError):
                            continue
    finally:
        os.close(directory)
lines = deque()
size = 0
for _, _, entry in sorted(entries):
    line = json.dumps(entry, ensure_ascii=True) + "\n"
    lines.append(line)
    size += len(line.encode())
    while len(lines) > 150 or size > 48 << 10:
        size -= len(lines.popleft().encode())
print(json.dumps(dict(version=1, lines="".join(lines))))
