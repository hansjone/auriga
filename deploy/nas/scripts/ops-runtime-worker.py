#!/usr/bin/env python3
"""One bounded host-side tick. No credentials or subprocess stderr are logged.

Run once a minute through the installation's scheduler. The API never receives
the worker config or Docker socket. fcntl.lockf interoperates with Java FileLock.
"""
import argparse
from contextlib import contextmanager
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile
import uuid


def now():
    return datetime.now(timezone.utc)


def stamp(value=None):
    return (value or now()).isoformat().replace('+00:00', 'Z')


def uuid7():
    return str(uuid.UUID(int=(int(now().timestamp() * 1000) << 80) | (7 << 76) | (secrets.randbits(12) << 64) | (2 << 62) | secrets.randbits(62)))


def own(path, settings):
    if 'ownerUid' in settings:
        os.chown(path, settings['ownerUid'], settings['ownerGid'])


def safe_directory(path):
    path = Path(path).absolute()
    if any(p.is_symlink() for p in [path, *path.parents]):
        raise ValueError('Symlink control directory')
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    return path


@contextmanager
def locked(root, name):
    import fcntl
    descriptor = os.open(root / name, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'r+b') as lock:
        fcntl.lockf(lock, fcntl.LOCK_EX | (fcntl.LOCK_NB if name == 'worker.lock' else 0))
        yield


def read(root):
    path = root / 'state.json'
    if path.is_symlink():
        raise ValueError('Symlink state')
    return json.loads(path.read_text()) if path.exists() else {}


def write(root, state, settings):
    fd, temporary = tempfile.mkstemp(prefix='.state-', dir=root)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(state, output, separators=(',', ':'))
            output.flush()
            os.fsync(output.fileno())
        own(temporary, settings)
        os.replace(temporary, root / 'state.json')
        directory = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def inventory(settings):
    compose = settings['compose']
    identifiers = subprocess.run(compose + ['ps', '-q'], capture_output=True, text=True, check=True, timeout=30).stdout.split()
    compose_ids = set(identifiers)
    if settings.get('tenantId'):
        managed = subprocess.run(['docker', 'ps', '-a', '-q', '--filter', 'label=auriga.managed=true'], capture_output=True, text=True, check=True, timeout=30).stdout.split()
        identifiers = list(dict.fromkeys(identifiers + managed))
    items = []
    if identifiers:
        containers = json.loads(subprocess.run(['docker', 'inspect', *identifiers], capture_output=True, text=True, check=True, timeout=30).stdout)
        for container in containers:
            if container.get('Id') not in compose_ids and not any(
                container.get('Id', '').startswith(identifier) for identifier in compose_ids
            ) and settings.get('tenantId'):
                if 'AURIGA_TENANT_ID=' + settings['tenantId'] not in container['Config'].get('Env', []):
                    continue
            # Never serialize Env, mounts, or the rest of inspect (may contain secrets).
            items.append({'name': container['Name'].lstrip('/'), 'image': container['Config']['Image'], 'imageId': container['Image'], 'state': container['State']['Status'], 'version': (container['Config'].get('Labels') or {}).get('org.opencontainers.image.version', '')})
    return {'reportedAt': stamp(), 'items': items}


def upgrade(settings, request):
    target = settings.get('targets', {}).get(request['targetId'])
    if not target:
        return {'status': 'failed', 'reason': 'target_not_approved'}
    if not re.fullmatch(r'[^\s]+@sha256:[a-f0-9]{64}', target['image']) or not Path(target['bundle']).is_absolute():
        return {'status': 'failed', 'reason': 'invalid_target'}
    try:
        result = subprocess.run(['sh', str(Path(settings['nasRoot']) / 'scripts/runtime-upgrade.sh'), target['bundle'], target['image']], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=900)
        return {'status': 'succeeded' if result.returncode == 0 else 'failed', 'exitCode': result.returncode, 'reason': 'deployment_committed' if result.returncode == 0 else 'deployment_failed'}
    except (OSError, subprocess.TimeoutExpired):
        # Never retry automatically: the script may have crossed its commit point.
        return {'status': 'failed', 'reason': 'deployment_outcome_unknown'}


