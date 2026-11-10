import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('shared_clipboard', Path(__file__).with_name('shared-clipboard.py'))
clipboard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(clipboard)


class CacheOwnershipTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.registry = clipboard.CacheRegistry(self.root / 'receipts')
        self.cache = self.root / 'created'
        self.cache.mkdir()
        self.file = self.cache / 'fixture.txt'
        self.file.write_bytes(b'complete copied bytes')
        self.paths = [str(self.file)]
        self.registry.register(self.paths, 'current-session')

    def test_current_clipboard_and_reader_references_retain_retired_files(self):
        self.registry.retire(self.paths)
        self.registry.collect(self.paths, 'current-session')
        self.assertTrue(self.file.exists())
        with self.registry.reading(self.paths):
            self.registry.collect([], 'current-session')
            self.assertEqual(self.file.read_bytes(), b'complete copied bytes')
        self.registry.collect([], 'current-session')
        self.assertFalse(self.cache.exists())
        self.assertEqual(self.registry.records(), [])

    def test_delayed_external_path_requires_recopy_but_open_reader_finishes(self):
        self.registry.retire(self.paths)
        with self.file.open('rb') as reader:
            self.registry.collect([], 'current-session')
            self.assertEqual(reader.read(), b'complete copied bytes')
        self.assertFalse(self.file.exists())

    def test_only_created_cache_is_collected(self):
        unrelated = self.root / 'cage-clipboard-qa-test-unrelated'
        unrelated.mkdir()
        (unrelated / 'keep.txt').write_text('user content')
        self.registry.collect([], 'new-session')
        self.assertFalse(self.cache.exists())
        self.assertEqual((unrelated / 'keep.txt').read_text(), 'user content')

    def test_active_and_legacy_unpublished_selections_are_retained(self):
        self.registry.collect([], 'current-session')
        self.assertTrue(self.file.exists())
        record = self.registry.records()[0]
        record['session'] = ''
        self.registry.save(record)
        self.registry.collect([], 'new-session')
        self.assertTrue(self.file.exists())
        self.registry.retire(self.paths)
        self.registry.collect([], 'new-session')
        self.assertFalse(self.cache.exists())

    def test_changed_creation_marker_blocks_deletion(self):
        record = self.registry.records()[0]
        (self.cache / record['marker']).write_text('changed owner')
        self.registry.retire(self.paths)
        with self.assertRaises(ValueError):
            self.registry.collect([], 'current-session')
        self.assertEqual(self.file.read_bytes(), b'complete copied bytes')

    def test_directory_removal_failure_retains_proof_for_retry(self):
        self.registry.retire(self.paths)
        with patch.object(Path, 'rmdir', side_effect=PermissionError('simulated busy directory')):
            with self.assertRaises(PermissionError):
                self.registry.collect([], 'current-session')
        self.registry.collect([], 'current-session')
        self.assertFalse(self.cache.exists())

    def test_malformed_receipt_and_symlink_registry_fail_without_removing_files(self):
        record = self.registry.records()[0]
        path = self.registry.directory / (record['id'] + '.json')
        path.write_text(json.dumps({'id': None}))
        with self.assertRaises(ValueError):
            self.registry.records()
        self.assertTrue(self.file.exists())
        alias = self.root / 'alias'
        alias.symlink_to(self.registry.directory, target_is_directory=True)
        with self.assertRaises(ValueError):
            clipboard.CacheRegistry(alias).records()

    def test_import_registers_exact_owned_directory_and_keeps_original_bytes(self):
        source = io.BytesIO()
        clipboard.export_files(self.paths, source)
        source.seek(0)
        with patch.object(clipboard.tempfile, 'tempdir', str(self.root)):
            registry = clipboard.CacheRegistry()
            registry.begin_session('import-session')
            paths = clipboard.import_files(source, 'import-session')
            self.assertEqual(registry.records()[0]['paths'], paths)
            registry.retire(paths)
            registry.collect([], 'import-session')
        self.assertEqual(self.file.read_bytes(), b'complete copied bytes')
        self.assertFalse(Path(paths[0]).parent.exists())

    def test_failed_archive_is_removed_before_it_can_be_published(self):
        source = io.BytesIO()
        with tarfile.open(fileobj=source, mode='w') as archive:
            entry = tarfile.TarInfo('../outside')
            entry.size = 1
            archive.addfile(entry, io.BytesIO(b'x'))
        source.seek(0)
        with patch.object(clipboard.tempfile, 'tempdir', str(self.root)):
            clipboard.CacheRegistry().ensure_directory()
            before = set(self.root.iterdir())
            with self.assertRaises(ValueError):
                clipboard.import_files(source)
        self.assertEqual(set(self.root.iterdir()), before)

    def test_an_import_finishing_after_disconnect_releases_its_created_directory(self):
        source = io.BytesIO()
        clipboard.export_files(self.paths, source)
        source.seek(0)
        with patch.object(clipboard.tempfile, 'tempdir', str(self.root)):
            registry = clipboard.CacheRegistry()
            registry.begin_session('import-session')
            original = clipboard.extract_files
            created = []
            def disconnected(stream, root):
                created.append(root)
                paths = original(stream, root)
                registry.end_session('import-session')
                return paths
            with patch.object(clipboard, 'extract_files', side_effect=disconnected):
                with self.assertRaises(ValueError):
                    clipboard.import_files(source, 'import-session')
            self.assertFalse(created[0].exists())
            registry.collect([], 'next-session')
            self.assertEqual(registry.records(), [])
        self.assertEqual(self.file.read_bytes(), b'complete copied bytes')

    def test_an_incomplete_copy_keeps_a_live_producer_and_reclaims_a_terminated_one(self):
        record = self.registry.records()[0]
        record['pending'] = True
        self.registry.save(record)
        self.registry.collect([], 'next-session')
        self.assertTrue(self.cache.exists())
        record['producer'] = 2147483647
        self.registry.save(record)
        self.registry.collect([], 'next-session')
        self.assertFalse(self.cache.exists())


if __name__ == '__main__':
    unittest.main()
