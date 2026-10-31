// Module-level discovery evidence, using unmodified packages from real-dsh's
// lockfile. This does not assert managed login, browser execution or peer send.
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomBytes } from 'node:crypto';
import { mkdirSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const [runtimeRoot, patchFile, scratch] = process.argv.slice(2);
const req = createRequire(
  pathToFileURL(join(resolve(runtimeRoot), 'package.json')),
);
const load = (name) => import(pathToFileURL(req.resolve(name)).href);
const version = (name) =>
  JSON.parse(readFileSync(req.resolve(`${name}/package.json`), 'utf8')).version;
const expected = JSON.parse(
  readFileSync(
    new URL(
      '../../../../../deploy/compose/real-dsh/versions.json',
      import.meta.url,
    ),
    'utf8',
  ),
).dshVersion;
assert.deepEqual(
  readFileSync(join(resolve(runtimeRoot), 'package-lock.json')),
  readFileSync(
    new URL(
      '../../../../../deploy/compose/real-dsh/package-lock.json',
      import.meta.url,
    ),
  ),
  'runtime installation must use the real-dsh lockfile',
);
assert.equal(version('@deepseek-ai/dsh'), expected);
assert.equal(version('@deepseek-ai/dsh-client-modules'), expected);
assert.equal(version('@deepseek-ai/dsh-app-boot'), expected);
process.env.DSH_HOME = scratch;
process.env.DSH_TELEMETRY_DISABLED = '1';
process.env.AURIGA_MODE = 'managed';
process.env.AURIGA_INSTANCE_ID = 'inst-c1';
process.env.AURIGA_OWNER_ID = 'u1';
process.env.AURIGA_TENANT_ID = 't1';
process.env.AURIGA_WORKSPACE_ROOT = scratch;
process.env.AURIGA_IDENTITY_KEYS_FILE = join(scratch, 'identity-keys.json');
process.env.AURIGA_INSTANCE_TOKEN_FILE = join(scratch, 'instance.token');
const { publicKey } = generateKeyPairSync('ed25519');
writeFileSync(
  process.env.AURIGA_IDENTITY_KEYS_FILE,
  JSON.stringify({
    keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'collector-probe' }],
  }),
);
writeFileSync(
  process.env.AURIGA_INSTANCE_TOKEN_FILE,
  randomBytes(32).toString('hex'),
  { mode: 0o600 },
);

const [{ boot, loadOverlayPatches }, { default: Modules }, yaml] =
  await Promise.all([
    load('@deepseek-ai/dsh-app-boot'),
    load('@deepseek-ai/dsh-client-modules'),
    load('js-yaml'),
  ]);
const rendered = yaml.load(readFileSync(patchFile, 'utf8'));
const row = rendered[0].insert.find((entry) => entry.id === 'auriga-dsh');
assert.equal(row.name, 'auriga-dsh');
const repo = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../..');
const plugin = join(repo, 'clients/dsh-plugin');
const results = [];

for (const name of ['auriga-dsh/plugin', row.name]) {
  const profile = join(scratch, name.includes('/') ? 'legacy' : 'root');
  mkdirSync(join(profile, 'node_modules'), { recursive: true });
  symlinkSync(
    plugin,
    join(profile, 'node_modules/auriga-dsh'),
    process.platform === 'win32' ? 'junction' : 'dir',
  );
  writeFileSync(
    join(profile, 'package.json'),
    JSON.stringify({
      private: true,
      dependencies: { 'auriga-dsh': '0.0.0' },
    }),
  );
  const config = join(profile, 'cordis.yml');
  writeFileSync(config, '[]\n');
  // Use the rendered plugin row verbatim, changing only name for the control.
  // Host services use their real implementations; port 0 keeps the listener
  // temporary and avoids the renderer's allocated production port.
  const overlay = join(profile, 'probe.patch.yml');
  writeFileSync(overlay, yaml.dump([{ insert: [{ ...row, name }] }]));
  let ctx;
  try {
    const [{ default: Web }, { default: Credentials }, Connection] =
      await Promise.all([
        load('@deepseek-ai/dsh-host-webserver'),
        load('@deepseek-ai/dsh-credentials-local'),
        load('@deepseek-ai/dsh-client-connection'),
      ]);
    ctx = await boot(
      'auriga-collector-probe',
      config,
      loadOverlayPatches('auriga-collector-probe', overlay),
      async (host) => {
        await host.plugin(Web, { host: '127.0.0.1', port: 0 });
        // No platform is contacted. Heartbeats address this isolated host and do
        // not establish a logged-in platform session with the generated token.
        process.env.AURIGA_PLATFORM_URL = `http://127.0.0.1:${host.webServer.port}`;
        await host.plugin(Credentials, {
          path: join(profile, 'credentials.yaml'),
          watch: false,
        });
        await host.plugin(Connection);
      },
    );
    const entry = [...ctx.loader.entries()].find(
      (entry) => entry.options.id === row.id,
    );
    assert.ok(entry?.fiber, `${name}: Node entry must load`);
    assert.equal(
      entry.fiber.state,
      2,
      `${name}: managed Node entry must activate`,
    );
    await ctx.plugin(Modules);
    const ids = ctx.clientModules.graph().entries.map((entry) => entry.id);
    const clientPath = ctx.clientModules.clientPath('auriga-dsh');
    // Record activation independently of collector discovery and managed login.
    results.push({
      name,
      nodeState: entry.fiber.state,
      nodeActive: entry.fiber.state === 2,
      ids,
      clientPath,
    });
    assert.equal(ids.includes('auriga-dsh'), name === 'auriga-dsh');
    if (name === 'auriga-dsh') {
      assert.equal(clientPath, join(plugin, 'src/client.js'));
      assert.ok(readFileSync(clientPath).length > 0);
    } else {
      assert.equal(clientPath, undefined);
    }
  } finally {
    await ctx?.fiber.dispose();
  }
}
assert.equal(
  results[0].nodeState,
  results[1].nodeState,
  'changing the row name must preserve Node activation behavior',
);
console.log(
  JSON.stringify({
    dshVersion: expected,
    nodeVersion: process.versions.node,
    results,
  }),
);
