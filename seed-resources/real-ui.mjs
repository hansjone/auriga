import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createHash } from 'node:crypto';
import { chromium } from './browser/node_modules/playwright/index.mjs';
const dir = resolve('seed-resources'),
  runtime = join(dir, 'host'),
  pkg = join(runtime, 'node_modules/auriga-dsh');
await mkdir(pkg, { recursive: true });
await writeFile(
  join(pkg, 'package.json'),
  JSON.stringify({
    name: 'auriga-dsh',
    version: '0.0.0',
    type: 'module',
    main: 'server.mjs',
    exports: {
      '.': './server.mjs',
      './client': './client.js',
      './package.json': './package.json',
    },
    dsh: { client: { platform: 'web', immediately: true } },
  }),
);
const baseline = process.argv.includes('--baseline');
let client = await readFile('clients/dsh-plugin/src/client.js', 'utf8');
if (baseline)
  client = client.replace(
    'hidden: desktop?.required || !resourcesReady ? true : null',
    'hidden: desktop?.required || !resourcesReady',
  );
await writeFile(join(pkg, 'client.js'), client);
const bundle = await readFile('web/panel/dist/auriga-team-panel.js');
await writeFile(join(pkg, 'panel.js'), bundle);
const sri = 'sha384-' + createHash('sha384').update(bundle).digest('base64');
await writeFile(
  join(pkg, 'server.mjs'),
  `import {readFileSync} from 'node:fs';
export const inject=['webServer']; export function apply(ctx){ctx.effect(()=>ctx.webServer.register({kind:'prefix',path:'/auriga',handler(req,res){res.setHeader('cache-control','no-store');if(req.url.split('?')[0]==='/auriga/panel.js'){res.setHeader('content-type','text/javascript');res.end(readFileSync(new URL('./panel.js',import.meta.url)));return;}res.setHeader('content-type','application/json');res.end(JSON.stringify({mode:'managed',protocol:1,pluginVersion:'0.0.0',manifest:{version:'0.0.0',entry:'panel.js',integrity:{'panel.js':'${sri}'}},panelBase:'/auriga/',fetchBase:'/',connection:{connected:true}}));}}));}`,
);
await writeFile(
  join(runtime, 'probe.yml'),
  '- insert:\n    - id: auriga-dsh\n      name: auriga-dsh\n',
);
const env = Object.fromEntries(
  Object.entries(process.env).filter(
    ([k]) => !k.startsWith('DSH_') && !k.startsWith('AURIGA_'),
  ),
);
const home = join(runtime, 'isolated-home-' + Date.now());
await mkdir(home, { recursive: true });
let log = '';
const child = spawn(
  process.execPath,
  [
    join(runtime, 'node_modules/@deepseek-ai/dsh/lib/bin.js'),
    'web',
    '--patch',
    join(runtime, 'probe.yml'),
    '--no-open',
    '--host',
    '127.0.0.1',
    '--port',
    '0',
  ],
  {
    cwd: runtime,
    env: { ...env, DSH_HOME: home, DSH_AGENTS_HOME: join(home, 'agents') },
    windowsHide: true,
  },
);
child.stdout.on('data', (b) => (log += b));
child.stderr.on('data', (b) => (log += b));
let browser, page;
try {
  const deadline = Date.now() + 60000;
  let url;
  while (!url) {
    url = log.match(/http:\/\/127\.0\.0\.1:\d+[^\s]*/)?.[0];
    if (Date.now() > deadline) throw Error('host startup timeout');
    await new Promise((r) => setTimeout(r, 100));
  }
  browser = await chromium.launch({
    headless: true,
    executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
  });
  page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  const requests = [];
  let status = 200;
  await page.route('**/api/resource/v1/packages**', (route) => {
    requests.push({
      path: new URL(route.request().url()).pathname,
      correlationId: route.request().headers()['x-correlation-id'] ?? null,
      status,
    });
    return route.fulfill({
      status,
      contentType: 'application/json',
      body: JSON.stringify(
        status === 200
          ? { items: [] }
          : { code: 'common.forbidden', correlationId: 'seed-ui-forbidden' },
      ),
    });
  });
  await page.goto(url);
  console.log('loaded DSH');
  await page.screenshot({ path: join(dir, 'host-start.png') });
  await writeFile(
    join(dir, 'host-page.txt'),
    await page.locator('body').innerText(),
  );
  const proceed = page.getByRole('button', {
    name: /Continue|\u7ee7\u7eed/,
    exact: true,
  });
  await proceed.waitFor();
  await proceed.click();
  await page.getByText('\u5f00\u59cb\u4f7f\u7528', { exact: true }).waitFor();
  await page.mouse.click(1110, 483);
  await page.getByText('Auriga', { exact: true }).last().click();
  await page
    .getByText('\u8d44\u6e90\u5e02\u573a', { exact: true })
    .first()
    .click();
  if (baseline) {
    await page.locator('auriga-team-panel').waitFor({ state: 'attached' });
    const observed = await page
      .locator('auriga-team-panel')
      .evaluate((el) => ({
        hidden: el.hidden,
        attribute: el.getAttribute('hidden'),
        mounted: !!el.shadowRoot?.textContent,
      }));
    if (!observed.hidden || observed.attribute !== 'false' || !observed.mounted)
      throw Error('baseline mismatch ' + JSON.stringify(observed));
    await page.screenshot({ path: join(dir, 'baseline-hidden.png') });
    await writeFile(
      join(dir, 'baseline-result.json'),
      JSON.stringify(observed, null, 2),
    );
    throw Error('BASELINE_CONFIRMED');
  }
  await page
    .getByText(/No plugins are visible|\u5e02\u573a\u6682\u65e0/)
    .waitFor();
  await page.screenshot({ path: join(dir, 'market-wide.png') });
  const element = page.locator('auriga-team-panel');
  if (!(await element.isVisible())) throw Error('resource element invisible');
  status = 403;
  await element.getByRole('button', { name: /Refresh|\u5237\u65b0/ }).click();
  await page.getByText(/cannot read|\u65e0\u6743\u8bfb\u53d6/).waitFor();
  await page.screenshot({ path: join(dir, 'market-forbidden.png') });
  status = 503;
  await element.getByRole('button', { name: /Retry|\u91cd\u8bd5/ }).click();
  await page.getByText(/unreachable|\u4e0d\u53ef\u8fbe/).waitFor();
  status = 200;
  await element.getByRole('button', { name: /Retry|\u91cd\u8bd5/ }).click();
  await page
    .getByText(/No plugins are visible|\u5e02\u573a\u6682\u65e0/)
    .waitFor();
  await page
    .getByText('\u6211\u7684\u8d44\u6e90', { exact: true })
    .first()
    .click();
  await page.screenshot({ path: join(dir, 'mine-wide.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByText('\u8d44\u6e90\u5e02\u573a', { exact: true })
    .first()
    .click();
  await page.screenshot({ path: join(dir, 'market-narrow.png') });
  const overflow = await page
    .locator('.auriga-app')
    .evaluate((el) => ({ client: el.clientWidth, scroll: el.scrollWidth }));
  if (overflow.scroll > overflow.client + 1)
    throw Error('narrow overflow ' + JSON.stringify(overflow));
  await writeFile(
    join(dir, 'real-ui-result.json'),
    JSON.stringify(
      { host: '0.2.1-rc.3', errors, requests, overflow, visible: true },
      null,
      2,
    ),
  );
  console.log('real rc.3 UI passed');
} catch (error) {
  if (error.message === 'BASELINE_CONFIRMED')
    console.log('baseline hidden=false reproduced');
  else if (page) {
    await page.screenshot({ path: join(dir, 'failure.png') });
    await writeFile(
      join(dir, 'failure.txt'),
      await page.locator('body').innerText(),
    );
  }
  if (error.message !== 'BASELINE_CONFIRMED') throw error;
} finally {
  await browser?.close();
  child.kill();
  await once(child, 'exit');
  await writeFile(
    join(dir, 'real-host.log'),
    log.replace(/token=[^&\s]+/g, 'token=REDACTED'),
  );
}