def backup(root, settings, identifier):
    uuid.UUID(identifier)
    folder = safe_directory(root / 'backups')
    own(folder, settings)
    temporary = folder / (identifier + '.dump.part')
    final = folder / (identifier + '.dump')
    if temporary.exists() or temporary.is_symlink() or final.exists() or final.is_symlink():
        return {'status': 'failed', 'reason': 'archive_already_exists'}
    try:
        command = settings['compose'] + ['exec', '-T', 'postgres', 'pg_dump', '--format=custom', '--no-password', '--username', settings['databaseUser'], '--dbname', settings['database']]
        with temporary.open('xb') as output:
            result = subprocess.run(command, stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.DEVNULL, timeout=3600)
            output.flush()
            os.fsync(output.fileno())
        digest = hashlib.sha256()
        size = temporary.stat().st_size
        with temporary.open('rb') as archive:
            if result.returncode != 0 or archive.read(5) != b'PGDMP':
                return {'status': 'failed', 'reason': 'pg_dump_failed'}
            archive.seek(0)
            for chunk in iter(lambda: archive.read(1024 * 1024), b''):
                digest.update(chunk)
        if size > 9007199254740991:
            return {'status': 'failed', 'reason': 'archive_too_large'}
        temporary.chmod(0o600)
        own(temporary, settings)
        os.replace(temporary, final)
        directory = os.open(folder, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
        return {'status': 'succeeded', 'sizeBytes': size, 'sha256': digest.hexdigest()}
    except (OSError, subprocess.TimeoutExpired):
        return {'status': 'failed', 'reason': 'pg_dump_unavailable'}
    finally:
        if temporary.exists():
            temporary.unlink()


def schedule(state):
    plan = state.get('plan', {})
    backups = state.setdefault('backups', [])
    if not plan.get('enabled') or len(backups) >= 1000 or any(item['status'] in ('queued', 'running') for item in backups):
        return
    interval = plan.get('intervalHours', 24)
    if not isinstance(interval, int) or not 1 <= interval <= 720:
        return
    if datetime.fromisoformat(plan.get('nextAt', stamp()).replace('Z', '+00:00')) <= now():
        backups.append({'id': uuid7(), 'actor': 'scheduler', 'status': 'queued', 'createdAt': stamp()})
        plan['nextAt'] = stamp(now() + timedelta(hours=interval))


def tick(root, settings):
    root = safe_directory(root)
    own(root, settings)
    with locked(root, 'worker.lock'):
        # An interrupted prior tick must remain an explicit failure, never a replay.
        observed = inventory(settings)
        with locked(root, 'state.lock'):
            state = read(root)
            tenant = str(uuid.UUID(settings['tenantId']))
            if state.get('tenantId', tenant) != tenant:
                raise ValueError('Operations installation tenant mismatch')
            state['tenantId'] = tenant
            for kind in ['upgrades', 'backups']:
                for item in state.get(kind, []):
                    if item['status'] == 'running':
                        item.update(status='failed', reason='worker_interrupted', finishedAt=stamp())
            state['inventory'] = observed
            state['available'] = [{'id': key, 'label': value['label'], 'image': value['image']} for key, value in settings.get('targets', {}).items()]
            schedule(state)
            work = []
            for kind in ['upgrades', 'backups']:
                for item in state.get(kind, []):
                    if item['status'] == 'queued':
                        item.update(status='running', startedAt=stamp())
                        work.append((kind, dict(item)))
                        break
            write(root, state, settings)
            own(root / 'state.lock', settings)
        for kind, item in work:
            receipt = upgrade(settings, item) if kind == 'upgrades' else backup(root, settings, item['id'])
            with locked(root, 'state.lock'):
                state = read(root)
                for entry in state[kind]:
                    if entry['id'] == item['id']:
                        entry.update(receipt, finishedAt=stamp())
                # Report actual containers after deployment, not requested images.
                try:
                    state['inventory'] = inventory(settings)
                except (OSError, subprocess.SubprocessError, ValueError):
                    pass  # Preserve last known report time; API rejects stale workers.
                write(root, state, settings)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('--directory', required=True)
    args = parser.parse_args()
    config = Path(args.config)
    if config.is_symlink() or config.stat().st_mode & 0o022:
        raise ValueError('Worker config must not be group/world writable')
    settings = json.loads(config.read_text())
    settings['tenantId'] = str(uuid.UUID(settings['tenantId']))
    if not all(re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]{0,62}', settings[key]) for key in ['database', 'databaseUser']):
        raise ValueError('Invalid database identifiers')
    tick(Path(args.directory), settings)


if __name__ == '__main__':
    try:
        main()
    except BlockingIOError:
        pass  # An earlier scheduled tick still owns worker.lock; do not queue another process.
    except Exception:
        # Even command failures and config exceptions can contain credentials.
        print('ops_runtime_tick_failed', file=__import__('sys').stderr)
        raise SystemExit(1)
