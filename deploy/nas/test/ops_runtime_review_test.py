import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('worker', Path(__file__).parents[1] / 'scripts/ops-runtime-worker.py')
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


@unittest.skipUnless(os.name == 'posix', 'Linux host worker')
class ReviewRegressionTest(unittest.TestCase):
    def setUp(self):
        probe = patch.object(worker, 'database_exists', return_value=True)
        probe.start()
        self.addCleanup(probe.stop)

    def test_failed_attempts_do_not_evict_last_successful_archive(self):
        with tempfile.TemporaryDirectory() as temporary:
            good = {'id':'01900000-0000-7000-8000-000000000001', 'status':'succeeded', 'createdAt':'2026-11-11T00:00:00Z'}
            state = {'plan':{'retentionCount':1,'retentionDays':3650}, 'backups':[good] + [
                {'id':f'01900000-0000-7000-8000-{n:012d}', 'status':'failed', 'createdAt':'2026-11-11T01:00:00Z'} for n in range(2,12)]}
            worker.retain(Path(temporary),state)
            self.assertIn(good, state['backups'])

    @unittest.skipUnless(hasattr(os, 'geteuid') and os.geteuid() == 0, 'Host ownership race requires root fixture')
    def test_replaced_temporary_state_never_chowns_outside_target(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            outside = root / 'outside'
            outside.write_bytes(b'untouched'); outside.chmod(0o640)
            original = worker.tempfile.mkstemp
            def replace(*args, **kwargs):
                fd, name = original(*args, **kwargs)
                os.unlink(name); os.symlink(outside, name)
                return fd, name
            with patch.object(worker.tempfile, 'mkstemp', side_effect=replace):
                with self.assertRaises(OSError):
                    worker.write(root, {'value':1}, {'ownerUid':10001, 'ownerGid':10001})
            self.assertEqual(0, outside.stat().st_uid)
            self.assertEqual(0o640, outside.stat().st_mode & 0o777)
            self.assertEqual(b'untouched', outside.read_bytes())

    def test_termination_allows_real_shell_rollback_trap(self):
        import subprocess
        import time
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / 'rollback'
            ready = Path(temporary) / 'ready'
            process = subprocess.Popen(['sh','-c', 'trap \'printf rollback > "$1"; exit 0\' TERM; touch "$2"; while :; do sleep 1; done', 'fixture', str(marker), str(ready)], start_new_session=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            try:
                for _ in range(100):
                    if ready.exists(): break
                    time.sleep(0.02)
                self.assertTrue(ready.exists())
                worker.stop(process)
                self.assertEqual('rollback',marker.read_text())
            finally:
                if process.poll() is None: worker.stop(process)

    def test_missing_component_never_publishes_partial_bundle(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            def component(settings, identifier, arguments, output, folder):
                if arguments[0]=='pg_dumpall': raise OSError('roles_failed')
                output.write(b'PGDMP-fixture')
            with patch.object(worker,'dump_component',side_effect=component):
                result=worker.backup(root,{'databaseUser':'fixture'},'01900000-0000-7000-8000-000000000001')
            self.assertEqual('failed',result['status'])
            self.assertEqual([],list((root/'backups').iterdir()))

    def test_retention_removes_only_owned_uuid_archives_and_records(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'backups').mkdir()
            items = []
            for n in range(4):
                identifier = f'01900000-0000-7000-8000-{n:012d}'
                (root / 'backups' / (identifier + '.tar')).write_bytes(b'owned')
                items.append({'id': identifier, 'status': 'succeeded', 'createdAt': f'2026-11-11T00:00:0{n}Z'})
            (root / 'backups/unrelated.tar').write_bytes(b'keep')
            state = {'plan': {'retentionCount': 2, 'retentionDays': 3650}, 'backups': items}
            oldest = items[0]['id']
            worker.retain(root, state)
            self.assertEqual(2, len(state['backups']))
            self.assertTrue((root / 'backups/unrelated.tar').exists())
            self.assertFalse((root / 'backups' / (oldest + '.tar')).exists())

    def test_full_history_is_pruned_before_schedule_and_expiry_revokes_tickets(self):
        with tempfile.TemporaryDirectory() as temporary:
            state = {'plan': {'enabled': True}, 'backups': [
                {'id': f'01900000-0000-7000-8000-{n:012d}', 'status': 'failed', 'createdAt': '2020-01-01T00:00:00Z'} for n in range(1000)]}
            state['tickets'] = [{'backupId': state['backups'][0]['id']}]
            worker.retain(Path(temporary), state)
            worker.schedule(state)
            self.assertEqual(1, len(state['backups']))
            self.assertEqual('queued', state['backups'][0]['status'])
            self.assertEqual([], state['tickets'])

    def test_upgrade_timeout_terminates_process_group_before_kill(self):
        import signal
        import subprocess
        from unittest.mock import Mock, call
        process = Mock(pid=12345)
        process.wait.side_effect = [subprocess.TimeoutExpired('sh',900), subprocess.TimeoutExpired('sh',150), 0]
        settings = {'nasRoot':'/approved', 'targets':{'approved':{'bundle':'/approved/bundle','image':'example.com/runtime@sha256:'+'a'*64}}}
        with patch.object(worker.subprocess,'Popen',return_value=process), patch.object(worker.os,'killpg') as kill:
            self.assertEqual('deployment_outcome_unknown',worker.upgrade(settings,{'targetId':'approved'})['reason'])
        self.assertEqual([call(12345,signal.SIGTERM),call(12345,signal.SIGKILL)],kill.call_args_list)

    def test_low_space_never_starts_database_command(self):
        with tempfile.TemporaryDirectory() as temporary:
            with patch.object(worker, 'free_bytes', return_value=1), patch.object(worker.subprocess, 'Popen') as run:
                result = worker.backup(Path(temporary), {'minFreeMiB': 1024}, '01900000-0000-7000-8000-000000000001')
            self.assertEqual('backup_storage_low', result['reason'])
            run.assert_not_called()

    def test_symlink_metadata_target_is_untouched(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            outside = root / 'outside'
            outside.write_bytes(b'unchanged')
            outside.chmod(0o640)
            link = root / 'link'
            link.symlink_to(outside)
            before = outside.stat()
            with self.assertRaises(OSError):
                with worker.directory(link, {'ownerUid': os.getuid(), 'ownerGid': os.getgid()}):
                    self.fail('Symlink directory accepted')
            self.assertEqual(before.st_mode, outside.stat().st_mode)
            self.assertEqual(b'unchanged', outside.read_bytes())

    def test_null_env_container_does_not_break_inventory(self):
        outputs = ['a', 'b', json.dumps([{'Id': 'a', 'Name': '/platform', 'Config': {'Image': 'actual'}, 'Image': 'id', 'State': {'Status': 'running'}}, {'Id': 'b', 'Config': {'Env': None}}])]
        with patch.object(worker.subprocess, 'run', side_effect=lambda *a, **k: type('R', (), {'stdout': outputs.pop(0)})()):
            self.assertEqual(1, len(worker.inventory({'compose': [], 'tenantId': 'tenant'})['items']))


if __name__ == '__main__':
    unittest.main()
