import importlib.util
from pathlib import Path
import unittest
import sys

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location('editor_migrate', Path(__file__).with_name('editor-migrate.py'))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class EditorIdentityTests(unittest.TestCase):
    def setUp(self):
        self.argv = [helper.NODE, helper.ROOT, '--bind-addr', '127.0.0.1:18080', '--config', helper.CONFIG, '--disable-telemetry', '/shared/project']
        self.exe = str(Path(helper.NODE).resolve())

    def test_only_installed_cage_editor_identity_matches(self):
        self.assertTrue(helper.is_legacy_editor(self.argv, self.exe, 1000, 1000))
        direct = self.argv.copy()
        direct[1] = helper.ENTRY
        self.assertTrue(helper.is_legacy_editor(direct, self.exe, 1000, 1000))
        self.assertFalse(helper.is_legacy_editor(self.argv, '/home/cage/fake-node', 1000, 1000))
        self.assertFalse(helper.is_legacy_editor(self.argv, self.exe, 1001, 1000))
        for index, value in ((1, '/home/cage/custom.js'), (3, '127.0.0.1:18081'), (5, '/home/cage/other.yaml')):
            other = self.argv.copy()
            other[index] = value
            self.assertFalse(helper.is_legacy_editor(other, self.exe, 1000, 1000))
        self.assertFalse(helper.is_legacy_editor(['/bin/bash', '-l'], '/bin/bash', 1000, 1000))


if __name__ == '__main__':
    unittest.main()
