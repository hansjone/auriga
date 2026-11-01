"""Linux deployment regressions; no third-party Python packages required."""
import ast
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile
import unittest

DEPLOY = Path(__file__).resolve().parents[2]
GROUPS = {'master': 10003, 'salt': 10003, 'team': 10001, 'pg_password': 999}


class ComposePermissionsTest(unittest.TestCase):
    def test_services_use_only_supplementary_groups(self):
        for file in [DEPLOY / 'compose/docker-compose.yml',
                     DEPLOY / 'nas/docker-compose.yml',
                     DEPLOY / 'nas/litellm/test-compose.yml']:
            raw = file.read_text()
            for name, groups in [('litellm', {10003, 999, 10004}),
                                 ('litellm-init', {10003, 999, 10001, 10004})]:
                with self.subTest(file=str(file), service=name):
                    block = re.search(r'^  ' + name + r':\n(.*?)(?=^  [\w-]+:|\Z)',
                                      raw, re.M | re.S).group(1)
                    self.assertNotIn('cap_add:', block)
                    self.assertNotIn('privileged: true', block)
                    groups_line = re.search(r'group_add: (\[.*\])', block)
                    self.assertIsNotNone(groups_line)
                    self.assertEqual(set(map(int, ast.literal_eval(groups_line[1]))), groups)
                    if '<<: *cap_drop_all' in block:
                        block += raw.split('services:')[0]
                    self.assertIn('cap_drop: [ALL]', block)
                    self.assertIn('no-new-privileges:true', block)

    def test_deployment_entrypoints_apply_permissions(self):
        self.assertIn('scripts/litellm-permissions.sh',
                      (DEPLOY / 'compose/scripts/prepare.sh').read_text())
        self.assertIn('install_litellm_materials',
                      (DEPLOY / 'nas/scripts/render.sh').read_text())
        self.assertNotIn('litellm/prepare-ui.sh', (DEPLOY / 'nas/scripts/installer.sh').read_text())
        self.assertIn('litellm/prepare-ui.sh', (DEPLOY / 'nas/scripts/render.sh').read_text())
        raw = (DEPLOY / 'nas/docker-compose.yml').read_text().split('\nsecrets:\n')[1]
        for name in GROUPS:
            self.assertIn('/config/litellm/' + name, raw)
        self.assertNotIn('/config/materials/litellm/', raw)

    def test_edge_native_ui_has_independent_credentials_without_master(self):
        for file in [DEPLOY / 'compose/docker-compose.yml', DEPLOY / 'nas/docker-compose.yml']:
            block = re.search(r'^  edge:\n(.*?)(?=^  [\w-]+:|\Z)', file.read_text(), re.M | re.S).group(1)
            self.assertIn('secrets: [litellm_ui_password, litellm_ui_reader]', block)
            self.assertIn("group_add: ['10004']", block)
            self.assertIn('AURIGA_EDGE_LITELLM_UI_PASSWORD_FILE: /run/secrets/litellm_ui_password', block)
            self.assertIn('AURIGA_EDGE_LITELLM_UI_EXTERNAL_ORIGINS:', block)
            self.assertNotIn('litellm_master', block)
            self.assertIn('AURIGA_EDGE_LITELLM_KEY_INFO_FILE: /run/secrets/litellm_ui_reader', block)
            self.assertNotIn('cap_add:', block)


