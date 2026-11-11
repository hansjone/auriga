import importlib.util
import json
import os
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).parents[1] / 'scripts' / 'ops-runtime-worker.py'
spec = importlib.util.spec_from_file_location('ops_worker', SCRIPT)
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


class RuntimeWorkerTest(unittest.TestCase):
    def test_managed_instance_inventory_is_tenant_scoped_and_has_no_secrets(self):
        tenant = '00000000-0000-0000-0000-000000000001'
        def container(identifier, owner):
            return {'Id': identifier, 'Name': '/' + identifier, 'Config': {'Image': 'example.com/runtime:actual', 'Env': ['AURIGA_TENANT_ID=' + owner, 'SECRET=never-return']}, 'Image': 'sha256:actual', 'State': {'Status': 'running'}}
        outputs = ['platform\n', 'instance\nother\n', json.dumps([container('platform', ''), container('instance', tenant), container('other', 'other-tenant')])]
        with patch.object(worker.subprocess, 'run', side_effect=lambda *args, **kwargs: type('Result', (), {'stdout': outputs.pop(0)})()):
            result = worker.inventory({'compose': ['docker', 'compose'], 'tenantId': tenant})
        self.assertEqual(['platform', 'instance'], [item['name'] for item in result['items']])
        self.assertNotIn('never-return', json.dumps(result))

    @unittest.skipUnless(os.name == 'posix', 'POSIX host worker locks')
    def test_tick_persists_receipts_and_does_not_replay_interrupted_work(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            tenant = '00000000-0000-0000-0000-000000000001'
            settings = {'tenantId': tenant, 'targets': {}}
            worker.write(root, {'upgrades': [{'id': 'queued', 'targetId': 'gone', 'status': 'queued'}, {'id': 'interrupted', 'status': 'running'}]}, settings)
            with patch.object(worker, 'inventory', return_value={'reportedAt': worker.stamp(), 'items': []}), patch.object(worker, 'upgrade', return_value={'status': 'succeeded', 'reason': 'deployment_committed'}) as execute:
                worker.tick(root, settings)
                worker.tick(root, settings)
            execute.assert_called_once()
            state = worker.read(root)
            self.assertEqual(tenant, state['tenantId'])
            self.assertEqual('succeeded', state['upgrades'][0]['status'])
            self.assertEqual('worker_interrupted', state['upgrades'][1]['reason'])
            self.assertIn('finishedAt', state['upgrades'][0])
            with patch.object(worker, 'inventory', return_value={}):
                with self.assertRaises(ValueError):
                    worker.tick(root, {'tenantId': '00000000-0000-0000-0000-000000000002'})

    @unittest.skipUnless(os.name == 'posix', 'POSIX host worker locks')
    def test_worker_lock_does_not_queue_another_process_and_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            code = "import fcntl,sys; f=open(sys.argv[1], 'a+b'); fcntl.lockf(f,fcntl.LOCK_EX|fcntl.LOCK_NB)"
            with worker.locked(root, 'worker.lock'):
                result = subprocess.run([sys.executable, '-c', code, str(root / 'worker.lock')], capture_output=True)
            self.assertNotEqual(0, result.returncode)
            (root / 'bad.lock').symlink_to(root / 'worker.lock')
            with self.assertRaises(OSError):
                with worker.locked(root, 'bad.lock'):
                    self.fail('Symlink accepted')

    def test_dump_is_verified_and_failed_output_not_published(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            settings = {'compose': ['docker', 'compose'], 'database': 'auriga', 'databaseUser': 'auriga_bootstrap'}
            def dump(settings, identifier, arguments, output, folder):
                kwargs = {'output': output}
                self.assertNotIn('shell', kwargs)
                output = kwargs['output']
                output.write(b'PGDMP-test-archive' if arguments[0] == 'pg_dump' else b'CREATE ROLE fixture;')
            with patch.object(worker, 'dump_component', side_effect=dump):
                result = worker.backup(root, settings, '01900000-0000-7000-8000-000000000001')
            self.assertEqual('succeeded', result['status'])
            self.assertGreater(result['sizeBytes'], 18)
            self.assertEqual(64, len(result['sha256']))
            with patch.object(worker, 'dump_component', side_effect=OSError('pg_dump_failed')):
                result = worker.backup(root, settings, '01900000-0000-7000-8000-000000000002')
            self.assertEqual('failed', result['status'])
            self.assertFalse((root / 'backups/01900000-0000-7000-8000-000000000002.dump').exists())

    def test_inventory_uses_docker_inspect_not_compose_expectations(self):
        outputs = ['container-one\n', json.dumps([{'Name': '/running', 'Config': {'Image': 'example.com/platform@sha256:actual', 'Labels': {'org.opencontainers.image.version': '1.2.3'}}, 'Image': 'sha256:actual-id', 'State': {'Status': 'running'}}])]
        def run(*args, **kwargs):
            return type('Result', (), {'returncode': 0, 'stdout': outputs.pop(0)})()
        with patch.object(worker.subprocess, 'run', side_effect=run):
            inventory = worker.inventory({'compose': ['docker', 'compose']})
        self.assertEqual('sha256:actual-id', inventory['items'][0]['imageId'])

    def test_rejects_unknown_upgrade_and_never_executes_browser_fields(self):
        with patch.object(worker.subprocess, 'run') as run:
            receipt = worker.upgrade({'targets': {}}, {'targetId': 'unapproved', 'command': 'bad'})
        self.assertEqual('failed', receipt['status'])
        run.assert_not_called()

    def test_approved_upgrade_uses_fixed_deployer_and_reports_failure_or_unknown(self):
        image = 'example.com/runtime@sha256:' + 'a' * 64
        settings = {'nasRoot': '/approved/nas', 'targets': {'approved': {'bundle': '/approved/bundle', 'image': image}}}
        with patch.object(worker.subprocess, 'Popen', return_value=type('Result', (), {'returncode': 73, 'wait': lambda self, **kwargs: None})()) as run:
            receipt = worker.upgrade(settings, {'targetId': 'approved', 'command': 'ignored'})
        self.assertEqual('deployment_failed', receipt['reason'])
        self.assertEqual(['sh', '/approved/nas/scripts/runtime-upgrade.sh', '/approved/bundle', image], run.call_args.args[0])
        with patch.object(worker.subprocess, 'Popen', side_effect=OSError('unavailable')):
            receipt = worker.upgrade(settings, {'targetId': 'approved'})
        self.assertEqual('deployment_outcome_unknown', receipt['reason'])

    def test_schedule_advances_and_avoids_duplicate_active_backup(self):
        state = {'plan': {'enabled': True, 'intervalHours': 24, 'nextAt': '2020-01-01T00:00:00Z'}, 'backups': []}
        worker.schedule(state)
        self.assertEqual(1, len(state['backups']))
        worker.schedule(state)
        self.assertEqual(1, len(state['backups']))


if __name__ == '__main__':
    unittest.main()
