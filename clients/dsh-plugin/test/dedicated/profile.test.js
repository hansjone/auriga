import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { test, load } from './public.js';

test('strict profile attests actual public native Tool/Skill/Env/Manager modules and rejects wrong roots/extra shared module', async (t) => {
  const home = mkdtempSync(join(tmpdir(), 'auriga-dedicated-profile-')),
    file = join(home, 'package.json');
  writeFileSync(file, '{"name":"fixture-profile","private":true}');
  const [
    { Context },
    { ToolRuntime },
    { default: SystemPrompt },
    { ShellEnvRegistry },
    { SkillRegistry },
    SkillFilesystem,
    { default: Manager },
    { Loader },
  ] = await Promise.all([
    load('cordis'),
    load('dsh-tools'),
    load('dsh-system-prompt'),
    load('dsh-shell-env'),
    load('dsh-skill'),
    load('dsh-skill-filesystem'),
    load('dsh-plugin-manager'),
    load('cordis-plugin-loader'),
  ]);
  const ctx = new Context();
  t.after(async () => {
    await ctx.fiber.dispose();
    rmSync(home, { recursive: true, force: true });
  });
  ctx.provide('profileContext', {
    name: 'auriga-dedicated',
    dir: '/home/dsh/.host/.dsh/profiles/auriga-dedicated',
    installAnchor: '/opt/dsh/package.json',
    startedBundles: [],
  });
  new Loader(ctx);
  new SkillRegistry(ctx);
  await ctx.plugin(SystemPrompt, {});
  await ctx.plugin(ToolRuntime, { mode: 'native' });
  await ctx.plugin(ShellEnvRegistry, {});
  await ctx.plugin(Manager, { fallbackRegistries: [] });
  const skills = ctx.plugin(SkillFilesystem, {
    providerName: 'auriga-dedicated-local',
    includeDefaultRoots: true,
    customSkillDirs: [],
    watch: false,
    dshHome: join(home, 'dsh'),
    agentsHome: join(home, 'agents'),
  });
  await skills;
  // Only the fixed Linux pathname is mapped to a real Windows temp file in
  // this test loader. Public registry modules/configuration remain untouched.
  const profileUrl = new URL('../../src/dedicated/profile.js', import.meta.url);
  const protocolUrl = pathToFileURL(
    fileURLToPath(new URL('../../src/shared/protocol.js', import.meta.url)),
  ).href;
  const intentUrl = pathToFileURL(
    fileURLToPath(new URL('../../src/dedicated/intent.js', import.meta.url)),
  ).href;
  const source = readFileSync(profileUrl, 'utf8')
    .replace(
      "import { accessSync, constants } from 'node:fs';",
      `import { accessSync as accessActual, constants } from 'node:fs'; const accessSync=(path,mode)=>accessActual(path==='/home/dsh/.host/.dsh/profiles/auriga-dedicated'?${JSON.stringify(home)}:path==='/home/dsh/.host/.dsh/profiles/auriga-dedicated/package.json'?${JSON.stringify(file)}:path,mode);`,
    )
    .replace("from '../shared/protocol.js'", `from '${protocolUrl}'`)
    .replace(
      "import { readOwnedFile } from './intent.js';",
      `import { readOwnedFile as readActualFile } from '${intentUrl}';\nconst readOwnedFile=(path,maximum)=>readActualFile(path==='/home/dsh/.host/.dsh/profiles/auriga-dedicated/package.json'?${JSON.stringify(file)}:path,maximum);`,
    );
  const {
    assertDedicatedProfile,
    DEDICATED_PROFILE,
    createDedicatedProfileHandler,
  } = await import(
    'data:text/javascript;base64,' + Buffer.from(source).toString('base64')
  );
  let current = true;
  const resources = {
    isCurrent: () => current,
    verify: async () => {
      if (!current) throw new Error('stale');
    },
    receipt: () => ({ status: current ? 'ready' : 'failed' }),
  };
  assert.equal(assertDedicatedProfile(ctx, DEDICATED_PROFILE, resources), true);
  assert.equal(
    assertDedicatedProfile(
      ctx,
      { ...DEDICATED_PROFILE, shell: true },
      resources,
    ),
    false,
  );
  const handler = createDedicatedProfileHandler({
    ctx,
    resources,
    physicalInstanceId: '019a0000-0000-7000-8000-000000000001',
  });
  const response = () => ({
    writeHead(status) {
      this.status = status;
    },
    end(body) {
      this.body = body;
    },
  });
  let res = response();
  await handler({ method: 'GET', url: '/_auriga/dedicated-profile' }, res);
  assert.equal(res.status, 200);
  assert.deepEqual(Object.keys(JSON.parse(res.body)).sort(), [
    'capabilities',
    'physicalInstanceId',
    'profile',
    'schemaVersion',
  ]);
  current = false;
  res = response();
  await handler({ method: 'GET', url: '/_auriga/dedicated-profile' }, res);
  assert.equal(res.status, 503);
  current = true;
  const conflict = ctx.plugin({ name: 'auriga-dsh-shared', apply() {} });
  await conflict;
  assert.equal(
    assertDedicatedProfile(ctx, DEDICATED_PROFILE, resources),
    false,
  );
  await conflict.dispose();
  assert.equal(assertDedicatedProfile(ctx, DEDICATED_PROFILE, resources), true);
  await skills.dispose();
  assert.equal(
    assertDedicatedProfile(ctx, DEDICATED_PROFILE, resources),
    false,
  );
});
