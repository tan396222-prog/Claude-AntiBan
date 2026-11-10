import fcntl
import json
import os
import sys
import tempfile
from pathlib import Path

def owner(window, pid_atom):
    from Xlib import X
    prop = window.get_full_property(pid_atom, X.AnyPropertyType)
    if prop is None or len(prop.value) != 1:
        return None
    pid = int(prop.value[0])
    try:
        if Path(f'/proc/{pid}/exe').resolve() != Path('/usr/lib/claude-desktop/claude-desktop'):
            return None
        fields = Path(f'/proc/{pid}/stat').read_text().split(') ', 1)[1].split()
        return [pid, fields[19]]
    except (OSError, ValueError):
        return None


def visibility(show):
    runtime = Path('/run/cage-desktop')
    if not runtime.is_dir():
        return
    # Startup and legacy unmanaged sessions must remain viewable before the
    # managed session is ready. Previously hidden windows can always restore.
    if not (runtime / 'keyring-ready').exists() and not (runtime / 'hidden-windows.json').exists():
        return
    from Xlib import X, display, error, protocol
    os.environ.update(DISPLAY=':78', XAUTHORITY=str(runtime / 'Xauthority'))
    os.umask(0o077)
    with (runtime / 'visibility.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        connection = display.Display()
        try:
            root = connection.screen().root
            pid_atom = connection.intern_atom('_NET_WM_PID')
            saved_file = runtime / 'hidden-windows.json'
            saved = json.loads(saved_file.read_text()) if saved_file.exists() else {'windows': {}, 'active': 0}
            if show:
                mapped = []
                for key, identity in saved['windows'].items():
                    window = connection.create_resource_object('window', int(key))
                    try:
                        if owner(window, pid_atom) != identity:
                            continue
                        window.map()
                        mapped.append(window.id)
                    except error.XError:
                        continue
                connection.sync()
                target = saved['active'] if saved['active'] in mapped else (mapped[-1] if mapped else 0)
                if target:
                    root.send_event(protocol.event.ClientMessage(window=target, client_type=connection.intern_atom('_NET_ACTIVE_WINDOW'),
                                    data=(32, [2, X.CurrentTime, 0, 0, 0])),
                                    event_mask=X.SubstructureRedirectMask | X.SubstructureNotifyMask)
                    connection.sync()
                saved_file.unlink(missing_ok=True)
                return
            clients = root.get_full_property(connection.intern_atom('_NET_CLIENT_LIST'), X.AnyPropertyType)
            active = root.get_full_property(connection.intern_atom('_NET_ACTIVE_WINDOW'), X.AnyPropertyType)
            windows = []
            for wid in clients.value if clients is not None else []:
                window = connection.create_resource_object('window', int(wid))
                try:
                    identity = owner(window, pid_atom)
                    if identity is None or window.get_attributes().map_state != X.IsViewable:
                        continue
                    saved['windows'][str(window.id)] = identity
                    windows.append(window)
                except error.XError:
                    continue
            if not windows:
                return
            if active is not None and len(active.value) == 1 and str(int(active.value[0])) in saved['windows']:
                saved['active'] = int(active.value[0])
            # Save the owned windows before withdrawing them from the window manager.
            # Their IDs disappear from _NET_CLIENT_LIST while they are hidden.
            with tempfile.NamedTemporaryFile(mode='w', dir=runtime, prefix='hidden-windows-', delete=False) as pending:
                json.dump(saved, pending)
            Path(pending.name).replace(saved_file)
            for window in windows:
                try:
                    if owner(window, pid_atom) == saved['windows'][str(window.id)]:
                        window.unmap()
                except error.XError:
                    continue
            connection.sync()
        finally:
            connection.close()


if __name__ == '__main__':
    visibility(sys.argv[1] == 'true')
