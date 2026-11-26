import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomBytes } from 'node:crypto';
import { linuxSeal, linuxUnseal, Vault } from '../vault.mjs';

test('linux seal round-trips and rejects tampering', () => {
  const key = randomBytes(32);
  const sealed = linuxSeal(key, Buffer.from('{"ok":true}'));
  assert.equal(linuxUnseal(key, sealed).toString('utf8'), '{"ok":true}');
  const bad = Buffer.from(sealed);
  bad[bad.length - 1] ^= 0xff;
  assert.throws(() => linuxUnseal(key, bad));
});

test(
  'linux vault persists credentials under 0600 key and sealed file',
  { skip: process.platform !== 'linux' },
  async () => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-linux-vault-'));
    try {
      const vault = new Vault(join(directory, 'vault'), null);
      await vault.save({ token: 'x', refresh_token: 'y' });
      const loaded = await vault.load();
      assert.equal(loaded.token, 'x');
      assert.equal(loaded.refresh_token, 'y');
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  },
);
