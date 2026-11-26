import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { fixture } from '../network/host/native/fixtures.js';
import { installSubmissionTools } from '../../src/resources/submission-tools.js';

const packageId = '00000000-0000-0000-0000-000000000001';
const versionId = '00000000-0000-0000-0000-000000000002';
const input = {
  kind: 'skill',
  directory: 'skills/example',
  metadata: {
    license: 'MIT',
    version: '1.0.0',
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
  },
};
const result = { packageId, versionId, state: 'pending_review' };

function setup(t, { submit, prepare, now, registry, ownedPackages } = {}) {
  const f = fixture();
  const agent = f.attach('session-a');
  const registered = new Map();
  const calls = [];
  let dispose,
    identity = {
      tenantId: packageId,
      ownerId: packageId,
      clientId: versionId,
    },
    generation = 'login-1';
  const runtime = {
    identity: () => identity,
    catalogGeneration: () => generation,
  };
  let current = runtime;
  const ctx = Object.assign(f.ctx, {
    inject(names, use) {
      for (const name of names) assert.ok(this[name], name);
      use(this);
    },
    tools: {
      register(tool) {
        assert.ok(!registered.has(tool.name));
        registered.set(tool.name, tool);
        const unregister = registry?.register(tool);
        return () => {
          unregister?.();
          registered.delete(tool.name);
        };
      },
    },
    effect(init) {
      dispose = init();
    },
  });
  const service = {
    async submitPrepared(args, assertCurrent) {
      calls.push(['submit', args]);
      return submit
        ? submit(args, assertCurrent)
        : { ...result, extra: 'internal detail' };
    },
    ...(ownedPackages ? { ownedPackages } : {}),
  };
  const install = () =>
    installSubmissionTools(ctx, {
      current: () => current,
      service: (active) => {
        assert.equal(active, runtime);
        return service;
      },
      prepare: async (args) => {
        calls.push(['prepare', args]);
        return prepare
          ? prepare(args)
          : {
              manifest: {
                kind: args.kind,
                name: 'example',
                version: '1.0.0',
                details: 'internal detail',
              },
              archive: Buffer.from('portable snapshot'),
            };
      },
      now,
    });
  install();
  t.after(() => dispose?.());
  return {
    ...f,
    agent,
    registered,
    calls,
    install,
    close: () => dispose(),
    change: (values) => {
      if ('identity' in values) identity = values.identity;
      if ('generation' in values) generation = values.generation;
      if ('runtime' in values) current = values.runtime;
    },
    call(name, args, caller = agent, signal) {
      return registered
        .get('auriga_resource_' + name)
        .execute(args, { agent: caller, signal });
    },
  };
}

test('uses the exact native session cwd, advertises three tools, rejects author and exposes only safe fields', async (t) => {
  const f = setup(t);
  assert.deepEqual(
    [...f.registered.keys()],
    [
      'auriga_resource_list',
      'auriga_resource_prepare',
      'auriga_resource_submit',
    ],
  );
  await assert.rejects(
    f.call('prepare', { ...input, cwd: '/elsewhere' }),
    /common.validation_failed/,
  );
  await assert.rejects(
    f.call('prepare', { ...input, kind: 'tool' }),
    /common.validation_failed/,
  );
  await assert.rejects(
    f.call('prepare', {
      ...input,
      metadata: { ...input.metadata, author: 'Spoofed' },
    }),
    /common.validation_failed/,
  );
  await assert.rejects(
    f.call('prepare', {
      ...input,
      metadata: { ...input.metadata, role: 'admin' },
    }),
    /common.validation_failed/,
  );
  await assert.rejects(
    f.call('prepare', input, { ...f.agent }),
    /common.forbidden/,
  );
  const preflight = await f.call('prepare', input);
  assert.deepEqual(f.calls[0], ['prepare', { cwd: '/work/project', ...input }]);
  assert.deepEqual(preflight, {
    handle: preflight.handle,
    mode: 'create',
    kind: 'skill',
    name: 'example',
    version: '1.0.0',
    description: '',
    license: '',
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    archiveBytes: 17,
    fileCount: 0,
    files: [],
    estimatedRisk: 'r1',
  });
  const submission = { handle: preflight.handle, reason: 'Review this skill' };
  await assert.rejects(
    f.call('submit', { ...submission, origin: 'https://example.com' }),
    /common.validation_failed/,
  );
  assert.deepEqual(await f.call('submit', submission), result);
});

