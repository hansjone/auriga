import test from 'node:test';
import assert from 'node:assert/strict';
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
  cpSync,
  statSync,
  utimesSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { validateUacConfigDir } from '../scripts/build.mjs';
import { pathToFileURL } from 'node:url';
import { install, profileDir, readState } from '../runtime/core.mjs';
import { computeId } from '../runtime/verify.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

async function launchFixture(t, bundled = true) {
  const base = tempBase(t);
  const { dir: payload } = buildPayload(base);
  const contents = {
    'adapter.json': '{"cookies":{"domain":"portal.example.com"}}\n',
    'provider.json':
      '{"baseUrl":"https://identity.example.com","systemCode":"EXAMPLE"}\n',
  };
  if (bundled) {
    const manifestPath = join(payload, 'manifest.json');
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
    const { createHash } = process.getBuiltinModule('node:crypto');
    mkdirSync(join(payload, 'auriga/uac'), { recursive: true });
    for (const [name, content] of Object.entries(contents)) {
      writeFileSync(join(payload, 'auriga/uac', name), content);
      manifest.files.push({
        path: `auriga/uac/${name}`,
        size: Buffer.byteLength(content),
        sha256: createHash('sha256').update(content).digest('hex'),
      });
    }
    manifest.id = computeId(manifest);
    writeFileSync(manifestPath, JSON.stringify(manifest));
  }
  const root = join(base, 'install'),
    dataHome = join(base, 'data');
  install({ payload, root, dataHome, smoke: () => {} });
  // Run the real launcher and package verification with OS/DSH boundaries stubbed.
  // This fixture never starts DSH or reads any real browser/credential store.
  const runtime = join(base, 'runtime');
  cpSync(new URL('../runtime/', import.meta.url), runtime, { recursive: true });
  writeFileSync(
    join(runtime, 'process-tree.mjs'),
    `export class ProcessTree { record = {children:[]}; singleton(){return ()=>{};} async stopTree(){} }
    export function assertNTFS(){} export function processIdentity(){return null;} export function sameProcess(){return false;}
    export function notifyPrivateOnly(){} export function notifySharedRestart(){}`,
  );
  writeFileSync(
    join(runtime, 'supervisor.mjs'),
    `export class Supervisor { guardian = {read:()=>null}; async prepare(){} async reconcile(){} } export function packagedTrust(){return {};}`,
  );
  writeFileSync(
    join(runtime, 'desktop-supervision.mjs'),
    'export async function superviseDsh(){return 0;}',
  );
  writeFileSync(
    join(runtime, 'resource-plugins.mjs'),
    'export function resourcePluginControl(){return {};}',
  );
  writeFileSync(
    join(runtime, 'update.mjs'),
    'export async function applyPending(){return false;}',
  );
  const { launch } = await import(pathToFileURL(join(runtime, 'core.mjs')));
  return {
    root,
    dataHome,
    contents,
    launch: () => launch({ root }),
    destination: join(profileDir(dataHome), 'auriga-connect/uac'),
  };
}

test('launch repairs stale bundled UAC configuration idempotently and preserves the identity vault', async (t) => {
  const f = await launchFixture(t);
  const vault = join(profileDir(f.dataHome), 'auriga-connect/uac-local.json');
  writeFileSync(vault, 'synthetic-owner-vault');
  for (const name of Object.keys(f.contents))
    writeFileSync(join(f.destination, name), 'stale');
  assert.equal(await f.launch(), 0);
  for (const [name, content] of Object.entries(f.contents)) {
    const file = join(f.destination, name);
    assert.equal(readFileSync(file, 'utf8'), content);
    utimesSync(
      file,
      new Date('2000-01-01T00:00:00Z'),
      new Date('2000-01-01T00:00:00Z'),
    );
  }
  const before = Object.keys(f.contents).map(
    (name) => statSync(join(f.destination, name)).mtimeMs,
  );
  assert.equal(await f.launch(), 0);
  assert.deepEqual(
    Object.keys(f.contents).map(
      (name) => statSync(join(f.destination, name)).mtimeMs,
    ),
    before,
  );
  assert.equal(readFileSync(vault, 'utf8'), 'synthetic-owner-vault');
});

test('launch never syncs a tampered package and preflights both destination files', async (t) => {
  const f = await launchFixture(t);
  const state = readState(f.root);
  const source = join(
    f.root,
    'versions',
    state.current,
    'auriga/uac/provider.json',
  );
  writeFileSync(source, 'tampered');
  writeFileSync(join(f.destination, 'adapter.json'), 'local-before');
  await assert.rejects(f.launch());
  assert.equal(
    readFileSync(join(f.destination, 'adapter.json'), 'utf8'),
    'local-before',
  );
  writeFileSync(source, f.contents['provider.json']);
  rmSync(join(f.destination, 'provider.json'));
  mkdirSync(join(f.destination, 'provider.json'));
  await assert.rejects(f.launch(), /non-regular UAC file/);
  assert.equal(
    readFileSync(join(f.destination, 'adapter.json'), 'utf8'),
    'local-before',
  );
});

test('launch without bundled UAC configuration leaves site configuration intact', async (t) => {
  const f = await launchFixture(t, false);
  mkdirSync(f.destination, { recursive: true });
  writeFileSync(join(f.destination, 'adapter.json'), 'local-only');
  assert.equal(await f.launch(), 0);
  assert.equal(
    readFileSync(join(f.destination, 'adapter.json'), 'utf8'),
    'local-only',
  );
});

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
