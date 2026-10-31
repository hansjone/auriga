import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const script = join(root, '.mvn/wrapper/bootstrap.sh');
const bash = process.platform === 'win32'
  ? join(process.env.ProgramFiles, 'Git/bin/bash.exe') : 'bash';

for (const [failures, expectedCalls, status] of [[0, 1, 0], [2, 3, 0], [3, 3, 1]]) {
  test(`Maven bootstrap: ${failures} initial failures`, () => {
    const dir = mkdtempSync(join(root, '.github/scripts/.bootstrap-test-'));
    try {
      mkdirSync(join(dir, 'bin'));
      writeFileSync(join(dir, 'bin/sleep'), '#!/bin/sh\nexit 0\n', { mode: 0o755 });
      writeFileSync(join(dir, 'mvnw'), `#!/bin/sh
test "$*" = '-B --version' || exit 99
n=0; test ! -f calls || n=$(cat calls)
n=$((n + 1)); echo "$n" > calls
test "$n" -gt ${failures}
`, { mode: 0o755 });
      const result = spawnSync(bash, ['-c', 'PATH="$PWD/bin:$PATH"; export PATH; sh "$1"', 'test', script.replaceAll('\\', '/')], {
        cwd: dir, encoding: 'utf8',
      });
      assert.equal(result.status, status, result.stderr + result.stdout);
      assert.equal(Number(readFileSync(join(dir, 'calls'), 'utf8')), expectedCalls);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
}