test('lists owned packages, detects upgrade mode, warns on duplicate version and forwards changeNotes', async (t) => {
  const existingId = '00000000-0000-0000-0000-000000000099';
  const f = setup(t, {
    ownedPackages: async (kind) => [
      {
        packageId: existingId,
        kind: kind ?? 'plugin',
        name: 'my-decoration',
        visibility: 'tenant',
        enabled: true,
        defaultVersionId: versionId,
        publishedVersion: '1.0.0',
        latestVersion: '1.0.0',
        latestState: 'published',
        description: 'Decoration plugin',
      },
    ],
    prepare: async (args) => ({
      manifest: {
        kind: args.kind,
        name: 'my-decoration',
        version: args.metadata.version ?? '1.1.0',
        description: 'Decoration plugin v2',
        license: 'MIT',
        permissions: args.metadata.permissions,
        plugin: {
          packageName: '@local/my-decoration',
          dshVersionRange: '>=0.2.1-rc.3',
        },
      },
      archive: Buffer.from('upgrade snapshot'),
      files: ['package.json', 'cordis.patch.yml', 'index.js'],
      changeNotes: args.metadata.changeNotes,
    }),
  });

  const listed = await f.call('list', { kind: 'plugin' });
  assert.equal(listed.items.length, 1);
  assert.equal(listed.items[0].packageId, existingId);

  const dup = await f.call('prepare', {
    kind: 'plugin',
    directory: 'plugins/my-decoration',
    metadata: {
      version: '1.0.0',
      permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    },
  });
  assert.equal(dup.mode, 'upgrade');
  assert.equal(dup.existingPackage.packageId, existingId);
  assert.ok(dup.warnings?.some((w) => w.includes('1.0.0')));

  const preflight = await f.call('prepare', {
    kind: 'plugin',
    directory: 'plugins/my-decoration',
    metadata: {
      version: '1.1.0',
      changeNotes: '新增导出能力',
      permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    },
  });
  assert.equal(preflight.mode, 'upgrade');
  assert.equal(preflight.fileCount, 3);
  assert.equal(preflight.estimatedRisk, 'r2');
  assert.equal(preflight.warnings, undefined);

  await f.call('submit', {
    handle: preflight.handle,
    reason: '升级到 1.1.0',
  });
  const submitCall = f.calls.find(([kind]) => kind === 'submit');
  assert.equal(submitCall[1].packageId, existingId);
  assert.equal(submitCall[1].changeNotes, '新增导出能力');
});

test('freezes bytes, coalesces concurrent submits and retries only the frozen operation', async (t) => {
  const archive = Buffer.from('first');
  const manifest = {
    kind: 'skill',
    name: 'example',
    version: '1.0.0',
    nested: { label: 'first' },
  };
  let release;
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  let tries = 0;
  const f = setup(t, {
    prepare: async () => ({ archive, manifest }),
    submit: async () => {
      if (++tries === 1) {
        await pending;
        throw Object.assign(new Error('private service URL'), {
          code: 'common.unavailable',
        });
      }
      return result;
    },
  });
  const { handle } = await f.call('prepare', input);
  archive.fill(0);
  manifest.nested.label = 'changed';
  const same = { handle, reason: 'Review', packageId };
  const first = f.call('submit', same);
  const duplicate = f.call('submit', same);
  const rejected = Promise.allSettled([first, duplicate]);
  await assert.rejects(
    f.call('submit', { ...same, reason: 'Different' }),
    /common.conflict/,
  );
  release();
  for (const attempt of await rejected)
    assert.match(attempt.reason.message, /^common\.unavailable \[submit\]/);
  assert.deepEqual(await f.call('submit', same), result);
  assert.deepEqual(await f.call('submit', same), result);
  const submissions = f.calls
    .filter(([action]) => action === 'submit')
    .map(([, args]) => args);
  assert.equal(submissions.length, 2);
  assert.equal(submissions[0].key, submissions[1].key);
  assert.equal(submissions[0].archive.toString(), 'first');
  assert.equal(submissions[0].manifest.nested.label, 'first');
});

