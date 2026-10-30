// Real browser journey. No mocked routes, injected sessions, model calls or screenshots.
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { readFileSync, writeFileSync, lstatSync } from 'node:fs';
import { resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import {
  assertReady,
  assertEntry,
  assertManaged,
  origin,
  totp,
  validateAccess,
  sharedNodeReady,
  loginOutcome,
  totpNotBefore,
  otpField,
  browserPortalOrigin,
  assertAdminInvitation,
} from './checks.mjs';
import { realImageDigest } from '../lib/real-dsh.mjs';
import { openAurigaPanel } from './dsh-entry.mjs';

export function cloudReadyJourney(options, config, host) {
  const compose = config.composeDir ?? process.env.AURIGA_PHASE1_COMPOSE_DIR;
  const shared = config.shared !== false;
  const report = {
    status: 'pending',
    deploymentKind: shared ? 'shared' : 'fresh',
    steps: [],
    failures: [],
  };
  let activePage;
  let stage = 'preflight';
  let access, portal, page, memberContext, memberId;
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
    report.operation = 'submit-login-form';
    const response = page.waitForResponse(
      (r) =>
        r.request().method() === 'POST' &&
        /\/login\/transactions\/[^/]+\/advance$/.test(
          new URL(r.url()).pathname,
        ),
    );
    const [result] = await Promise.all([
      response,
      page.locator('[data-testid="form-step"] button[type="submit"]').click(),
    ]);
    assert.equal(result.status(), 200);
    const tx = await host.retainedLogin(new URL(result.url()).pathname);
    report.loginOutcomes ??= [];
    report.loginOutcomes.push({
      stage,
      ...loginOutcome(tx, result.headers()['x-trace-id']),
    });
    return tx;
  }

  async function provider(page, type) {
    report.operation = 'read-providers';
    const list = await json(page, '/api/iam/v1/login/providers');
    const matches = list.items.filter((p) => p.connectorType === type);
    assert.equal(matches.length, 1);
    assert.match(matches[0].id, /^[0-9a-f-]{36}$/);
    report.operation = 'select-provider';
    await page.locator(`[data-provider-id="${matches[0].id}"]`).click();
    report.operation = 'wait-login-form';
    await page.locator('[data-testid="form-step"]').waitFor();
  }

  async function fillOtp(page, credentials, field = 'otp') {
    assert.ok(credentials.totpSecret);
    const waitUntil = totpNotBefore(Date.now(), credentials.lastTotpCounter);
    if (waitUntil > Date.now()) await delay(waitUntil - Date.now());
    credentials.lastTotpCounter = Math.floor(Date.now() / 30_000);
    await page
      .locator(`input[name="${field}"]`)
      .fill(totp(credentials.totpSecret));
  }

  async function fillLogin(page, credentials) {
    await page.locator('input[name="username"]').fill(credentials.username);
    await page.locator('input[name="password"]').fill(credentials.password);
    if (
      credentials.totpSecret &&
      (await page.locator('input[name="otp"]').count())
    ) {
      await fillOtp(page, credentials);
    }
    let tx = await submit(page);
    if (
      tx.status === 'pending' &&
      ['totp', 'platform_totp'].includes(tx.step?.formId)
    ) {
      await fillOtp(page, credentials, otpField(tx.step));
      tx = await submit(page);
    }
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

  async function invite() {
    access = validateAccess(
      config.access ??
        JSON.parse(readFileSync(resolve(compose, 'var/access.json'), 'utf8')),
    );
    if (compose)
      report.imageDigest = realImageDigest(
        JSON.parse(readFileSync(resolve(compose, 'var/real-dsh.json'), 'utf8')),
      );
    portal = browserPortalOrigin(access, options.baseUrl, config.portalProxy);
    host.access = access;
    const admin = origin(access.adminOrigin);
    if (config.admin?.url)
      assert.equal(new URL(config.admin.url).origin, admin);
    const enrollment =
      config.admin ??
      JSON.parse(
        readFileSync(
          resolve(compose, 'var/enrollment/emergency-enrollment.json'),
          'utf8',
        ),
      );
    assert.ok(
      enrollment.username && enrollment.password && enrollment.totpSecret,
    );

    await step('browser-launch', async () => {
      const loopback =
        config.resolveLoopback === true ||
        process.env.AURIGA_PHASE1_RESOLVE_LOOPBACK === '1';
      report.entryMode = loopback
        ? 'domain-browser-loopback-resolver'
        : access.mode;
      await host.ensureBrowser();
      return {};
    });
    const context = async () => {
      const ctx = await host.context();
      ctx.on('response', (r) => {
        if (
          r.request().isNavigationRequest() &&
          r.frame().page() === activePage
        )
          report.navigationStatus = r.status();
        // Whitelist diagnostic fields. Never persist bodies, query strings,
        // cookies, headers, DOM, Playwright errors, traces or invitation URLs.
        if (r.status() < 400 || report.failures.length >= 30) return;
        const path = new URL(r.url()).pathname;
        const service =
          /^\/api\/(iam|org|workspace)\//.exec(path)?.[1] ??
          (path.includes('/auriga/') ? 'plugin' : 'page');
        const trace = r.headers()['x-trace-id'];
        const failure = {
          stage,
          service,
          status: r.status(),
          ...(trace && /^[0-9a-f]{32}$/.test(trace) ? { traceId: trace } : {}),
        };
        report.failures.push(failure);
        void r
          .json()
          .then((body) => {
            const safe = loginOutcome({
              step: { type: 'fail', code: body?.code },
            });
            if (safe.code) failure.code = safe.code;
            if (body?.detail === 'instance state=starting')
              failure.routeState = 'starting';
          })
          .catch(() => {});
      });
      return ctx;
    };
    const adminContext = await context();
    const adminPage = await adminContext.newPage();
    activePage = adminPage;
    adminPage.setDefaultTimeout(20_000);
    await step('admin-login', async () => {
      await adminPage.goto(admin + '/login');
      const type = enrollment.providerType ?? 'emergency';
      assert.ok(
        ['emergency', 'team-account'].includes(type),
        'unsupported admin provider type',
      );
      await provider(adminPage, type);
      await fillLogin(adminPage, enrollment);
      await adminPage.waitForURL(admin + '/overview');
      const me = await json(adminPage, '/api/iam/v1/me');
      assert.ok(me.account.id);
      return { accountId: me.account.id };
    });

    if (config.nodeEnrollment !== false)
      await step('node-join', async () => {
        const template = config.nodeTemplateLabel ?? 'real-dsh-e2e';
        assert.match(template, /^[a-z0-9][a-z0-9._-]{0,62}$/);
        await adminPage.goto(admin + '/nodes');
        const nodes = await json(adminPage, '/api/workspace/v1/admin/nodes');
        if (shared) {
          const available = nodes.items.find((node) =>
            sharedNodeReady(node, template),
          );
          assert.ok(
            available,
            'shared deployment has no suitable real DSH node',
          );
          return { nodeId: available.id, reused: true };
        }
        // A fresh installation must have no previously enrolled nodes.
        assert.equal(nodes.items.length, 0);
        await adminPage
          .locator('[aria-controls="node-join-materials"]')
          .click();
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
        const tokenFile = config.nodeJoinTokenFile
          ? resolve(config.nodeJoinTokenFile)
          : resolve(compose, 'var/join/token');
        assert.ok(
          lstatSync(tokenFile).isFile() &&
            !lstatSync(tokenFile).isSymbolicLink(),
        );
        writeFileSync(tokenFile, material.token + '\n', { mode: 0o600 });
        const joined = await poll(
          () => json(adminPage, '/api/workspace/v1/admin/nodes'),
          (value) =>
            value.items.length === 1 && value.items[0].status === 'online',
        );
        const id = joined.items[0].id;
        await adminPage.reload();
        await adminPage
          .getByRole('button', { name: '详情', exact: true })
          .click();
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
          .fill(template);
        await details
          .getByRole('button', { name: '保存配置', exact: true })
          .click();
        await adminPage.getByTestId('confirm-action').click();
        await poll(
          () => json(adminPage, '/api/workspace/v1/admin/nodes'),
          (value) => value.items[0]?.labels?.template === template,
          20_000,
        );
        return { nodeId: id };
      });

    let invitation;
    const suffix = randomBytes(6).toString('hex');
    const member = (host.member = {
      username: `phase1-${suffix}`,
      displayName: `Phase1 ${suffix}`,
      password: randomBytes(24).toString('base64url'),
    });
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
      assertAdminInvitation(invitation, access);
      return {};
    });

    const registrationContext = await context();
    const registration = await registrationContext.newPage();
    activePage = registration;
    registration.setDefaultTimeout(20_000);
    await step('member-register', async () => {
      report.operation = 'open-invitation';
      await registration.goto(invitation);
      report.operation = 'accept-invitation';
      await registration.locator('.invite-card button').click();
      await provider(registration, 'team-account');
      report.operation = 'fill-registration';
      await registration
        .locator('input[name="username"]')
        .fill(member.username);
      await registration
        .locator('input[name="displayName"]')
        .fill(member.displayName);
      await registration
        .locator('input[name="password"]')
        .fill(member.password);
      let tx = await submit(registration);
      if (tx.status === 'pending') {
        assert.equal(tx.step?.formId, 'first_totp');
        // Read the real enrollment displayed by the public form. Never persist it.
        const info = registration.locator('[data-testid="form-step"] .info');
        report.operation = 'read-authenticator-enrollment';
        await info.waitFor();
        const match = /([A-Z2-7]{16,})\s*$/.exec(await info.textContent());
        assert.ok(match, 'first authenticator enrollment was not displayed');
        member.totpSecret = match[1];
        await fillOtp(registration, member, otpField(tx.step));
        tx = await submit(registration);
      }
      assert.equal(tx.status, 'completed');
      await registration.waitForURL(new URL(invitation).origin + '/');
      memberId = (await json(registration, '/api/iam/v1/me')).account.id;
      assert.ok(memberId);
      const preview = await json(
        registration,
        '/api/org/v1/invitations/preview/' +
          new URL(invitation).pathname.split('/').pop(),
      );
      assert.equal(preview.valid, false);
      assert.equal(preview.reason, 'used');
      return { accountId: memberId };
    });
    // A second empty cookie jar proves the new credentials can log in again.
    await registrationContext.close();
    memberContext = host.memberContext = await context();
    page = host.portalPage = await memberContext.newPage();
    activePage = page;
    page.setDefaultTimeout(20_000);
    await step('member-login', async () => {
      await page.goto(portal + '/login');
      await provider(page, 'team-account');
      await fillLogin(page, member);
      await page.waitForURL(portal + '/');
      assert.equal((await json(page, '/api/iam/v1/me')).account.id, memberId);
      return { accountId: memberId };
    });

    if (config.memberCredentialsFile) {
      // Explicit opt-in handoff for a test deployment; never part of the report.
      await step('member-handoff', async () => {
        writeFileSync(
          config.memberCredentialsFile,
          JSON.stringify({
            schemaVersion: 1,
            accountId: memberId,
            portalUrl: portal,
            username: member.username,
            password: member.password,
            totpSecret: member.totpSecret,
          }) + '\n',
          { flag: 'wx', mode: 0o600 },
        );
        return {};
      });
    }

    report.status = 'identity-passed';
    return { memberVerified: true };
  }

  async function cloud() {
    let instance;
    await step('instance-ready', async () => {
      // Keep state observation in the portal when the original UI navigates.
      // Only the original page's real button may issue the open write.
      const observer = await memberContext.newPage();
      try {
        await observer.goto(portal + '/');
        const open = page.waitForResponse(
          (r) =>
            r.request().method() === 'POST' &&
            new URL(r.url()).pathname === '/api/workspace/v1/me/instance/open',
        );
        const [response] = await Promise.all([
          open,
          page.getByRole('button', { name: '启动并进入', exact: true }).click(),
        ]);
        assert.equal(response.status(), 200);
        const trace = response.headers()['x-trace-id'];
        if (/^[0-9a-f]{32}$/.test(trace ?? '')) report.openTraceId = trace;
        const value = await poll(
          () => json(observer, '/api/workspace/v1/me/instances'),
          (result) =>
            result.items.some(
              (item) =>
                item.ownerId === memberId &&
                (item.ready || item.phase === 'failed'),
            ),
          125_000,
        );
        instance = value.items.find((item) => item.ownerId === memberId);
      } finally {
        await observer.close();
      }
      assertReady(instance, memberId);
      return {
        instanceId: instance.id,
        ownerId: instance.ownerId,
        phase: instance.phase,
        ready: instance.ready,
      };
    });
    await step('dsh-page', async () => {
      report.operation = 'wait-instance-entry';
      await page.waitForURL(
        (url) => {
          try {
            assertEntry(url.href, access, instance.id, portal);
            return true;
          } catch {
            return false;
          }
        },
        { timeout: 30_000 },
      );
      report.operation = 'open-plugin-panel';
      return openAurigaPanel(page);
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
      const anonymous = await host.browser.newContext();
      try {
        const denied = await (await anonymous.newPage()).goto(pluginURL.href);
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
    host.cloudPage = page;
    report.status = 'passed';
    return { ready: true, pluginLoggedIn: true };
  }

  async function failed(error) {
    report.status = 'failed';
    report.failedStep = stage;
    report.errorType = [
      'Error',
      'TypeError',
      'ReferenceError',
      'TimeoutError',
      'AssertionError',
    ].includes(error?.name)
      ? error.name
      : 'unknown';
    try {
      report.formState = await activePage?.evaluate(() =>
        [...document.querySelectorAll('.form-step input')].map((input) => ({
          name: ['username', 'displayName', 'password', 'otp', 'code'].includes(
            input.name,
          )
            ? input.name
            : 'other',
          valid: input.validity.valid,
          missing: input.validity.valueMissing,
          patternMismatch: input.validity.patternMismatch,
        })),
      );
      if (stage === 'dsh-page')
        report.surfaceState = await activePage.evaluate(async () => {
          const response = await globalThis.fetch(
            new URL('plugins/', document.baseURI),
            {
              credentials: 'same-origin',
              signal: globalThis.AbortSignal.timeout(15_000),
            },
          );
          const text = await response.text();
          return {
            aurigaLabels: document.querySelectorAll('[aria-label="Auriga"]')
              .length,
            panelElements:
              document.querySelectorAll('auriga-team-panel').length,
            buttons: document.querySelectorAll('button').length,
            moduleCatalogStatus: response.status,
            catalogMentionsAuriga: text.includes('auriga-dsh'),
            catalogMentionsClient: text.includes('auriga-dsh/client'),
          };
        });
    } catch {
      /* The browser may have already closed. */
    }
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
    throw error;
  }

  async function guarded(action) {
    try {
      return await action();
    } catch (error) {
      return failed(error);
    }
  }
  return { invite: () => guarded(invite), cloud: () => guarded(cloud), report };
}
