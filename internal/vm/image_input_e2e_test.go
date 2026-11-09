//go:build e2e

package vm

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// An isolated VM and X11 application verify actual PNG ownership, key delivery,
// large INCR transfers, repeated paste, and private terminal attachment storage.
func TestImageInput(t *testing.T) {
	for _, key := range []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "all_proxy"} {
		t.Setenv(key, "")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	l := newTestLima(t)
	state := t.TempDir()
	t.Setenv("AGENT_VM_STATE_DIR", state)
	if port := os.Getenv("CAGE_TEST_PROXY_PORT"); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			t.Fatal("invalid CAGE_TEST_PROXY_PORT")
		}
		if err := os.WriteFile(filepath.Join(state, "network"), []byte("allow 127.0.0.1:"+port+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"} {
			t.Setenv(key, "http://127.0.0.1:"+port)
		}
	}
	mounts := []Mount{}
	name := "image-input-test"
	if err := l.Create(ctx, name, "template:debian-13", Settings{CPUs: 2, MemoryGiB: 2, DiskGiB: 10, Mounts: &mounts, NoContainerd: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Delete(context.Background(), name) })
	var log bytes.Buffer
	if err := l.Start(ctx, name, &log); err != nil {
		t.Fatalf("start: %v\n%s", err, log.String())
	}
	run := func(script string, input []byte) {
		t.Helper()
		var output bytes.Buffer
		code, err := l.Shell(ctx, name, ShellOpts{Workdir: "/tmp", Args: []string{"bash", "-c", script}, Stdin: bytes.NewReader(input), Stdout: &output, Stderr: &output})
		if err != nil || code != 0 {
			t.Fatalf("guest: %v / %d\n%s", err, code, output.String())
		}
		text := output.String()
		if len(text) > 1600 {
			text = text[len(text)-1600:]
		}
		t.Log(text)
	}
	run("sudo apt-get update -qq && sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends libgtk-3-0 xdotool xvfb xauth python3-gi gir1.2-gtk-3.0", nil)
	helper, err := os.ReadFile("../cage/image-input.py")
	if err != nil {
		t.Fatal(err)
	}
	run("cat > /tmp/cage-image-input.py", helper)
	run("xvfb-run -a python3 -", []byte(imageInputAcceptance))
}

const imageInputAcceptance = `
import gi
gi.require_version('Gtk', '3.0')
from gi.repository import Gtk, Gdk, GLib
import hashlib, json, os, pathlib, struct, subprocess, threading, zlib

def png(seed):
    def chunk(kind, data):
        return struct.pack('!I',len(data))+kind+data+struct.pack('!I',zlib.crc32(kind+data))
    # Over 3 MB: this must traverse X11's INCR protocol.
    raw=b''.join(b'\0'+hashlib.shake_256(bytes([seed,i%256])).digest(1024*3) for i in range(1024))
    return b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('!2I5B',1024,1024,8,2,0,0,0))+chunk(b'IDAT',zlib.compress(raw))+chunk(b'IEND',b'')
images=[png(1),png(2)]
received=[]
errors=[]
window=Gtk.Window()
window.set_title('Cage image input test')
field=Gtk.TextView()
window.add(field)
def key(_widget,event):
    if event.keyval==Gdk.KEY_v and event.state & Gdk.ModifierType.CONTROL_MASK:
        def contents(_clipboard,data,_unused):
            received.append(bytes(data.get_data() or b''))
        Gtk.Clipboard.get(Gdk.SELECTION_CLIPBOARD).request_contents(Gdk.Atom.intern('image/png',False),contents,None)
        return True
field.connect('key-press-event',key)
window.show_all()
field.grab_focus()
def worker():
    try:
        subprocess.run(['xdotool','search','--name','Cage image input test','windowfocus','--sync'],check=True,timeout=3)
        for image in images:
            result=subprocess.run(['python3','/tmp/cage-image-input.py','paste'],input=image,capture_output=True,timeout=15)
            assert result.returncode==0,(result.returncode,result.stderr)
        result=subprocess.run(['python3','/tmp/cage-image-input.py','save'],input=images[0],capture_output=True,check=True)
        path=pathlib.Path(json.loads(result.stdout)['path'])
        assert path.read_bytes()==images[0]
        assert path.stat().st_mode & 0o777 == 0o600
        path.unlink()
    except BaseException as error:
        errors.append(repr(error))
    GLib.timeout_add(1000, Gtk.main_quit)
GLib.timeout_add(300,lambda:(threading.Thread(target=worker).start(),False)[1])
GLib.timeout_add(35000,Gtk.main_quit)
Gtk.main()
assert not errors,errors
assert received==images,[len(item) for item in received]
print('PASS: two real X11 image pastes, >3 MB PNG integrity, private saved attachment')
`