test('fences device, account generation, session replacement, expiry, and disable/reload', async (t) => {
  let time = 0;
  const f = setup(t, { now: () => time });
  const prepare = (caller) => f.call('prepare', input, caller);
  const submit = ({ handle }, caller) =>
    f.call('submit', { handle, reason: 'Review' }, caller);
  f.seed('session-b');
  const other = f.attach('session-b');
  let ready = await prepare();
  await assert.rejects(submit(ready, other), /common.not_found/);
  await assert.rejects(
    submit(ready, f.attach('session-a')),
    /common.not_found/,
  );
  ready = await prepare(other);
  f.change({ generation: undefined });
  await assert.rejects(submit(ready, other), /common.unauthenticated/);
  f.change({ generation: 'login-2' });
  await assert.rejects(submit(ready, other), /common.not_found/);
  ready = await prepare(other);
  f.change({
    identity: { tenantId: packageId, ownerId: packageId, clientId: packageId },
  });
  await assert.rejects(submit(ready, other), /common.not_found/);
  ready = await prepare(other);
  time += 20 * 60000;
  await assert.rejects(submit(ready, other), /common.not_found/);
  ready = await prepare(other);
  const stale = f.registered.get('auriga_resource_submit');
  f.close();
  assert.equal(f.registered.size, 0);
  await assert.rejects(
    stale.execute({ handle: ready.handle, reason: 'Review' }, { agent: other }),
    /common.unauthenticated/,
  );
  f.install();
  await assert.rejects(submit(ready, other), /common.not_found/);
  assert.equal(f.calls.filter(([action]) => action === 'submit').length, 0);
});

test('discards prepare or submit results after auth changes while work is pending', async (t) => {
  let releasePreflight, releaseSubmission;
  const preflightGate = Promise.withResolvers();
  const submitGate = Promise.withResolvers();
  let generation = 'prepare';
  const f = setup(t, {
    async prepare({ kind }) {
      const value = {
        archive: Buffer.from('package'),
        manifest: { kind, name: 'example', version: '1.0.0' },
      };
      if (generation === 'prepare') {
        const work = Promise.withResolvers();
        releasePreflight = () => work.resolve(value);
        preflightGate.resolve();
        return work.promise;
      }
      return value;
    },
    async submit() {
      const work = Promise.withResolvers();
      releaseSubmission = () => work.resolve(result);
      submitGate.resolve();
      return work.promise;
    },
  });
  const preparing = f.call('prepare', input);
  const rejectedPrepare = assert.rejects(preparing, /common.unauthenticated/);
  await preflightGate.promise;
  generation = 'submit';
  f.change({ generation });
  releasePreflight();
  await rejectedPrepare;

  const { handle } = await f.call('prepare', input);
  const submitting = f.call('submit', { handle, reason: 'Review' });
  const rejectedSubmit = assert.rejects(submitting, /common.unauthenticated/);
  await submitGate.promise;
  f.change({ generation: undefined });
  releaseSubmission();
  await rejectedSubmit;
});

test('keeps a bounded shared set of handles and recovers expired capacity', async (t) => {
  let time = 0;
  const f = setup(t, { now: () => time });
  for (let i = 0; i < 8; i++) await f.call('prepare', input);
  await assert.rejects(f.call('prepare', input), /common.rate_limited/);
  time += 15 * 60000;
  await f.call('prepare', input);
});

