import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  realpathSync,
  symlinkSync,
  rmdirSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { catalogFixture } from './catalog-fixture.js';
import { assertLocalPath } from '../../src/resources/host-lock.js';

test('catalog fixture resolves a temporary-root alias without relaxing production path admission', async (t) => {
  const root = realpathSync.native(
    mkdtempSync(join(tmpdir(), 'auriga-fixture-path-')),
  );
  const target = join(root, 'physical'),
    alias = join(root, 'temporary-alias');
  mkdirSync(target);
  symlinkSync(target, alias, process.platform === 'win32' ? 'junction' : 'dir');
  try {
    await t.test(
      'physical fixture home can enter Desktop storage while raw aliases remain rejected',
      (child) => {
        const f = catalogFixture(child, alias);
        assert.equal(f.home, realpathSync.native(f.home));
        assert.equal(join(f.home, '..'), target);
        assert.doesNotThrow(() => assertLocalPath(f.home));
        assert.throws(() => assertLocalPath(alias), /link|alias/);
      },
    );
  } finally {
    // Unlink the alias itself before recursively removing this test's physical tree.
    if (process.platform === 'win32') rmdirSync(alias);
    else {
      const { unlinkSync } = await import('node:fs');
      unlinkSync(alias);
    }
    rmSync(root, { recursive: true, force: true });
  }
});
