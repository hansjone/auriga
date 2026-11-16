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
import sys

import docker_fixture_test as transport

ROOT = transport.ROOT
run, write, sha = transport.run, transport.write, transport.sha


def archive_tree(source, output):
    with tarfile.open(output, 'w') as archive:
        for path in source.rglob('*'):
            if path.is_file():
                archive.add(path, arcname=str(path.relative_to(source)))


def windows_exercise(original, kit):
    parent = ROOT / '.offline-releases'
    parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='windows-entry-', dir=parent) as temporary:
        stage = Path(temporary)
        checkout, copied_kit = stage / 'source with spaces', stage / 'kit'
        shutil.copytree(original, checkout)
        shutil.copytree(kit, copied_kit)
        def winpath(path):
            return run('wslpath', '-w', path)
        def ps(*args):
            result = subprocess.run(['/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe',
                                     '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File',
                                     winpath(checkout / 'auriga.ps1'), *map(str, args)],
                                    capture_output=True, text=True, encoding='utf-8', errors='replace')
            print(result.stdout)
            print(result.stderr)
            assert result.returncode == 0, 'PowerShell entry failed'
            return result.stdout + result.stderr
        ps('verify', '.', '--verify-key', winpath(checkout / 'release-public.pem'))
        keydir = stage / 'site-key'
        ps('keygen', 'site', '--out', winpath(keydir))
        for path in list(checkout.rglob('*.sh')) + list(checkout.rglob('go.mod')):
            path.write_bytes(path.read_bytes().replace(b'\r\n', b'\n').replace(b'\n', b'\r\n'))
            path.chmod(0o644)
        write(checkout / 'windows-new.sh', '#!/bin/sh\r\necho windows-edit\r\n')
        ps('devkit', 'verify', winpath(copied_kit), '--backend', 'wsl')
        patch = stage / 'windows changes.tar'
        ps('changes', 'export', '--out', winpath(patch))
        output = stage / 'windows-server.tar'
        log = ps('bundle', '--build', '--offline', '--backend', 'wsl', '--out', winpath(output), '--sign-key', winpath(keydir / 'site-private.pem'))
        assert 'fixture-cgroup-memory=4294967296' in log
        assert 'fixture-net-interfaces=lo,' in log
        with tarfile.open(output) as archive:
            provenance = json.load(archive.extractfile('provenance.json'))
        assert provenance['siteChanges'] and provenance['version'].startswith('0.2.0-site.')
        print(f'PASS powershell51_wsl_engine_offline_bundle_crlf_modes bytes={output.stat().st_size}')


