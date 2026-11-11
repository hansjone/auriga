"""Optional isolated Linux Docker smoke: AURIGA_OPS_TEST_POSTGRES_IMAGE=postgres:17.

Uses one temporary PostgreSQL container, no published ports and no shared stack.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import tarfile
import io
import unittest
import uuid

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location('ops_worker', ROOT / 'deploy/nas/scripts/ops-runtime-worker.py')
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


@unittest.skipUnless(os.name == 'posix' and os.environ.get('AURIGA_OPS_TEST_POSTGRES_IMAGE'), 'Opt-in isolated Linux PostgreSQL container')
class PostgresArchiveTest(unittest.TestCase):
    def test_scheduled_dump_receipt_digest_and_real_restore(self):
        (ROOT / 'target').mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='ops-runtime-pg-', dir=ROOT / 'target') as temporary:
            folder = Path(temporary)
            project = 'ops-runtime-test-' + uuid.uuid4().hex[:12]
            compose_file = folder / 'compose.json'
            compose_file.write_text(json.dumps({'services': {'postgres': {'image': os.environ['AURIGA_OPS_TEST_POSTGRES_IMAGE'], 'network_mode': 'none', 'mem_limit': '256m', 'environment': {'POSTGRES_HOST_AUTH_METHOD': 'trust'}, 'tmpfs': ['/var/lib/postgresql/data']}}}))
            compose = ['docker', 'compose', '-p', project, '-f', str(compose_file)]
            def run(arguments, **kwargs):
                return subprocess.run(compose + arguments, check=True, capture_output=True, timeout=90, **kwargs)
            try:
                run(['up', '-d', '--pull', 'never'])
                for _ in range(60):
                    ready = subprocess.run(compose + ['exec', '-T', 'postgres', 'pg_isready', '-U', 'postgres'], capture_output=True, timeout=5)
                    if ready.returncode == 0:
                        break
                    time.sleep(0.5)
                else:
                    self.fail('Temporary PostgreSQL was not ready')
                run(['exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'postgres', '-c', 'CREATE ROLE ops_fixture_role LOGIN; CREATE TABLE ops_fixture(value int); INSERT INTO ops_fixture VALUES (42); ALTER TABLE ops_fixture OWNER TO ops_fixture_role'])
                run(['exec', '-T', 'postgres', 'createdb', '-U', 'postgres', 'bifrost'])
                run(['exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'bifrost', '-c', 'CREATE TABLE relay_fixture(value int); INSERT INTO relay_fixture VALUES (84)'])
                directory = folder / 'state'
                directory.mkdir()
                tenant = str(uuid.uuid4())
                settings = {'compose': compose, 'databases': ['postgres', 'bifrost'], 'databaseUser': 'postgres', 'tenantId': tenant}
                worker.write(directory, {'plan': {'enabled': True, 'intervalHours': 24, 'nextAt': '2020-01-01T00:00:00Z'}}, settings)
                worker.tick(directory, settings)
                result = worker.read(directory)['backups'][0]
                self.assertEqual('succeeded', result['status'])
                archive = directory / 'backups' / (result['id'] + '.tar')
                content = archive.read_bytes()
                self.assertEqual(len(content), result['sizeBytes'])
                self.assertEqual(hashlib.sha256(content).hexdigest(), result['sha256'])
                self.assertEqual(['postgres', 'bifrost'], result['databases'])
                self.assertTrue(result['includesRoles'])
                with tarfile.open(fileobj=io.BytesIO(content)) as bundle:
                    self.assertEqual(['postgres.dump', 'bifrost.dump', 'roles.sql'], bundle.getnames())
                    primary = bundle.extractfile('postgres.dump').read()
                    relay = bundle.extractfile('bifrost.dump').read()
                    roles = bundle.extractfile('roles.sql').read()
                self.assertNotIn(b'PASSWORD', roles.upper())
                run(['exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'postgres', '-c', 'ALTER TABLE ops_fixture OWNER TO postgres; DROP ROLE ops_fixture_role'])
                # The target bootstrap role already exists in a fresh cluster.
                roles = roles.replace(b'CREATE ROLE postgres;', b'')
                run(['exec', '-T', 'postgres', 'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'postgres', '-d', 'postgres'], input=roles)
                run(['exec', '-T', 'postgres', 'createdb', '-U', 'postgres', 'restored_relay'])
                run(['exec', '-T', 'postgres', 'pg_restore', '--exit-on-error', '-U', 'postgres', '-d', 'restored_relay'], input=relay)
                run(['exec', '-T', 'postgres', 'createdb', '-U', 'postgres', 'restored'])
                run(['exec', '-T', 'postgres', 'pg_restore', '--exit-on-error', '-U', 'postgres', '-d', 'restored'], input=primary)
                restored = run(['exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'restored', '-Atc', 'SELECT value FROM ops_fixture'])
                self.assertEqual(b'42', restored.stdout.strip())
                relay_value = run(['exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'restored_relay', '-Atc', 'SELECT value FROM relay_fixture'])
                self.assertEqual(b'84', relay_value.stdout.strip())
                owner = run(['exec', '-T', 'postgres', 'psql', '-U', 'postgres', '-d', 'restored', '-Atc', "SELECT tableowner FROM pg_tables WHERE tablename='ops_fixture'"])
                self.assertEqual(b'ops_fixture_role', owner.stdout.strip())
                worker.tick(directory, settings)
                self.assertEqual(1, len(worker.read(directory)['backups']))
            finally:
                run(['down', '--volumes', '--remove-orphans'])


if __name__ == '__main__':
    unittest.main()
