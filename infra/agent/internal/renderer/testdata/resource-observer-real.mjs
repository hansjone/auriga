import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, writeFile, readFile, rename } from 'node:fs/promises';
import { join, resolve, dirname } from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { generateKeyPairSync, randomBytes } from 'node:crypto';
import { get as httpGet } from 'node:http';
import {
  signIdentity,
  INSTANCE_ID,
  OWNER_ID,
} from '../../../../../clients/dsh-plugin/test/managed-helpers.js';

// Isolated real loader and native services, no portal, platform or user data.
const [runtimeArg, evidenceArg, sourceArg = '.', mode = 'fixed'] =
  process.argv.slice(2);
assert(['fixed', 'baseline'].includes(mode));
const [runtime, evidence, source] = [runtimeArg, evidenceArg, sourceArg].map(
  (value) => resolve(value),
);
await mkdir(evidence, { recursive: true });
const root = await mkdtemp(join(evidence, 'observer-'));
const home = join(root, 'home'),
  workspace = join(home, 'workspace');
await mkdir(workspace, { recursive: true });
for (const [directory, name] of [
  ['skills/private-probe', 'private-probe'],
  ['auriga/skills-active/current/shared-probe', 'shared-probe'],
]) {
  await mkdir(join(home, directory), { recursive: true });
  await writeFile(
    join(home, directory, 'SKILL.md'),
    `---\nname: ${name}\ndescription: Public isolated node fixture.\n---\n\n${name} body.\n`,
  );
}
const cli = join(runtime, 'node_modules/@deepseek-ai/dsh/lib/bin.js');
const require = createRequire(cli);
const bundled = join(
  dirname(require.resolve('@deepseek-ai/dsh-agent-preset/package.json')),
  'skills',
);
assert.equal(require('@deepseek-ai/dsh/package.json').version, '0.2.1-rc.3');
assert.deepEqual(
  await readFile(join(runtime, 'package-lock.json')),
  await readFile(join(source, 'deploy/compose/real-dsh/package-lock.json')),
  'the real DSH fixture must use the runtime lockfile',
);
const observer = join(
  source,
  'infra/agent/internal/renderer/resource_observer.mjs',
);
const managed = join(source, 'clients/dsh-plugin/src/plugin.js');
const token = join(root, 'instance.token'),
  policy = join(root, 'policy.json'),
  probe = join(root, 'probe.mjs'),
  result = join(root, 'result.json');
const keys = join(root, 'identity-keys.json'),
  { publicKey, privateKey } = generateKeyPairSync('ed25519');
const nodeToken = randomBytes(32).toString('base64url');
const redact = (value) =>
  value
    .replaceAll(nodeToken, 'REDACTED')
    .replace(/token=[^&\s]+/gi, 'token=REDACTED');
