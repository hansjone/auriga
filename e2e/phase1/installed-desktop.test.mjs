import test from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdir,
  mkdtemp,
  writeFile,
  rm,
  symlink,
  readFile,
} from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';
import { launchDesktop } from './cloud-desk.desktop.mjs';

test('installed desktop refuses a profile outside this worktree before running its executable', async (t) => {
  const state = await mkdtemp(join(tmpdir(), 'auriga-foreign-profile-'));
  t.after(() => rm(state, { recursive: true, force: true }));
  await assert.rejects(
    launchDesktop({
      state,
      runtime: {
        kind: 'installed-app',
        executable: process.execPath,
        cli: 'missing-cli.js',
      },
      profile: join(state, 'dsh/profiles/desktop'),
    }),
    /only this worktree owns the desktop profile/,
  );
});

// Process/HTTP orchestration fixture only; real installed-app compatibility is
// separately required by the NAS acceptance journey on Windows.
test(
  'installed app orchestration uses its own HOME, verifies package and version, and stops its process',
  { skip: process.platform === 'win32' },
  async (t) => {
    const parent = resolve('e2e/phase1/.local');
    await mkdir(parent, { recursive: true });
    const state = await mkdtemp(join(parent, 'installed-fixture-'));
    t.after(() => rm(state, { recursive: true, force: true }));
    const profile = join(state, 'dsh/profiles/desktop');
    await mkdir(join(profile, 'node_modules/auriga-dsh'), { recursive: true });
    await mkdir(join(state, 'cwd'));
    await writeFile(
      join(profile, 'node_modules/auriga-dsh/package.json'),
      JSON.stringify({ name: 'auriga-dsh', version: '0.1.0' }),
    );
    const pluginPackage = join(state, 'fixture.tgz');
    await writeFile(pluginPackage, 'orchestration-fixture');
    const packageSHA256 = createHash('sha256')
      .update('orchestration-fixture')
      .digest('hex');
    const executable = join(state, 'fixture-app.cjs');
    await writeFile(
      executable,
      `#!${process.execPath}
if (process.argv.includes('--version')) { console.log('0.2.1-rc.3'); process.exit(0); }
if (process.env.DSH_HOME !== ${JSON.stringify(join(state, 'dsh'))}) process.exit(23);
if (process.env.ELECTRON_RUN_AS_NODE || process.env.OPENAI_API_KEY || process.env.NODE_OPTIONS) process.exit(24);
const http = require('node:http');
const server = http.createServer((req,res) => {
  if(req.url.startsWith('/?token=')) { res.writeHead(302, {'set-cookie':'session=fixture; Path=/',location:'/'});res.end();return; }
  if(req.headers.cookie !== 'session=fixture') { res.writeHead(401);res.end('{}');return; }
  res.setHeader('content-type','application/json');res.end(JSON.stringify({configured:false,platformOrigin:''}));
});
server.listen(0,'127.0.0.1',()=>console.log('dsh web: http://127.0.0.1:'+server.address().port+'/?token=fixture-token'));
`,
      { mode: 0o755 },
    );
    const prepared = {
      state,
      profile,
      runtime: {
        kind: 'installed-app',
        executable,
        cli: join(state, 'cli.js'),
      },
      installation: 'installed-app-tarball',
      dshVersion: '0.2.1-rc.3',
      pluginPackage,
      packageSHA256,
      packageVersion: '0.1.0',
    };
    const host = await launchDesktop(prepared);
    try {
      assert.equal(host.initialStatus.settingsAvailable, true);
      assert.equal(
        (await host.request('/auriga/settings/state')).configured,
        false,
      );
    } finally {
      await host.stop();
    }
    assert.throws(() => process.kill(host.pid, 0));
    await assert.rejects(
      launchDesktop({ ...prepared, packageSHA256: '0'.repeat(64) }),
      /installed package asset changed/,
    );
    const foreign = await mkdtemp(join(tmpdir(), 'auriga-foreign-desktop-'));
    t.after(() => rm(foreign, { recursive: true, force: true }));
    await mkdir(join(foreign, 'node_modules/auriga-dsh'), { recursive: true });
    await writeFile(
      join(foreign, 'node_modules/auriga-dsh/package.json'),
      JSON.stringify({ name: 'auriga-dsh', version: '0.1.0' }),
    );
    await rm(profile, { recursive: true });
    await symlink(foreign, profile);
    let unexpected;
    try {
      await assert.rejects(async () => {
        unexpected = await launchDesktop({ ...prepared, connect: {} });
      }, /desktop profile must not redirect through a link/);
    } finally {
      await unexpected?.stop();
    }
    await assert.rejects(readFile(join(foreign, 'cordis.patch.yml')), {
      code: 'ENOENT',
    });
  },
);
