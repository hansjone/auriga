// Installed Electron acceptance. Private fixtures are read-only; reports omit
// origins, credentials, cookies, account ids, prompts and remote error bodies.
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir, readFile, writeFile, stat, readdir } from 'node:fs/promises';
import { join, resolve, relative, isAbsolute } from 'node:path';
import { randomUUID } from 'node:crypto';
import { chromium } from 'playwright';
import { isolatedEnvironment } from '../network/support/native-install.mjs';
import { privatePath } from '../../scripts/beta-ui/runner.mjs';
import { createHostOutput } from '../../scripts/beta-ui/plugin-dsh.mjs';
import { poll } from './cloud-desk.desktop.mjs';
import { nativeClient, configureFixtureModel } from './cloud-desk.native.mjs';
import { startFixture } from './cloud-desk.fixture.mjs';
import { loginInstalledDesktop } from './installed-login.mjs';

const [fixtureArgument, packageArgument, stateArgument] = process.argv.slice(2);
assert.ok(
  fixtureArgument && packageArgument && stateArgument,
  'private fixture root, package and new state are required',
);
const fixtures = resolve(fixtureArgument),
  spec = resolve(packageArgument),
  state = resolve(stateArgument);
const stateRelative = relative(resolve('e2e/target'), state);
assert.ok(
  stateRelative &&
    !stateRelative.startsWith('..') &&
    !isAbsolute(stateRelative),
  'state must stay in owned e2e/target',
);
const runtime = JSON.parse(
  await readFile(join(fixtures, 'R8-desktop.json'), 'utf8'),
).runtime;
const connect = JSON.parse(
  await readFile(join(fixtures, 'R9-phase1.json'), 'utf8'),
).cloudDesk.connect;
const member = JSON.parse(
  await readFile(join(fixtures, 'R3b-member.json'), 'utf8'),
);
const report = {
  status: 'failed',
  dshVersion: '0.2.1-rc.3',
  steps: [],
  cleanup: [],
};
await mkdir(state);
privatePath(state, true);
for (const p of [
  'cwd',
  'session-cwd',
  'dsh',
  'home/roaming',
  'home/local',
  'config',
  'tmp',
  'cache',
  'data',
])
  await mkdir(join(state, p), { recursive: true });
const env = isolatedEnvironment(state);
for (const p of [env.npm_config_userconfig, env.npm_config_globalconfig])
  await writeFile(p, '');
