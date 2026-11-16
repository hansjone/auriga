#!/usr/bin/env python3
"""CLI transport exercise with real tiny images and explicit fixture NAS scripts.

No production stack is started. doctor must report unavailable authentication.
The caller provides prebuilt Linux CLI/bootstrap binaries and a local Alpine.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
SERVICES = ('platform', 'edge', 'agent', 'bifrost', 'operator')


def run(*args, **kwargs):
    return subprocess.check_output([str(arg) for arg in args], text=True, **kwargs).strip()


def sha(path):
    with open(path, 'rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def write(path, value, mode=0o644):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(value)
    path.chmod(mode)


def exercise(directory):
    run('docker', 'image', 'inspect', 'alpine:3.22')
    base = f'auriga/offline-fixture:{os.getpid()}'
    build = directory / 'build'
    build.mkdir()
    write(build / 'Dockerfile', 'FROM alpine:3.22\nLABEL auriga.offline.fixture=true\n')
    run('docker', 'build', '--platform', 'linux/amd64', '--network', 'none', '--pull=false',
        '--provenance=false', '--sbom=false', '-t', base, build)
    nas = directory / 'nas'
    recipe = nas / 'deploy-nas'
    shutil.copytree(ROOT / 'deploy/nas', recipe, ignore=shutil.ignore_patterns('test', 'bootstrap'))
    for path in recipe.rglob('*.sh'):
        path.write_bytes(path.read_bytes().replace(b'\r\n', b'\n'))
    boot = recipe / 'bin/auriga-nas-bootstrap'
    boot.chmod(0o755)
    runtime = recipe / 'bin/runtime-release'
    write(runtime, '#!/bin/sh\nexit 0\n', 0o755)
    images = nas / 'images'
    images.mkdir()
    for service in SERVICES:
        tag = f'auriga/{service}:nas'
        run('docker', 'tag', base, tag)
        run('docker', 'save', '-o', images / f'{service}.tar', tag)
    metadata = json.loads(run(boot, 'image-metadata', images))
    for name, record in metadata.items():
        record.update(id=run('docker', 'image', 'inspect', '--format', '{{.Id}}', f'auriga/{name}:nas'), tar=f'{name}.tar')
    manifest = dict(commit='a' * 40, source_tree='b' * 40, images=metadata,
                    bootstrap=dict(file='deploy-nas/bin/auriga-nas-bootstrap', sha256=sha(boot)),
                    runtime_release=dict(file='deploy-nas/bin/runtime-release', sha256=sha(runtime), os='linux', arch='amd64'))
    write(images / 'manifest.json', json.dumps(manifest))
    write(images / 'SHA256SUMS', ''.join(f'{sha(images / (name + ".tar"))}  ./{name}.tar\n' for name in SERVICES))
    write(nas / 'versions.txt', 'commit=' + 'a' * 40 + '\n')
    write(nas / 'SHA256SUMS', ''.join(f'{sha(nas / name)}  {name}\n' for name in
          ('deploy-nas/bin/auriga-nas-bootstrap', 'deploy-nas/bin/runtime-release', 'images/manifest.json', 'versions.txt')))
    # Explicit fixtures substitute service orchestration. The real image loader,
    # package verifier, config/key generator and object-write probe remain active.
    write(recipe / 'scripts/init.sh', '''#!/bin/sh
set -eu
. "$NAS_ROOT/scripts/lib.sh"
load_stack_env
docker run --rm --pull never --network none --mount "type=bind,src=$AURIGA_DATA_ROOT,dst=/data" --entrypoint sh "$AURIGA_IMAGE_POSTGRES" -ec 'touch /data/.auriga-initialized'
''', 0o755)
    for name in ('preflight', 'start', 'stop'):
        write(recipe / f'scripts/{name}.sh', '#!/bin/sh\nexit 0\n', 0o755)
    health = json.dumps([dict(Service=name, State='running') for name in
                         ('postgres', 'nats', 'registry', 'platform', 'edge', 'agent', 'bifrost', 'egress')])
    write(recipe / 'scripts/oneclick-compose.sh', f"#!/bin/sh\nprintf '%s\\n' '{health}'\n", 0o755)
    artifact = directory / 'nas.tar'
    with tarfile.open(artifact, 'w') as archive:
        for path in nas.rglob('*'):
            if path.is_file():
                archive.add(path, arcname=str(path.relative_to(nas)))

    index = directory / 'release.json'
    identity = run('docker', 'image', 'inspect', '--format', '{{.Id}}', base)
    write(index, json.dumps(dict(schemaVersion=1, version='fixture', nas=dict(url=str(artifact), sha256=sha(artifact)),
          images={name: dict(ref=identity, tag='', archive='') for name in ('postgres', 'nats', 'registry')})))
    output = directory / 'offline.tar'
    print(run('unshare', '--net', ROOT / 'bin/auriga-cli', 'bundle', '--release', index, '--out', output, cwd=ROOT))
    # Both bundle and the field CLI run with no interfaces/routes. All container
    # helpers also use --network none and talk only through the local socket.
    extracted = directory / 'field'
    extracted.mkdir()
    with tarfile.open(output) as archive:
        archive.extractall(extracted, filter='data')
    access = json.loads((recipe / 'access-ip.example.json').read_text())
    write(directory / 'access.json', json.dumps(access))
    data_parent = directory / 'site'
    command = ['unshare', '--net', extracted / 'auriga', 'install', '--proxy', directory / 'access.json', '--data', data_parent]
    print(run(*command))
    check = subprocess.run(['unshare', '--net', extracted / 'auriga', 'doctor', '--data', data_parent], capture_output=True, text=True)
    print(check.stdout)
    assert check.returncode == 1 and 'FAIL' in check.stdout and 'PASS' in check.stdout
    assert 'PASS 对象存储UID10001写入' in check.stdout
    assert (data_parent / 'auriga-data/resource-keys/offline/downgrade.pk8').stat().st_mode & 0o777 == 0o600
    print(f'PASS fixture_bundle_install_doctor bytes={output.stat().st_size} field_namespace=unshare--net')


def main():
    if os.geteuid() != 0:
        raise SystemExit('run as root on the local Linux Docker host')
    for name in SERVICES:
        if subprocess.run(['docker', 'image', 'inspect', f'auriga/{name}:nas'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise SystemExit('fixture requires unused auriga/*:nas transport tags')
    had_network = subprocess.run(['docker', 'network', 'inspect', 'auriga-offline-lan'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    try:
        with tempfile.TemporaryDirectory(prefix='auriga-offline-fixture-') as name:
            directory = Path(name)
            directory.chmod(0o755)
            exercise(directory)
    finally:
        for name in SERVICES:
            subprocess.run(['docker', 'image', 'rm', f'auriga/{name}:nas'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for name in ('postgres', 'nats', 'registry'):
            subprocess.run(['docker', 'image', 'rm', f'auriga/offline-{name}:fixture'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['docker', 'image', 'rm', f'auriga/offline-fixture:{os.getpid()}'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if not had_network:
            subprocess.run(['docker', 'network', 'rm', 'auriga-offline-lan'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
