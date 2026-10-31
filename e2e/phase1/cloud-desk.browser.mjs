import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { authenticatorCode } from '../network/support/dual-cli.mjs';
import { poll } from './cloud-desk.desktop.mjs';

export function accessOrigin(value) {
  assert.ok(['http', 'https'].includes(value?.scheme));
  const url = new URL(`${value.scheme}://${value.host}:${value.port}`);
  assert.equal(url.username + url.password + url.search + url.hash, '');
  assert.equal(url.hostname, value.host);
  return url.origin;
}

export function loopbackBrowserArgs(access, enabled) {
  if (!enabled) return [];
  assert.equal(access.mode, 'domain');
  return [
    '--proxy-server=direct://',
    '--proxy-bypass-list=*',
    `--host-resolver-rules=MAP ${access.portalOrigin.host} 127.0.0.1,MAP ${access.adminOrigin.host} 127.0.0.1,MAP *.${access.instances.parentDomain} 127.0.0.1`,
  ];
}

export function assertCloudEntry(value, access, id) {
  const url = new URL(value);
  assert.equal(url.username + url.password + url.search + url.hash, '');
  if (access.mode === 'domain') {
    assert.equal(
      url.origin,
      accessOrigin({
        ...access.instances,
        host: `${id}.${access.instances.parentDomain}`,
      }),
    );
    assert.equal(url.pathname, '/');
  } else {
    assert.equal(access.mode, 'ip');
    assert.equal(url.origin, accessOrigin(access.portalOrigin));
    assert.equal(url.pathname, `/i/${id}/`);
  }
}

async function get(page, path) {
  const response = await page.evaluate(async (path) => {
    const result = await globalThis.fetch(path, {
      credentials: 'same-origin',
      redirect: 'error',
      signal: globalThis.AbortSignal.timeout(45000),
    });
    return { status: result.status, body: await result.json() };
  }, path);
  assert.equal(response.status, 200, 'authenticated public read');
  return response.body;
}
async function provider(page, type) {
  const choices = (await get(page, '/api/iam/v1/login/providers')).items.filter(
    (p) => p.connectorType === type,
  );
  assert.equal(choices.length, 1);
  assert.match(choices[0].id, /^[0-9a-f-]{36}$/);
  await page.locator(`[data-provider-id="${choices[0].id}"]`).click();
  await page.getByTestId('form-step').waitFor();
}
async function submit(page, allowPending = false) {
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
  const value = await result.json();
  if (!allowPending) assert.equal(value.status, 'completed');
  return value;
}

export function loginOtpField(step) {
  const fields = step?.fields?.filter((field) => field.kind === 'otp') ?? [];
  assert.equal(fields.length, 1, 'one production OTP field required');
  assert.ok(['otp', 'code'].includes(fields[0].name));
  return fields[0].name;
}

export function assessManagedPlugin(health, panel) {
  assert.equal(health.status, 200, 'managed Node health must succeed');
  assert.equal(health.body?.status, 'ok');
  assert.equal(health.body?.ready, true);
  assert.equal(health.body?.protocol, 1);
  assert.ok(health.body?.pluginVersion);
  assert.ok(
    [200, 502].includes(panel.status),
    'panel authentication must not be bypassed',
  );
  if (panel.status === 200) assert.equal(panel.body?.mode, 'managed');
  return {
    mode: 'managed',
    pluginVersion: health.body.pluginVersion,
    panelStatus: panel.status,
    panelAvailable: panel.status === 200,
    nodeHealthy: true,
  };
}

