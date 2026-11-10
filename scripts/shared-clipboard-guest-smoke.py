"""Isolated real-X11 clipboard check; never connects to the user's VM.

Run with CAGE_CLIPBOARD_QA_SSH_CONFIG and CAGE_CLIPBOARD_QA_SSH_TARGET.
The supplied guest must have disposable :77 and :78 displays and the helper.
"""
import base64
import json
import os
from pathlib import Path
import queue
import subprocess
import threading
import time

ssh = ['ssh', '-F', os.environ['CAGE_CLIPBOARD_QA_SSH_CONFIG'], os.environ['CAGE_CLIPBOARD_QA_SSH_TARGET']]
directory = '/tmp/cage-clipboard-ui-qa'
helper = directory + '/shared-clipboard.py'
bridge = subprocess.Popen(ssh + ['sudo python3 ' + helper], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
events = queue.Queue()
threading.Thread(target=lambda: [events.put(json.loads(line)) for line in bridge.stdout], daemon=True).start()
owners = []


def remote(code, display=':78'):
    return subprocess.check_output(ssh + ['env DISPLAY=' + display + ' NO_AT_BRIDGE=1 python3 -'], input=code, text=True, timeout=12)


def expect(predicate):
    until = time.monotonic() + 8
    observed = []
    while time.monotonic() < until:
        try:
            value = events.get(timeout=.2)
        except queue.Empty:
            if bridge.poll() is not None:
                raise RuntimeError('Clipboard bridge exited: ' + bridge.stderr.read())
            continue
        observed.append({key: (len(value[key]) if key == 'png' else value[key]) for key in value})
        if predicate(value):
            return value
    raise AssertionError('Expected clipboard event not received: ' + json.dumps(observed))


def send(value, serial):
    bridge.stdin.write(json.dumps({**value, 'serial': serial}) + '\n')
    bridge.stdin.flush()
    expect(lambda event: event.get('ack') == serial)


imports = '''import gi, base64, json, ctypes as C
gi.require_version('Gtk', '3.0')
from gi.repository import Gtk, Gdk, GdkPixbuf, GLib
Gtk.init([])
clipboard = Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD)
'''


def owner(action, display=':78', user=None):
    code = imports + action + "\nprint('ready',flush=True)\nGLib.timeout_add_seconds(30, lambda: (Gtk.main_quit(), False)[1])\nGtk.main()\n"
    prefix = 'sudo -H -u ' + user + ' ' if user else ''
    process = subprocess.Popen(ssh + [prefix + 'env DISPLAY=' + display + ' NO_AT_BRIDGE=1 python3 -'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    process.stdin.write(code); process.stdin.close()
    assert process.stdout.readline().strip() == 'ready'
    owners.append(process)
    return process


try:
    send({'text': 'host 中文🙂\nsecond line'}, 1)
    print('host text acknowledged', flush=True)
    for display in (':77', ':78'):
        assert json.loads(remote(imports + 'print(json.dumps(clipboard.wait_for_text()))', display)) == 'host 中文🙂\nsecond line'
    print('both displays read text', flush=True)

    # A real GTK button writes the system clipboard only after an X11 click.
    owner("""window = Gtk.Window()
window.set_default_size(240,100)
window.move(40,40)
button = Gtk.Button(label='Copy message')
button.connect('clicked', lambda _button: clipboard.set_text('guest 消息🙂\\nexact text', -1))
window.add(button)
window.show_all()
""")
    subprocess.check_call(ssh + ['env DISPLAY=:78 xdotool mousemove 160 90 click 1'])
    expect(lambda event: event.get('text') == 'guest 消息🙂\nexact text')
    print('native button copy received', flush=True)
    assert json.loads(remote(imports + 'print(json.dumps(clipboard.wait_for_text()))', ':77')) == 'guest 消息🙂\nexact text'

    owner("image = GdkPixbuf.Pixbuf.new(GdkPixbuf.Colorspace.RGB, True, 8, 12, 9)\nimage.fill(0x2468acff)\nclipboard.set_image(image)", ':77')
    image = expect(lambda event: 'png' in event)
    print('native image copy received', flush=True)
    assert base64.b64decode(image['png']).startswith(b'\x89PNG\r\n\x1a\n')
    assert json.loads(remote(imports + "image=clipboard.wait_for_image(); print(json.dumps([image.get_width(),image.get_height()]))")) == [12, 9]
    send({'png': image['png']}, 2)
    assert json.loads(remote(imports + "image=clipboard.wait_for_image(); print(json.dumps([image.get_width(),image.get_height()]))", ':77')) == [12, 9]

    paths = [directory + '/中文.bin', directory + '/folder']
    remote("from pathlib import Path\np=Path('/tmp/cage-clipboard-ui-qa'); (p/'中文.bin').write_bytes(bytes([0,255,128])); (p/'folder').mkdir(exist_ok=True); (p/'folder'/'blank').write_bytes(b'')")
    send({'files': paths}, 3)
    print('files acknowledged', flush=True)
    for display in (':77', ':78'):
        actual = json.loads(remote(imports + 'print(json.dumps(clipboard.wait_for_uris()))', display))
        assert len(actual) == 2 and actual[0].endswith('%E4%B8%AD%E6%96%87.bin') and actual[1].endswith('/folder')

    file_owner = """
gtk = C.CDLL('libgtk-3.so.0'); gdk = C.CDLL('libgdk-3.so.0')
gdk.gdk_atom_intern_static_string.restype=C.c_void_p
gdk.gdk_atom_intern_static_string.argtypes=[C.c_char_p]
class Target(C.Structure):
    _fields_=[('target',C.c_char_p),('flags',C.c_uint),('info',C.c_uint)]
Provide=C.CFUNCTYPE(None,C.c_void_p,C.c_void_p,C.c_uint,C.c_void_p)
Clear=C.CFUNCTYPE(None,C.c_void_p,C.c_void_p)
gtk.gtk_selection_data_set.argtypes=[C.c_void_p,C.c_void_p,C.c_int,C.c_void_p,C.c_int]
gtk.gtk_clipboard_set_with_data.argtypes=[C.c_void_p,C.POINTER(Target),C.c_uint,Provide,Clear,C.c_void_p]
encoded=(uri+'\\r\\n').encode(); buffer=C.create_string_buffer(encoded)
@Provide
def provide(_clipboard,selection,_info,_data):
    gtk.gtk_selection_data_set(selection,gdk.gdk_atom_intern_static_string(b'text/uri-list'),8,buffer,len(encoded))
@Clear
def clear(_clipboard,_data): pass
targets=(Target*1)(Target(b'text/uri-list',0,0))
assert gtk.gtk_clipboard_set_with_data(hash(clipboard),targets,1,provide,clear,None)
"""
    owner("from pathlib import Path\np=Path.home()/'.cache/clipboard-private-qa'; p.mkdir(parents=True,exist_ok=True,mode=0o700); p.chmod(0o700); file=p/'private.bin'; file.write_bytes(bytes([0,255,128])); uri=file.as_uri()\n" + file_owner, ':77', 'cage-browser')
    copied = expect(lambda event: 'files' in event)
    assert copied['source'] == 'cage' and copied['files'][0].startswith('/tmp/cage-clipboard-')
    actual = json.loads(remote(imports + 'from pathlib import Path\nfrom urllib.parse import urlsplit,unquote\nuris=clipboard.wait_for_uris(); print(json.dumps(list(Path(unquote(urlsplit(uris[0]).path)).read_bytes())))'))
    assert actual == [0,255,128], 'private files must be materialized for the other application UID'
    owner("uri='file:///etc/shadow'\n" + file_owner, ':77', 'cage-browser')
    expect(lambda event: 'error' in event)

    # Ordinary drag-selection PRIMARY changes must not replace the clipboard.
    owner("Gtk.Clipboard.get(Gdk.SELECTION_PRIMARY).set_text('drag selection',-1)")
    time.sleep(.3)
    while not events.empty():
        assert events.get().get('text') != 'drag selection'
    print(json.dumps({'text_both_displays': True, 'native_copy_button': True, 'images_both_displays': True, 'files_and_folders_both_displays': True, 'private_file_cross_uid_copy': True, 'root_only_file_denied': True, 'primary_selection_ignored': True}))
finally:
    bridge.stdin.close()
    try:
        bridge.wait(timeout=5)
    except subprocess.TimeoutExpired:
        bridge.terminate()
    for process in owners:
        process.terminate()
