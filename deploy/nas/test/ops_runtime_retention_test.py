import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('worker_retention', Path(__file__).parents[1] / 'scripts/ops-runtime-worker.py')
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


@unittest.skipUnless(os.name == 'posix', 'Linux host worker')
class RetentionSafetyTest(unittest.TestCase):
    def test_configured_floor_keeps_multiple_expired_successes(self):
        state = {'plan': {'minimumSuccesses': 2, 'retentionCount': 3}, 'backups': [
            {'id': f'01900000-0000-7000-8000-{n:012d}', 'status': 'succeeded', 'createdAt': f'2020-01-0{n}T00:00:00Z'} for n in range(1,4)]}
        with tempfile.TemporaryDirectory() as temporary:
            worker.retain(Path(temporary), state)
        self.assertEqual(2, len(state['backups']))
        self.assertNotIn('01900000-0000-7000-8000-000000000001', [item['id'] for item in state['backups']])

    def test_database_cancellation_does_not_hold_api_state_lock(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            settings = {'tenantId': '00000000-0000-0000-0000-000000000001'}
            worker.write(root, {'backups': [{'id': '01900000-0000-7000-8000-000000000001', 'status': 'running', 'createdAt': worker.stamp()}]}, settings)
            def cancel(*args):
                check = subprocess.run([sys.executable, '-c', "import fcntl,sys; f=open(sys.argv[1],'r+b'); fcntl.lockf(f,fcntl.LOCK_EX|fcntl.LOCK_NB)", str(root/'state.lock')], capture_output=True)
                self.assertEqual(0, check.returncode)
                return True
            with patch.object(worker, 'inventory', return_value={'reportedAt': worker.stamp(), 'items': []}), patch.object(worker, 'cancel_dump', side_effect=cancel):
                worker.tick(root, settings)

    def test_expiry_never_removes_last_success_after_failures_or_manual_only(self):
        for enabled, failures in [(True, 0), (True, 12), (False, 0)]:
            with self.subTest(enabled=enabled, failures=failures), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                (root / 'backups').mkdir()
                identifier = '01900000-0000-7000-8000-000000000001'
                archive = root / 'backups' / (identifier + '.tar')
                archive.write_bytes(b'last-good')
                good = {'id': identifier, 'status': 'succeeded', 'createdAt': '2020-01-01T00:00:00Z'}
                state = {'plan': {'enabled': enabled, 'retentionCount': 7, 'retentionDays': 30}, 'backups': [good] + [
                    {'id': f'01900000-0000-7000-8000-{n:012d}', 'status': 'failed', 'createdAt': '2020-01-02T00:00:00Z'} for n in range(2, failures + 2)]}
                worker.retain(root, state)
                self.assertIn(good, state['backups'])
                self.assertEqual(b'last-good', archive.read_bytes())

    def test_only_newest_expired_success_is_protected_and_manual_deletion_is_explicit(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            old = {'id': '01900000-0000-7000-8000-000000000001', 'status': 'succeeded', 'createdAt': '2020-01-01T00:00:00Z'}
            newer = {'id': '01900000-0000-7000-8000-000000000002', 'status': 'succeeded', 'createdAt': '2020-01-02T00:00:00Z'}
            state = {'backups': [old, newer]}
            worker.retain(root, state)
            self.assertEqual([newer], state['backups'])
            newer['deleteRequestedAt'] = worker.stamp()
            worker.retain(root, state)
            self.assertEqual([], state['backups'])

    def test_invalid_legacy_interval_does_not_clean_any_archive(self):
        state = {'plan': {'intervalHours': 720, 'retentionDays': 29}, 'backups': [
            {'id': '01900000-0000-7000-8000-000000000001', 'status': 'succeeded', 'createdAt': '2020-01-01T00:00:00Z'}]}
        with tempfile.TemporaryDirectory() as temporary:
            worker.retain(Path(temporary), state)
        self.assertEqual(1, len(state['backups']))
        self.assertIn('backup_policy_invalid', state['alerts'])

    def test_cancel_timeout_is_reported_and_next_tick_can_recover(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            identifier = '01900000-0000-7000-8000-000000000001'
            settings = {'compose': ['docker', 'compose'], 'databaseUser': 'fixture', 'tenantId': '00000000-0000-0000-0000-000000000001'}
            worker.write(root, {'backups': [{'id': identifier, 'status': 'running', 'createdAt': '2020-01-01T00:00:00Z'}]}, settings)
            with patch.object(worker, 'inventory', return_value={'reportedAt': worker.stamp(), 'items': []}), patch.object(worker.subprocess, 'run', side_effect=subprocess.TimeoutExpired('docker', 30)):
                worker.tick(root, settings)
            state = worker.read(root)
            self.assertEqual('pg_cancel_failed', state['backups'][0]['reason'])
            self.assertTrue(state['backups'][0]['cancelPending'])
            self.assertIn('backup_cancel_failed', state['alerts'])
            with patch.object(worker, 'inventory', return_value={'reportedAt': worker.stamp(), 'items': []}), patch.object(worker.subprocess, 'run', return_value=type('R', (), {'returncode': 0, 'stdout': 't\n'})()):
                worker.tick(root, settings)
            self.assertFalse(any(item.get('cancelPending') for item in worker.read(root).get('backups', [])))

    def test_missing_bifrost_is_explicitly_skipped_but_primary_and_roles_are_required(self):
        with tempfile.TemporaryDirectory() as temporary:
            def dump(settings, identifier, arguments, output, folder):
                output.write(b'PGDMP-fixture' if arguments[0] == 'pg_dump' else b'CREATE ROLE fixture;')
            result = type('R', (), {'returncode': 0, 'stdout': ''})()
            with patch.object(worker.subprocess, 'run', return_value=result), patch.object(worker, 'dump_component', side_effect=dump):
                receipt = worker.backup(Path(temporary), {'compose': [], 'databaseUser': 'fixture'}, '01900000-0000-7000-8000-000000000001')
            self.assertEqual('succeeded', receipt['status'])
            self.assertEqual(['auriga'], receipt['databases'])
            self.assertEqual(['bifrost'], receipt['skippedDatabases'])

    def test_backup_timeout_returns_failure_instead_of_publishing(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(worker, 'dump_component', side_effect=TimeoutError('dump timeout')):
                receipt = worker.backup(Path(temporary), {'databaseUser': 'fixture', 'databases': ['auriga']}, '01900000-0000-7000-8000-000000000001')
            self.assertEqual('pg_dump_timeout', receipt['reason'])
            self.assertFalse(list((Path(temporary) / 'backups').iterdir()))


if __name__ == '__main__':
    unittest.main()
