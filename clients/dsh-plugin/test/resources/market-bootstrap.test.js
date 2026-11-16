import { test } from 'node:test';
import { generateKeyPairSync } from 'node:crypto';
import { Supervisor } from '../../../installer/runtime/supervisor.mjs';
import { catalogIdentity } from '../../src/resources/catalog-api.js';
import assert from 'node:assert/strict';
import {
  rmSync,
  existsSync,
  readFileSync,
  mkdirSync,
  writeFileSync,
} from 'node:fs';
import { join } from 'node:path';
import { catalogFixture } from './catalog-fixture.js';
import { catalogService } from './broker-fixture.js';
import { createPrivateSkills } from '../../src/resources/private-skills.js';
import { readResourceRoots } from '../../src/resources/trust.js';
import {
  diagnoseResource,
  resourceStage,
} from '../../src/resources/diagnostics.js';

test('first signed skill installation can prepare before guardian state exists; only guardian can activate it', async (t) => {
  const f = catalogFixture(t, undefined, {
    releaseKid: 'auriga-dev-b1-release',
  });
  rmSync(join(f.home, 'auriga/guardian/state.json'));
  const service = catalogService({
    ...f,
    privateSkills: createPrivateSkills({ home: f.home }),
    host: {},
  });
  const accepted = await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'first-desktop-install',
  });
  assert.equal(accepted.accepted, true);
  assert.equal(accepted.installation.state, 'applying');
  assert.equal(existsSync(f.active), false);
  assert.equal(existsSync(join(f.home, 'auriga/guardian/state.json')), false);
  await service.list();
  assert.equal(existsSync(f.active), false);
  f.allow();
  await service.list();
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
  assert.equal(f.getRow().state, 'pending_restart');
});

test('packaged launcher trust format loads auriga-dev-b1-release without an unconfigured environment override', async (t) => {
  const f = catalogFixture(t, undefined, {
    releaseKid: 'auriga-dev-b1-release',
  });
  const roots = await f.roots();
  const runtime = join(f.home, 'version/auriga/runtime');
  mkdirSync(runtime, { recursive: true });
  const entry = (kid, key) => ({
    kid,
    publicKeySpkiBase64: key
      .export({ format: 'der', type: 'spki' })
      .toString('base64'),
  });
  writeFileSync(
    join(runtime, 'resource-roots.json'),
    JSON.stringify({
      release: entry(
        'auriga-dev-b1-release',
        roots.releaseKeys.get('auriga-dev-b1-release'),
      ),
      revocation: entry('revoke-test', roots.revocationKeys.get('revoke-test')),
      downgrade: entry(
        'downgrade-test',
        generateKeyPairSync('ed25519').publicKey,
      ),
    }),
  );
  const previous = process.env.AURIGA_DESKTOP_RUNTIME;
  process.env.AURIGA_DESKTOP_RUNTIME = join(runtime, 'update.mjs');
  t.after(() => {
    if (previous === undefined) delete process.env.AURIGA_DESKTOP_RUNTIME;
    else process.env.AURIGA_DESKTOP_RUNTIME = previous;
  });
  const actual = await readResourceRoots(undefined);
  assert.ok(actual.releaseKeys.has('auriga-dev-b1-release'));
  // An explicit deployment override is authoritative and must not silently fall back.
  await assert.rejects(readResourceRoots(join(f.home, 'missing.json')), {
    code: 'resource.storage_unavailable',
  });
  const service = catalogService({
    ...f,
    roots: undefined,
    privateSkills: createPrivateSkills({ home: f.home }),
    host: {},
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'packaged-trust',
  });
  f.allow();
  await service.list();
  assert.ok(existsSync(join(f.active, 'example/SKILL.md')));
});

