import errno
import importlib.util
import os
from pathlib import Path
import tempfile
import sys
import unittest
from unittest import mock

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location('save_download', Path(__file__).with_name('save-download.py'))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class DownloadSaveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='cage-download-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / 'source'
        self.source.write_bytes(b'complete download\x00data')
        self.workspace = self.root / 'workspace'
        self.workspace.mkdir()

    def copy(self, name='report.txt'):
        fd = os.open(self.source, os.O_RDONLY | os.O_NOFOLLOW)
        try:
            return helper.copy_to_workspace(fd, self.source.stat().st_size, str(self.workspace), name)
        finally:
            os.close(fd)

    def test_atomic_publish_does_not_overwrite_existing_file_or_symlink(self):
        destination = self.workspace / 'Downloads'
        destination.mkdir()
        (destination / 'report.txt').write_text('original')
        (destination / 'report (1).txt').symlink_to(self.source)
        result = self.copy()
        self.assertEqual(Path(result['path']).name, 'report (2).txt')
        self.assertEqual(Path(result['path']).read_bytes(), self.source.read_bytes())
        self.assertEqual((destination / 'report.txt').read_text(), 'original')
        self.assertTrue((destination / 'report (1).txt').is_symlink())
        self.assertFalse(list(destination.glob('.cage-download-*')))

    def test_write_failure_keeps_source_and_publishes_no_partial_file(self):
        with mock.patch.object(helper.os, 'fsync', side_effect=OSError(errno.ENOSPC, 'full')):
            with self.assertRaises(OSError):
                self.copy()
        self.assertTrue(self.source.exists())
        self.assertEqual(list((self.workspace / 'Downloads').iterdir()), [])
        result = self.copy()
        self.assertEqual(Path(result['path']).read_bytes(), self.source.read_bytes())

    def test_symlink_download_directory_is_refused(self):
        outside = self.root / 'outside'
        outside.mkdir()
        (self.workspace / 'Downloads').symlink_to(outside, target_is_directory=True)
        with self.assertRaises(OSError):
            self.copy()
        self.assertEqual(list(outside.iterdir()), [])

    def test_cleanup_does_not_delete_replaced_source(self):
        directory = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY)
        self.addCleanup(os.close, directory)
        original = self.source.stat()
        self.source.rename(self.root / 'original')
        self.source.write_text('replacement')
        self.assertFalse(helper.remove_source_if_unchanged(directory, 'source', original))
        self.assertEqual(self.source.read_text(), 'replacement')
        current = self.source.stat()
        self.assertTrue(helper.remove_source_if_unchanged(directory, 'source', current))
        self.assertFalse(self.source.exists())

    def test_only_fixed_guid_and_leaf_names_are_accepted(self):
        valid = '01234567-89ab-cdef-0123-456789abcdef'
        helper.checked_download(valid, '中文文件.txt')
        for name in ('../outside', '/absolute', '.', '..', 'bad\0name'):
            with self.assertRaises(ValueError):
                helper.checked_download(valid, name)
        with self.assertRaises(ValueError):
            helper.checked_download('../outside', 'report.txt')

    def test_cleanup_keeps_source_modified_after_copy(self):
        directory = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY)
        self.addCleanup(os.close, directory)
        original = self.source.stat()
        with self.source.open('ab') as changed:
            changed.write(b'new bytes')
        self.assertFalse(helper.remove_source_if_unchanged(directory, 'source', original))
        self.assertTrue(self.source.read_bytes().endswith(b'new bytes'))


if __name__ == '__main__':
    unittest.main()
