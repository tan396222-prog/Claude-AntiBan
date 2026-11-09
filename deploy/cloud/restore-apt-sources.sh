#!/bin/sh
# Build mirrors are temporary. Runtime maintenance admits official origins.
set -eu
python3 - <<'PY'
from pathlib import Path
from urllib.parse import urlsplit

source = Path('/etc/apt/sources.list.d/debian.sources')
rows = source.read_text().splitlines()
seen = set()
for index, row in enumerate(rows):
    if not row.startswith('URIs:'):
        continue
    entries = row.split()[1:]
    if len(entries) != 1:
        raise SystemExit('Unexpected Debian source layout; source file was not changed.')
    uri = urlsplit(entries[0])
    path = uri.path.rstrip('/')
    if uri.scheme not in ('http', 'https') or path not in ('/debian', '/debian-security') or uri.query or uri.fragment:
        raise SystemExit('Unexpected Debian source origin; source file was not changed.')
    if path in seen:
        raise SystemExit('Duplicate Debian source; source file was not changed.')
    seen.add(path)
    rows[index] = 'URIs: http://deb.debian.org' + path
if seen != {'/debian', '/debian-security'}:
    raise SystemExit('Both Debian archive and security sources are required; source file was not changed.')
source.write_text('\n'.join(rows) + '\n')
PY
