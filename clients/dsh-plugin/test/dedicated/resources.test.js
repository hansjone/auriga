import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { catalogFixture } from '../resources/catalog-fixture.js';
import { unpackSkill } from '../../src/resources/skill-package.js';
import {
  normalizeView,
  readIntent,
  loadResources,
  sha,
} from '../../src/dedicated/intent.js';
import { createDedicatedResources } from '../../src/dedicated/resources.js';
import { test, load } from './public.js';

test('actual Node wire and protojson fixtures agree through dedicated typed normalization', () => {
  const wire = JSON.parse(
    readFileSync(new URL('./node-wire.json', import.meta.url), 'utf8'),
  );
  const protojson = JSON.parse(
    readFileSync(new URL('./node-protojson.json', import.meta.url), 'utf8'),
  );
  assert.equal(typeof protojson.binding.epoch, 'string');
  assert.equal(protojson.binding.mode, 'DSH_INSTANCE_MODE_DEDICATED');
  assert.equal(typeof wire.binding.epoch, 'number');
  assert.equal(wire.binding.mode, 'dedicated');
  assert.deepEqual(normalizeView(protojson), normalizeView(wire));
});

async function setup(t, kind = 'skill', restart = false) {
  const f = catalogFixture(t, undefined, { kind });
  const [{ Context }, { SkillRegistry }] = await Promise.all([
    load('cordis'),
    load('dsh-skill'),
  ]);
  const ctx = new Context();
  new SkillRegistry(ctx);
  const binding = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    placementId: randomUUID(),
    physicalInstanceId: randomUUID(),
    mode: 'dedicated',
    epoch: 1,
    revision: 1,
  };
  const artifactRoot = join(f.home, 'node-cache'),
    intentFile = join(f.home, 'intent.json'),
    stateFile = join(f.home, 'state.json');
  const manifestDigest = sha(f.raw),
    artifactDigest = sha(f.archive),
    resourceId = randomUUID();
  const manifestRoot = join(artifactRoot, manifestDigest.slice(7)),
    payloadRoot = join(artifactRoot, artifactDigest.slice(7));
  mkdirSync(manifestRoot, { recursive: true });
  mkdirSync(payloadRoot, { recursive: true });
  writeFileSync(join(manifestRoot, 'manifest.json'), f.raw);
  writeFileSync(join(payloadRoot, 'payload.tgz'), f.archive);
  if (kind === 'skill')
    for (const [file, bytes] of Object.entries(
      unpackSkill(f.archive, {
        name: f.manifest.name,
        description: f.manifest.description,
      }),
    )) {
      const target = join(payloadRoot, 'skill', file);
      mkdirSync(join(target, '..'), { recursive: true });
      writeFileSync(target, bytes);
    }
  let view = {
    schemaVersion: 1,
    binding,
    viewId: randomUUID(),
    authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
    source: 'platform',
    resources: [
      {
        resourceId,
        digest: manifestDigest,
        leaseId: randomUUID(),
        expiresAt: 2000,
      },
    ],
  };
  let metadata = [
    {
      resourceId,
      manifestDigest,
      artifactDigest,
      artifactBytes: f.archive.length,
      installationRevision: 1,
    },
  ];
  const write = () =>
    writeFileSync(
      intentFile,
      JSON.stringify({ schemaVersion: 1, view, resources: metadata }),
    );
  write();
  // Only the external package process is substituted. Public SkillRegistry,
  // intent/archive validation, state recovery and manager outcome semantics run.
  const bundles = new Map(),
    operations = [];
  const result = (target) => ({
    target,
    application: restart ? 'restart-required' : 'applied',
  });
  const manager = {
    listBundles: async () => [...bundles.values()],
    listPlugins: async () =>
      [...bundles.values()]
        .filter((b) => b.installed)
        .map((b) => ({
          entryId: b.rows[0].entryId,
          enabled: b.enabled,
          fiberPhase: b.active === false ? 'failed' : 'active',
        })),
    installBundle: async (spec, options) => {
      operations.push(['install', spec, options]);
      const name = f.manifest.plugin.packageName;
      bundles.set(name, {
        name,
        version: f.manifest.version,
        enabled: false,
        installed: true,
        availability: 'profile',
        rows: [{ entryId: 'approved-entry' }],
      });
      return result(name);
    },
    setBundleEnabled: async (name, enabled) => {
      operations.push(['enable', name, enabled]);
      bundles.get(name).enabled = enabled;
      return result(name);
    },
    removeBundle: async (name) => {
      operations.push(['remove', name]);
      bundles.get(name).installed = false;
      return result(name);
    },
  };
  ctx.provide('pluginManager', manager);
  let current = () => view,
    idle = true;
  const compose = (instanceId) =>
    createDedicatedResources(ctx, {
      binding,
      intentFile,
      artifactRoot,
      stateFile,
      currentView: async () => current(),
      quiescent: async () => idle,
      now: () => 1000,
      instanceId,
    });
  let resources = compose('fixture-process-1');
  t.after(async () => {
    await resources.close();
    await ctx.fiber.dispose();
  });
  return {
    ...f,
    ctx,
    binding,
    resources,
    artifactRoot,
    intentFile,
    stateFile,
    bundles,
    operations,
    write,
    get view() {
      return view;
    },
    compose,
    async restart() {
      await resources.close();
      resources = compose('fixture-process-2');
      return resources;
    },
    setView: (value, rows = metadata) => {
      view = value;
      metadata = rows;
      write();
    },
    setCurrent: (value) => {
      current = value;
    },
    setIdle: (value) => {
      idle = value;
    },
  };
}