@unittest.skipUnless(os.geteuid() == 0, 'requires Linux root for real UID/GID checks')
class MaterialPermissionsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='auriga-litellm-permissions-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def create(self, directory, uid):
        directory.mkdir(parents=True)
        for name in GROUPS:
            leaf = directory / name
            leaf.write_text(secrets.token_hex(32))
            leaf.chmod(0o600)
            os.chown(leaf, uid, uid)

    def run_permissions(self, nas=False, success=True):
        if nas:
            cmd = ['sh', '-c', '. "$NAS_ROOT/scripts/lib.sh"; install_litellm_materials']
        else:
            cmd = ['sh', str(DEPLOY / 'compose/scripts/litellm-permissions.sh'), str(self.root)]
        result = subprocess.run(cmd, env={**os.environ, 'NAS_ROOT': str(DEPLOY / 'nas'),
                                         'AURIGA_DATA_ROOT': str(self.root),
                                         'NAS_ALLOW_NONROOT': '0'}, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stderr)

    def probe(self, path, groups):
        cmd = ['setpriv', '--bounding-set=-all', '--reuid=0', '--regid=0']
        cmd += ['--groups=' + ','.join(map(str, groups))] if groups else ['--clear-groups']
        return subprocess.run(cmd + ['cat', str(path)], stdout=subprocess.DEVNULL,
                              stderr=subprocess.DEVNULL).returncode == 0

    def check_runtime(self, runtime, uid_by_name):
        for name, gid in GROUPS.items():
            leaf = runtime / name
            metadata = leaf.stat()
            self.assertEqual((metadata.st_uid, metadata.st_gid,
                              metadata.st_mode & 0o777, metadata.st_nlink),
                             (uid_by_name[name], gid, 0o640, 1))
            self.assertTrue(self.probe(leaf, [gid]), name)
            self.assertFalse(self.probe(leaf, []), name)
            self.assertFalse(self.probe(leaf, [20001]), name)

    def test_compose_runner_1001_and_reentry(self):
        runtime = self.root / 'litellm'
        self.create(runtime, 1001)
        before = {n: (runtime / n).read_bytes() for n in GROUPS}
        self.run_permissions()
        self.run_permissions()
        self.check_runtime(runtime, dict.fromkeys(GROUPS, 1001))
        self.assertEqual(before, {n: (runtime / n).read_bytes() for n in GROUPS})

    def test_nas_copies_preserve_sources_and_reentry(self):
        source = self.root / 'config/materials/litellm'
        self.create(source, 10001)
        before = {n: ((source / n).read_bytes(), (source / n).stat()) for n in GROUPS}
        self.run_permissions(nas=True)
        self.run_permissions(nas=True)
        runtime = self.root / 'config/litellm'
        self.check_runtime(runtime, {n: 999 if n == 'pg_password' else 10001 for n in GROUPS})
        for name, (raw, metadata) in before.items():
            self.assertEqual((source / name).read_bytes(), raw)
            self.assertEqual((runtime / name).read_bytes(), raw)
            after = (source / name).stat()
            self.assertEqual((after.st_uid, after.st_gid, after.st_mode, after.st_ino),
                             (metadata.st_uid, metadata.st_gid, metadata.st_mode, metadata.st_ino))
            self.assertNotEqual((runtime / name).stat().st_ino, after.st_ino)

    def test_compose_rejects_links_before_changing_any_permissions(self):
        runtime = self.root / 'litellm'
        self.create(runtime, 1001)
        outside = self.root / 'outside'
        outside.write_text(secrets.token_hex(32))
        outside.chmod(0o600)
        for link in ['symlink', 'hardlink']:
            (runtime / 'salt').unlink()
            if link == 'symlink':
                (runtime / 'salt').symlink_to(outside)
            else:
                os.link(outside, runtime / 'salt')
            self.run_permissions(success=False)
            self.assertEqual((runtime / 'master').stat().st_mode & 0o777, 0o600)
            self.assertEqual(outside.stat().st_mode & 0o777, 0o600)

    def test_nas_rejects_changed_and_linked_runtime(self):
        self.create(self.root / 'config/materials/litellm', 10001)
        self.run_permissions(nas=True)
        leaf = self.root / 'config/litellm/master'
        original = leaf.read_bytes()
        leaf.write_text(secrets.token_hex(32))
        self.run_permissions(nas=True, success=False)
        leaf.unlink()
        leaf.symlink_to(self.root / 'config/materials/litellm/master')
        self.run_permissions(nas=True, success=False)
        self.assertEqual(leaf.read_bytes(), original)

    def test_nas_rejects_permission_drift_without_repair(self):
        self.create(self.root / 'config/materials/litellm', 10001)
        self.run_permissions(nas=True)
        leaf = self.root / 'config/litellm/master'
        leaf.chmod(0o644)
        self.run_permissions(nas=True, success=False)
        self.assertEqual(leaf.stat().st_mode & 0o777, 0o644)

    def test_nas_rejects_source_hardlink_and_runtime_directory_symlink(self):
        source = self.root / 'config/materials/litellm'
        self.create(source, 10001)
        os.link(source / 'master', self.root / 'outside')
        self.run_permissions(nas=True, success=False)
        self.assertEqual((source / 'master').stat().st_mode & 0o777, 0o600)
        (self.root / 'outside').unlink()
        (self.root / 'config/litellm').rmdir()
        (self.root / 'config/litellm').symlink_to(source)
        self.run_permissions(nas=True, success=False)
        self.assertEqual((source / 'master').stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
