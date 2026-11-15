import fs from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
const { chromium } = await import(
  process.env.AURIGA_PLAYWRIGHT_MODULE ?? 'playwright'
);
process.chdir(fileURLToPath(new URL('../../', import.meta.url)));
const browser = await chromium.launch({ channel: 'msedge', headless: true });
try {
  const page = await browser.newPage({ deviceScaleFactor: 1 });
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
} finally {
  await browser.close();
}
