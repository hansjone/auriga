#!/usr/bin/env node
// Real browser journey. No mocked routes, injected sessions, model calls or screenshots.
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { readFileSync, writeFileSync, mkdirSync, lstatSync } from 'node:fs';
import { resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import {
  assertReady,
  assertEntry,
  assertManaged,
  origin,
  totp,
  validateAccess,
} from './checks.mjs';
import { realImageDigest } from '../lib/real-dsh.mjs';

const root = resolve(import.meta.dirname, '../..');
const output = resolve(import.meta.dirname, 'output');
const report = { status: 'failed', steps: [], failures: [] };
let browser;
let stage = 'preflight';

async function step(name, action) {
  stage = name;
  console.log(`STEP ${name}`);
  const started = new Date().toISOString();
  const detail = await action();
  report.steps.push({
    name,
    status: 'passed',
    started,
    finished: new Date().toISOString(),
    ...detail,
  });
  return detail;
}

async function json(page, path) {
  // Use the browser's actual same-origin cookie jar, never an API-side login.
  const result = await page.evaluate(async (path) => {
    const response = await globalThis.fetch(path, {
      credentials: 'same-origin',
      redirect: 'error',
      cache: 'no-store',
      signal: globalThis.AbortSignal.timeout(15_000),
    });
    return { status: response.status, body: await response.json() };
  }, path);
  assert.equal(result.status, 200);
  return result.body;
}

async function submit(page) {
  const response = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' &&
      /\/login\/transactions\/[^/]+\/advance$/.test(new URL(r.url()).pathname),
  );
  const [result] = await Promise.all([
    response,
    page.locator('[data-testid="form-step"] button[type="submit"]').click(),
  ]);
  assert.equal(result.status(), 200);
  return result.json();
}

async function provider(page, type) {
  const list = await json(page, '/api/iam/v1/login/providers');
  const matches = list.items.filter((p) => p.connectorType === type);
  assert.equal(matches.length, 1);
  assert.match(matches[0].id, /^[0-9a-f-]{36}$/);
  await page.locator(`[data-provider-id="${matches[0].id}"]`).click();
  await page.locator('[data-testid="form-step"]').waitFor();
}

async function fillLogin(page, credentials) {
  await page.locator('input[name="username"]').fill(credentials.username);
  await page.locator('input[name="password"]').fill(credentials.password);
  if (credentials.totpSecret) {
    // Avoid submitting a code at the end of its validity window.
    const remaining = 30_000 - (Date.now() % 30_000);
    if (remaining < 5_000) await delay(remaining + 100);
    await page.locator('input[name="otp"]').fill(totp(credentials.totpSecret));
  }
  const tx = await submit(page);
  assert.equal(tx.status, 'completed');
}

async function poll(read, predicate, timeout = 180_000) {
  const deadline = Date.now() + timeout;
  do {
    const value = await read();
    if (predicate(value)) return value;
    await delay(1_000);
  } while (Date.now() < deadline);
  throw new Error('poll_timeout');
}

