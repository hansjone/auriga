import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  isolatedEnvironment,
  assertRuntimeLock,
} from './support/native-install.mjs';
import {
  nativeIdentityBypass,
  assertNativeVerdict,
  staleObjectMarker,
} from './support/native-overlay.mjs';

test('native runtime locks rc.3 integrity and uses a private environment without model keys', async () => {
  const lock = JSON.parse(
    await readFile(
      new URL('./support/dsh-runtime/package-lock.json', import.meta.url),
      'utf8',
    ),
  );
  assertRuntimeLock(lock);
  const changed = structuredClone(lock);
  changed.packages['node_modules/@deepseek-ai/dsh'].integrity =
    'sha512-invalid';
  assert.throws(() => assertRuntimeLock(changed));
  const env = isolatedEnvironment('/private-case', {
    PATH: 'path',
    DSH_HOME: '/user-dsh',
    HOME: '/user',
    NODE_OPTIONS: '--import=untrusted',
    DEEPSEEK_API_KEY: 'not-a-real-key',
  });
  assert.equal(env.PATH, 'path');
  assert.notEqual(env.DSH_HOME, '/user-dsh');
  assert.equal(env.NODE_OPTIONS, undefined);
  assert.equal(env.DEEPSEEK_API_KEY, undefined);
});

test('native identity overlay changes exact production checks and rejects generic failure verdicts', async () => {
  const source = await readFile(
    new URL(
      '../../clients/dsh-plugin/src/network/host/native/source-context.js',
      import.meta.url,
    ),
    'utf8',
  );
  assert.notEqual(nativeIdentityBypass(source), source);
  assert.throws(() => nativeIdentityBypass(''));
  const result = {
    code: 1,
    stdout: `# pass 1\n# fail 1\n# cancelled 0\n# skipped 0\n${staleObjectMarker}\ncode: 'ERR_ASSERTION'`,
    stderr: '',
  };
  assertNativeVerdict(result, 'NativeIdentityBypass');
  assert.throws(() =>
    assertNativeVerdict(
      { ...result, stdout: 'initialization failed' },
      'NativeIdentityBypass',
    ),
  );
  assert.throws(() =>
    assertNativeVerdict({ ...result, code: 0 }, 'NativeIdentityBypass'),
  );
  assert.throws(() =>
    assertNativeVerdict(
      { ...result, stderr: 'native teardown failed' },
      'NativeIdentityBypass',
    ),
  );
});