test('diagnostics retain finite reason, stage and trace without exception content', async () => {
  const events = [];
  const failure = diagnoseResource(
    new Error('password=secret, private resource body'),
    'catalog',
    (...args) => events.push(args),
  );
  assert.equal(failure.status, 503);
  assert.equal(failure.body.reason, 'internal_error');
  assert.match(failure.body.trace_id, /^[a-f0-9]{32}$/);
  assert.equal(events[0][2].trace_id, failure.body.trace_id);
  assert.doesNotMatch(
    JSON.stringify([events, failure]),
    /password|secret|private resource/,
  );
  try {
    await resourceStage('verification', async () => {
      throw new Error('manifest_binding');
    });
  } catch (error) {
    assert.equal(
      diagnoseResource(error, 'catalog', () => {}).body.reason,
      'signature_invalid',
    );
  }
  const notFound = diagnoseResource(
    { code: 'common.not_found' },
    'catalog',
    () => {},
  );
  assert.equal(notFound.status, 404);
  await assert.rejects(
    resourceStage('verification', async () => {
      throw null;
    }),
    { code: 'resource.signature_invalid', stage: 'verification' },
  );
});

test('managed catalog clientId is the verified did and identity disagreement fails closed', () => {
  const r = {
    tenantId: '019a0000-0000-7000-8000-000000000001',
    ownerId: '019a0000-0000-7000-8000-000000000002',
    instanceId: '019a0000-0000-7000-8000-000000000003',
  };
  const clientId = '019a0000-0000-7000-8000-000000000004';
  assert.equal(
    catalogIdentity({
      resourceIdentity: r,
      identity: () => ({ ...r, clientId }),
    }).hostId,
    clientId,
  );
  assert.throws(() => catalogIdentity({ resourceIdentity: r }), {
    code: 'common.unauthenticated',
  });
  assert.throws(
    () =>
      catalogIdentity({
        resourceIdentity: r,
        identity: () => ({ ...r, ownerId: clientId, clientId }),
      }),
    { code: 'common.unauthenticated' },
  );
});
test('independent installer supervisor authorizes the first prepared signed ledger before C activates it', async (t) => {
  const f = catalogFixture(t, undefined, {
    releaseKid: 'auriga-dev-b1-release',
  });
  rmSync(join(f.home, 'auriga/guardian/state.json'));
  const previous = process.env.AURIGA_DESKTOP_RUNTIME;
  process.env.AURIGA_DESKTOP_RUNTIME = join(
    f.home,
    'auriga/runtime/update.mjs',
  );
  t.after(() => {
    if (previous === undefined) delete process.env.AURIGA_DESKTOP_RUNTIME;
    else process.env.AURIGA_DESKTOP_RUNTIME = previous;
  });
  const marker = join(f.home, 'auriga/guardian/supervisor.lock.d');
  mkdirSync(marker, { recursive: true });
  writeFileSync(join(marker, 'owner.json'), '{}');
  const roots = await f.roots();
  const entries = (map) =>
    [...map].map(([kid, key]) => ({
      kid,
      publicKeySpkiBase64: key
        .export({ format: 'der', type: 'spki' })
        .toString('base64'),
    }));
  const tree = {
    record: { launcher: null, root: null, children: [] },
    isStopped: async () => true,
    stopTree: async () => {},
  };
  const supervisor = new Supervisor(f.home, tree, {
    release: entries(roots.releaseKeys)[0],
    revocation: entries(roots.revocationKeys)[0],
  });
  await supervisor.prepare();
  assert.equal(supervisor.guardian.read(), null);
  const service = catalogService({
    ...f,
    desktop: undefined,
    privateSkills: createPrivateSkills({ home: f.home }),
    host: {},
  });
  await service.apply({
    packageId: f.packageId,
    versionId: f.versionId,
    action: 'install',
    revision: 0,
    key: 'real-supervisor-first-install',
  });
  assert.equal(existsSync(f.active), false);
  await supervisor.reconcile();
  assert.equal(
    supervisor.guardian.read()?.guardian.state,
    'active',
    supervisor.privateReason,
  );
  assert.equal(supervisor.marks().entries[0].releaseSequence, 7);
  await service.list();
  assert.match(
    readFileSync(join(f.active, 'example/SKILL.md'), 'utf8'),
    /Verified body/,
  );
});
