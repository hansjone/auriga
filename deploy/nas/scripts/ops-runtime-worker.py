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
import selectors
import signal
import shutil
import subprocess
import tarfile
import tempfile
import time
import uuid


def now():
    return datetime.now(timezone.utc)


def stamp(value=None):
    return (value or now()).isoformat().replace('+00:00', 'Z')


def uuid7():
    return str(uuid.UUID(int=(int(now().timestamp() * 1000) << 80) | (7 << 76) | (secrets.randbits(12) << 64) | (2 << 62) | secrets.randbits(62)))


def permissions(descriptor, settings, mode=None):
    if 'ownerUid' in settings:
        os.fchown(descriptor, settings['ownerUid'], settings['ownerGid'])
    if mode is not None:
        os.fchmod(descriptor, mode)


@contextmanager
def directory(path, settings):
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        permissions(descriptor, settings)
        yield Path('/proc/self/fd/' + str(descriptor))
    finally:
        os.close(descriptor)


def safe_directory(path):
    path = Path(path).absolute()
    if any(p.is_symlink() for p in [path, *path.parents]):
        raise ValueError('Symlink control directory')
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    return path


@contextmanager
def locked(root, name, settings=None):
    import fcntl
    descriptor = os.open(root / name, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'r+b') as lock:
        fcntl.lockf(lock, fcntl.LOCK_EX | (fcntl.LOCK_NB if name == 'worker.lock' else 0))
        permissions(lock.fileno(), settings or {})
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
            permissions(output.fileno(), settings, 0o600)
            json.dump(state, output, separators=(',', ':'))
            output.flush()
            os.fsync(output.fileno())
            if os.stat(temporary, follow_symlinks=False).st_ino != os.fstat(output.fileno()).st_ino:
                raise OSError('State replaced')
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
                if 'AURIGA_TENANT_ID=' + settings['tenantId'] not in (container['Config'].get('Env') or []):
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
        result = subprocess.Popen(['sh', str(Path(settings['nasRoot']) / 'scripts/runtime-upgrade.sh'), target['bundle'], target['image']], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        result.wait(timeout=900)
        return {'status': 'succeeded' if result.returncode == 0 else 'failed', 'exitCode': result.returncode, 'reason': 'deployment_committed' if result.returncode == 0 else 'deployment_failed'}
    except (OSError, subprocess.TimeoutExpired):
        if 'result' in locals():
            stop(result)
        # Never retry automatically: the script may have crossed its commit point.
        return {'status': 'failed', 'reason': 'deployment_outcome_unknown'}


def stop(process):
    # TERM reaches the shell's rollback trap and its children before any hard kill.
    try:
        os.killpg(process.pid, signal.SIGTERM)
        process.wait(timeout=150)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=30)
    except ProcessLookupError:
        pass


def cancel_dump(settings, identifier):
    application = 'auriga_ops_' + uuid.UUID(identifier).hex
    sql = "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name='" + application + "' AND pid<>pg_backend_pid()"
    subprocess.run(settings['compose'] + ['exec', '-T', 'postgres', 'psql', '--no-password', '-U', settings['databaseUser'], '-d', 'postgres', '-c', sql], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)


def free_bytes(folder):
    return shutil.disk_usage(folder).free


def dump_component(settings, identifier, arguments, output, folder):
    command = settings['compose'] + ['exec', '-T', '-e', 'PGAPPNAME=auriga_ops_' + uuid.UUID(identifier).hex, 'postgres'] + arguments
    process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
    deadline = time.monotonic() + 3300
    try:
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while True:
                if time.monotonic() >= deadline:
                    raise TimeoutError('dump timeout')
                if free_bytes(folder) < settings.get('minFreeMiB', 1024) * 1048576:
                    raise OSError('backup_storage_low')
                if not selector.select(timeout=1):
                    continue
                chunk = os.read(process.stdout.fileno(), 65536)
                if not chunk:
                    break
                output.write(chunk)
        if process.wait(timeout=30) != 0:
            raise OSError('pg_dump_failed')
    except BaseException:
        stop(process)
        cancel_dump(settings, identifier)
        raise
    finally:
        process.stdout.close()


