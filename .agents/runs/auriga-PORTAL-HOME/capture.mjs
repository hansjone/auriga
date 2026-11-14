import { chromium } from './.tools/node_modules/playwright/index.mjs';
import { createServer } from '../../../node_modules/vite/dist/node/index.js';
import fs from 'node:fs/promises';
import path from 'node:path';
import net from 'node:net';
const root = process.cwd();
const dir = path.join(root, '.agents/runs/auriga-PORTAL-HOME');
const browser = await chromium.launch({ channel: 'msedge', headless: true });
let server;
try {
  const page = await browser.newPage({ deviceScaleFactor: 1 });
  page.on('pageerror', (e) => console.log('PAGEERROR', e.message));
  const svg = await fs.readFile('assets/brand/auriga-dsh-icon.svg', 'utf8');
  const frames = [];
  for (const size of [16, 24, 32, 48, 64, 128, 180, 256]) {
    await page.setViewportSize({ width: size, height: size });
    await page.setContent(
      `<style>html,body{margin:0;width:100%;height:100%;overflow:hidden}svg{display:block;width:100%;height:100%}</style>${svg}`,
    );
    const png = await page.screenshot({ omitBackground: true });
    if (size === 32) await fs.writeFile('assets/brand/favicon-32.png', png);
    if (size === 180)
      await fs.writeFile('assets/brand/apple-touch-icon.png', png);
    if (size !== 180) frames.push({ size, png });
  }
  const header = Buffer.alloc(6 + 16 * frames.length);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(frames.length, 4);
  let offset = header.length;
  frames.forEach(({ size, png }, i) => {
    const p = 6 + i * 16;
    header[p] = size === 256 ? 0 : size;
    header[p + 1] = header[p];
    header.writeUInt16LE(1, p + 4);
    header.writeUInt16LE(32, p + 6);
    header.writeUInt32LE(png.length, p + 8);
    header.writeUInt32LE(offset, p + 12);
    offset += png.length;
  });
  await fs.writeFile(
    'assets/brand/auriga-dsh.ico',
    Buffer.concat([header, ...frames.map((f) => f.png)]),
  );
  for (const app of ['portal', 'admin']) {
    const dest = `web/${app}/public/brand`;
    await fs.mkdir(dest, { recursive: true });
    for (const f of ['favicon.svg', 'favicon-32.png', 'apple-touch-icon.png'])
      await fs.copyFile(`assets/brand/${f}`, `${dest}/${f}`);
  }
  process.chdir(path.join(root, 'web/portal'));
  const probe = net.createServer();
  await new Promise((r) => probe.listen(0, '127.0.0.1', r));
  const freePort = probe.address().port;
  await new Promise((r) => probe.close(r));
  server = await createServer({
    root: path.join(root, 'web/portal'),
    server: { port: freePort, strictPort: true, host: '127.0.0.1' },
  });
  await server.listen();
  const port = server.httpServer.address().port;
  await page.route(/^http:\/\/127\.0\.0\.1:\d+\/api\//, async (route) => {
    const url = new URL(route.request().url());
    let status = 200,
      body = {};
    if (url.pathname === '/api/iam/v1/me') {
      status = 401;
      body = { status: 401, code: 'common.unauthenticated' };
    } else if (url.pathname === '/api/iam/v1/login/providers')
      body = {
        items: [
          {
            id: 'team',
            connectorType: 'uac',
            displayName: { 'zh-CN': '团队账号登录', en: 'Team account' },
            order: 0,
            primaryStep: 'qr',
          },
          {
            id: 'emergency',
            connectorType: 'local-password',
            displayName: { 'zh-CN': '应急账号', en: 'Emergency account' },
            order: 1,
            primaryStep: 'password',
          },
        ],
      };
    else if (url.pathname === '/api/resource/v1/desktop/latest')
      body = {
        version: '0.1.0',
        sequence: 4,
        sizeBytes: 73400320,
        publishedAt: '2026-11-14T12:00:00Z',
        sha256: 'a'.repeat(64),
      };
    else {
      status = 404;
      body = { status: 404 };
    }
    await route.fulfill({
      status,
      contentType: 'application/json',
      body: JSON.stringify(body),
    });
  });
  const evidence = [];
  for (const [label, width, height] of [
    ['desktop', 1440, 1100],
    ['mobile', 390, 844],
  ]) {
    await page.setViewportSize({ width, height });
    await page.goto(`http://127.0.0.1:${port}/`);
    await page
      .locator('.download-button')
      .waitFor({ timeout: 60000 })
      .catch(async (e) => {
        console.log(await page.locator('body').innerText());
        throw e;
      });
    await page.screenshot({
      path: path.join(dir, `home-${label}.png`),
      fullPage: true,
    });
    await page.locator('.release-details summary').click();
    await page.screenshot({
      path: path.join(dir, `download-${label}.png`),
      fullPage: true,
    });
    evidence.push({
      page: 'home',
      width,
      overflow: await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
    });
    await page.goto(
      `http://127.0.0.1:${port}/login?returnTo=%2Faccount%2Fsecurity`,
    );
    await page.locator('[data-provider-id="team"]').waitFor();
    await page.screenshot({
      path: path.join(dir, `login-${label}.png`),
      fullPage: true,
    });
    evidence.push({
      page: 'login',
      width,
      overflow: await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
      downloadCards: await page.locator('.desktop-download').count(),
    });
  }
  await fs.writeFile(
    path.join(dir, 'browser-check.json'),
    JSON.stringify(
      {
        note: '本地 Vite 开发服务，API 使用合成匿名会话、登录提供者和发行信息；不是 NAS 部署证据。',
        evidence,
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify(evidence));
} finally {
  await server?.close();
  await browser.close();
}
