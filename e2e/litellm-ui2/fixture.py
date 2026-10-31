"""Disposable, local-Docker-only LiteLLM fixture; never uses deployment secrets."""
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
STATE = HERE / '.runtime'
REPO = HERE.parents[1]


def docker(*args, capture=False):
    return subprocess.run(['docker', *args], check=True, text=True,
                          stdout=subprocess.PIPE if capture else None)


def main():
    endpoint = json.loads(docker('context', 'inspect', '--format',
                                 '{{json .Endpoints.docker.Host}}', capture=True).stdout)
    if endpoint != 'unix:///var/run/docker.sock' or os.environ.get('DOCKER_HOST'):
        raise RuntimeError('fixture requires the local Docker socket')
    STATE.mkdir(exist_ok=True, mode=0o700)
    record = STATE / 'fixture.json'
    if sys.argv[1] == 'start':
        if record.exists():
            raise RuntimeError('fixture already recorded; stop it before restarting')
        root = Path(tempfile.mkdtemp(prefix='auriga-ui2-'))
        root.chmod(0o755)
        password = 'sk-' + secrets.token_hex(32)
        for name in ['bootstrap', 'master', 'salt', 'team', 'pg_password']:
            leaf = root / name
            leaf.write_text(password if name == 'master' else secrets.token_hex(32))
            leaf.chmod(0o640)
            os.chown(leaf, 0, 10003 if name in ['master', 'salt'] else 999)
        overlay = STATE / 'overlay.json'
        overlay.write_text(json.dumps({'services': {'litellm': {
            'environment': {'SERVER_ROOT_PATH': '/ai', 'NO_DOCS': 'true'},
            'ports': ['127.0.0.1::4000'], 'mem_limit': '1200m'},
            'postgres': {'mem_limit': '256m'}}}))
        data = {'project': 'auriga-ui2-' + secrets.token_hex(6), 'root': str(root)}
        record.write_text(json.dumps(data))
        (STATE / 'password').write_text(password)
        (STATE / 'password').chmod(0o600)
    else:
        data = json.loads(record.read_text())
    project, root = data['project'], Path(data['root'])
    if not project.startswith('auriga-ui2-') or root.parent != Path(tempfile.gettempdir()) or not root.name.startswith('auriga-ui2-'):
        raise RuntimeError('invalid fixture ownership record')
    os.environ['AURIGA_RELAY_TEST_ROOT'] = str(root)
    args = ['compose', '-p', project, '-f', str(REPO / 'deploy/nas/litellm/test-compose.yml'),
            '-f', str(STATE / 'overlay.json')]
    if sys.argv[1] == 'start':
        docker(*args, 'up', '-d', '--wait', '--wait-timeout', '240', 'postgres', 'fake-upstream', 'litellm')
        address = docker(*args, 'port', 'litellm', '4000', capture=True).stdout.strip()
        data['origin'] = 'http://' + address
        record.write_text(json.dumps(data))
        print(json.dumps({'origin': data['origin'], 'passwordFile': str(STATE / 'password')}))
    elif sys.argv[1] == 'stop':
        docker(*args, 'down', '--volumes', '--remove-orphans')
        shutil.rmtree(root)
        record.unlink()
        (STATE / 'password').unlink()
    else:
        raise RuntimeError('use start or stop')


if __name__ == '__main__':
    main()
