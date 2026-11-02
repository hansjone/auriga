"""No Docker/HTTP/secrets: only the production entry plus real OS metadata/UIDs."""
import json
import os
from pathlib import Path
import shutil
import subprocess


ROOT = Path('/tmp/fixture')
ROOT.mkdir(mode=0o755)
SCRIPTS = ROOT / 'scripts'
SCRIPTS.mkdir()
shutil.copyfile('/tmp/start.sh', SCRIPTS / 'start-collab-network.sh')
(ROOT / 'data/config/collab-source').mkdir(parents=True)
(SCRIPTS / 'lib.sh').write_text('''
NAS_ROOT=/tmp/fixture
AURIGA_DATA_ROOT="$NAS_ROOT/data"
load_stack_env() { :; }
require_cmd() { :; }
run_compose() { case "$*" in *'ps -q edge') echo edge-unchanged ;; esac; }
''')
for name in ('rotate-collab-keys.sh', 'render-collab.sh'):
    (SCRIPTS / name).write_text('#!/bin/sh\nexit 0\n')
BIN = ROOT / 'bin'
BIN.mkdir()
stat = BIN / 'stat'
stat.write_text('''#!/usr/bin/python3
import os, pathlib, subprocess, sys, time
result = subprocess.run(['/usr/bin/stat', *sys.argv[1:]], capture_output=True)
sys.stdout.buffer.write(result.stdout)
sys.stdout.flush()
sys.stderr.buffer.write(result.stderr)
if result.returncode == 0 and sys.argv[1:] == ['-c', '%a', os.environ.get('RACE_HOOK')]:
    sync = pathlib.Path(os.environ['RACE_SYNC'])
    (sync / 'sampled').write_text('actual stat completed')
    deadline = time.monotonic() + 5
    while not (sync / 'done').exists():
        if time.monotonic() >= deadline:
            raise SystemExit('fixture attacker did not finish')
        time.sleep(.005)
raise SystemExit(result.returncode)
''')
stat.chmod(0o755)
attacker = ROOT / 'attacker.py'
attacker.write_text('''import json, os, pathlib, sys, time
sync, target = map(pathlib.Path, sys.argv[1:])
assert os.getuid() == 65534
deadline = time.monotonic() + 5
while not (sync / 'sampled').exists():
    if (sync / 'stop').exists():
        (sync / 'attacker.json').write_text(json.dumps({'uid': os.getuid(), 'sampled': False}))
        raise SystemExit(0)
    if time.monotonic() >= deadline:
        raise SystemExit('fixture stat was never sampled')
    time.sleep(.005)
replacement = sync / 'replacement'
replacement.write_text('#!/bin/sh\\nprintf "replacement uid=%s\\\\n" "$(id -u)" >"$EXECUTED"\\n')
replacement.chmod(0o755)
try:
    os.replace(replacement, target)
    result = {'uid': os.getuid(), 'sampled': True, 'replaced': True}
except OSError as error:
    result = {'uid': os.getuid(), 'sampled': True, 'replaced': False, 'errno': error.errno}
(sync / 'attacker.json').write_text(json.dumps(result))
(sync / 'done').write_text('done')
''')


def uid_command(uid):
    return [] if uid == 0 else ['setpriv', f'--reuid={uid}', f'--regid={uid}', '--clear-groups']


def run_case(name, *, uid=0, parent_mode=0o755, parent_uid=0, file_uid=0,
             file_mode=0o700, link=None, race=False, pending=False):
    base = ROOT / name
    base.mkdir(mode=0o755)
    parent = base / 'hooks'
    parent.mkdir(mode=0o755)
    hook = parent / 'approved'
    hook.write_text('#!/bin/sh\nprintf "original uid=%s\\n" "$(id -u)" >"$EXECUTED"\n')
    hook.chmod(file_mode)
    os.chown(hook, file_uid, file_uid)
    os.chown(parent, parent_uid, parent_uid)
    parent.chmod(parent_mode)
    sync = base / 'sync'
    sync.mkdir(mode=0o777)
    sync.chmod(0o777)
    executed = sync / 'executed'
    path = hook
    if link == 'file':
        path = parent / 'link'
        path.symlink_to(hook)
    elif link == 'parent':
        (base / 'alias').symlink_to(parent, target_is_directory=True)
        path = base / 'alias/approved'
    elif link == 'dotdot':
        path = parent / '../hooks/approved'
    env = dict(os.environ, PATH=f'{BIN}:/usr/bin:/bin', EXECUTED=str(executed))
    if pending:
        env.pop('AURIGA_NETWORK_ANNOUNCEMENT_CHECK', None)
    else:
        env['AURIGA_NETWORK_ANNOUNCEMENT_CHECK'] = str(path)
    child = None
    if race:
        env.update(RACE_HOOK=str(hook), RACE_SYNC=str(sync))
        child = subprocess.Popen(uid_command(65534) + ['python3', str(attacker), str(sync), str(hook)],
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    initial = hook.stat()
    try:
        result = subprocess.run(uid_command(uid) + ['sh', str(SCRIPTS / 'start-collab-network.sh')],
                                env=env, capture_output=True, text=True, timeout=15)
    finally:
        if child:
            (sync / 'stop').write_text('stop')
            stdout, stderr = child.communicate(timeout=10)
            assert child.returncode == 0, (stdout, stderr)
    attack = json.loads((sync / 'attacker.json').read_text()) if child else None
    record = dict(case=name, exit=result.returncode,
                  ready='message_network=READY\n' in result.stdout,
                  executed=executed.read_text().strip() if executed.exists() else None,
                  initialOwner=initial.st_uid, initialMode=oct(initial.st_mode & 0o777),
                  parentOwner=parent.stat().st_uid, parentMode=oct(parent.stat().st_mode & 0o777),
                  finalOwner=hook.stat().st_uid, attacker=attack,
                  stdout=result.stdout.strip(), stderr=result.stderr.strip())
    print(json.dumps(record), flush=True)
    if pending:
        assert result.returncode == 2 and 'announcement_verification=pending' in result.stdout
        assert not record['ready'] and record['executed'] is None
    elif name in ('trusted-root', 'trusted-operator', 'race-trusted'):
        assert result.returncode == 0 and record['ready'], record
        assert record['executed'] == f'original uid={uid}', record
        if race:
            assert attack == dict(uid=65534, sampled=True, replaced=False, errno=13), attack
    else:
        assert result.returncode != 0 and not record['ready'] and record['executed'] is None, record
    print(f'PASS {name}', flush=True)


if os.environ.get('HOOK_SECURITY_CASE') == 'race-untrusted':
    run_case('race-untrusted', parent_mode=0o777, race=True)
else:
    run_case('trusted-root')
    run_case('trusted-operator', uid=65534, parent_uid=65534, file_uid=65534)
    run_case('race-untrusted', parent_mode=0o777, race=True)
    run_case('race-trusted', race=True)
    run_case('writable-parent', parent_mode=0o777)
    run_case('group-writable-parent', parent_mode=0o775)
    run_case('foreign-parent-owner', parent_uid=65534)
    run_case('foreign-file-owner', file_uid=65534)
    run_case('writable-file', file_mode=0o722)
    run_case('file-link', link='file')
    run_case('parent-link', link='parent')
    run_case('noncanonical', link='dotdot')
    run_case('no-hook-pending', pending=True)
