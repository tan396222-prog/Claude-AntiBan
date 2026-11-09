#!/usr/bin/python3
"""Receive a validated PNG on stdin; never read host paths or use the network."""
import ctypes as C
import fcntl
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import tempfile


def save_image(data):
    directory = Path.home() / '.local/share/cage/attachments'
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, path = tempfile.mkstemp(prefix='image-', suffix='.png', dir=directory)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
    except BaseException:
        os.unlink(path)
        raise
    return {'path': path}


def clipboard_owner(data, ack):
    # GTK handles X11 TARGETS and incremental transfers, including large PNGs.
    # Keep the owner alive after the caller returns until replaced or expired.
    gtk = C.CDLL('libgtk-3.so.0')
    gdk = C.CDLL('libgdk-3.so.0')
    glib = C.CDLL('libglib-2.0.so.0')
    pointer = C.c_void_p
    def bind(lib, name, result, *args):
        fn = getattr(lib, name)
        fn.restype, fn.argtypes = result, list(args)
        return fn
    init = bind(gtk, 'gtk_init_check', C.c_int, pointer, pointer)
    atom = bind(gdk, 'gdk_atom_intern_static_string', pointer, C.c_char_p)
    get = bind(gtk, 'gtk_clipboard_get', pointer, pointer)
    set_data = bind(gtk, 'gtk_selection_data_set', None, pointer, pointer, C.c_int, pointer, C.c_int)
    flush = bind(gdk, 'gdk_flush', None)
    main = bind(gtk, 'gtk_main', None)
    quit_main = bind(gtk, 'gtk_main_quit', None)
    callback = C.CFUNCTYPE(C.c_int, pointer)
    timeout = bind(glib, 'g_timeout_add', C.c_uint, C.c_uint, callback, pointer)
    class Target(C.Structure):
        _fields_ = [('target', C.c_char_p), ('flags', C.c_uint), ('info', C.c_uint)]
    provide_type = C.CFUNCTYPE(None, pointer, pointer, C.c_uint, pointer)
    clear_type = C.CFUNCTYPE(None, pointer, pointer)
    own = bind(gtk, 'gtk_clipboard_set_with_data', C.c_int, pointer, C.POINTER(Target), C.c_uint, provide_type, clear_type, pointer)
    if not init(None, None):
        raise RuntimeError('display unavailable')
    png = atom(b'image/png')
    buffer = C.create_string_buffer(data)
    acknowledged = False
    @callback
    def stop(_):
        quit_main()
        return 0
    @provide_type
    def provide(_clipboard, selection, _info, _user):
        nonlocal acknowledged
        set_data(selection, png, 8, buffer, len(data))
        flush()
        if not acknowledged:
            os.write(ack, b'ok')
            os.close(ack)
            acknowledged = True
    @clear_type
    def clear(_clipboard, _user):
        # An INCR transfer can outlive selection ownership.
        timeout(10000, stop, None)
    targets = (Target * 1)(Target(b'image/png', 0, 0))
    if not own(get(atom(b'CLIPBOARD')), targets, 1, provide, clear, None):
        raise RuntimeError('clipboard unavailable')
    flush()
    subprocess.run(['xdotool', 'key', '--clearmodifiers', 'ctrl+v'], check=True,
                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3)
    timeout(120000, stop, None)
    main()


def paste_image(data):
    lock_dir = Path.home() / '.cache/cage'
    lock_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    with open(lock_dir / 'image-input.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        read_fd, write_fd = os.pipe()
        pid = os.fork()
        if pid == 0:
            os.close(read_fd)
            lock.close()
            os.setsid()
            with open(os.devnull, 'r+b') as null:
                for fd in (0, 1, 2):
                    os.dup2(null.fileno(), fd)
            try:
                clipboard_owner(data, write_fd)
            finally:
                os._exit(0)
        os.close(write_fd)
        try:
            ready, _, _ = select.select([read_fd], [], [], 10)
            if not ready or os.read(read_fd, 2) != b'ok':
                os.kill(pid, signal.SIGTERM)
                os.waitpid(pid, 0)
                raise RuntimeError('目标没有接收图片，请点击支持图片的输入框后重试；若环境组件过旧，请先更新环境')
        finally:
            os.close(read_fd)
    return {'ok': True}


if __name__ == '__main__':
    try:
        data = sys.stdin.buffer.read(20 * 1024 * 1024 + 1)
        if not data.startswith(b'\x89PNG\r\n\x1a\n') or len(data) > 20 * 1024 * 1024:
            raise ValueError('PNG input invalid')
        if sys.argv[1:] == ['save']:
            result = save_image(data)
        elif sys.argv[1:] == ['paste']:
            result = paste_image(data)
        else:
            raise ValueError('unknown action')
        print(json.dumps(result))
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
