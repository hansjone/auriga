import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('prepare_ui', Path(__file__).with_name('prepare-ui.py'))
ui = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ui)


@unittest.skipUnless(hasattr(os, 'geteuid') and os.geteuid() == 0, 'Linux root metadata test')
class UISecretsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'ui'
        self.root.mkdir()

    def test_reentry_preserves_independent_credentials_and_metadata(self):
        ui.prepare(self.root)
        before = {p.name: p.read_bytes() for p in self.root.iterdir()}
        ui.prepare(self.root)
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.root.iterdir()})
        self.assertNotEqual(before['password'], before['reader'])
        for p in self.root.iterdir():
            s = p.stat()
            self.assertEqual((s.st_uid, s.st_gid, s.st_mode & 0o777, s.st_nlink), (10001, 10004, 0o640, 1))

    def test_partial_drift_and_links_fail_closed(self):
        ui.prepare(self.root)
        password = self.root / 'password'
        password.chmod(0o644)
        with self.assertRaises(RuntimeError):
            ui.prepare(self.root)
        password.unlink()
        with self.assertRaises(RuntimeError):
            ui.prepare(self.root)
        password.symlink_to(self.root / 'reader')
        with self.assertRaises(RuntimeError):
            ui.prepare(self.root)
        password.unlink()
        os.link(self.root / 'reader', password)
        with self.assertRaises(RuntimeError):
            ui.prepare(self.root)


if __name__ == '__main__':
    unittest.main()
