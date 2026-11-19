/* global AbortSignal */
import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { randomUUID } from 'node:crypto';
import { mkdtemp, mkdir, writeFile, rm, symlink } from 'node:fs/promises';
import { join, resolve, relative, sep, isAbsolute } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { installSharedHost, SHARED_PROFILE } from '../../src/shared/host.js';
import { assertSharedProfile } from '../../src/shared/profile-state.js';
import { PLATFORM_SKILL_ROOT } from '../../src/shared/skill-policy.js';

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');
const skip =
  !root && 'Plugin workflow runs the pinned rc.3 public skill composition';

test('actual Host loader accepts production-rendered read-only container skill root and rejects host/private/multiple roots', () => {
  const child = spawnSync(
    process.execPath,
    [fileURLToPath(new URL('./skill-loader-worker.mjs', import.meta.url))],
    { encoding: 'utf8', windowsHide: true },
  );
  assert.equal(child.status, 0, child.stderr || child.stdout);
});

async function composition(t) {
  const hostRequire = createRequire(join(root, 'package.json'));
  const load = async (name) =>
    import(pathToFileURL(hostRequire.resolve('@deepseek-ai/' + name)).href);
  const [
    { Context },
    { ToolRuntime },
    { default: SystemPrompt },
    { SkillRegistry },
    filesystem,
    toolSkill,
    { LocalFileSystem },
  ] = await Promise.all([
    load('cordis'),
    load('dsh-tools'),
    load('dsh-system-prompt'),
    load('dsh-skill'),
    load('dsh-skill-filesystem'),
    load('dsh-tool-skill'),
    load('dsh-fs-local'),
  ]);
  const parent = fileURLToPath(
    new URL('../../../../.dev/shared-platform-skill/', import.meta.url),
  );
  await mkdir(parent, { recursive: true });
  const directory = await mkdtemp(join(parent, 'case-'));
  const approved = join(directory, 'approved'),
    project = join(directory, 'project'),
    user = join(directory, 'user');
  const skill = async (where, name, body) => {
    await mkdir(where, { recursive: true });
    await writeFile(
      join(where, 'SKILL.md'),
      `---\nname: ${name}\ndescription: fixture skill\n---\n${body}\n`,
    );
  };
  await skill(
    join(approved, 'platform-skill'),
    'platform-skill',
    'APPROVED_PLATFORM_BODY',
  );
  await skill(
    join(project, '.agents', 'skills', 'private-project'),
    'private-project',
    'PRIVATE_PROJECT_BODY',
  );
  await skill(
    join(user, 'skills', 'private-user'),
    'private-user',
    'PRIVATE_USER_BODY',
  );
  const outside = join(directory, 'private-link');
  await skill(outside, 'private-link', 'PRIVATE_LINK_BODY');
  await symlink(
    outside,
    join(approved, 'private-link'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  const ctx = new Context();
  let host;
  t.after(async () => {
    host?.close();
    await ctx.fiber.dispose();
    assert.ok(resolve(directory).startsWith(resolve(parent) + sep));
    await rm(directory, { recursive: true, force: true });
  });
  await ctx.plugin(SystemPrompt, {});
  await ctx.plugin(ToolRuntime, { mode: 'native' });
  await ctx.plugin(SkillRegistry, {});
  const inside = (base, path) => {
    const value = relative(base, path);
    return (
      value === '' ||
      (value !== '..' && !value.startsWith('..' + sep) && !isAbsolute(value))
    );
  };
  const nativeMount = resolve(PLATFORM_SKILL_ROOT);
  // The OS mount seam maps ONLY the fixed container mount to an isolated real
  // directory. All discovery, symlink resolution and reads use public fs-local.
  class ReadOnlyMount extends LocalFileSystem {
    async resolve(path, options) {
      const absolute = resolve(options?.cwd ?? project, path),
        suffix = relative(nativeMount, absolute);
      const translated = inside(nativeMount, absolute)
        ? join(approved, suffix)
        : path;
      const target = await super.resolve(translated, options);
      return { ...target, displayPath: this.processPath(target) };
    }
    processPath(target) {
      const actual = String(target.targetKey);
      return inside(approved, actual)
        ? PLATFORM_SKILL_ROOT +
            (relative(approved, actual)
              ? '/' + relative(approved, actual).split(sep).join('/')
              : '')
        : actual;
    }
    async listDir(target, signal) {
      return (await super.listDir(target, signal)).map((entry) => ({
        ...entry,
        target: {
          ...entry.target,
          displayPath: this.processPath(entry.target),
        },
      }));
    }
  }
  new ReadOnlyMount(ctx, { cwd: project, diffBasisMaxBytes: 1024 });
  const id = randomUUID(),
    agent = { id, session: { id, header: { id, cwd: project } }, ctx };
  ctx.provide('agents', { get: (value) => (value === id ? agent : undefined) });
  ctx.provide('sessions', {
    get: (value) => (value === id ? agent.session : undefined),
  });
  ctx.provide('workingDirectory', { ensure: async () => project });
  ctx.provide('remoteAccess', { registerPolicy: () => () => {} });
  ctx.provide('webServer', { register: () => () => {} });
  const config = {
    providerName: 'auriga-shared',
    includeDefaultRoots: false,
    customSkillDirs: [PLATFORM_SKILL_ROOT],
    watch: false,
    dshHome: user,
    agentsHome: join(directory, 'agents'),
  };
  const fiber = await ctx.plugin(filesystem, config);
  await ctx.plugin(toolSkill, {});
  const binding = { sessionId: id, epoch: 1 };
  let epoch = 1;
  const runtime = {
    session: async () => ({ ...binding, epoch }),
    bindings: () => [],
    close() {},
  };
  const ports = {
    createPeer() {},
    assertProfile: (expected) =>
      assertSharedProfile(ctx, expected, [PLATFORM_SKILL_ROOT]),
  };
  assert.equal(
    ports.assertProfile(SHARED_PROFILE),
    true,
    'actual mounted public profile must pass',
  );
  host = installSharedHost(ctx, {
    runtime,
    enterprise: { handler() {} },
    ports,
  });
  const call = (name, args = {}) =>
    ctx.tools.execute({
      callId: randomUUID(),
      name,
      arguments: args,
      agent,
      signal: AbortSignal.timeout(5000),
    });
  return {
    ctx,
    agent,
    call,
    config,
    fiber,
    setEpoch: (value) => {
      epoch = value;
    },
    project,
  };
}

test(
  'real rc.3 ToolRuntime + SkillRegistry + SkillFilesystem load only platform view through production Host gate',
  { skip },
  async (t) => {
    const f = await composition(t);
    const loaded = await f.call('skill', { name: 'platform-skill' });
    assert.equal(loaded.isError, false, JSON.stringify(loaded));
    assert.equal(loaded.value.provider, 'auriga-shared');
    assert.match(loaded.value.content, /APPROVED_PLATFORM_BODY/);
    for (const name of [
      'private-project',
      'private-user',
      'private-link',
      '../../private',
      '/tmp/private',
    ]) {
      const result = await f.call('skill', { name });
      assert.equal(result.isError, true, name);
      assert.doesNotMatch(
        JSON.stringify(result),
        /PRIVATE_(PROJECT|USER|LINK)_BODY/,
      );
    }
    let ran = 0;
    for (const name of [
      'bash',
      'shell',
      'terminal',
      'plugin_install',
      'arbitrary_plugin',
    ]) {
      const dispose = f.ctx.tools.register({
        name,
        description: 'negative canary',
        parameters: {
          type: 'object',
          properties: {},
          additionalProperties: false,
        },
        output: { schema: { type: 'string' }, render: () => [] },
        execute: async () => {
          ran++;
          return 'forbidden';
        },
      });
      assert.equal((await f.call(name)).isError, true);
      dispose();
    }
    assert.equal(ran, 0);
    const dispose = f.ctx.skills.register({
      name: 'private-runtime',
      description: 'private',
      content: 'PRIVATE_RUNTIME_BODY',
    });
    const privateResult = await f.call('skill', { name: 'private-runtime' });
    assert.equal(privateResult.isError, true);
    assert.doesNotMatch(JSON.stringify(privateResult), /PRIVATE_RUNTIME_BODY/);
    dispose();
  },
);

test(
  'same-name private runtime shadow and changed/default/bundled roots cannot bypass public skill path gate',
  { skip },
  async (t) => {
    const f = await composition(t);
    const shadow = f.ctx.skills.register({
      name: 'platform-skill',
      description: 'shadow',
      content: 'PRIVATE_SHADOW_BODY',
    });
    const result = await f.call('skill', { name: 'platform-skill' });
    assert.equal(result.isError, true);
    assert.doesNotMatch(JSON.stringify(result), /PRIVATE_SHADOW_BODY/);
    shadow();
    assert.equal(
      (await f.call('skill', { name: 'platform-skill' })).isError,
      false,
    );
    const original = f.fiber.config;
    for (const patch of [
      { includeDefaultRoots: true },
      { customSkillDirs: [f.project] },
      { bundledSkillDir: f.project },
      { providerName: 'private' },
    ]) {
      f.fiber.config = { ...original, ...patch };
      assert.equal(
        (await f.call('skill', { name: 'platform-skill' })).isError,
        true,
      );
    }
    f.fiber.config = original;
  },
);

test(
  'an epoch change during real native skill loading discards the finished body',
  { skip },
  async (t) => {
    const f = await composition(t),
      get = f.ctx.skills.get.bind(f.ctx.skills);
    let reads = 0;
    f.ctx.skills.get = async (...args) => {
      const result = await get(...args);
      if (++reads === 2) f.setEpoch(2);
      return result;
    };
    const result = await f.call('skill', { name: 'platform-skill' });
    assert.equal(result.isError, true);
    assert.doesNotMatch(JSON.stringify(result), /APPROVED_PLATFORM_BODY/);
  },
);
