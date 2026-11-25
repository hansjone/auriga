import test from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { validateUacConfigDir } from '../scripts/build.mjs';
import { install, profileDir } from '../runtime/core.mjs';
import { computeId } from '../runtime/verify.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

test('validateUacConfigDir validates adapter.json + provider.json pair and rejects incomplete/invalid inputs', () => {
  const dir = mkdtempSync(join(tmpdir(), 'auriga-uac-validate-'));
  try {
    const adapter = JSON.stringify({
      tokenVerifyUrl: 'https://uac.example.com/verify',
      cookies: { domain: 'aip.example.com' },
    });
    const provider = JSON.stringify({
      baseUrl: 'https://uac.example.com',
      systemCode: 'SYS100',
    });
    writeFileSync(join(dir, 'adapter.json'), adapter);
    assert.throws(() => validateUacConfigDir(dir));

    writeFileSync(join(dir, 'provider.json'), JSON.stringify({ baseUrl: '' }));
    assert.throws(
      () => validateUacConfigDir(dir),
      /invalid UAC provider\.json/,
    );

    writeFileSync(join(dir, 'provider.json'), provider);
    const result = validateUacConfigDir(dir);
    assert.equal(result.adapterBytes.toString('utf8'), adapter);
    assert.equal(result.providerBytes.toString('utf8'), provider);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('install syncs bundled auriga/uac config into profile auriga-connect/uac and preserves unbundled installs', (t) => {
  const base = tempBase(t);
  const { dir: payload } = buildPayload(base, { name: 'with-uac' });
  const uacSrc = join(payload, 'auriga', 'uac');
  mkdirSync(uacSrc, { recursive: true });
  const adapterText = '{"cookies":{"domain":"aip.example.com"}}\n';
  const providerText =
    '{"baseUrl":"https://uac.example.com","systemCode":"SYS1"}\n';
  writeFileSync(join(uacSrc, 'adapter.json'), adapterText);
  writeFileSync(join(uacSrc, 'provider.json'), providerText);

  // Update manifest.json to include auriga/uac/* and recompute manifest.id
  const manifestPath = join(payload, 'manifest.json');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  const { createHash } = process.getBuiltinModule('node:crypto');
  for (const [name, content] of [
    ['adapter.json', adapterText],
    ['provider.json', providerText],
  ]) {
    manifest.files.push({
      path: `auriga/uac/${name}`,
      size: Buffer.byteLength(content),
      sha256: createHash('sha256').update(content).digest('hex'),
    });
  }
  manifest.id = computeId(manifest);
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + '\n');

  const root = join(base, 'install');
  const dataHome = join(base, 'data');
  install({ payload, root, dataHome, smoke: () => {} });

  const targetUac = join(profileDir(dataHome), 'auriga-connect', 'uac');
  assert.equal(existsSync(join(targetUac, 'adapter.json')), true);
  assert.equal(existsSync(join(targetUac, 'provider.json')), true);
  assert.equal(
    readFileSync(join(targetUac, 'adapter.json'), 'utf8'),
    adapterText,
  );
  assert.equal(
    readFileSync(join(targetUac, 'provider.json'), 'utf8'),
    providerText,
  );
});