def exercise(directory):
    index, base = transport.exercise(directory)
    go = f'auriga/devkit-fixture-go:{os.getpid()}'
    build = directory / 'go-image'
    build.mkdir()
    shutil.copy2('/usr/bin/docker', build / 'docker')
    for plugin in json.loads(run('docker', 'info', '--format', '{{json .ClientInfo.Plugins}}')):
        if plugin['Name'] in ('compose', 'buildx'):
            destination = build / 'cli-plugins' / ('docker-' + plugin['Name'])
            destination.parent.mkdir(exist_ok=True)
            shutil.copy2(plugin['Path'], destination)
    write(build / 'Dockerfile', 'FROM golang:1.27.3\nCOPY docker /usr/local/bin/docker\nCOPY cli-plugins/ /usr/local/lib/docker/cli-plugins/\nLABEL auriga.offline.fixture=true\n')
    run('docker', 'build', '--platform', 'linux/amd64', '--network', 'none', '--pull=false',
        '--provenance=false', '--sbom=false', '-t', go, build)
    go_id = run('docker', 'image', 'inspect', '--format', '{{.Id}}', go)
    base_id = run('docker', 'image', 'inspect', '--format', '{{.Id}}', base)
    source = directory / 'development'
    source.mkdir()
    shutil.copytree(ROOT / 'deploy/offline', source / 'deploy/offline', ignore=shutil.ignore_patterns('*.out', '*.test', '__pycache__'))
    shutil.copytree(directory / 'nas/deploy-nas', source / 'deploy/nas', ignore=shutil.ignore_patterns('bin'))
    shutil.copy2(ROOT / 'auriga', source / 'auriga')
    shutil.copy2(ROOT / 'auriga.ps1', source / 'auriga.ps1')
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
    write(recipes / 'go-inputs.sh', '#!/bin/sh\nset -eu\nprintf fixture-cgroup-memory=; cat /sys/fs/cgroup/memory.max\nprintf fixture-net-interfaces=; ls /sys/class/net | tr "\\n" ","; echo\nif [ -f /source/windows-new.sh ]; then test -x /source/windows-new.sh; /source/windows-new.sh; fi\ncp /inputs/auriga-nas-bootstrap /inputs/runtime-release "$2/"\n', 0o755)
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
                            tools=dict(go=go_id, web=base_id, java=base_id, runner=go_id),
                            bases={name: base_id for name in transport.SERVICES})
    write(index, json.dumps(release))
    private, public = directory / 'release-private.pem', directory / 'release-public.pem'
    run('openssl', 'genpkey', '-algorithm', 'ED25519', '-out', private)
    run('openssl', 'pkey', '-in', private, '-pubout', '-out', public)
    cache = directory / 'release-cache'
    distribution = directory / 'release-0.2.0'
    print(run('unshare', '--net', ROOT / 'bin/auriga-cli', 'release', '--tag', '0.2.0',
              '--release', index, '--outdir', distribution, '--cache-dir', cache,
              '--sign-key', private, '--attachment-limit', 32 << 20, '--build', '--new-baseline', cwd=source))
    print(run('unshare', '--net', distribution / 'auriga', 'verify', distribution, '--verify-key', public))
    first_manifest = json.loads((distribution / 'release-manifest.json').read_text())
    assert first_manifest['offlineBuildGate']['passed']
    kit = directory / 'kit'
    print(run('unshare', '--net', distribution / 'auriga', 'unpack',
              distribution / 'auriga-devkit-0.2.0.tar.part001', '--out', kit, '--verify-key', public))
    original = directory / 'original-source'
    print(run('unshare', '--net', distribution / 'auriga', 'unpack',
              distribution / 'auriga-source-0.2.0.tar', '--out', original, '--verify-key', public))
    assert not (original / '.git').exists()
    if '--volume-only' not in sys.argv and '--linux-only' not in sys.argv:
        windows_exercise(original, kit)
    if '--linux-only' not in sys.argv:
        volume_env = dict(os.environ, AURIGA_VOLUME_FIXTURE_SOURCE=str(original), AURIGA_VOLUME_FIXTURE_KIT=str(kit))
        volume_result = subprocess.run([ROOT / 'deploy/offline/offline-linux.test', '-test.v', '-test.run', '^TestDockerVolumeTransferIntegration$'], env=volume_env, text=True, stdout=subprocess.PIPE)
        print(volume_result.stdout)
        volume_result.check_returncode()
    if '--windows-only' in sys.argv or '--volume-only' in sys.argv:
        return
    fakebin = directory / 'no-git'
    marker = directory / 'git-was-called'
    write(fakebin / 'git', '#!/bin/sh\nprintf "%s\\n" "$PWD: $*" >>"' + str(marker) + '"\nexit 99\n', 0o755)
    field_env = dict(os.environ, PATH=str(fakebin) + ':' + os.environ['PATH'])
    def fieldrun(*args, **kwargs):
        result = run('unshare', '--net', *args, env=field_env, **kwargs)
        assert not marker.exists(), 'field invoked Git: ' + (marker.read_text() if marker.exists() else '')
        return result
    print(fieldrun(original / 'auriga', 'devkit', 'verify', kit, cwd=original))
    write(original / 'local-feature.txt', 'local site addition\n')
    patch = directory / 'site-changes.tar'
    print(fieldrun(original / 'auriga', 'changes', 'export', '--out', patch, cwd=original))
    # Maintainer creates the next tag using Git. The field workflow never does.
    write(source / 'site.txt', 'new upstream\n')
    run('git', '-C', source, 'add', '.')
    run('git', '-C', source, 'commit', '-m', 'new upstream code')
    run('git', '-C', source, 'tag', '0.2.1')
    next_release = directory / 'release-0.2.1'
    print(run('unshare', '--net', ROOT / 'bin/auriga-cli', 'release', '--tag', '0.2.1',
              '--release', index, '--outdir', next_release, '--cache-dir', cache,
              '--sign-key', private, '--attachment-limit', 32 << 20, '--build',
              '--previous-devkit', kit, cwd=source))
    next_manifest = json.loads((next_release / 'release-manifest.json').read_text())
    assert next_manifest['offlineBuildGate']['passed']
    assert next_manifest['locks']['digest'] == first_manifest['locks']['digest']
    assert next_manifest['devkit']['asset']['sha256'] == first_manifest['devkit']['asset']['sha256']
    assert next_manifest['devkit']['sourceTag'] == '0.2.0'
    assert len(list(cache.glob('*/auriga-devkit-*.tar'))) == 1
    assert set(next_manifest['supportedVersions']) == {'0.2.0', '0.2.1'}
    delta = next_release / 'auriga-devkit-0.2.1-delta.tar'
    assert delta.stat().st_size < (1 << 20)
    print(fieldrun(original / 'auriga', 'devkit', 'merge', delta, '--kit', kit, cwd=original))
    print(fieldrun(original / 'auriga', 'devkit', 'verify', kit, '--version', '0.2.0', cwd=original))
    print(fieldrun(original / 'auriga', 'install', '--package',
                   next_release / 'auriga-offline-0.2.1.tar.part001',
                   '--proxy', directory / 'access.json', '--data', directory / 'multipart-site', '--verify-key', public))
    checkout = directory / 'new-source'
    print(fieldrun(original / 'auriga', 'unpack', next_release / 'auriga-source-0.2.1.tar', '--out', checkout, '--verify-key', public))
    print(fieldrun(checkout / 'auriga', 'changes', 'apply', patch, cwd=checkout))
    assert (checkout / 'local-feature.txt').read_text() == 'local site addition\n'
    print(fieldrun(checkout / 'auriga', 'devkit', 'verify', kit, cwd=checkout))
    site_keys = directory / 'site-build-key'
    print(fieldrun(checkout / 'auriga', 'keygen', 'site', '--out', site_keys))
    site_private, site_public = site_keys / 'site-private.pem', site_keys / 'site-public.pem'
    output = directory / 'site.tar'
    print(fieldrun(checkout / 'auriga', 'bundle', '--build', '--offline', '--version', '0.2.1-site.3', '--out', output, '--sign-key', site_private, cwd=checkout))
    with tarfile.open(output) as archive:
        provenance = json.load(archive.extractfile('provenance.json'))
    assert provenance['version'] == '0.2.1-site.3' and provenance['dirty'] and provenance['siteChanges']
    assert provenance['branch'] == 'site/archive' and provenance['upstreamVersion'] == '0.2.1'
    field = directory / 'new-field'
    field.mkdir()
    with tarfile.open(output) as archive:
        archive.extractall(field, filter='data')
    site = directory / 'multipart-site'
    untrusted = subprocess.run(['unshare', '--net', original / 'auriga', 'upgrade', output, '--data', site], capture_output=True, text=True)
    assert untrusted.returncode != 0 and 'state固定' in untrusted.stderr
    der = subprocess.check_output(['openssl', 'pkey', '-pubin', '-in', str(site_public), '-outform', 'DER'])
    import hashlib
    fingerprint = hashlib.sha256(der).hexdigest()
    print(fieldrun(original / 'auriga', 'trust', 'add-site-key', site_public, '--confirm-fingerprint', fingerprint, '--data', site))
    print(fieldrun(original / 'auriga', 'upgrade', output, '--data', site))
    result = subprocess.run(['unshare', '--net', field / 'auriga', 'doctor', '--data', site], env=field_env, capture_output=True, text=True)
    assert result.returncode == 1 and '0.2.1-site.3' in result.stdout and provenance['commit'] in result.stdout
    print(result.stdout)
    lock = checkout / 'deploy/offline/go.mod'
    lock_bytes = lock.read_bytes()
    lock.write_bytes(lock_bytes + b'\n// changed dependencies\n')
    failure = subprocess.run([checkout / 'auriga', 'devkit', 'verify'], cwd=checkout, env=field_env, capture_output=True, text=True)
    assert failure.returncode != 0 and 'deploy/offline/go.mod' in failure.stderr and 'auriga-devkit-0.2.1.tar' in failure.stderr
    lock.write_bytes(lock_bytes)
    catalog = json.loads((kit / 'devkit-catalog.json').read_text())
    dependency = kit / 'blobs' / catalog['versions']['0.2.1']['files']['inputs.tar']
    saved = dependency.with_suffix('.saved')
    dependency.rename(saved)
    failure = subprocess.run(['unshare', '--net', checkout / 'auriga', 'bundle', '--build', '--offline', '--sign-key', site_private], cwd=checkout, env=field_env, capture_output=True, text=True)
    assert failure.returncode != 0 and 'inputs.tar' in failure.stderr
    saved.rename(dependency)
    with open(dependency, 'r+b') as stream:
        stream.write(b'tampered')
    failure = subprocess.run(['unshare', '--net', checkout / 'auriga', 'bundle', '--build', '--offline', '--sign-key', site_private], cwd=checkout, env=field_env, capture_output=True, text=True)
    assert failure.returncode != 0 and 'inputs.tar' in failure.stderr
    assert not marker.exists(), 'field command invoked Git'
    full_size = next(a['size'] for a in next_manifest['archives'] if a['name'] == 'auriga-devkit-0.2.1.tar')
    source_size = next(a['size'] for a in next_manifest['archives'] if a['name'] == 'auriga-source-0.2.1.tar')
    print(f'PASS gitless_source_changes_cumulative_delta_offline_gate_build_install full_bytes={full_size} delta_bytes={delta.stat().st_size} source_bytes={source_size} bundle_bytes={output.stat().st_size}')



def main():
    if os.geteuid() != 0:
        raise SystemExit('requires local root Docker access')
    for name in transport.SERVICES:
        if subprocess.run(['docker', 'image', 'inspect', f'auriga/{name}:nas'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise SystemExit('fixture transport tags already in use')
    had_network = subprocess.run(['docker', 'network', 'inspect', 'auriga-offline-lan'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    before = set(run('docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}').splitlines())
    try:
        with tempfile.TemporaryDirectory(prefix='auriga-devkit-fixture-', dir='/var/tmp') as name:
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
