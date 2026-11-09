import importlib.util
import io
from pathlib import Path
import tempfile
import unittest

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


if __name__ == '__main__':
    unittest.main()
