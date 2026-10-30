import { randomBytes } from 'node:crypto';
import { launchBrowser } from './launch.mjs';
import { cloudReadyJourney } from './cloud-ready.mjs';
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
    this.loginResponses = new Map();
    this.state = undefined;
  }

  async ensureBrowser() {
    if (!this.browser) {
      this.browser = await launchBrowser(this.access, {
        ...this.config.browser,
        loopback:
          this.config.resolveLoopback === true ||
          process.env.AURIGA_PHASE1_RESOLVE_LOOPBACK === '1',
      });
    }
  }

  async context() {
    await this.ensureBrowser();
    const context = await this.browser.newContext({ locale: 'zh-CN' });
    context.setDefaultTimeout(15000);
    const binding = `phase1_${randomBytes(12).toString('hex')}`;
    await context.exposeBinding(binding, (_, path, value) => {
      this.loginResponses.set(path, value);
    });
    await context.addInitScript(
      ({ binding }) => {
        const original = globalThis.fetch;
        globalThis.fetch = async (...args) => {
          const response = await original(...args);
          const path = new URL(response.url).pathname;
          if (/\/login\/transactions\/[^/]+\/advance$/.test(path)) {
            // Observe the real response before the application navigates away.
            // Return it unchanged; retain only control fields, never login tokens.
            const tx = await response.clone().json();
            await globalThis[binding](path, {
              status: tx.status,
              step: tx.step && {
                type: tx.step.type,
                code: /^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$/.test(
                  tx.step.code ?? '',
                )
                  ? tx.step.code
                  : undefined,
                formId: tx.step.formId,
                fields: tx.step.fields?.map(({ name, kind }) => ({
                  name,
                  kind,
                })),
                error: tx.step.error && { code: tx.step.error.code },
              },
            });
          }
          return response;
        };
      },
      { binding },
    );
    const allowed = new Set([httpUrl(this.options.baseUrl).origin]);
    if (this.config.admin?.url)
      allowed.add(httpUrl(this.config.admin.url).origin);
    if (this.access)
      allowed.add(
        new URL(
          `${this.access.adminOrigin.scheme}://${this.access.adminOrigin.host}:${this.access.adminOrigin.port}`,
        ).origin,
      );
    if (this.config.cloud?.origin)
      allowed.add(httpUrl(this.config.cloud.origin).origin);
    if (this.desktopOrigin) allowed.add(this.desktopOrigin);
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

  async invite(state) {
    this.state = state;
    this.journey ??= cloudReadyJourney(this.options, this.config, this);
    return this.journey.invite();
  }

  async retainedLogin(path) {
    const deadline = Date.now() + 15000;
    while (!this.loginResponses.has(path) && Date.now() < deadline)
      await delay(10);
    const value = this.loginResponses.get(path);
    this.loginResponses.delete(path);
    requireThat(value, '未捕获真实登录响应状态');
    return value;
  }

  async cloud(state) {
    this.state = state;
    return this.journey.cloud();
  }

  evidence() {
    return this.journey?.report;
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
    this.loginResponses.clear();
    if (failure) throw failure;
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
