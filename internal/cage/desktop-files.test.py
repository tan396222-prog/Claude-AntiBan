import importlib.util
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('desktop_files', Path(__file__).with_name('desktop-files.py'))
files = importlib.util.module_from_spec(spec)
spec.loader.exec_module(files)


class FileBrokerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.delivered = []
        self.broker = files.Broker(self.temp.name, lambda request, paths: self.delivered.append((request, paths)))

    def test_original_bytes_names_and_lazy_reads(self):
        key = self.broker.start('chooser', multiple=True)['id']
        for name, data in [('视频 #+%.mp4', bytes(range(256)) * 32768), ('空文档.txt', b'')]:
            self.broker.receive(key, name, iter(lambda: stream.read(65536), b'') if (stream := io.BytesIO(data)) else [])
        self.assertEqual(self.broker.snapshot()['uploads'][0]['target'], 'desktop')
        self.broker.finish(key)
        paths = self.delivered[0][1]
        self.assertEqual(Path(paths[0]).name, '视频 #+%.mp4')
        self.assertEqual(Path(paths[0]).read_bytes(), bytes(range(256)) * 32768)
        self.assertEqual(Path(paths[1]).read_bytes(), b'')
        self.assertEqual(self.broker.snapshot()['uploads'], [])

    def test_single_selection_invalid_names_and_cancel_midstream(self):
        key = self.broker.start('chooser', multiple=False)['id']
        for name in ['../secret', 'a/b', 'a\\b', '..', 'a\x00b']:
            with self.assertRaises(ValueError): self.broker.receive(key, name, [b'bad'])
        self.broker.receive(key, 'one.txt', [b'one'])
        with self.assertRaises(ValueError): self.broker.receive(key, 'two.txt', [b'two'])
        self.broker.cancel(key)
        self.assertIsNone(self.delivered[-1][1])
        key = self.broker.start('paste')['id']
        def chunks():
            yield b'partial'
            self.broker.cancel(key)
            yield b'rejected'
        with self.assertRaises(ValueError): self.broker.receive(key, 'video.mp4', chunks())
        self.assertFalse((Path(self.temp.name) / key).exists())
        with self.assertRaises(ValueError): self.broker.finish(key)

    def test_failed_delivery_cleans_batch(self):
        key = self.broker.start('drop')['id']
        self.broker.receive(key, 'video.mp4', [b'content'])
        def fail(*_): raise ValueError('target closed')
        self.broker.deliver = fail
        with self.assertRaisesRegex(ValueError, 'target closed'): self.broker.finish(key)
        self.assertFalse((Path(self.temp.name) / key).exists())

    def test_image_preparation_retains_focus_and_releases_received_bytes(self):
        context = 'a' * 32
        received = []
        def paste(paths, expected_context=None):
            received.append((Path(paths[0]).read_bytes(), expected_context))
        with patch.object(files, 'image_context', lambda: context), patch.object(files, 'paste_files', paste):
            key = self.broker.start('image')['id']
            self.broker.receive(key, 'image.png', [b'PNG fixture'])
            with self.assertRaises(ValueError):
                self.broker.finish(key)
            self.broker.finish_image(key)
            self.assertEqual(received, [(b'PNG fixture', 'a' * 32)])
            self.assertFalse((Path(self.temp.name) / key).exists())
            key = self.broker.start('image')['id']
            self.broker.receive(key, 'image.png', [b'old image'])
            context = 'b' * 32
            with self.assertRaisesRegex(ValueError, '接收位置已变化'):
                self.broker.finish_image(key)
            self.assertEqual(len(received), 1)
            self.assertFalse((Path(self.temp.name) / key).exists())

    def test_directory_selection_returns_live_directory_without_copying(self):
        project = Path(self.temp.name) / '项目 #+%'
        project.mkdir()
        (project / 'keep.txt').write_text('project data')
        key = self.broker.start('chooser', multiple=False, directory=True)['id']
        self.assertTrue(self.broker.snapshot()['uploads'][0]['directory'])
        with self.assertRaises(ValueError): self.broker.receive(key, 'file.txt', [b'wrong mode'])
        with self.assertRaises(ValueError): self.broker.choose_directory(key, str(project / 'missing'))
        with self.assertRaises(ValueError): self.broker.choose_directory(key, 'relative')
        self.broker.choose_directory(key, str(project))
        self.assertEqual(self.delivered[-1][1], [str(project)])
        self.assertEqual(self.broker.snapshot()['uploads'], [])
        self.assertEqual((project / 'keep.txt').read_text(), 'project data')
        key = self.broker.start('chooser', multiple=False, directory=True)['id']
        self.broker.cancel(key)
        self.assertIsNone(self.delivered[-1][1])
        self.assertTrue(project.is_dir())
        key = self.broker.start('chooser')['id']
        with self.assertRaises(ValueError): self.broker.choose_directory(key, str(project))

    def test_local_file_uri_and_relative_folder_become_single_use_requests(self):
        document = Path(self.temp.name) / '报告 #+%.docx'
        document.write_bytes(b'document')
        key = self.broker.open_path(document.as_uri(), 'reveal')['id']
        self.assertEqual(self.broker.snapshot()['opens'], [{'id': key}])
        # Two native windows must not launch the same action twice.
        results = []
        threads = [threading.Thread(target=lambda: results.append(self.broker.claim_open(key))) for _ in range(2)]
        for thread in threads: thread.start()
        for thread in threads: thread.join()
        self.assertEqual(sum(r.get('handled', False) for r in results), 1)
        self.assertIn({'id': key, 'path': str(document.resolve()), 'action': 'reveal'}, results)
        self.broker.complete_open(key)
        self.assertEqual(self.broker.snapshot()['opens'], [])
        self.assertEqual(self.broker.claim_open(key), {'handled': True})
        for path in ['', 'file://remote-host/etc/passwd', document.as_uri() + '?invalid', 'https://example.com']:
            with self.assertRaises((ValueError, OSError)):
                self.broker.open_path(path)

    def test_portal_failure_timeout_and_close_release_requests(self):
        chooser = self.broker.start('chooser')['id']
        self.broker.complete_open(chooser)
        self.assertIn(chooser, self.broker.requests)
        self.broker.cancel(chooser)
        key = self.broker.open_path(self.temp.name, 'reveal', handle='/request/test')['id']
        self.broker.claim_open(key)
        self.broker.complete_open(key, 'permission denied')
        self.assertIsNone(self.delivered[-1][1])
        key = self.broker.open_path(self.temp.name, handle='/request/timeout')['id']
        self.broker.requests[key]['created'] -= 61
        self.assertEqual(self.broker.snapshot()['opens'], [])
        self.assertNotIn(key, self.broker.requests)
        self.assertIsNone(self.delivered[-1][1])


