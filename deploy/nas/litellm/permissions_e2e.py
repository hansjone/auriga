"""Verify production Compose/NAS service definitions on a local Linux Docker engine."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile

DEPLOY = Path(__file__).resolve().parents[2]
NAMES = ['master', 'salt', 'team', 'pg_password']


def command(args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def verify(mode, endpoint):
    project = 'auriga-litellm-permissions-' + secrets.token_hex(6)
    with tempfile.TemporaryDirectory(prefix=project + '-') as temporary:
        root = Path(temporary)
        # The actual writer is CI's UID1001, starting with owner-only files.
        os.chown(root, 1001, 1001)
        source = root / ('litellm' if mode == 'compose' else 'config/materials/litellm')
        source.mkdir(parents=True)
        os.chown(source, 1001, 1001)
        subprocess.run(['setpriv', '--reuid=1001', '--regid=1001', '--clear-groups',
                        'python3', '-c',
                        'import os,secrets,sys; from pathlib import Path; os.umask(0o077); '
                        '[(Path(sys.argv[1])/n).write_text(('
                        '"" if n=="pg_password" else "sk-")+secrets.token_hex(32)) '
                        'for n in sys.argv[2:]]', str(source), *NAMES], check=True)
        for name in NAMES:
            metadata = (source / name).stat()
            assert (metadata.st_uid, metadata.st_mode & 0o777) == (1001, 0o600)
        originals = {n: (source / n).read_text() for n in NAMES}
        if mode == 'compose':
            subprocess.run(['sh', str(DEPLOY / 'compose/scripts/litellm-permissions.sh'),
                            str(root)], check=True)
            runtime = source
        else:
            # Real NAS originals belong to its fixed installer UID10001.
            for name in NAMES:
                os.chown(source / name, 10001, 10001)
            subprocess.run(['sh', '-c',
                            '. "$NAS_ROOT/scripts/lib.sh"; install_litellm_materials'],
                           env={**os.environ, 'NAS_ROOT': str(DEPLOY / 'nas'),
                                'AURIGA_DATA_ROOT': str(root), 'NAS_ALLOW_NONROOT': '0'}, check=True)
            runtime = root / 'config/litellm'
            for name in NAMES:
                metadata = (source / name).stat()
                assert (metadata.st_uid, metadata.st_mode & 0o777) == (10001, 0o600)
        parent = root / 'ui-parent'
        parent.mkdir(mode=0o755)
        os.chown(parent, 0, 0)
        ui = parent / 'litellm-ui'
        # A Docker-authorized operator cannot create children under this parent
        # or cd into root's 0700 secret directory on the second invocation.
        socket_gid = Path(endpoint.removeprefix('unix://')).stat().st_gid
        for _ in range(2):
            subprocess.run(['setpriv', '--reuid=1001', '--regid=1001', '--groups='+str(socket_gid),
                            'sh', str(DEPLOY / 'nas/litellm/prepare-ui.sh'), str(ui)], check=True)
        docker = ['docker', '--host', endpoint]
        # Compose itself resolves YAML anchors, service settings and bind paths.
        definition = json.loads(command(docker + ['compose', '-f',
            str(DEPLOY / mode / 'docker-compose.yml'), 'config', '--no-interpolate',
            '--no-env-resolution', '--format', 'json']))
        services = {name: definition['services'][name] for name in ['litellm', 'litellm-init']}
        for service in services.values():
            assert not service.get('cap_add')
            assert service['cap_drop'] == ['ALL']
            service.pop('env_file', None)
            service['networks'] = {'default': None}
        services['postgres'] = {
            'image': 'postgres:17',
            'environment': {'POSTGRES_DB': 'auriga', 'POSTGRES_USER': 'auriga_bootstrap',
                            'POSTGRES_PASSWORD': secrets.token_hex(32)},
            'secrets': [{'source': 'litellm_pg_password'}],
            'volumes': [{'type': 'bind', 'source': str(DEPLOY / 'nas/litellm/postgres-init.sh'),
                         'target': '/docker-entrypoint-initdb.d/03-litellm.sh', 'read_only': True}],
            'healthcheck': {'test': ['CMD', 'pg_isready', '-U', 'auriga_bootstrap', '-d', 'auriga'],
                            'interval': '2s', 'timeout': '3s', 'retries': 60},
        }
        config = {'services': services, 'secrets': {
            'litellm_' + n: {'file': str(runtime / n)} for n in NAMES},
            'networks': {'default': {}}}
        config['secrets'].update({'litellm_ui_password': {'file': str(ui / 'password')}, 'litellm_ui_reader': {'file': str(ui / 'reader')}})
        config_path = root / 'compose.json'
        config_path.write_text(json.dumps(config))
        config_path.chmod(0o600)
        compose = docker + ['compose', '-p', project, '-f', str(config_path)]
        try:
            subprocess.run(compose + ['up', '-d', '--wait', '--wait-timeout', '240',
                                      'postgres', 'litellm'], check=True)
            subprocess.run(compose + ['up', '-d', 'litellm-init'], check=True)
            for name in ['litellm', 'litellm-init']:
                container = command(compose + ['ps', '-a', '-q', name])
                if name == 'litellm-init':
                    assert command(docker + ['wait', container], timeout=90) == '0'
                state = json.loads(command(docker + ['inspect', container]))[0]
                host = state['HostConfig']
                assert not host['CapAdd'] and host['CapDrop'] == ['ALL']
                assert 'no-new-privileges:true' in host['SecurityOpt']
                if name == 'litellm':
                    assert state['State']['Health']['Status'] == 'healthy'
                    probe = command(docker + ['exec', container, 'cat', '/proc/self/status'])
                    for field in ['CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb']:
                        assert int(probe.split(field + ':')[1].split()[0], 16) == 0
                print(mode + ': ' + name + ' healthy/exit0; cap_add=[]; groups=' + str(host['GroupAdd']))
        except Exception:
            result = subprocess.run(compose + ['logs', '--tail=80'], capture_output=True, text=True)
            sanitized = result.stdout + result.stderr
            for value in originals.values():
                sanitized = sanitized.replace(value, '[REDACTED]')
            print(sanitized)
            raise
        finally:
            subprocess.run(compose + ['down', '--volumes', '--remove-orphans'], check=True)


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit('requires Linux root for UID1001 and fixed GID fixture')
    endpoint = json.loads(command(['docker', 'context', 'inspect', '--format',
                                   '{{json .Endpoints.docker.Host}}']))
    if not endpoint.startswith('unix:///'):
        raise SystemExit('local Unix Docker socket required')
    for mode in ['compose', 'nas']:
        verify(mode, endpoint)