export async function openExistingCloud({
  chromium,
  access,
  member,
  instanceId,
  resolveLoopback,
  report,
  stage,
}) {
  const portal = accessOrigin(access.portalOrigin);
  assert.equal(
    member.portalUrl,
    portal,
    'credentials belong to this test installation',
  );
  assert.match(member.accountId, /^[a-f0-9-]{36}$/);
  assert.match(instanceId, /^[a-f0-9-]{36}$/);
  const args = loopbackBrowserArgs(access, resolveLoopback);
  const browser = await chromium.launch({ headless: true, args });
  try {
    const context = await browser.newContext({ locale: 'zh-CN' });
    const page = await context.newPage();
    page.setDefaultTimeout(30000);
    page.on('response', (r) => {
      if (r.status() < 400 || report.httpFailures.length >= 30) return;
      const traceId = r.headers()['x-trace-id'];
      report.httpFailures.push({
        stage: report.stage,
        status: r.status(),
        ...(/^[a-f0-9]{32}$/.test(traceId ?? '') ? { traceId } : {}),
      });
    });
    stage('existing-cloud-member-login');
    await page.goto(portal + '/login');
    await provider(page, 'team-account');
    await page.locator('input[name="username"]').fill(member.username);
    await page.locator('input[name="password"]').fill(member.password);
    const otp = async (name) => {
      assert.ok(member.totpSecret);
      await poll(
        () =>
          Math.floor(Date.now() / 30000) > (member.lastTotpCounter ?? -1) &&
          30000 - (Date.now() % 30000) > 5000,
        'fresh_authenticator_window',
      );
      member.lastTotpCounter = Math.floor(Date.now() / 30000);
      await page
        .locator(`input[name="${name}"]`)
        .fill(authenticatorCode(member.totpSecret));
    };
    if (await page.locator('input[name="otp"]').count()) await otp('otp');
    let tx = await submit(page, true);
    if (
      tx.status === 'pending' &&
      ['totp', 'platform_totp'].includes(tx.step?.formId)
    ) {
      await otp(loginOtpField(tx.step));
      tx = await submit(page, true);
    }
    assert.equal(tx.status, 'completed');
    await page.waitForURL(portal + '/');
    assert.equal(
      (await get(page, '/api/iam/v1/me')).account.id,
      member.accountId,
    );
    stage('existing-cloud-instance-ready');
    const observer = await context.newPage();
    let instance;
    try {
      await observer.goto(portal + '/');
      const response = page.waitForResponse(
        (r) =>
          r.request().method() === 'POST' &&
          new URL(r.url()).pathname === '/api/workspace/v1/me/instance/open',
      );
      const [opened] = await Promise.all([
        response,
        page.getByRole('button', { name: /^(启动并进入|进入实例)$/ }).click(),
      ]);
      assert.equal(opened.status(), 200);
      const trace = opened.headers()['x-trace-id'];
      if (/^[a-f0-9]{32}$/.test(trace ?? '')) report.openTraceId = trace;
      instance = await poll(
        async () => {
          const value = (
            await get(observer, '/api/workspace/v1/me/instances')
          ).items.find(
            (v) => v.id === instanceId && v.ownerId === member.accountId,
          );
          if (value?.phase === 'failed')
            throw Object.assign(new Error('cloud_instance_failed'), {
              code: 'cloud_instance_failed',
            });
          return value?.ready &&
            ['running', 'pending_restart'].includes(value.phase)
            ? value
            : null;
        },
        'existing_cloud_not_ready',
        125000,
      );
    } finally {
      await observer.close();
    }
    await page.waitForURL(
      (url) => {
        try {
          assertCloudEntry(url.href, access, instance.id);
          return true;
        } catch {
          return false;
        }
      },
      { timeout: 30000 },
    );
    const entry = new URL(page.url());
    const inspect = async (path, body, csrf) => {
      const url = new URL(path.replace(/^\//, ''), entry);
      assert.equal(url.origin, entry.origin);
      return page.evaluate(
        async ({ url, body, csrf }) => {
          const response = await globalThis.fetch(url, {
            method: body === undefined ? 'GET' : 'POST',
            credentials: 'same-origin',
            redirect: 'error',
            signal: globalThis.AbortSignal.timeout(45000),
            headers:
              body === undefined
                ? {}
                : {
                    'content-type': 'application/json',
                    ...(csrf ? { 'x-auriga-csrf': csrf } : {}),
                  },
            body: body === undefined ? undefined : JSON.stringify(body),
          });
          let value;
          try {
            value = await response.json();
          } catch {
            /* An HTML error is not an RPC result. */
          }
          return { status: response.status, body: value };
        },
        { url: url.href, body, csrf },
      );
    };
    const request = async (path, body, csrf) => {
      const response = await inspect(path, body, csrf);
      if (response.status !== 200)
        throw Object.assign(new Error('cloud_native_http_failed'), {
          code: 'cloud_native_http_failed',
          status: response.status,
        });
      return response.body;
    };
    stage('cloud-node-plugin-health');
    const health = await inspect('/_auriga/health');
    const panel = await inspect('/auriga/bootstrap');
    const plugin = assessManagedPlugin(health, panel);
    report.cloud = {
      instanceId: instance.id,
      ownerId: member.accountId,
      ready: true,
      browserAuthenticated: true,
      ...plugin,
    };
    return { browser, request, inspect, member, instance, entry: entry.href };
  } catch (error) {
    await browser.close();
    throw error;
  }
}

export async function createCloudMember({
  chromium,
  access,
  enrollment,
  resolveLoopback,
  report,
  stage,
}) {
  const portal = accessOrigin(access.portalOrigin),
    admin = accessOrigin(access.adminOrigin);
  const args = loopbackBrowserArgs(access, resolveLoopback);
  const browser = await chromium.launch({ headless: true, args });
  try {
    const context = await browser.newContext({ locale: 'zh-CN' });
    const adminPage = await context.newPage();
    adminPage.setDefaultTimeout(20000);
    const watch = (page) =>
      page.on('response', (r) => {
        if (r.status() < 400 || report.httpFailures.length >= 30) return;
        const traceId = r.headers()['x-trace-id'];
        report.httpFailures.push({
          stage: report.stage,
          status: r.status(),
          ...(/^[a-f0-9]{32}$/.test(traceId ?? '') ? { traceId } : {}),
        });
      });
    watch(adminPage);
    stage('admin-login');
    await adminPage.goto(admin + '/login');
    await provider(adminPage, 'emergency');
    await adminPage.locator('input[name="username"]').fill(enrollment.username);
    await adminPage.locator('input[name="password"]').fill(enrollment.password);
    await poll(
      () => 30000 - (Date.now() % 30000) > 5000,
      'authenticator_window',
    );
    await adminPage
      .locator('input[name="otp"]')
      .fill(authenticatorCode(enrollment.totpSecret));
    await submit(adminPage);
    await adminPage.waitForURL(admin + '/overview');
    stage('new-member-invitation');
    const suffix = randomBytes(6).toString('hex');
    const member = {
      username: `cloud-desk-${suffix}`,
      displayName: `Cloud Desk ${suffix}`,
      password: randomBytes(24).toString('base64url'),
    };
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
    const invitation = adminPage.getByTestId('invitation-url');
    await invitation.waitFor();
    const url = new URL((await invitation.textContent()).trim());
    assert.equal(url.origin, portal);
    assert.match(url.pathname, /^\/invite\/[^/]+$/);
    assert.equal(url.username + url.password + url.search + url.hash, '');
    const memberContext = await browser.newContext({ locale: 'zh-CN' });
    const page = await memberContext.newPage();
    page.setDefaultTimeout(20000);
    watch(page);
    stage('new-member-registration');
    await page.goto(url.href);
    await page.locator('.invite-card button').click();
    await provider(page, 'team-account');
    for (const name of ['username', 'displayName', 'password'])
      await page.locator(`input[name="${name}"]`).fill(member[name]);
    await submit(page);
    await page.waitForURL(portal + '/');
    const memberId = (await get(page, '/api/iam/v1/me')).account.id;
    assert.match(memberId, /^[0-9a-f-]{36}$/);
    stage('cloud-instance-ready');
    let instance;
    const ready = page.waitForResponse(
      async (r) => {
        if (
          r.status() !== 200 ||
          !/^\/api\/workspace\/v1\/(me\/instance\/open|instances\/[^/]+)$/.test(
            new URL(r.url()).pathname,
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
        instance = value;
        return true;
      },
      { timeout: 125000 },
    );
    await Promise.all([
      ready,
      page.getByRole('button', { name: '启动并进入', exact: true }).click(),
    ]);
    assert.equal(instance.ownerId, memberId);
    assert.equal(instance.ready, true);
    await page.waitForURL(
      (url) => {
        try {
          assertCloudEntry(url.href, access, instance.id);
          return true;
        } catch {
          return false;
        }
      },
      { timeout: 30000 },
    );
    const entry = new URL(page.url());
    const request = async (path, body, csrf) => {
      const target = new URL(path.replace(/^\//, ''), entry);
      assert.equal(target.origin, entry.origin);
      const response = await page.evaluate(
        async ({ url, body, csrf }) => {
          const result = await globalThis.fetch(url, {
            method: body === undefined ? 'GET' : 'POST',
            credentials: 'same-origin',
            redirect: 'error',
            signal: globalThis.AbortSignal.timeout(45000),
            headers:
              body === undefined
                ? {}
                : {
                    'content-type': 'application/json',
                    ...(csrf ? { 'x-auriga-csrf': csrf } : {}),
                  },
            body: body === undefined ? undefined : JSON.stringify(body),
          });
          return { status: result.status, body: await result.json() };
        },
        { url: target.href, body, csrf },
      );
      assert.equal(response.status, 200, 'cloud native public request');
      return response.body;
    };
    stage('cloud-managed-plugin');
    const boot = await request('/auriga/bootstrap');
    assert.equal(boot.mode, 'managed');
    assert.equal(boot.protocol, 1);
    assert.ok(boot.pluginVersion);
    report.cloud = {
      instanceId: instance.id,
      ownerId: memberId,
      ready: true,
      mode: boot.mode,
      pluginVersion: boot.pluginVersion,
      browserAuthenticated: true,
    };
    return { browser, request, member, instance, entry: entry.href };
  } catch (error) {
    await browser.close();
    throw error;
  }
}