await writeFile(
  keys,
  JSON.stringify({
    keys: [{ ...publicKey.export({ format: 'jwk' }), kid: 'edge-fixture' }],
  }),
);
await writeFile(token, nodeToken, { mode: 0o600 });
await writeFile(
  probe,
  `import { writeFile } from 'node:fs/promises';
export const name = 'node-resource-test-probe';
export const inject = ['appReady', 'webServer', 'sessionController'];
export function apply(ctx) {
  ctx.effect(() => ctx.appReady.onReady(() => void run()));
  async function run() {
    try {
      const old = process.env.OLD_TEST_SESSION;
      if (old) {
        let denied = false;
        try { await ctx.sessionController.prompt({sessionId: old, prompt: 'must not execute'}); }
        catch (error) { denied = String(error).includes('Private-only new session required'); }
        if (!denied) throw new Error('old context was not refused');
      }
      const session = await ctx.sessionController.create({cwd: process.cwd(), agentPreset: 'cordis'});
      await writeFile(process.env.NODE_TEST_RESULT, JSON.stringify({
        port: ctx.webServer.port, sessionId: session.sessionId, oldDenied: !!old,
      }));
    } catch (error) {
      await writeFile(process.env.NODE_TEST_RESULT, JSON.stringify({error: String(error)}));
    }
  }
}
`,
);
let old;
for (const privateOnly of [false, true]) {
  if (privateOnly)
    await rename(
      join(home, 'auriga/skills-active/current'),
      join(home, 'quarantine'),
    );
  await mkdir(join(home, 'auriga/skills-active/current'), { recursive: true });
  await writeFile(
    policy,
    JSON.stringify({ schemaVersion: 1, newSessionRequired: privateOnly }),
  );
  await writeFile(result, '');
  const patch = join(
    root,
    privateOnly ? 'private.patch.yml' : 'active.patch.yml',
  );
  await writeFile(
    patch,
    `- insert:
    - id: auriga-shared-skills
      name: '@deepseek-ai/dsh-skill-filesystem'
      config:
        providerName: auriga-shared
        includeDefaultRoots: false
        customSkillDirs: [${JSON.stringify(join(home, 'auriga/skills-active/current'))}]
        watch: true
    - id: auriga-dsh
      name: ${JSON.stringify(pathToFileURL(managed).href)}
      inject: [webServer, connection]
      config:
        browserOrigin: {scheme: https, host: instance.i.example.com, port: 443}
    - id: auriga-node-resource-observer
      name: ${JSON.stringify(pathToFileURL(observer).href)}
      config: ${JSON.stringify({ tokenFile: token, policyFile: policy })}
    - id: node-resource-test-probe
      name: ${JSON.stringify(pathToFileURL(probe).href)}
`,
  );
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([key]) => !/^(AURIGA_|DSH_|(?:HTTP|HTTPS|ALL)_PROXY$)/i.test(key),
    ),
  );
  let log = '';
  const child = spawn(
    process.execPath,
    [
      cli,
      'web',
      '--patch',
      patch,
      '--no-open',
      '--host',
      '127.0.0.1',
      '--port',
      '0',
    ],
    {
      cwd: workspace,
      env: {
        ...env,
        DSH_HOME: home,
        DSH_TELEMETRY_DISABLED: '1',
        DSH_AGENTS_HOME: join(root, 'agents'),
        DSH_BUNDLED_SKILL_DIR: bundled,
        AURIGA_MODE: 'managed',
        AURIGA_INSTANCE_ID: INSTANCE_ID,
        AURIGA_OWNER_ID: OWNER_ID,
        AURIGA_PLATFORM_URL: 'http://127.0.0.1:1',
        AURIGA_WORKSPACE_ROOT: workspace,
        AURIGA_IDENTITY_KEYS_FILE: keys,
        AURIGA_INSTANCE_TOKEN_FILE: token,
        NODE_TEST_RESULT: result,
        ...(old ? { OLD_TEST_SESSION: old } : {}),
      },
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  child.stdout.on('data', (b) => (log += b));
  child.stderr.on('data', (b) => (log += b));
  let exited = false;
  child.on('exit', () => (exited = true));
  try {
    let ready;
    const end = Date.now() + 180000;
    while (Date.now() < end && !exited) {
      try {
        ready = JSON.parse(await readFile(result, 'utf8'));
        break;
      } catch {}
      await delay(100);
    }
    assert(ready && !ready.error, redact(JSON.stringify(ready) + '\n' + log));
    assert(ready.port > 0);
    old = ready.sessionId;
    const base = `http://127.0.0.1:${ready.port}`;
    const path = '/auriga-node-resource-catalog';
    const signed = (htu) => ({
      'Auriga-Identity': signIdentity(privateKey, 'edge-fixture', {
        htm: 'GET',
        htu,
      }).token,
      Origin: 'https://instance.i.example.com',
    });
    // Match the Go node client; Node fetch adds browser-style Sec-Fetch-Mode.
    const get = (path, headers) =>
      new Promise((resolve, reject) => {
        const req = httpGet(
          base + path,
          { headers, signal: AbortSignal.timeout(10000) },
          async (res) => {
            try {
              const chunks = [];
              for await (const chunk of res) chunks.push(chunk);
              resolve({
                status: res.statusCode,
                json: () => JSON.parse(Buffer.concat(chunks)),
              });
            } catch (error) {
              reject(error);
            }
          },
        );
        req.on('error', reject);
      });
    const health = await get('/_auriga/health');
    assert.equal(health.status, 200, redact(log));
    assert.equal((await health.json()).ready, false);
    const panel = await get(
      '/auriga/shell/status',
      signed('/auriga/shell/status'),
    );
    assert.equal(panel.status, 200, redact(log));
    assert.equal((await panel.json()).authenticated, true);
    const denied = await get(path);
    assert.equal(denied.status, 401);
    const response = await get(path, { Authorization: 'Bearer ' + nodeToken });
    assert.equal(response.status, mode === 'fixed' ? 200 : 401, redact(log));
    if (mode === 'baseline') {
      console.log(
        JSON.stringify({
          mode,
          health: health.status,
          authorized: response.status,
          anonymous: denied.status,
        }),
      );
      break;
    }
    assert.equal((await get(path, signed(path))).status, 401);
    assert.equal(
      (
        await get(path, {
          Authorization: 'Bearer ' + randomBytes(32).toString('base64url'),
        })
      ).status,
      401,
    );
    assert.equal(
      (await get(path + '?query=1', { Authorization: 'Bearer ' + nodeToken }))
        .status,
      401,
    );
    const catalog = await response.json();
    assert.equal(catalog.privateOnly, privateOnly);
    for (const preset of ['cordis', 'standard', 'ptc']) {
      const rows = catalog.skills.filter((s) => s.preset === preset);
      assert(
        rows.some((s) => s.name === 'private-probe'),
        'private skill missing ' + preset,
      );
      assert(
        rows.some((s) => s.name === 'editing-cordis-compositions'),
        'upstream skill missing ' + preset,
      );
      const shared = rows.find((s) => s.name === 'shared-probe');
      if (privateOnly) {
        assert.equal(shared, undefined);
        assert(ready.oldDenied);
      } else {
        assert.equal(shared.provider, 'auriga-shared');
        assert.equal(shared.content, 'shared-probe body.');
      }
    }
    console.log(
      JSON.stringify({
        mode,
        privateOnly,
        health: health.status,
        authorized: response.status,
        anonymous: denied.status,
        panel: panel.status,
        realPresets: 3,
        oldDenied: ready.oldDenied,
      }),
    );
  } finally {
    if (!exited) {
      const stopped = once(child, 'exit');
      child.kill();
      await stopped;
    }
    await writeFile(
      join(root, privateOnly ? 'private.log' : 'active.log'),
      redact(log),
    );
  }
}
console.log('Real DSH fixture finished; all children stopped. ' + root);