const switchFile = join(state, 'platform-state');
await writeFile(switchFile, 'online');
const isolated = {
  ...env,
  AURIGA_DESKTOP_OFFLINE_FILE: switchFile,
  AURIGA_DESKTOP_OFFLINE_TARGETS: JSON.stringify(
    [connect.platformOrigin, connect.network?.wssUrl].filter(Boolean),
  ),
};
let app, model;
async function launch(label, isolation = false) {
  const launchedAt = Date.now();
  let closed,
    logs = '',
    launchURL;
  const child = spawn(
    runtime.executable,
    [
      '--user-data-dir=' + join(state, 'electron'),
      '--remote-debugging-port=0',
      '--disable-gpu',
    ],
    {
      env: isolation ? isolated : env,
      cwd: join(state, 'cwd'),
      windowsHide: false,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  const done = new Promise((r) =>
    child.once('close', (code, signal) => {
      closed = { code, signal };
      r();
    }),
  );
  const collector = createHostOutput(
    (x) => (logs += x),
    (url) => (launchURL ??= url),
  );
  for (const stream of [child.stdout, child.stderr])
    stream.on('data', (x) => collector.write(x));
  let browser;
  const stop = async () => {
    if (!closed)
      await promisify(execFile)(
        join(process.env.SystemRoot, 'System32/taskkill.exe'),
        ['/PID', String(child.pid), '/T', '/F'],
        { windowsHide: true },
      );
    await done;
    await browser?.close().catch(() => {});
    collector.end();
    await writeFile(join(state, label + '-host.log'), logs);
  };
  try {
    const port = await poll(async () => {
      if (closed) throw new Error('owned_electron_exited');
      try {
        const path = join(state, 'electron/DevToolsActivePort');
        if ((await stat(path)).mtimeMs < launchedAt) return null;
        const p = Number((await readFile(path, 'utf8')).split('\n')[0]);
        const r = await fetch('http://127.0.0.1:' + p + '/json/version', {
          signal: AbortSignal.timeout(1000),
        });
        return r.ok ? p : null;
      } catch {
        return null;
      }
    }, 'owned_devtools_missing');
    browser = await chromium.connectOverCDP('http://127.0.0.1:' + port);
    const page = await poll(
      () =>
        browser
          .contexts()
          .flatMap((c) => c.pages())
          .find((p) => p.url().startsWith('dsh-app://app/')),
      'owned_page_missing',
    );
    const client = nativeClient((path, body) => request(page, path, body));
    await poll(async () => {
      try {
        await client.rpc('settings/describe');
        return true;
      } catch {
        return null;
      }
    }, 'native_startup_not_ready');
    const targets = [];
    for (const p of browser.contexts().flatMap((c) => c.pages())) {
      const u = new URL(p.url());
      if (u.protocol === 'dsh-app:')
        targets.push({
          path: u.host + u.pathname,
          buttons: await p.getByRole('button').allTextContents(),
        });
    }
    await writeFile(
      join(state, label + '-targets.json'),
      JSON.stringify(targets, null, 2),
    );
    return { page, browser, stop, launchURL: () => launchURL };
  } catch (error) {
    await stop();
    throw error;
  }
}
async function request(page, path, body) {
  return page.evaluate(
    async ({ path, body }) => {
      const r = await fetch(path, {
        method: body === undefined ? 'GET' : 'POST',
        headers: {
          'content-type': 'application/json',
          'x-auriga-settings': '1',
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!r.ok) throw Error('native_http_' + r.status);
      return r.json();
    },
    { path, body },
  );
}
async function auth(page, action, args = {}) {
  const method = 'aurigaAuth/' + action,
    rpcId = randomUUID();
  const result = await request(page, '/auriga-native/' + method, {
    type: 'client-request',
    rpcId,
    method,
    payload: { args },
  });
  assert.equal(result.rpcId, rpcId);
  assert.equal(result.result?.ok, true, 'native_auth_failed');
  return result.result.value;
}
async function assertLocal(page, label, fixtureProvider) {
  assert.equal(
    new URL(page.url()).pathname,
    '/',
    'desktop startup must stay on main page',
  );
  await page
    .getByRole('button', { name: 'Auriga', exact: true })
    .waitFor({ timeout: 30000 });
  const client = nativeClient((path, body) => request(page, path, body));
  await client.activePlugin();
  const id = await client.createSession(
    join(state, 'session-cwd'),
    fixtureProvider,
  );
  const before = model.state.requests;
  await client.prompt(id, model.state.marker(label));
  await poll(() => model.state.requests > before, 'local_model_not_called');
  await client.cancel();
  report.steps.push(label);
}
async function login(page, credential) {
  await page.goto('dsh-app://app/auriga/local-login');
  await loginInstalledDesktop(page, credential, (path, body) =>
    request(page, path, body),
  );
  report.steps.push('team-login-identity');
  await poll(
    async () => (await auth(page, 'status')).network?.connected === true,
    'platform_network_not_ready',
    90000,
  );
}
async function captureSettings(page, label) {
  let stage = 'foreground';
  try {
    await page.bringToFront();
    stage = 'viewport';
    await page.setViewportSize({ width: 1280, height: 900 });
    stage = 'open-settings';
    const settings = page.getByRole('button', { name: /^(Settings|设置)$/ });
    if (await settings.count()) await settings.first().click({ timeout: 8000 });
    else {
      const account = page.getByRole('button', {
        name: /^(Account menu|账号菜单)$/,
      });
      if (await account.count()) await account.click();
      else
        await page
          .getByRole('button', { name: /^(More|更多)$/ })
          .first()
          .click();
      await page
        .getByText(/^(Settings|设置)$/)
        .last()
        .click({ timeout: 8000 });
    }
    stage = 'select-auriga';
    await page
      .getByText('Auriga', { exact: true })
      .last()
      .click({ timeout: 8000 });
    stage = 'content';
    await page.locator('.auriga-settings-content').waitFor({ timeout: 8000 });
    stage = 'status';
    const expected = label.endsWith('signed-out')
      ? /^(未登录|Not signed in)$/
      : label.endsWith('offline')
        ? /总部不可达|Platform unreachable/
        : /消息网络 就绪|Message network Ready/;
    await page
      .locator('.auriga-status-card [role=status]')
      .filter({ hasText: expected })
      .first()
      .waitFor({ timeout: 10000 });
    stage = 'screenshot';
    await page.screenshot({
      path: join(state, label + '.png'),
      timeout: 10000,
    });
    (report.screenshots ??= []).push({ label, status: 'passed' });
    await page.keyboard.press('Escape');
  } catch (error) {
    (report.screenshots ??= []).push({
      label,
      status: 'failed',
      error: error.name,
      stage,
    });
    await writeFile(
      join(state, label + '-ui.txt'),
      await page
        .locator('body')
        .innerText()
        .catch(() => ''),
    );
    await page
      .screenshot({ path: join(state, label + '-failure.png'), timeout: 5000 })
      .catch(() => {});
    await page.keyboard.press('Escape').catch(() => {});
  }
}
async function skipLogin(page, offline) {
  await page.goto('dsh-app://app/auriga/local-login');
  const skip = page.locator('[data-action=skip]');
  await skip.waitFor();
  if (offline)
    await page
      .locator('[role=alert]')
      .filter({ hasText: /Platform unreachable|总部不可达/ })
      .waitFor({ timeout: 30000 });
  await skip.click();
  await page.waitForURL('dsh-app://app/');
}

try {
  report.stage = 'install';
  app = await launch('install');
  const launchURL = await poll(
    () => app.launchURL(),
    'native_launch_url_missing',
  );
  const base = new URL(launchURL).origin,
    cookies = new Map();
  const accept = (r) => {
    for (const line of r.headers.getSetCookie()) {
      const pair = line.split(';')[0],
        at = pair.indexOf('=');
      cookies.set(pair.slice(0, at), pair.slice(at + 1));
    }
  };
  const first = await fetch(launchURL, { redirect: 'manual' });
  accept(first);
  await first.arrayBuffer();
  const installRequest = async (path, body) => {
    const r = await fetch(base + path, {
      method: body === undefined ? 'GET' : 'POST',
      headers: {
        cookie: [...cookies].map(([k, v]) => k + '=' + v).join('; '),
        ...(body === undefined
          ? {}
          : {
              origin: base,
              'content-type': 'application/json',
              'x-auriga-settings': '1',
            }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(180000),
    });
    accept(r);
    if (!r.ok) throw new Error('native_http_' + r.status);
    return r.json();
  };
  const client = nativeClient(installRequest);
  report.stage = 'install-bundle';
  const isolationSpec = resolve(spec, '../auriga-offline-isolation-0.0.0.tgz');
  await client.rpc('pluginManager/installBundle', {
    spec,
    options: { enabled: true, requestId: randomUUID() },
  });
  report.stage = 'save-platform';
  await poll(async () => {
    try {
      return await installRequest('/auriga/settings/state');
    } catch {
      return null;
    }
  }, 'settings_missing');
  await installRequest('/auriga/settings/save', {
    platformOrigin: connect.platformOrigin,
    deployment: 'local',
  });
  await app.stop();
  app = undefined;
  const installedFixture = await promisify(execFile)(
    runtime.executable,
    [
      '--expose-internals',
      runtime.cli,
      'plugin',
      '--profile',
      'desktop',
      'add',
      isolationSpec,
      '--ignore-scripts',
      '--store-dir',
      join(state, 'cache/pnpm-home/store'),
    ],
    {
      env: { ...env, ELECTRON_RUN_AS_NODE: '1' },
      cwd: join(state, 'cwd'),
      windowsHide: true,
      timeout: 180000,
      maxBuffer: 8 * 1024 * 1024,
    },
  );
  await writeFile(
    join(state, 'isolation-cli.log'),
    installedFixture.stdout + installedFixture.stderr,
  );
  const installedProfile = JSON.parse(
    await readFile(join(state, 'dsh/profiles/desktop/package.json'), 'utf8'),
  );
  assert.ok(
    installedProfile.dependencies['auriga-dsh'] &&
      installedProfile.dependencies['auriga-offline-isolation'],
    'both isolated plugins must remain installed',
  );
  await writeFile(
    join(state, 'dsh/profiles/desktop/cordis.patch.yml'),
    '- id: auriga-offline-isolation\n  disabled: false\n  config: ' +
      JSON.stringify({
        file: switchFile,
        targets: [connect.platformOrigin, connect.network?.wssUrl].filter(
          Boolean,
        ),
      }) +
      '\n- id: auriga-dsh\n  disabled: false\n  config: ' +
      JSON.stringify({ connect }) +
      '\n',
  );
  model = await startFixture();
  const provider =
    'cloud-desk-' + randomUUID().replaceAll('-', '').slice(0, 12);
  report.stage = 'offline-cold-start';
  await writeFile(switchFile, 'offline');
  app = await launch('offline', true);
  await poll(async () => {
    try {
      return !!(await readFile(switchFile + '.active', 'utf8'));
    } catch {
      return false;
    }
  }, 'isolation_plugin_not_loaded');
  const local = nativeClient((path, body) => request(app.page, path, body));
  await configureFixtureModel(local, model.urls.desktop, provider, []);
  await assertLocal(app.page, 'offline-cold-start-local-prompt', provider);
  assert.equal((await auth(app.page, 'status')).loggedIn, false);
  await captureSettings(app.page, 'settings-signed-out');
  await skipLogin(app.page, true);
  await assertLocal(
    app.page,
    'unreachable-login-skipped-local-prompt',
    provider,
  );
  report.stage = 'team-login';
  await writeFile(switchFile, 'online');
  await poll(
    async () => {
      const probe = await request(app.page, '/auriga/settings/test', {
        platformOrigin: connect.platformOrigin,
      });
      return probe.reachable;
    },
    'platform_probe_not_reachable',
    30000,
  );
  await skipLogin(app.page, false);
  assert.equal((await auth(app.page, 'status')).loggedIn, false);
  await assertLocal(app.page, 'team-login-skipped-local-prompt', provider);
  await login(app.page, member);
  report.steps.push('team-login-ready');
  await captureSettings(app.page, 'settings-ready');
  report.stage = 'offline-after-login';
  await writeFile(switchFile, 'offline');
  await poll(
    async () => (await auth(app.page, 'status')).network?.connected === false,
    'network_did_not_disconnect',
  );
  await assertLocal(app.page, 'signed-in-offline-local-prompt', provider);
  await captureSettings(app.page, 'settings-offline');
  report.stage = 'automatic-recovery';
  await writeFile(switchFile, 'online');
  await poll(
    async () => (await auth(app.page, 'status')).network?.connected === true,
    'network_did_not_recover',
    90000,
  );
  report.steps.push('automatic-recovery-ready');
  await auth(app.page, 'logout');
  await assertLocal(app.page, 'platform-logout-local-prompt', provider);
  await app.stop();
  app = undefined;
  report.stage = 'final-cold-start';
  await writeFile(switchFile, 'offline');
  app = await launch('final', true);
  await assertLocal(
    app.page,
    'final-offline-cold-start-local-prompt',
    provider,
  );
  report.status = report.screenshots?.some((x) => x.status !== 'passed')
    ? 'failed'
    : 'passed';
  if (report.status !== 'passed') process.exitCode = 1;
} catch (error) {
  if (app)
    await writeFile(
      join(state, 'failure-ui.txt'),
      await app.page
        .locator('body')
        .innerText()
        .catch(() => ''),
    );
  await app?.page
    .screenshot({
      timeout: 5000,
      path: join(state, 'failure.png'),
      fullPage: true,
    })
    .catch(() => {});
  report.failure = {
    name: error.name,
    code: error.code,
    stage: report.stage,
    rpcMethod: error.rpcMethod,
    remoteCode: error.remoteCode,
    message:
      String(error.message).match(
        /native_http_\d+|Failed to fetch|native_startup_not_ready|owned_electron_exited/,
      )?.[0] ??
      (/^[a-z0-9_]+$/.test(error.message)
        ? error.message
        : 'acceptance_failed'),
    frame: String(error.stack)
      .split('\n')
      .find((r) => r.includes('desktop-offline.mjs:'))
      ?.trim(),
  };
  process.exitCode = 1;
} finally {
  if (app) {
    await app.stop();
    report.cleanup.push('owned-electron-stopped');
  }
  await model?.close();
  report.crashLogCount = (
    await readdir(join(state, 'electron/logs')).catch(() => [])
  ).filter((n) => n.startsWith('crash-')).length;
  if (report.crashLogCount) {
    report.status = 'failed';
    process.exitCode = 1;
  }
  await writeFile(join(state, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report));
}