test('returns actionable native error text without forwarding private server details', async (t) => {
  let localFailure = true;
  let platformError = {
    code: 'common.validation_failed',
    stage: 'upload',
    rule: 'bundle_patch_missing',
    reason: 'private server details',
    path: '/private/server/file',
  };
  const f = setup(t, {
    prepare: async ({ kind }) => {
      if (localFailure)
        throw Object.assign(new Error('/private/workspace'), {
          code: 'common.validation_failed',
          path: 'SKILL.md',
          reason: 'invalid skill front matter',
        });
      return {
        archive: Buffer.from('package'),
        manifest: { kind, name: 'example', version: '1.0.0' },
      };
    },
    submit: async () => {
      throw Object.assign(new Error('private exception'), platformError);
    },
  });
  await assert.rejects(
    f.call('prepare', input),
    (error) =>
      /common\.validation_failed \[prepare\] SKILL\.md: invalid skill front matter/.test(
        error.message,
      ) && !error.message.includes('/private/workspace'),
  );
  localFailure = false;
  const { handle } = await f.call('prepare', input);
  const args = { handle, reason: 'Review this skill' };
  await assert.rejects(
    f.call('submit', { ...args, reason: 'x'.repeat(1025) }),
    /common.validation_failed/,
  );
  await assert.rejects(
    f.call('submit', args),
    (error) =>
      /common\.validation_failed \[upload\] bundle_patch_missing/.test(
        error.message,
      ) && !error.message.includes('private'),
  );
  platformError = { code: 'common.forbidden', stage: 'package' };
  await assert.rejects(f.call('submit', args), /common\.forbidden \[package\]/);
});

const sdkRoot = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
test(
  'public DSH dispatch delivers bounded corrective feedback and removes disabled tools',
  {
    skip: !sdkRoot && 'Plugin workflow supplies the public rc.3 SDK',
  },
  async (t) => {
    const require = createRequire(join(sdkRoot, 'package.json'));
    const load = (name) =>
      import(pathToFileURL(require.resolve('@deepseek-ai/' + name)).href);
    const [{ Context }, { ToolRuntime }, { default: SystemPrompt }] =
      await Promise.all([
        load('cordis'),
        load('dsh-tools'),
        load('dsh-system-prompt'),
      ]);
    const ctx = new Context();
    t.after(() => ctx.fiber.dispose());
    await ctx.plugin(SystemPrompt, {});
    await ctx.plugin(ToolRuntime, { mode: 'native' });
    const f = setup(t, {
      registry: ctx.tools,
      async prepare() {
        throw Object.assign(new Error('private exception'), {
          code: 'common.validation_failed',
          path: 'SKILL.md',
          reason: 'invalid skill front matter',
        });
      },
    });
    const call = () =>
      ctx.tools.execute({
        name: 'auriga_resource_prepare',
        callId: randomUUID(),
        agent: f.agent,
        arguments: input,
        signal: new AbortController().signal,
      });
    const validation = await call();
    assert.equal(validation.isError, true);
    assert.match(
      JSON.stringify(validation.content),
      /SKILL\.md: invalid skill front matter/,
    );
    assert.doesNotMatch(
      JSON.stringify(validation.content),
      /private exception/,
    );
    f.close();
    const disabled = await call();
    assert.equal(disabled.isError, true);
    assert.doesNotMatch(
      JSON.stringify(disabled.content),
      /invalid skill front matter/,
    );
  },
);

for (const change of ['archive', 'cwd', 'cancel', 'resolve', 'login']) {
  test(`original execution fences later writes after ${change}`, async (t) => {
    const controller = new AbortController();
    const writes = [];
    const f = setup(t, {
      async submit(_input, assertCurrent) {
        await assertCurrent();
        writes.push('package');
        if (change === 'archive')
          f.ctx.workspaceRegistry.archivedSessionIds.push(f.agent.id);
        if (change === 'cwd') f.agent.session.header.cwd = '/work/other';
        if (change === 'cancel') controller.abort();
        if (change === 'resolve')
          f.ctx.sessionController.resolveAgent = async () => ({
            error: 'closed',
          });
        if (change === 'login') f.change({ generation: 'login-2' });
        await assertCurrent();
        writes.push('upload');
        await assertCurrent();
        writes.push('submit');
        return result;
      },
    });
    const { handle } = await f.call('prepare', input);
    await assert.rejects(
      f.call(
        'submit',
        { handle, reason: 'Review' },
        f.agent,
        controller.signal,
      ),
      change === 'login' ? /common.unauthenticated/ : /common.forbidden/,
    );
    assert.deepEqual(writes, ['package']);
  });
}
