import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const script = join(root, '.mvn/wrapper/bootstrap.sh');
const bash = process.platform === 'win32'
  ? join(process.env.ProgramFiles, 'Git/bin/bash.exe') : 'bash';

function runBootstrap(failures, distribution) {
  const dir = mkdtempSync(join(root, '.github/scripts/.bootstrap-test-'));
  mkdirSync(join(dir, 'bin'));
  mkdirSync(join(dir, '.mvn/wrapper'), { recursive: true });
  writeFileSync(join(dir, 'bin/sleep'), '#!/bin/sh\nexit 0\n', { mode: 0o755 });
  writeFileSync(join(dir, '.mvn/wrapper/maven-wrapper.properties'),
    `distributionUrl=https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.16/${distribution}.zip\n`);
  // A truncated download left by a previous attempt.
  const stale = join(dir, 'm2/wrapper/dists', distribution, 'hash', `${distribution}.zip`);
  mkdirSync(dirname(stale), { recursive: true });
  writeFileSync(stale, 'truncated');
  writeFileSync(join(dir, 'mvnw'), `#!/bin/sh
test "$*" = '-B --version' || exit 99
n=0; test ! -f calls || n=$(cat calls)
n=$((n + 1)); echo "$n" > calls
test "$n" -gt ${failures}
`, { mode: 0o755 });
  const result = spawnSync(bash, ['-c', 'PATH="$PWD/bin:$PATH"; MAVEN_USER_HOME="$PWD/m2"; export PATH MAVEN_USER_HOME; sh "$1"', 'test', script.replaceAll('\\', '/')], {
    cwd: dir, encoding: 'utf8',
  });
  return { dir, result, stale };
}

for (const [failures, expectedCalls, status] of [[0, 1, 0], [2, 3, 0], [3, 3, 1]]) {
  test(`Maven bootstrap: ${failures} initial failures`, () => {
    const { dir, result, stale } = runBootstrap(failures, 'apache-maven-3.9.16-bin');
    try {
      assert.equal(result.status, status, result.stderr + result.stdout);
      assert.equal(Number(readFileSync(join(dir, 'calls'), 'utf8')), expectedCalls);
      // A retry must not reuse a truncated distribution.
      assert.equal(existsSync(stale), failures === 0);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
}

test('Maven bootstrap keeps distributions outside apache-maven-*', () => {
  const { dir, result, stale } = runBootstrap(1, 'other-dist');
  try {
    assert.equal(result.status, 0, result.stderr + result.stdout);
    assert.equal(existsSync(stale), true);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