@unittest.skipUnless(sys.platform == 'linux' and os.environ.get('DBUS_SESSION_BUS_ADDRESS'),
                     'requires an isolated Linux D-Bus session')
class DesktopFileDBusTests(unittest.TestCase):
    def test_real_filemanager_portal_fd_and_xdg_open(self):
        import gi
        gi.require_version('Gio', '2.0')
        from gi.repository import Gio, GLib
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            document = root / 'Word 报告 #+%.docx'
            document.write_bytes(b'generated document')
            broker = files.Broker(root / 'uploads')
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
            client.call_sync('org.freedesktop.FileManager1', '/org/freedesktop/FileManager1',
                'org.freedesktop.FileManager1', 'ShowItems', GLib.Variant('(ass)', ([document.as_uri()], '')),
                None, Gio.DBusCallFlags.NONE, 5000, None)
            key = broker.snapshot()['opens'][0]['id']
            self.assertEqual(broker.claim_open(key)['path'], str(document))
            broker.complete_open(key)

            for failure in (False, True):
                descriptors = Gio.UnixFDList.new()
                with document.open('rb') as source:
                    index = descriptors.append(source.fileno())
                result, _ = client.call_with_unix_fd_list_sync('org.freedesktop.portal.Desktop', files.ROOT,
                    'org.freedesktop.portal.OpenURI', 'OpenDirectory',
                    GLib.Variant('(sha{sv})', ('', index, {'handle_token': GLib.Variant('s', 'reveal_' + str(failure))})),
                    GLib.VariantType.new('(o)'), Gio.DBusCallFlags.NONE, 5000, descriptors, None)
                key = broker.snapshot()['opens'][0]['id']
                request = broker.claim_open(key)
                self.assertEqual(request['path'], str(document))
                self.assertEqual(request['action'], 'reveal')
                self.assertEqual(broker.requests[key]['handle'], result.unpack()[0])
                received.clear()
                broker.complete_open(key, 'test denied' if failure else '')
                self.assertTrue(received.wait(5))
                self.assertEqual(responses[-1], (2 if failure else 0, {}))

            # Run the actual shell and Python entry points against their private
            # Unix socket. This also exercises Electron's relative `.` fallback.
            socket = Path('/run/cage-desktop/files.sock')
            server = files.FileServer(str(socket), files.Handler)
            server.broker = broker
            threading.Thread(target=server.serve_forever, daemon=True).start()
            try:
                for argument, want in ((document.as_uri(), str(document)), ('.', str(root))):
                    subprocess.run(['sh', str(Path(__file__).with_name('guest-open.sh').resolve()), argument],
                                   cwd=root, check=True, timeout=10)
                    key = broker.snapshot()['opens'][0]['id']
                    self.assertEqual(broker.claim_open(key)['path'], want)
                    broker.complete_open(key)
            finally:
                server.shutdown()
                server.server_close()
                socket.unlink(missing_ok=True)


if __name__ == '__main__':
    unittest.main()
