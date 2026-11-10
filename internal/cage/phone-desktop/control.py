#!/usr/bin/python3
"""Attach an authenticated phone view to the desktop application's display."""
import fcntl
import json
import math
import os
from pathlib import Path
import pwd
import re
import socket
import subprocess
import sys

STATE = Path('/run/cage-phone-desktop-owner')
RUNTIME = Path('/run/cage-phone-desktop')
TOOLS = '/usr/local/lib/cage-phone-desktop'
UNIT = 'cage-phone-desktop.service'


class Failure(Exception):
    def __init__(self, status):
        self.status = status


def command(*args, timeout=10):
    return subprocess.run(args, check=True, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                          stderr=subprocess.DEVNULL, timeout=timeout).stdout


def unit_state():
    return command('systemctl', 'show', UNIT, '-p', 'ActiveState', '--value').strip()


def stop():
    if unit_state() not in (b'inactive', b'failed'):
        command('systemctl', 'stop', UNIT, timeout=20)
    # A collected inactive transient unit often no longer exists.
    if unit_state() == b'failed':
        command('systemctl', 'reset-failed', UNIT)


def launch(request, record):
    if not Path('/usr/bin/claude-desktop').is_file():
        raise Failure(501)
    ident = request['id']
    w, h = request['width'], request['height']
    pixel_ratio = request.get('pixel_ratio', 0)
    if type(pixel_ratio) not in (int, float) or not math.isfinite(pixel_ratio) or (pixel_ratio != 0 and not 1 <= pixel_ratio <= 8):
        raise Failure(400)
    if type(w) is not int or type(h) is not int or not (240 <= w <= 1440 and 160 <= h <= 1600):
        raise Failure(400)
    desktop = command('systemctl', 'show', 'cage-claude-desktop.service', '-p', 'ActiveState', '--value').strip()
    if desktop != b'active' or not Path('/run/cage-desktop/keyring-ready').exists():
        raise Failure(412)
    owner = {'id': ident, 'mode': 'shared'}
    state = unit_state()
    if record.exists() and json.loads(record.read_bytes()) == owner and state in (b'active', b'activating'):
        return
    stop()
    user = pwd.getpwnam('cage')
    args = ['systemd-run', '--quiet', '--collect', '--unit=' + UNIT, '--service-type=exec',
            '--working-directory=' + user.pw_dir, '--uid=' + user.pw_name, '--gid=' + str(user.pw_gid)]
    # This unit owns only capture and input. Closing it leaves Desktop running.
    for prop in ['RuntimeDirectory=cage-phone-desktop',
                 'RuntimeDirectoryMode=0700', 'UMask=0077', 'KillMode=control-group', 'TimeoutStopSec=8',
                 'ProtectSystem=strict', 'ProtectHome=read-only',
                 'ReadWritePaths=/run/cage-desktop',
                 'ExecStopPost=' + TOOLS + '/session.sh --restore',
                 'BindsTo=cage-claude-desktop.service', 'After=cage-claude-desktop.service',
                 'StandardOutput=null', 'StandardError=null']:
        args.append('--property=' + prop)
    for name, value in [('HOME', user.pw_dir), ('CAGE_PHONE_ID', ident), ('CAGE_PHONE_WIDTH', w), ('CAGE_PHONE_HEIGHT', h),
                        ('CAGE_PHONE_PIXEL_RATIO', pixel_ratio)]:
        args.append('--setenv=' + name + '=' + str(value))
    command(*args, TOOLS + '/session.sh')
    temporary = record.with_suffix('.new')
    temporary.write_text(json.dumps(owner))
    os.replace(temporary, record)


def main():
    os.umask(0o077)
    if os.geteuid() or len(sys.argv) != 1:
        raise Failure(403)
    raw = sys.stdin.buffer.readline(65537)
    if len(raw) > 65536:
        raise Failure(400)
    req = json.loads(raw)
    if not re.fullmatch('[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}', req.get('id', '')):
        raise Failure(400)
    if req.get('op') not in ('open', 'frame', 'action', 'close'):
        raise Failure(400)
    STATE.mkdir(mode=0o700, exist_ok=True)
    record = STATE / 'owner'
    with open(STATE / 'lock', 'a+b') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if req['op'] == 'open':
            launch(req, record)
            return {'id': req['id']}
        owner = json.loads(record.read_bytes()) if record.exists() else {}
        if owner.get('id') != req['id'] or owner.get('mode') != 'shared':
            raise Failure(410)
        if req['op'] == 'close':
            stop()
            record.unlink(missing_ok=True)
            return {'id': req['id']}
        if unit_state() not in (b'active', b'activating'):
            raise Failure(503)
        user = pwd.getpwnam('cage')
        os.setgroups([])
        os.setgid(user.pw_gid)
        os.setuid(user.pw_uid)
    with socket.socket(socket.AF_UNIX) as channel:
        channel.settimeout(8)
        try:
            channel.connect(str(RUNTIME / 'view.sock'))
        except (FileNotFoundError, ConnectionRefusedError):
            raise Failure(409)
        channel.sendall(raw)
        reply = channel.makefile('rb').readline(3600001)
        if not reply or len(reply) > 3600000:
            raise Failure(503)
        return json.loads(reply)


if __name__ == '__main__':
    try:
        print(json.dumps(main(), separators=(',', ':')))
    except Failure as error:
        print(json.dumps({'status': error.status}))
    except Exception:
        print('{"status":503}')
