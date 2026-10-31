import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createPublicKey, createHash, verify } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { Buffer } from 'node:buffer';
import { fileURLToPath } from 'node:url';

const cli = new URL('./generate-test-approval.mjs', import.meta.url);
const canonical = (value) => JSON.stringify(value, Object.keys(value).sort());
test('explicit test approval produces independent signed keys, matching state and no root in installation input', async (t) => {
  const parent = await mkdtemp(join(tmpdir(), 'collab-approval-'));
  t.after(() => rm(parent, { recursive: true, force: true }));
  const cfg = join(parent, 'request.json');
  const output = join(parent, 'approval');
  await writeFile(
    cfg,
    JSON.stringify({
      tenantId: '00000000-0000-0000-0000-000000000001',
      realm: '19',
      approval:
        'Authorized disposable test root, never a production release root.',
    }),
  );
  const run = (...args) =>
    spawnSync(process.execPath, [fileURLToPath(cli), ...args], {
      encoding: 'utf8',
    });
  assert.notEqual(run(cfg, output).status, 0);
  const result = run('--test-only', cfg, output);
  assert.equal(result.status, 0, result.stderr);
  const json = async (name) =>
    JSON.parse(await readFile(join(output, name), 'utf8'));
  const input = await json('input.json');
  const signed = await json('signed.json');
  const state = await json('state.json');
  const announcement = await json('announcement.json');
  const receipt = await json('approval.json');
  const root = await readFile(join(output, input.rootPublicFile));
  const rootKey = createPublicKey({ key: root, format: 'der', type: 'spki' });
  assert.equal(input.realm, '19');
  assert.match(input.rootKid, /^test-only-/);
  assert.equal(
    input.rootFingerprint,
    'sha256:' + createHash('sha256').update(root).digest('hex'),
  );
  const stable = (v) =>
    JSON.stringify(v, (_, x) =>
      x && typeof x === 'object' && !Array.isArray(x)
        ? Object.fromEntries(
            Object.entries(x).sort(([a], [b]) => a.localeCompare(b, 'en')),
          )
        : x,
    );
  assert.equal(
    verify(
      null,
      Buffer.concat([
        Buffer.from('Auriga.NetworkKeyset.v1\0'),
        Buffer.from(stable(signed.keyset)),
      ]),
      rootKey,
      Buffer.from(signed.root_auth.signature, 'base64url'),
    ),
    true,
  );
  const { root_signature, ...unsigned } = announcement;
  assert.equal(
    verify(
      null,
      Buffer.concat([
        Buffer.from('Auriga.NetworkKeysetAnnouncement.v1\0'),
        Buffer.from(canonical(unsigned)),
      ]),
      rootKey,
      Buffer.from(root_signature, 'base64url'),
    ),
    true,
  );
  assert.equal(
    announcement.manifest_digest,
    'sha256:' +
      createHash('sha256').update(stable(signed.keyset)).digest('hex'),
  );
  assert.equal(state.version, input.minimumVersion);
  assert.equal(state.keys.length, 3);
  assert.equal(
    new Set(signed.keyset.keys.map((x) => x.public_key_spki)).size,
    3,
  );
  assert.ok(
    signed.keyset.keys.every(
      (x) => x.public_key_spki !== root.toString('base64url'),
    ),
  );
  for (let i = 0; i < state.keys.length; i++) {
    const { alg, ...key } = signed.keyset.keys[i];
    assert.equal(alg, 'Ed25519');
    assert.deepEqual(state.keys[i], { ...key, realm: '19', state: 'ACTIVE' });
  }
  assert.equal(
    Object.values(input.privateFiles).includes('offline-root-private.der'),
    false,
  );
  assert.equal(receipt.testOnly, true);
  assert.match(receipt.approval, /disposable/);
  assert.equal(
    receipt.inputSha256,
    createHash('sha256')
      .update(await readFile(join(output, 'input.json')))
      .digest('hex'),
  );
  assert.notEqual(run('--test-only', cfg, output).status, 0);
  await writeFile(
    cfg,
    JSON.stringify({
      tenantId: input.tenantId,
      realm: '0',
      approval: receipt.approval,
    }),
  );
  assert.notEqual(run('--test-only', cfg, join(parent, 'bad')).status, 0);
});