test('real protoJSON integer and enum representations normalize only typed fields, malformed/expired/mismatched input refuses', async (t) => {
  const f = await setup(t),
    raw = JSON.parse(JSON.stringify(f.view));
  raw.binding.epoch = '1';
  raw.binding.revision = '1';
  raw.binding.mode = 'DSH_INSTANCE_MODE_DEDICATED';
  raw.resources[0].expiresAt = '2000';
  assert.deepEqual(normalizeView(raw), f.view);
  for (const change of [
    () => {
      raw.binding.epoch = '01';
    },
    () => {
      raw.binding.epoch = '9007199254740992';
    },
    () => {
      raw.binding.mode = 'DSH_INSTANCE_MODE_SHARED';
    },
    () => {
      raw.extra = true;
    },
  ]) {
    const copy = JSON.parse(JSON.stringify(raw));
    change();
    assert.throws(() => normalizeView(raw));
    for (const key of Object.keys(raw)) delete raw[key];
    Object.assign(raw, copy);
  }
  const intent = readIntent(f.intentFile, f.binding, () => 1000);
  assert.equal((await loadResources(intent, f.artifactRoot)).length, 1);
  assert.throws(() =>
    readIntent(
      f.intentFile,
      { ...f.binding, memberId: randomUUID() },
      () => 1000,
    ),
  );
  assert.throws(() => readIntent(f.intentFile, f.binding, () => 2001));
  writeFileSync(
    join(f.artifactRoot, sha(f.archive).slice(7), 'skill', 'unexpected.txt'),
    'not signed',
  );
  await assert.rejects(loadResources(intent, f.artifactRoot));
});

test('actual SkillRegistry exposes verified selected skill and removes it with a fresh exact view; stale authority blocks get', async (t) => {
  const f = await setup(t);
  assert.equal((await f.resources.reconcile()).status, 'ready');
  const skill = await f.ctx.skills.get('example');
  assert.match(skill.content, /Verified body/);
  assert.equal(skill.provider, 'auriga-dedicated-catalog');
  f.setCurrent(() => ({ ...f.view, viewId: randomUUID() }));
  await assert.rejects(f.ctx.skills.get('example'));
  f.setCurrent(() => f.view);
  f.setView({ ...f.view, viewId: randomUUID(), resources: [] }, []);
  assert.equal((await f.resources.reconcile()).status, 'ready');
  assert.equal(await f.ctx.skills.get('example'), undefined);
});

test('plugin enable/install/removal use exact manager outcomes, real active rows and durable restart receipts', async (t) => {
  const f = await setup(t, 'plugin', true);
  assert.equal((await f.resources.reconcile()).status, 'restart-required');
  assert.deepEqual(
    f.operations.map((row) => row[0]),
    ['install', 'enable'],
  );
  assert.equal((await f.resources.reconcile()).status, 'restart-required');
  assert.equal(f.operations.length, 2);
  const restarted = await f.restart();
  assert.equal((await restarted.reconcile()).status, 'ready');
  f.setView({ ...f.view, viewId: randomUUID(), resources: [] }, []);
  assert.equal((await restarted.reconcile()).status, 'restart-required');
  assert.deepEqual(
    f.operations.slice(2).map((row) => row[0]),
    ['enable', 'remove'],
  );
  assert.equal(
    JSON.parse(readFileSync(f.stateFile, 'utf8')).pending.status,
    'restart-required',
  );
});

test('failed active plugin and busy native work cannot be reported ready or mutated', async (t) => {
  const f = await setup(t, 'plugin');
  f.setIdle(false);
  assert.equal((await f.resources.reconcile()).status, 'applying');
  assert.equal(f.operations.length, 0);
  f.setIdle(true);
  assert.equal((await f.resources.reconcile()).status, 'ready');
  f.bundles.values().next().value.active = false;
  assert.equal((await f.resources.reconcile()).status, 'failed');
  await assert.rejects(f.resources.verify());
});
