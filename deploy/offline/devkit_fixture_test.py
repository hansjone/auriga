#!/usr/bin/env python3
"""Small application fixtures with the real devkit CLI, Go toolchain and Docker.

Runs no Auriga stack and performs no full language/application cold build.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

import docker_fixture_test as transport

ROOT = transport.ROOT
run, write, sha = transport.run, transport.write, transport.sha


def archive_tree(source, output):
    with tarfile.open(output, 'w') as archive:
        for path in source.rglob('*'):
            if path.is_file():
                archive.add(path, arcname=str(path.relative_to(source)))


def exercise(directory):
    index, base = transport.exercise(directory)
    go = f'auriga/devkit-fixture-go:{os.getpid()}'
    build = directory / 'go-image'
    build.mkdir()
    write(build / 'Dockerfile', 'FROM golang:1.27.3\nLABEL auriga.offline.fixture=true\n')
    run('docker', 'build', '--platform', 'linux/amd64', '--network', 'none', '--pull=false',
        '--provenance=false', '--sbom=false', '-t', go, build)
    go_id = run('docker', 'image', 'inspect', '--format', '{{.Id}}', go)
    base_id = run('docker', 'image', 'inspect', '--format', '{{.Id}}', base)
    source = directory / 'development'
    source.mkdir()
    shutil.copytree(ROOT / 'deploy/offline', source / 'deploy/offline', ignore=shutil.ignore_patterns('*.out', '*.test', '__pycache__'))
    shutil.copytree(directory / 'nas/deploy-nas', source / 'deploy/nas', ignore=shutil.ignore_patterns('bin'))
    shutil.copy2(ROOT / 'auriga', source / 'auriga')
    (source / 'auriga').chmod(0o755)
    write(source / '.gitignore', '/bin/\n/.auriga-devkit.json\n/auriga-offline-*.tar\n/auriga-offline-*.tar.sha256\n')
    write(source / 'site.txt', 'upstream\n')
    run('git', 'init', '-b', 'upstream', source)
    run('git', '-C', source, 'config', 'user.name', 'alice')
    run('git', '-C', source, 'config', 'user.email', 'alice@example.com')
    run('git', '-C', source, 'add', '.')
    run('git', '-C', source, 'commit', '-m', 'fixture source')
    run('git', '-C', source, 'tag', '0.2.0')
    recipes = directory / 'fixture-recipes/deploy/nas/offline-build'
    recipes.mkdir(parents=True)
    write(recipes / 'go-inputs.sh', '#!/bin/sh\nset -eu\ncp /inputs/auriga-nas-bootstrap /inputs/runtime-release "$2/"\n', 0o755)
    write(recipes / 'web-inputs.sh', '#!/bin/sh\nset -eu\nmkdir -p "$2/admin" "$2/portal" "$2/panel"\nprintf html >"$2/admin/index.html"\n', 0o755)
    write(recipes / 'java-inputs.sh', '#!/bin/sh\nset -eu\nprintf fixture >"$2/platform.jar"\n', 0o755)
    write(recipes / 'assemble-image.sh', '''#!/bin/sh
set -eu
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
printf 'FROM %s\nLABEL auriga.fixture.service=%s\n' "$3" "$1" >"$scratch/Dockerfile"
docker build --platform linux/amd64 --network none --pull=false --provenance=false --sbom=false -t "$2" "$scratch"
''', 0o755)
    recipe_tar = directory / 'recipes.tar'
    archive_tree(directory / 'fixture-recipes', recipe_tar)
    inputs = directory / 'inputs'
    for name in ('go', 'web', 'java', 'system'):
        (inputs / name).mkdir(parents=True)
        write(inputs / name / 'fixture-input', 'offline only\n')
    for name in ('auriga-nas-bootstrap', 'runtime-release'):
        shutil.copy2(directory / 'nas/deploy-nas/bin' / name, inputs / 'go' / name)
    inputs_tar = directory / 'inputs.tar'
    archive_tree(inputs, inputs_tar)
    release = json.loads(index.read_text())
    release['version'] = '0.2.0'
    release['build'] = dict(inputs=dict(url=str(inputs_tar), sha256=sha(inputs_tar)),
                            recipes=dict(url=str(recipe_tar), sha256=sha(recipe_tar)),
                            tools=dict(go=go_id, web=base_id, java=base_id),
                            bases={name: base_id for name in transport.SERVICES})
    write(index, json.dumps(release))
    private, public = directory / 'release-private.pem', directory / 'release-public.pem'
    run('openssl', 'genpkey', '-algorithm', 'ED25519', '-out', private)
    run('openssl', 'pkey', '-in', private, '-pubout', '-out', public)
    cache = directory / 'release-cache'
    distribution = directory / 'release-0.2.0'
    print(run('unshare', '--net', ROOT / 'bin/auriga-cli', 'release', '--tag', '0.2.0',
              '--release', index, '--outdir', distribution, '--cache-dir', cache,
              '--sign-key', private, '--attachment-limit', 32 << 20, '--build', cwd=source))
    print(run('unshare', '--net', distribution / 'auriga', 'verify', distribution, '--verify-key', public))
    first_manifest = json.loads((distribution / 'release-manifest.json').read_text())
    devkit_size = first_manifest['devkit']['asset']['size']
    kit = directory / 'kit'
    print(run('unshare', '--net', distribution / 'auriga', 'unpack',
              distribution / 'auriga-devkit-0.2.0.tar.part001', '--out', kit, '--verify-key', public))
    print(run(kit / 'source/auriga', 'devkit', '--verify', kit, '--verify-key', public, cwd=kit / 'source'))
    # The next upstream tag changes only code. The release still rebuilds its
    # installation images, while dependency-identical devkit bytes are reused.
    write(source / 'site.txt', 'new upstream\n')
    run('git', '-C', source, 'add', '.')
    run('git', '-C', source, 'commit', '-m', 'new upstream code')
    run('git', '-C', source, 'tag', '0.2.1')
    next_release = directory / 'release-0.2.1'
    print(run('unshare', '--net', ROOT / 'bin/auriga-cli', 'release', '--tag', '0.2.1',
              '--release', index, '--outdir', next_release, '--cache-dir', cache,
              '--sign-key', private, '--attachment-limit', 32 << 20, '--build', cwd=source))
    next_manifest = json.loads((next_release / 'release-manifest.json').read_text())
    assert next_manifest['locks']['digest'] == first_manifest['locks']['digest']
    assert next_manifest['devkit']['asset']['sha256'] == first_manifest['devkit']['asset']['sha256']
    assert next_manifest['devkit']['sourceTag'] == '0.2.0'
    assert len(list(cache.glob('*/auriga-devkit-*.tar'))) == 1
    print(run('unshare', '--net', next_release / 'auriga', 'install', '--package',
              next_release / 'auriga-offline-0.2.1.tar.part001', '--verify-key', public,
              '--proxy', directory / 'access.json', '--data', directory / 'multipart-site'))
    checkout = source
    (checkout / 'bin').mkdir(exist_ok=True)
    shutil.copy2(next_release / 'auriga', checkout / 'bin/auriga-cli')
    (checkout / 'bin/auriga-cli').chmod(0o755)
    print(run('unshare', '--net', checkout / 'auriga', 'devkit', 'verify', kit, '--verify-key', public, cwd=checkout))
    run('git', '-C', checkout, 'switch', '-c', 'site/fix')
    write(checkout / 'site.txt', 'site modification\n')
    output = directory / 'site.tar'
    print(run('unshare', '--net', checkout / 'auriga', 'bundle', '--build', '--offline', '--version', '0.2.0-site.3', '--out', output, cwd=checkout))
    with tarfile.open(output) as archive:
        provenance = json.load(archive.extractfile('provenance.json'))
    assert provenance['version'] == '0.2.0-site.3' and provenance['dirty'] and provenance['siteChanges']
    assert provenance['branch'] == 'site/fix' and provenance['upstreamVersion'] == '0.2.1'
    # Install the resulting exact format through the same fixture entry points.
    field = directory / 'new-field'
    field.mkdir()
    with tarfile.open(output) as archive:
        archive.extractall(field, filter='data')
    site = directory / 'new-site'
    print(run('unshare', '--net', field / 'auriga', 'install', '--proxy', directory / 'access.json', '--data', site))
    result = subprocess.run(['unshare', '--net', field / 'auriga', 'doctor', '--data', site], capture_output=True, text=True)
    assert result.returncode == 1 and '0.2.0-site.3' in result.stdout and provenance['commit'] in result.stdout
    print(result.stdout)
    lock = checkout / 'deploy/offline/go.mod'
    lock_bytes = lock.read_bytes()
    lock.write_bytes(lock_bytes + b'\n// changed dependencies\n')
    failure = subprocess.run([checkout / 'auriga', 'devkit', 'verify'], cwd=checkout, capture_output=True, text=True)
    assert failure.returncode != 0 and 'deploy/offline/go.mod' in failure.stderr and 'auriga-devkit-0.2.1.tar' in failure.stderr
    lock.write_bytes(lock_bytes)
    dependency = kit / 'cache/inputs.tar'
    dependency.rename(kit / 'cache/inputs.saved')
    failure = subprocess.run(['unshare', '--net', checkout / 'auriga', 'bundle', '--build', '--offline'], cwd=checkout, capture_output=True, text=True)
    assert failure.returncode != 0 and 'inputs.tar' in failure.stderr
    (kit / 'cache/inputs.saved').rename(dependency)
    with open(dependency, 'r+b') as stream:
        stream.write(b'tampered')
    failure = subprocess.run(['unshare', '--net', checkout / 'auriga', 'bundle', '--build', '--offline'], cwd=checkout, capture_output=True, text=True)
    assert failure.returncode != 0 and 'inputs.tar' in failure.stderr
    print(f'PASS release_signed_multipart_devkit_reuse_verify_offline_build_install devkit_bytes={devkit_size} bundle_bytes={output.stat().st_size}')


def main():
    if os.geteuid() != 0:
        raise SystemExit('requires local root Docker access')
    for name in transport.SERVICES:
        if subprocess.run(['docker', 'image', 'inspect', f'auriga/{name}:nas'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise SystemExit('fixture transport tags already in use')
    had_network = subprocess.run(['docker', 'network', 'inspect', 'auriga-offline-lan'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    before = set(run('docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}').splitlines())
    try:
        with tempfile.TemporaryDirectory(prefix='auriga-devkit-fixture-') as name:
            directory = Path(name)
            directory.chmod(0o755)
            exercise(directory)
    finally:
        after = set(run('docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}').splitlines())
        for tag in after - before:
            if tag.startswith(('auriga/devkit-', 'auriga/offline-')) or tag in {f'auriga/{name}:nas' for name in transport.SERVICES}:
                subprocess.run(['docker', 'image', 'rm', tag], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if not had_network:
            subprocess.run(['docker', 'network', 'rm', 'auriga-offline-lan'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    main()