try {
  const access = validateAccess(
    JSON.parse(
      readFileSync(resolve(root, 'deploy/compose/var/access.json'), 'utf8'),
    ),
  );
  const imageDigest = realImageDigest(
    JSON.parse(
      readFileSync(resolve(root, 'deploy/compose/var/real-dsh.json'), 'utf8'),
    ),
  );
  report.imageDigest = imageDigest;
  const portal = origin(access.portalOrigin);
  const admin = origin(access.adminOrigin);
  const enrollment = JSON.parse(
    readFileSync(
      resolve(root, 'deploy/compose/var/enrollment/emergency-enrollment.json'),
      'utf8',
    ),
  );
  assert.ok(
    enrollment.username && enrollment.password && enrollment.totpSecret,
  );
  // The browser dependency is isolated from the application workspace lock.
  const { chromium } = await import('playwright');
  await step('browser-launch', async () => {
    const args = [];
    if (process.env.AURIGA_PHASE1_RESOLVE_LOOPBACK === '1') {
      assert.equal(access.mode, 'domain');
      args.push(
        '--no-proxy-server',
        `--host-resolver-rules=MAP ${access.portalOrigin.host} 127.0.0.1,MAP ${access.adminOrigin.host} 127.0.0.1,MAP *.${access.instances.parentDomain} 127.0.0.1`,
      );
      report.entryMode = 'domain-browser-loopback-resolver';
    } else report.entryMode = access.mode;
    browser = await chromium.launch({ headless: true, args });
    return {};
  });
  const context = async () => {
    const ctx = await browser.newContext({ locale: 'zh-CN' });
    ctx.on('response', (r) => {
      // Whitelist diagnostic fields. Never persist bodies, query strings,
      // cookies, headers, DOM, Playwright errors, traces or invitation URLs.
      if (r.status() < 400 || report.failures.length >= 30) return;
      const path = new URL(r.url()).pathname;
      const service =
        /^\/api\/(iam|org|workspace)\//.exec(path)?.[1] ??
        (path.includes('/auriga/') ? 'plugin' : 'page');
      const trace = r.headers()['x-trace-id'];
      report.failures.push({
        stage,
        service,
        status: r.status(),
        ...(trace && /^[0-9a-f]{32}$/.test(trace) ? { traceId: trace } : {}),
      });
    });
    return ctx;
  };
  const adminContext = await context();
  const adminPage = await adminContext.newPage();
  adminPage.setDefaultTimeout(20_000);
  await step('admin-login', async () => {
    await adminPage.goto(admin + '/login');
    await provider(adminPage, 'emergency');
    await fillLogin(adminPage, enrollment);
    await adminPage.waitForURL(admin + '/overview');
    const me = await json(adminPage, '/api/iam/v1/me');
    assert.ok(me.account.id);
    return { accountId: me.account.id };
  });

  await step('node-join', async () => {
    await adminPage.goto(admin + '/nodes');
    const nodes = await json(adminPage, '/api/workspace/v1/admin/nodes');
    // A fresh installation must have no previously enrolled nodes.
    assert.equal(nodes.items.length, 0);
    await adminPage.locator('[aria-controls="node-join-materials"]').click();
    const result = adminPage.waitForResponse(
      (r) =>
        r.request().method() === 'POST' &&
        new URL(r.url()).pathname === '/api/workspace/v1/admin/join-tokens',
    );
    const [response] = await Promise.all([
      result,
      adminPage
        .getByRole('button', { name: '生成加入材料', exact: true })
        .click(),
    ]);
    assert.equal(response.status(), 201);
    const material = await response.json();
    assert.ok(typeof material.token === 'string' && material.token);
    const tokenFile = resolve(root, 'deploy/compose/var/join/token');
    assert.ok(
      lstatSync(tokenFile).isFile() && !lstatSync(tokenFile).isSymbolicLink(),
    );
    writeFileSync(tokenFile, material.token + '\n', { mode: 0o600 });
    const joined = await poll(
      () => json(adminPage, '/api/workspace/v1/admin/nodes'),
      (value) => value.items.length === 1 && value.items[0].status === 'online',
    );
    const id = joined.items[0].id;
    await adminPage.reload();
    await adminPage.getByRole('button', { name: '详情', exact: true }).click();
    const details = adminPage.locator('section.details');
    await details
      .getByRole('button', { name: '添加标签', exact: true })
      .click();
    await details
      .getByRole('textbox', { name: '标签名', exact: true })
      .last()
      .fill('template');
    await details
      .getByRole('textbox', { name: '标签值', exact: true })
      .last()
      .fill('real-dsh-e2e');
    await details
      .getByRole('button', { name: '保存配置', exact: true })
      .click();
    await adminPage.getByTestId('confirm-action').click();
    await poll(
      () => json(adminPage, '/api/workspace/v1/admin/nodes'),
      (value) => value.items[0]?.labels?.template === 'real-dsh-e2e',
      20_000,
    );
    return { nodeId: id };
  });

  let invitation;
  const suffix = randomBytes(6).toString('hex');
  const member = {
    username: `phase1-${suffix}`,
    displayName: `Phase1 ${suffix}`,
    password: randomBytes(24).toString('base64url'),
  };
  await step('invitation', async () => {
    await adminPage.goto(admin + '/invitations');
    await adminPage
      .getByRole('button', { name: '创建普通成员邀请', exact: true })
      .click();
    const form = adminPage.locator('section.details');
    await form
      .getByRole('textbox', { name: '显示名', exact: true })
      .fill(member.displayName);
    await form
      .getByRole('button', { name: '创建普通成员邀请', exact: true })
      .click();
    await adminPage.getByTestId('confirm-action').click();
    const result = adminPage.getByTestId('invitation-url');
    await result.waitFor();
    invitation = (await result.textContent()).trim();
    const url = new URL(invitation);
    assert.equal(url.origin, portal);
    assert.match(url.pathname, /^\/invite\/[^/]+$/);
    assert.equal(url.username + url.password + url.search + url.hash, '');
    return {};
  });

  const registrationContext = await context();
  const registration = await registrationContext.newPage();
  registration.setDefaultTimeout(20_000);
  let memberId;
  await step('member-register', async () => {
    await registration.goto(invitation);
    await registration.locator('.invite-card button').click();
    await provider(registration, 'team-account');
    await registration.locator('input[name="username"]').fill(member.username);
    await registration
      .locator('input[name="displayName"]')
      .fill(member.displayName);
    await registration.locator('input[name="password"]').fill(member.password);
    assert.equal((await submit(registration)).status, 'completed');
    await registration.waitForURL(portal + '/');
    memberId = (await json(registration, '/api/iam/v1/me')).account.id;
    assert.ok(memberId);
    return { accountId: memberId };
  });
  // A second empty cookie jar proves the new credentials can log in again.
  await registrationContext.close();
  const memberContext = await context();
  const page = await memberContext.newPage();
  page.setDefaultTimeout(20_000);
  await step('member-login', async () => {
    await page.goto(portal + '/login');
    await provider(page, 'team-account');
    await fillLogin(page, member);
    await page.waitForURL(portal + '/');
    assert.equal((await json(page, '/api/iam/v1/me')).account.id, memberId);
    return { accountId: memberId };
  });

  let instance;
  let ready;
  await step('instance-ready', async () => {
    // Read only actual UI-initiated open/poll responses; never start via API.
    const responseReady = page.waitForResponse(
      async (r) => {
        const path = new URL(r.url()).pathname;
        if (
          r.status() !== 200 ||
          !/^\/api\/workspace\/v1\/(me\/instance\/open|instances\/[^/]+)$/.test(
            path,
          )
        )
          return false;
        const body = await r.json();
        const value = body.instance ?? body;
        if (
          !value.ready ||
          !['running', 'pending_restart'].includes(value.phase)
        )
          return false;
        ready = value;
        return true;
      },
      { timeout: 125_000 },
    );
    await Promise.all([
      responseReady,
      page.getByRole('button', { name: '启动并进入', exact: true }).click(),
    ]);
    instance = ready;
    assertReady(instance, memberId);
    return {
      instanceId: instance.id,
      ownerId: instance.ownerId,
      phase: instance.phase,
      ready: instance.ready,
    };
  });
  await step('dsh-page', async () => {
    await page.waitForURL(
      (url) => {
        try {
          assertEntry(url.href, access, instance.id);
          return true;
        } catch {
          return false;
        }
      },
      { timeout: 30_000 },
    );
    await page.getByRole('button', { name: 'Auriga', exact: true }).click();
    await page.locator('auriga-team-panel').waitFor({ state: 'visible' });
    return {};
  });
  await step('plugin-login', async () => {
    const pluginURL = new URL('auriga/bootstrap', page.url());
    const boot = await json(page, pluginURL.pathname);
    assertManaged(boot);
    await page.waitForFunction(() => {
      const panel = document.querySelector('auriga-team-panel');
      return (
        panel?.context?.mode === 'managed' &&
        panel.context.protocol === 1 &&
        !!panel.shadowRoot?.querySelector('.resources')
      );
    });
    assert.equal(await page.locator('input[type="password"]').count(), 0);
    // Same public plugin route must refuse a browser with no member session.
    const anonymous = await browser.newContext();
    try {
      const denied = await anonymous.request.get(pluginURL.href, {
        maxRedirects: 0,
      });
      assert.equal(denied.status(), 401);
    } finally {
      await anonymous.close();
    }
    return {
      mode: boot.mode,
      protocol: boot.protocol,
      pluginVersion: boot.pluginVersion,
      anonymousStatus: 401,
    };
  });
  report.status = 'passed';
} catch (error) {
  report.failedStep = stage;
  // Playwright's messages include DOM and full URLs (invitation/ticket secrets).
  report.reason =
    error?.code === 'ENOENT'
      ? 'missing_deployment_material'
      : error?.code === 'ERR_MODULE_NOT_FOUND'
        ? 'missing_browser_dependency'
        : error?.name === 'TimeoutError'
          ? 'timeout'
          : error?.name === 'AssertionError'
            ? 'assertion_failed'
            : 'execution_failed';
  process.exitCode = 1;
} finally {
  try {
    await browser?.close();
  } catch {
    report.status = 'failed';
    report.cleanupFailed = true;
    process.exitCode = 1;
  }
  mkdirSync(output, { recursive: true });
  const serialized = JSON.stringify(report, null, 2) + '\n';
  writeFileSync(
    resolve(
      output,
      `cloud-ready-${Date.now()}-${randomBytes(4).toString('hex')}.json`,
    ),
    serialized,
    { flag: 'wx' },
  );
  writeFileSync(resolve(output, 'cloud-ready.json'), serialized);
  console.log(JSON.stringify(report));
}
