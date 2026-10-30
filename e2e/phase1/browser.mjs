import { randomBytes } from 'node:crypto';
import {
  readFile,
  mkdtemp,
  mkdir,
  writeFile,
  rm,
  access,
} from 'node:fs/promises';
import { join, dirname, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import net from 'node:net';
import { clearTimeout } from 'node:timers';
import { setTimeout as delay } from 'node:timers/promises';
import {
  StepError,
  Skip,
  requireThat,
  httpUrl,
  nextTotp,
  traceHeader,
  observeTrace,
  waitSpend,
} from './run.mjs';

// Only local test resources are owned by this adapter. No remote instances or
// registered members are deleted from a shared deployment after acceptance.
export class BrowserSteps {
  constructor(options, config) {
    this.options = options;
    this.config = config;
    this.children = [];
    this.jsonResponses = new Map();
    this.instances = new Map();
    this.state = undefined;
  }

  async context() {
    if (!this.browser) {
      const { chromium } = await import('playwright');
      this.browser = await chromium.launch({
        headless: true,
        ...this.config.browser,
      });
    }
    const context = await this.browser.newContext();
    context.setDefaultTimeout(15000);
    const allowed = new Set([httpUrl(this.options.baseUrl).origin]);
    if (this.config.admin?.url)
      allowed.add(httpUrl(this.config.admin.url).origin);
    if (this.config.cloud?.origin)
      allowed.add(httpUrl(this.config.cloud.origin).origin);
    if (this.desktopOrigin) allowed.add(this.desktopOrigin);
    await context.route('**/api/**', async (route) => {
      try {
        if (!this.state || !allowed.has(new URL(route.request().url()).origin))
          return route.continue();
        const headers = {
          ...route.request().headers(),
          traceparent: traceHeader(this.state),
        };
        const request = route.request();
        const instancePoll =
          request.method() === 'GET' &&
          /^\/api\/workspace\/v1\/instances\/[^/]+$/.test(
            new URL(request.url()).pathname,
          );
        if (
          instancePoll ||
          (request.method() === 'POST' &&
            /\/login\/transactions(?:\/[^/]+\/advance)?$|\/me\/instance\/open$/.test(
              new URL(request.url()).pathname,
            ))
        ) {
          // Retain JSON before a successful login/open navigates the document and
          // Chromium discards the response body. Never retain screenshots/HAR.
          const response = await route.fetch({
            headers,
            maxRedirects: 0,
            timeout: 15000,
          });
          const json = await response.json();
          if (instancePoll) this.instances.set(json.id, json);
          else this.jsonResponses.set(request.url(), json);
          await route.fulfill({ response });
        } else await route.continue({ headers });
      } catch {
        // Do not let Playwright print an exception containing a login payload.
        await route.abort().catch(() => {});
      }
    });
    context.on('response', (response) => {
      if (
        this.state &&
        allowed.has(new URL(response.url()).origin) &&
        new URL(response.url()).pathname.startsWith('/api/')
      ) {
        observeTrace(this.state, response.headers(), response.status());
      }
    });
    return context;
  }

  async json(
    page,
    path,
    { method = 'GET', body, headers = {}, expected = [200] } = {},
  ) {
    // page.evaluate uses the browser's real cookies, Origin and CSRF boundary.
    const result = await page.evaluate(
      async ({ path, method, body, headers }) => {
        const url = new URL(path, globalThis.location.href);
        if (url.origin !== globalThis.location.origin) return { status: 0 };
        const response = await globalThis.fetch(url, {
          method,
          credentials: 'same-origin',
          redirect: 'error',
          headers: {
            ...headers,
            ...(body === undefined
              ? {}
              : { 'content-type': 'application/json' }),
          },
          body: body === undefined ? undefined : JSON.stringify(body),
          signal: globalThis.AbortSignal.timeout(15000),
        });
        let data;
        try {
          data = await response.json();
        } catch {
          data = null;
        }
        return { status: response.status, data };
      },
      { path, method, body, headers },
    );
    requireThat(
      expected.includes(result.status),
      `浏览器API状态${result.status}，预期${expected.join('/')}`,
    );
    return result.data;
  }

  async login(page, origin, credentials, invitationCode) {
    await page.goto(
      new URL(
        invitationCode
          ? `/login?invitation=${encodeURIComponent(invitationCode)}`
          : '/login',
        origin,
      ).href,
    );
    const providers = await this.json(page, '/api/iam/v1/login/providers');
    const provider = credentials.providerId
      ? providers.items.find((item) => item.id === credentials.providerId)
      : providers.items.find(
          (item) =>
            item.connectorType ===
            (credentials.connectorType ||
              (invitationCode ? 'team-account' : 'emergency')),
        );
    requireThat(provider, '没有找到要求的身份源；需配置providerId');
    const created = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/iam/v1/login/transactions' &&
        response.request().method() === 'POST',
    );
    await page.locator(`[data-provider-id="${provider.id}"]`).click();
    let tx = this.retained(await created);
    for (let round = 0; round < 8 && tx.status !== 'completed'; round++) {
      requireThat(
        tx.status === 'pending' && tx.step?.type === 'form' && !tx.step.error,
        '登录步骤失败或需要未支持的交互（仅支持表单与TOTP）',
      );
      const form = page.locator('[data-testid="form-step"]');
      if (tx.step.formId === 'first_totp') {
        const enrollment = tx.step.fields.find(
          (field) => field.name === 'enrollment',
        );
        const text = Object.values(enrollment?.label || {}).find(
          (value) => typeof value === 'string' && /[A-Z2-7]{16,}/.test(value),
        );
        credentials.totpSecret = text?.match(/[A-Z2-7]{16,}/)?.[0];
        requireThat(credentials.totpSecret, '首个TOTP登记未提供密钥');
      }
      for (const field of tx.step.fields) {
        if (['info', 'hidden'].includes(field.kind)) continue;
        let value = credentials[field.name];
        if (field.name === 'otp') {
          value = await nextTotp(credentials);
        }
        requireThat(
          value !== undefined || !field.required,
          '登录表单存在未提供的必填字段',
        );
        if (value === undefined) continue;
        const locator = form.locator(`[name="${field.name}"]`);
        if (field.kind === 'checkbox') await locator.setChecked(Boolean(value));
        else if (field.kind === 'select')
          await locator.selectOption(String(value));
        else await locator.fill(String(value));
      }
      const advanced = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname ===
          `/api/iam/v1/login/transactions/${tx.id}/advance`,
      );
      await form.locator('button[type="submit"]').click();
      const response = await advanced;
      requireThat(response.ok(), '登录步骤提交失败');
      const next = this.retained(response);
      requireThat(
        next.status === 'completed' ||
          (next.step?.formId !== tx.step.formId &&
            !next.step?.error &&
            !next.step?.fieldErrors),
        '登录提交未推进步骤；不自动重放',
      );
      tx = next;
    }
    requireThat(tx.status === 'completed', '登录事务未完成');
    await page.waitForURL((url) => url.pathname !== '/login');
    const me = await this.json(page, '/api/iam/v1/me');
    requireThat(
      me?.account?.id || me?.account?.accountId,
      '登录后me缺少账号身份',
    );
    return me;
  }

  async csrf(page, admin) {
    const mode = await page
      .locator('meta[name="auriga-access-mode"]')
      .getAttribute('content');
    requireThat(mode === 'domain' || mode === 'ip', '缺少可信访问模式meta');
    if (admin && mode === 'ip') {
      const token = randomBytes(32).toString('base64url');
      await this.json(page, '/api/iam/v1/admin/session/csrf-tokens', {
        method: 'POST',
        body: { token },
        headers: { 'X-Auriga-CSRF-Bootstrap': 'admin-session-v1' },
      });
      return { 'X-Auriga-CSRF': token };
    }
    const cookies = await page.context().cookies(page.url());
    const token = cookies.find((cookie) =>
      ['auriga_csrf', '__Host-auriga_csrf'].includes(cookie.name),
    )?.value;
    requireThat(token, '缺少CSRF cookie');
    return { 'X-Auriga-CSRF': token };
  }

  async invite(state) {
    this.state = state;
    const admin = this.config.admin;
    requireThat(
      admin?.url && admin.username && admin.password,
      '必须配置admin.url、username、password（凭据只从文件读）',
    );
    this.adminContext = await this.context();
    const page = await this.adminContext.newPage();
    await this.login(page, httpUrl(admin.url).href, { ...admin });
    const token = await this.csrf(page, true);
    const invitation = await this.json(page, '/api/org/v1/admin/invitations', {
      method: 'POST',
      headers: token,
      expected: [201],
      body: {
        memberType: 'member',
        maxUses: 1,
        displayName: 'phase1-e2e',
        expiresAt: new Date(Date.now() + 3600000).toISOString(),
        ...(this.config.member?.providerId
          ? { providerIds: [this.config.member.providerId] }
          : {}),
      },
    });
    requireThat(invitation?.code && invitation.id, '创建邀请未返回code/id');
    this.member = {
      username: `phase1-${randomBytes(6).toString('hex')}`,
      displayName: 'phase1-e2e',
      password: randomBytes(24).toString('base64url'),
      providerId: this.config.member?.providerId,
      connectorType: 'team-account',
    };
    this.memberContext = await this.context();
    this.portalPage = await this.memberContext.newPage();
    await this.portalPage.goto(
      new URL(
        `/invite/${encodeURIComponent(invitation.code)}`,
        this.options.baseUrl,
      ).href,
    );
    await this.portalPage.locator('.invite-card button').waitFor();
    // Follow the public acceptance link, rather than bypassing the invite page.
    await this.portalPage.locator('.invite-card button').click();
    await this.portalPage.waitForURL((url) => url.pathname === '/login');
    const first = await this.login(
      this.portalPage,
      this.options.baseUrl,
      this.member,
      invitation.code,
    );
    const preview = await this.json(
      this.portalPage,
      `/api/org/v1/invitations/preview/${encodeURIComponent(invitation.code)}`,
    );
    requireThat(
      preview?.valid === false && preview.reason === 'used',
      '邀请未被实际消费',
    );
    // A fresh browser context proves credentials work after registration.
    const previous = this.memberContext;
    this.memberContext = await this.context();
    this.portalPage = await this.memberContext.newPage();
    const second = await this.login(
      this.portalPage,
      this.options.baseUrl,
      this.member,
    );
    requireThat(
      first.account.id === second.account.id,
      '重新登录账号与受邀账号不一致',
    );
    await previous.close();
    return { memberVerified: true };
  }

  async cloud(state) {
    this.state = state;
    const page = this.portalPage;
    await page.goto(this.options.baseUrl);
    this.instances.clear();
    const opened = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname ===
          '/api/workspace/v1/me/instance/open' &&
        response.request().method() === 'POST',
    );
    await page
      .getByRole('button', {
        name: /启动并进入|进入实例|Start and enter|Enter instance/i,
      })
      .click();
    const response = await opened;
    requireThat(response.ok(), '云端instance open失败');
    const result = this.retained(response);
    // Contract returns {instance}; never follow a server supplied arbitrary URL.
    const instance = result.instance;
    requireThat(instance?.id, 'open未返回实例id');
    const deadline = Date.now() + 125000;
    let ready = Boolean(
      instance.ready && ['running', 'pending_restart'].includes(instance.phase),
    );
    while (!ready && Date.now() < deadline) {
      // Install the observer before open so even an immediate READY poll is kept.
      const observed = this.instances.get(instance.id);
      ready = Boolean(
        observed?.id === instance.id &&
        observed.ready &&
        ['running', 'pending_restart'].includes(observed.phase),
      );
      if (page.isClosed()) throw new StepError('云端页面已关闭');
      if (!ready) await delay(200);
    }
    requireThat(ready, '云端实例未在时限内达到running/ready');
    await page
      .locator(this.config.cloud.loggedInSelector)
      .waitFor({ state: 'visible', timeout: 30000 });
    this.cloudPage = page;
    return { ready: true, pluginLoggedIn: true };
  }

  async desktop(state) {
    this.state = state;
    const descriptor = JSON.parse(
      (await readFile(this.options.desktopDsh, 'utf8')).replace(/^\uFEFF/, ''),
    );
    if (!descriptor.dshBin || !descriptor.pluginPackage)
      throw new Skip(
        'PLUGIN-DIST缺少dshBin或pluginPackage；仅接受公开成品tgz安装',
      );
    requireThat(
      descriptor.pluginPackage.endsWith('.tgz'),
      '桌面插件必须是本地成品tgz',
    );
    await access(descriptor.dshBin);
    await access(descriptor.pluginPackage);
    const target = httpUrl(descriptor.url);
    requireThat(
      ['127.0.0.1', '[::1]', 'localhost'].includes(target.hostname),
      '桌面隔离DSH只允许回环URL',
    );
    requireThat(
      descriptor.patchFile,
      '需提供DSH公开--patch文件，将监听绑定为回环地址和临时端口',
    );
    await access(descriptor.patchFile);
    const patch = await readFile(descriptor.patchFile, 'utf8');
    requireThat(
      patch.includes('__PHASE1_PORT__'),
      'DSH patch须包含__PHASE1_PORT__，供每次运行分配随机端口',
    );
    const port = await randomPort();
    target.port = String(port);
    this.desktopOrigin = target.origin;
    const parent = resolve(
      dirname(this.options.output || 'phase1-report.json'),
      '.phase1-desktop',
    );
    await mkdir(parent, { recursive: true });
    this.desktopHome = await mkdtemp(join(parent, 'run-'));
    const home = this.desktopHome;
    const env = desktopEnvironment(home);
    for (const name of ['config', 'data', 'cache', 'tmp', 'roaming', 'local'])
      await mkdir(join(home, name), { recursive: true });
    const profile = join(env.DSH_HOME, 'profiles', 'web');
    await mkdir(profile, { recursive: true });
    await writeFile(
      join(profile, 'package.json'),
      JSON.stringify({
        name: 'phase1-isolated-profile',
        private: true,
        dsh: {
          profile: {
            bundles: ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-web-app'],
          },
        },
      }),
    );
    const npmCli =
      descriptor.npmCli ||
      join(
        dirname(process.execPath),
        'node_modules',
        'npm',
        'bin',
        'npm-cli.js',
      );
    await this.child(
      [
        npmCli,
        'install',
        '--prefix',
        profile,
        '--ignore-scripts',
        '--no-audit',
        '--no-fund',
        resolve(descriptor.pluginPackage),
      ],
      env,
      home,
      true,
    );
    const pkg = JSON.parse(
      await readFile(
        join(profile, 'node_modules', 'auriga-dsh', 'package.json'),
        'utf8',
      ),
    );
    requireThat(pkg.name === 'auriga-dsh', '安装成品包不是auriga-dsh');
    const patchPath = join(home, 'cordis.patch.yml');
    await writeFile(
      patchPath,
      patch.replaceAll('__PHASE1_PORT__', String(port)),
    );
    await this.child(
      [resolve(descriptor.dshBin), 'web', '--patch', patchPath, '--no-open'],
      env,
      home,
      false,
    );
    this.desktopContext = await this.context();
    this.desktopPage = await this.desktopContext.newPage();
    const until = Date.now() + 30000;
    let loaded = false;
    while (Date.now() < until && !loaded) {
      requireThat(
        this.children.at(-1).exitCode === null,
        '隔离桌面DSH进程已退出',
      );
      try {
        await this.desktopPage.goto(target.href, { timeout: 2000 });
        loaded = true;
      } catch {
        await delay(200);
      }
    }
    requireThat(loaded, '隔离桌面DSH未启动');
    await this.actions(
      this.desktopPage,
      this.config.desktop.loginActions,
      this.member,
    );
    await this.desktopPage
      .locator(this.config.desktop.loggedInSelector)
      .waitFor({ state: 'visible' });
    return { installed: true, pluginLoggedIn: true };
  }

  async child(args, env, cwd, wait) {
    const child = spawn(process.execPath, args, {
      cwd,
      env,
      stdio: 'ignore',
      windowsHide: true,
      detached: process.platform !== 'win32',
    });
    this.children.push(child);
    const spawned = new Promise((resolve, reject) => {
      child.once('spawn', resolve);
      child.once('error', () =>
        reject(new StepError('无法启动隔离桌面子进程')),
      );
    });
    const exited = new Promise((resolve) =>
      child.once('exit', (code) => resolve(code)),
    );
    child.phase1Exit = exited;
    await spawned;
    if (wait) {
      let timer;
      try {
        const code = await Promise.race([
          exited,
          new Promise((resolve) => {
            timer = setTimeout(() => resolve('timeout'), 120000);
          }),
        ]);
        requireThat(code === 0, '桌面插件成品包安装失败或超时');
      } finally {
        clearTimeout(timer);
      }
    }
  }

  async actions(page, actions, values) {
    requireThat(
      Array.isArray(actions) && actions.length > 0,
      '缺少公开UI操作步骤',
    );
    for (const action of actions) {
      const locator = page.locator(action.selector);
      if (action.type === 'click') await locator.click();
      else if (action.type === 'fill') {
        let value = action.valueFrom ? values[action.valueFrom] : action.value;
        if (action.valueFrom === 'otp') {
          requireThat(values.totpSecret, '桌面登录需要TOTP但没有密钥');
          value = await nextTotp(values);
        }
        requireThat(typeof value === 'string', 'UI fill缺少值');
        await locator.fill(value);
      } else if (action.type === 'wait')
        await locator.waitFor({ state: 'visible' });
      else throw new StepError('未知UI操作类型（仅click/fill/wait）');
    }
  }

  async messages(state) {
    this.state = state;
    for (const [source, target, settings] of [
      [this.cloudPage, this.desktopPage, this.config.messaging.cloudToDesktop],
      [this.desktopPage, this.cloudPage, this.config.messaging.desktopToCloud],
    ]) {
      requireThat(
        settings?.replyActions &&
          settings.receivedSelector &&
          settings.replyReceivedSelector,
        '双向消息必须配置发送、收到、回复与回复收到UI证据',
      );
      const marker = `phase1-${randomBytes(12).toString('hex')}`;
      await this.actions(source, settings.sendActions, { marker });
      await target
        .locator(settings.receivedSelector)
        .filter({ hasText: marker })
        .waitFor();
      await this.actions(target, settings.replyActions, {
        marker: `reply-${marker}`,
      });
      await source
        .locator(settings.replyReceivedSelector)
        .filter({ hasText: `reply-${marker}` })
        .waitFor();
    }
  }

  async models(state) {
    this.state = state;
    for (const model of this.config.relay.models) {
      for (const [page, settings] of [
        [this.cloudPage, this.config.modelUi.cloud],
        [this.desktopPage, this.config.modelUi.desktop],
      ]) {
        const marker = `phase1-model-${randomBytes(12).toString('hex')}`;
        await this.actions(page, settings?.actions, {
          model: model.name,
          marker,
        });
        requireThat(settings?.responseSelector, '缺少DSH模型响应UI证据');
        // The free fake upstream must echo the unique marker, preventing an old
        // conversation or a static "connected" label from satisfying the check.
        await page
          .locator(settings.responseSelector)
          .filter({ hasText: marker })
          .waitFor();
        requireThat(
          settings.requestIdAttribute,
          'DSH模型回复需提供可关联LiteLLM用量的requestIdAttribute',
        );
        const requestId = await page
          .locator(settings.responseSelector)
          .filter({ hasText: marker })
          .getAttribute(settings.requestIdAttribute);
        await waitSpend(this.config.relay, requestId, state);
      }
    }
  }

  async close() {
    let failure;
    try {
      await this.browser?.close();
    } catch {
      failure = new StepError('浏览器清理失败');
    }
    for (const child of this.children.reverse()) {
      if (child.exitCode !== null || child.signalCode !== null) continue;
      try {
        if (process.platform === 'win32') {
          await new Promise((resolve, reject) => {
            const cleanup = spawn(
              join(process.env.SystemRoot, 'System32', 'taskkill.exe'),
              ['/PID', String(child.pid), '/T', '/F'],
              { windowsHide: true, stdio: 'ignore' },
            );
            cleanup.once('error', reject);
            cleanup.once('exit', () => resolve());
          });
        } else process.kill(-child.pid, 'SIGTERM');
        let timer;
        try {
          const exited = await Promise.race([
            child.phase1Exit.then(() => true),
            new Promise((resolve) => {
              timer = setTimeout(() => resolve(false), 5000);
            }),
          ]);
          requireThat(exited, '桌面临时进程未退出');
        } finally {
          clearTimeout(timer);
        }
      } catch {
        failure = new StepError('桌面临时进程树清理失败');
      }
    }
    if (!failure && this.desktopHome)
      await rm(this.desktopHome, { recursive: true, force: true });
    this.jsonResponses.clear();
    this.instances.clear();
    if (failure) throw failure;
  }

  retained(response) {
    const value = this.jsonResponses.get(response.url());
    this.jsonResponses.delete(response.url());
    requireThat(value, '未捕获登录/实例响应JSON');
    return value;
  }
}

export function desktopEnvironment(home, source = process.env) {
  const env = Object.fromEntries(
    Object.entries(source).filter(([key]) =>
      /^(PATH|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|SYSTEMDRIVE|NODE_EXTRA_CA_CERTS)$/i.test(
        key,
      ),
    ),
  );
  return {
    ...env,
    DSH_HOME: join(home, 'dsh'),
    HOME: home,
    USERPROFILE: home,
    APPDATA: join(home, 'roaming'),
    LOCALAPPDATA: join(home, 'local'),
    XDG_CONFIG_HOME: join(home, 'config'),
    XDG_DATA_HOME: join(home, 'data'),
    XDG_CACHE_HOME: join(home, 'cache'),
    TMP: join(home, 'tmp'),
    TEMP: join(home, 'tmp'),
    TMPDIR: join(home, 'tmp'),
    AURIGA_MODE: 'connect',
    npm_config_cache: join(home, 'npm-cache'),
    npm_config_userconfig: join(home, 'config', 'npmrc'),
    npm_config_globalconfig: join(home, 'config', 'global-npmrc'),
  };
}

async function randomPort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const port = server.address().port;
  await new Promise((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve())),
  );
  return port;
}