def backup(root, settings, identifier):
    identifier = str(uuid.UUID(identifier))
    folder_path = root / 'backups'
    folder_path.mkdir(mode=0o700, exist_ok=True)
    with directory(folder_path, settings) as folder:
        if free_bytes(folder) < settings.get('minFreeMiB', 1024) * 1048576:
            return {'status': 'failed', 'reason': 'backup_storage_low'}
        temporary = folder / (identifier + '.tar.part')
        final = folder / (identifier + '.tar')
        if temporary.exists() or temporary.is_symlink() or final.exists() or final.is_symlink():
            return {'status': 'failed', 'reason': 'archive_already_exists'}
        try:
            databases = settings.get('databases', [settings.get('database', 'auriga'), 'bifrost'])
            if not databases or len(set(databases)) != len(databases) or not all(re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]{0,62}', name) for name in databases):
                raise ValueError('Invalid databases')
            with tempfile.TemporaryDirectory(prefix='.' + identifier + '-', dir=folder) as work:
                with directory(work, {}) as working:
                    with temporary.open('xb') as output:
                        permissions(output.fileno(), settings, 0o600)
                        with tarfile.open(fileobj=output, mode='w|') as archive:
                            components = [(name + '.dump', ['pg_dump', '--format=custom', '--no-password', '--username', settings['databaseUser'], '--dbname', name]) for name in databases]
                            components.append(('roles.sql', ['pg_dumpall', '--roles-only', '--no-role-passwords', '--no-password', '--username', settings['databaseUser']]))
                            for name, arguments in components:
                                component = working / name
                                with component.open('xb+') as data:
                                    dump_component(settings, identifier, arguments, data, folder)
                                    data.flush()
                                    data.seek(0)
                                    if name.endswith('.dump') and data.read(5) != b'PGDMP':
                                        raise OSError('pg_dump_failed')
                                    # Tar serialization reads only this bounded component from disk.
                                    data.seek(0)
                                    metadata = tarfile.TarInfo(name)
                                    metadata.size = os.fstat(data.fileno()).st_size
                                    if free_bytes(folder) - metadata.size < settings.get('minFreeMiB', 1024) * 1048576:
                                        raise OSError('backup_storage_low')
                                    metadata.mode = 0o600
                                    archive.addfile(metadata, data)
                                component.unlink()
                        output.flush()
                        os.fsync(output.fileno())
                        stat = os.fstat(output.fileno())
                        if temporary.lstat().st_ino != stat.st_ino:
                            raise OSError('archive_replaced')
            digest = hashlib.sha256()
            descriptor = os.open(temporary, os.O_RDONLY | os.O_NOFOLLOW)
            with os.fdopen(descriptor, 'rb') as archive:
                for chunk in iter(lambda: archive.read(1048576), b''):
                    digest.update(chunk)
            os.replace(temporary, final)
            descriptor = os.open(folder, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
            return {'status': 'succeeded', 'sizeBytes': stat.st_size, 'sha256': digest.hexdigest(), 'format': 'tar', 'databases': databases, 'includesRoles': True}
        except (OSError, TimeoutError, subprocess.SubprocessError) as error:
            return {'status': 'failed', 'reason': 'backup_storage_low' if str(error) == 'backup_storage_low' else 'pg_dump_failed'}
        finally:
            if temporary.exists() or temporary.is_symlink():
                temporary.unlink()


def retain(root, state):
    plan = state.get('plan', {})
    count, days = plan.get('retentionCount', 7), plan.get('retentionDays', 30)
    if not 1 <= count <= 365 or not 1 <= days <= 3650:
        raise ValueError('Invalid retention')
    terminal = [item for item in state.get('backups', []) if item['status'] not in ('queued', 'running')]
    terminal.sort(key=lambda item: item['createdAt'], reverse=True)
    successes, failures = 0, 0
    for item in terminal:
        if item['status'] == 'succeeded':
            successes += 1
            over_count = successes > count
        else:
            failures += 1
            over_count = failures > 100
        identifier = str(uuid.UUID(item['id']))
        try:
            path = root / 'backups'
            if path.exists() or path.is_symlink():
                with directory(path, {}) as folder:
                    for suffix in ['.tar.part', '.dump.part']:
                        (folder / (identifier + suffix)).unlink(missing_ok=True)
                    for leftover in folder.iterdir():
                        if re.fullmatch(r'\.' + re.escape(identifier) + r'-[A-Za-z0-9_-]+', leftover.name):
                            if leftover.is_symlink():
                                leftover.unlink()
                            else:
                                shutil.rmtree(leftover)
        except OSError:
            state.setdefault('alerts', []).append('backup_retention_blocked')
            continue
        expired = datetime.fromisoformat(item['createdAt'].replace('Z', '+00:00')) < now() - timedelta(days=days)
        if not (item.get('deleteRequestedAt') or over_count or expired):
            continue
        try:
            path = root / 'backups'
            if path.exists() or path.is_symlink():
                with directory(path, {}) as folder:
                    for suffix in ['.tar', '.dump', '.tar.part', '.dump.part']:
                        (folder / (identifier + suffix)).unlink(missing_ok=True)
            state['backups'].remove(item)
            state['tickets'] = [ticket for ticket in state.get('tickets', []) if ticket['backupId'] != identifier]
        except OSError:
            state.setdefault('alerts', []).append('backup_retention_blocked')


def schedule(state):
    plan = state.get('plan', {})
    backups = state.setdefault('backups', [])
    if len(backups) >= 1000:
        state.setdefault('alerts', []).append('backup_history_full')
        return
    if not plan.get('enabled') or any(item['status'] in ('queued', 'running') for item in backups):
        return
    interval = plan.get('intervalHours', 24)
    if not isinstance(interval, int) or not 1 <= interval <= 720:
        return
    if datetime.fromisoformat(plan.get('nextAt', stamp()).replace('Z', '+00:00')) <= now():
        backups.append({'id': uuid7(), 'actor': 'scheduler', 'status': 'queued', 'createdAt': stamp()})
        plan['nextAt'] = stamp(now() + timedelta(hours=interval))


def tick(root, settings):
    root = safe_directory(root)
    with directory(root, settings) as anchored:
        tick_anchored(anchored, settings)


def tick_anchored(root, settings):
    with locked(root, 'worker.lock'):
        # An interrupted prior tick must remain an explicit failure, never a replay.
        observed = inventory(settings)
        with locked(root, 'state.lock', settings):
            state = read(root)
            tenant = str(uuid.UUID(settings['tenantId']))
            if state.get('tenantId', tenant) != tenant:
                raise ValueError('Operations installation tenant mismatch')
            state['tenantId'] = tenant
            state['alerts'] = []
            for kind in ['upgrades', 'backups']:
                for item in state.get(kind, []):
                    if item['status'] == 'running':
                        if kind == 'backups':
                            cancel_dump(settings, item['id'])
                        item.update(status='failed', reason='worker_interrupted', finishedAt=stamp())
            state['inventory'] = observed
            state['available'] = [{'id': key, 'label': value['label'], 'image': value['image']} for key, value in settings.get('targets', {}).items()]
            retain(root, state)
            schedule(state)
            work = []
            for kind in ['upgrades', 'backups']:
                for item in state.get(kind, []):
                    if item['status'] == 'queued':
                        item.update(status='running', startedAt=stamp())
                        work.append((kind, dict(item)))
                        break
            write(root, state, settings)
        for kind, item in work:
            receipt = upgrade(settings, item) if kind == 'upgrades' else backup(root, {**settings, 'minFreeMiB': state.get('plan', {}).get('minFreeMiB', 1024)}, item['id'])
            with locked(root, 'state.lock'):
                state = read(root)
                for entry in state[kind]:
                    if entry['id'] == item['id']:
                        entry.update(receipt, finishedAt=stamp())
                if receipt.get('reason') == 'backup_storage_low':
                    state.setdefault('alerts', []).append('backup_storage_low')
                retain(root, state)
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
    if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]{0,62}', settings['databaseUser']):
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
