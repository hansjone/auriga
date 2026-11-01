import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('prepare-ui.sh')


@unittest.skipUnless(hasattr(os, 'geteuid') and os.geteuid() == 0, 'Linux root metadata test')
class UISecretsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'ui'
        self.root.mkdir()

    def prepare(self, env=None):
        result = subprocess.run(['sh', str(SCRIPT), str(self.root)], capture_output=True, text=True,
                                env=env)
        if result.returncode:
            raise RuntimeError('UI preparation failed')

    def test_preparation_needs_no_docker_or_litellm_image(self):
        tools = Path(self.temp.name) / 'bin'
        tools.mkdir()
        docker = tools / 'docker'
        docker.write_text('#!/bin/sh\nexit 91\n')
        docker.chmod(0o755)
        self.prepare({**os.environ, 'PATH': str(tools)+':'+os.environ['PATH'],
                      'AURIGA_IMAGE_LITELLM': 'missing-fixture-image'})
        self.prepare({**os.environ, 'PATH': str(tools)+':'+os.environ['PATH']})

    def test_second_random_failure_does_not_publish_partial_credentials(self):
        self.root.rmdir()
        tools = Path(self.temp.name) / 'bin'
        tools.mkdir()
        openssl = tools / 'openssl'
        real = shutil.which('openssl')
        openssl.write_text('#!/bin/sh\nif [ -e "$RANDOM_PROBE" ]; then exit 73; fi\n'
                           ': >"$RANDOM_PROBE"\nexec "'+real+'" "$@"\n')
        openssl.chmod(0o755)
        env = {**os.environ, 'PATH': str(tools)+':'+os.environ['PATH'],
               'RANDOM_PROBE': str(Path(self.temp.name) / 'called')}
        with self.assertRaises(RuntimeError):
            self.prepare(env)
        self.assertFalse(self.root.exists())
        self.assertFalse(list(Path(self.temp.name).glob('.litellm-ui.*')))
        self.prepare()

    def test_missing_installer_fails_before_creating_secrets(self):
        self.root.rmdir()
        Path(self.temp.name).chmod(0o755)
        tools = Path(self.temp.name) / 'bin'
        tools.mkdir()
        docker = tools / 'docker'
        docker.write_text('#!/bin/sh\nexit 91\n')
        docker.chmod(0o755)
        result = subprocess.run(['setpriv', '--reuid=1001', '--regid=1001', '--clear-groups',
                                 'sh', str(SCRIPT.resolve()), str(self.root)], capture_output=True,
                                text=True, env={**os.environ, 'PATH': str(tools)+':'+os.environ['PATH'],
                                'AURIGA_INSTALLER_IMAGE': 'sha256:'+'1'*64})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('must already exist locally', result.stderr)
        self.assertFalse(self.root.exists())

    def test_reentry_preserves_independent_credentials_and_metadata(self):
        self.prepare()
        before = {p.name: p.read_bytes() for p in self.root.iterdir()}
        self.prepare()
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.root.iterdir()})
        self.assertNotEqual(before['password'], before['reader'])
        for p in self.root.iterdir():
            s = p.stat()
            self.assertEqual((s.st_uid, s.st_gid, s.st_mode & 0o777, s.st_nlink), (10001, 10004, 0o640, 1))

    def test_partial_drift_and_links_fail_closed(self):
        self.prepare()
        password = self.root / 'password'
        password.chmod(0o644)
        with self.assertRaises(RuntimeError):
            self.prepare()
        password.unlink()
        with self.assertRaises(RuntimeError):
            self.prepare()
        password.symlink_to(self.root / 'reader')
        with self.assertRaises(RuntimeError):
            self.prepare()
        password.unlink()
        os.link(self.root / 'reader', password)
        with self.assertRaises(RuntimeError):
            self.prepare()


if __name__ == '__main__':
    unittest.main()
