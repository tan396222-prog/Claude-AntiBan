import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


downloads = load('downloads', 'desktop-downloads.py')
copy = load('copy_download', 'save-download.py')


@unittest.skipUnless(sys.platform == 'linux', 'inotify is supplied by the Linux guest')
class DesktopDownloadsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.workspace = self.root / 'shared'
        self.workspace.mkdir()
        self.finished = threading.Event()
        self.manager = downloads.Downloads(self.root / 'private', str(self.workspace), copy.copy_to_workspace)
        original = self.manager.persist
        def persist():
            original()
            if self.manager.records and list(self.manager.records.values())[-1]['state'] in ('saved', 'failed', 'canceled'):
                self.finished.set()
        self.manager.persist = persist

    def wait(self):
        self.assertTrue(self.finished.wait(5), self.manager.snapshot())
        return self.manager.snapshot()[0]

    def test_chromium_partial_is_not_published_until_final_rename(self):
        target = Path(self.manager.begin('视频 #+%.mp4'))
        target.write_bytes(b'')  # Chromium reserves the destination first.
        temporary = target.with_name(target.name + '.crdownload')
        temporary.write_bytes(b'partial')
        self.assertFalse(self.finished.wait(.1))
        self.assertEqual(self.manager.snapshot()[0]['state'], 'downloading')
        self.assertFalse((self.workspace / 'Downloads').exists())
        with temporary.open('ab') as output:
            output.write(b' complete')
        temporary.rename(target)
        item = self.wait()
        self.assertEqual(item['state'], 'saved')
        self.assertEqual(Path(item['path']).read_bytes(), b'partial complete')

    def test_direct_export_waits_for_close_and_preserves_existing_file(self):
        (self.workspace / 'Downloads').mkdir()
        (self.workspace / 'Downloads' / 'video.mp4').write_bytes(b'original')
        target = Path(self.manager.begin('video.mp4'))
        with target.open('wb') as output:
            output.write(b'export')
            output.flush()
            self.assertFalse(self.finished.wait(.1))
        item = self.wait()
        self.assertEqual(item['name'], 'video (1).mp4')
        self.assertEqual(Path(item['path']).read_bytes(), b'export')
        self.assertEqual((self.workspace / 'Downloads' / 'video.mp4').read_bytes(), b'original')

    def test_cancel_discards_partial_and_empty_download_completes_by_rename(self):
        target = Path(self.manager.begin('canceled.mp4'))
        temporary = target.with_name(target.name + '.crdownload')
        temporary.write_bytes(b'partial')
        temporary.unlink()
        self.assertEqual(self.wait()['state'], 'canceled')
        self.assertFalse((self.workspace / 'Downloads').exists())
        self.finished.clear()
        target = Path(self.manager.begin('empty.txt'))
        temporary = target.with_name(target.name + '.crdownload')
        temporary.write_bytes(b'')
        temporary.rename(target)
        item = self.wait()
        self.assertEqual(item['state'], 'saved')
        self.assertEqual(Path(item['path']).read_bytes(), b'')

    def test_failed_copy_can_retry_after_restart_but_incomplete_cannot(self):
        def fail(*args):
            raise OSError('test disk full')
        self.manager.copier = fail
        target = Path(self.manager.begin('retry.mp4'))
        target.write_bytes(bytes(range(256)) * 1024)
        item = self.wait()
        self.assertEqual(item['state'], 'failed')
        self.assertTrue(item['retryable'])
        restored = downloads.Downloads(self.manager.directory, str(self.workspace), copy.copy_to_workspace)
        restored.save(restored.records[item['id']])
        self.assertEqual(restored.snapshot()[0]['state'], 'saved')
        self.assertEqual(Path(restored.snapshot()[0]['path']).read_bytes(), target.read_bytes())
        # A crash while Chromium is still writing must never promote partial data.
        records = json.loads(restored.manifest.read_text())
        records[0].update(state='downloading', complete=False)
        restored.manifest.write_text(json.dumps(records))
        interrupted = downloads.Downloads(self.manager.directory, str(self.workspace), copy.copy_to_workspace)
        self.assertFalse(interrupted.snapshot()[0]['retryable'])
        with self.assertRaises(ValueError): interrupted.retry(item['id'])

    def test_invalid_names_and_symlinks_never_copy_arbitrary_files(self):
        for name in ['', '..', '../secret', 'a/b', 'a\\b', 'a\x00b', 'a\nb']:
            with self.assertRaises(ValueError): self.manager.begin(name)
        secret = self.root / 'secret'
        secret.write_bytes(b'private')
        target = Path(self.manager.begin('link.mp4'))
        temporary = target.with_name('link.tmp')
        temporary.symlink_to(secret)
        temporary.rename(target)
        self.assertEqual(self.wait()['state'], 'failed')
        self.assertFalse((self.workspace / 'Downloads').exists())

    @unittest.skipUnless(os.environ.get('DBUS_SESSION_BUS_ADDRESS'), 'run under dbus-run-session')
    def test_real_portal_savefile_response_and_cancel_openfile(self):
        import gi
        gi.require_version('Gio', '2.0')
        from gi.repository import Gio, GLib
        files = load('desktop_files', 'desktop-files.py')
        broker = files.Broker(self.root / 'uploads', downloads=self.manager)
        portal = files.Portal(broker)
        broker.deliver = portal.deliver
        threading.Thread(target=portal.run, daemon=True).start()
        client = Gio.DBusConnection.new_for_address_sync(os.environ['DBUS_SESSION_BUS_ADDRESS'],
            Gio.DBusConnectionFlags.AUTHENTICATION_CLIENT | Gio.DBusConnectionFlags.MESSAGE_BUS_CONNECTION, None, None)
        responses, received = [], threading.Event()
        def response(_connection, _sender, _path, _interface, _signal, parameters):
            responses.append(parameters.unpack())
            received.set()
        client.signal_subscribe(None, 'org.freedesktop.portal.Request', 'Response', None, None,
                                Gio.DBusSignalFlags.NONE, response)
        result = client.call_sync('org.freedesktop.portal.Desktop', files.ROOT, files.PORTAL, 'SaveFile',
            GLib.Variant('(ssa{sv})', ('', '保存视频', {'current_name': GLib.Variant('s', '真实视频.mp4'),
                                                     'handle_token': GLib.Variant('s', 'save_probe')})),
            GLib.VariantType.new('(o)'), Gio.DBusCallFlags.NONE, 5000, None)
        self.assertTrue(result.unpack()[0].endswith('/save_probe'))
        self.assertTrue(received.wait(5))
        self.assertEqual(responses[0][0], 0)
        from urllib.parse import unquote, urlsplit
        target = Path(unquote(urlsplit(responses[0][1]['uris'][0]).path))
        target.write_bytes(b'real dbus export')
        self.assertEqual(Path(self.wait()['path']).read_bytes(), b'real dbus export')
        received.clear()
        client.call_sync('org.freedesktop.portal.Desktop', files.ROOT, files.PORTAL, 'OpenFile',
            GLib.Variant('(ssa{sv})', ('', '打开文件', {'handle_token': GLib.Variant('s', 'open_probe')})),
            GLib.VariantType.new('(o)'), Gio.DBusCallFlags.NONE, 5000, None)
        request = broker.snapshot()['uploads'][0]
        broker.cancel(request['id'])
        self.assertTrue(received.wait(5))
        self.assertEqual(responses[-1][0], 1)

        if os.environ.get('CAGE_TEST_CHROMIUM'):
            # Real Chromium uses the same portal backend as Electron. Do not set
            # CDP download behavior: it would bypass the broken SaveFile entry.
            from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
            import subprocess
            payload = bytes(range(256)) * 8192
            class DownloadHandler(BaseHTTPRequestHandler):
                def log_message(self, *_): pass
                def do_GET(self):
                    self.send_response(200)
                    self.send_header('Content-Type', 'video/mp4')
                    self.send_header('Content-Disposition', 'attachment; filename="chromium-video.mp4"')
                    self.send_header('Content-Length', str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
            server = ThreadingHTTPServer(('127.0.0.1', 0), DownloadHandler)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            profile = self.root / 'chromium'
            (profile / 'Default').mkdir(parents=True)
            (profile / 'Default/Preferences').write_text(json.dumps({'download': {'prompt_for_download': True}}))
            self.finished.clear()
            log = (self.root / 'chromium.log').open('w+')
            process = subprocess.Popen(['/usr/bin/chromium', f'--user-data-dir={profile}', '--no-first-run',
                '--no-default-browser-check', '--disable-background-networking', '--disable-gpu', '--password-store=basic',
                '--xdg-portal-required-version=3', f'http://127.0.0.1:{server.server_port}/video'],
                env={**os.environ, 'GTK_USE_PORTAL': '1'}, stdout=log, stderr=log)
            try:
                if not self.finished.wait(20):
                    log.seek(0)
                    self.fail('Chromium SaveFile did not complete: ' + log.read()[-3000:])
                item = self.manager.snapshot()[0]
                self.assertEqual(item['name'], 'chromium-video.mp4')
                self.assertEqual(item['state'], 'saved', item)
                self.assertEqual(Path(item['path']).read_bytes(), payload)
            finally:
                process.terminate()
                try: process.wait(timeout=5)
                except subprocess.TimeoutExpired: process.kill(); process.wait()
                log.close()
                server.shutdown()
                server.server_close()


if __name__ == '__main__':
    unittest.main()
