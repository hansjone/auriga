import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { launchBrowser } from './launch.mjs';
import { openAurigaPanel } from './dsh-entry.mjs';

for (const language of ['zh', 'en', 'unknown']) {
  test(`真实浏览器仅处理已知宿主引导：${language}`, async () => {
    const notice =
      language === 'zh'
        ? '预览版说明'
        : language === 'en'
          ? 'Preview Notice'
          : '删除实例';
    const next = language === 'zh' ? '继续' : 'Continue';
    const setup =
      language === 'zh'
        ? '添加一个 API Key 开始使用'
        : 'Add an API key to get started';
    const later = language === 'zh' ? '稍后配置' : 'Configure later';
    const server = createServer((_, response) => {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(`<!doctype html><style>[role=dialog]{position:fixed;inset:0;background:white;z-index:10}</style>
        <button aria-label="Auriga" id="auriga">Auriga</button>
        <div role="dialog" aria-label="${notice}"><button id="next">${next}</button></div>
        <script>
        document.querySelector('#next').onclick=()=>{
          document.querySelector('[role=dialog]').remove();
          setTimeout(()=>{const dialog=document.createElement('div');dialog.setAttribute('role','dialog');dialog.setAttribute('aria-label','${setup}');dialog.innerHTML='<input type=password><button>${later}</button>';document.body.append(dialog);dialog.querySelector('button').onclick=()=>dialog.remove();},0);
        };
        document.querySelector('#auriga').onclick=()=>{const panel=document.createElement('auriga-team-panel');panel.textContent='fixture panel';document.body.append(panel);};
        </script>`);
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    let browser;
    try {
      browser = await launchBrowser();
      const page = await browser.newPage();
      await page.goto(`http://127.0.0.1:${server.address().port}`);
      if (language === 'unknown') {
        page.setDefaultTimeout(800);
        await assert.rejects(openAurigaPanel(page), { name: 'TimeoutError' });
        assert.equal(await page.locator('auriga-team-panel').count(), 0);
        assert.equal(
          await page
            .getByRole('dialog', { name: notice, exact: true })
            .isVisible(),
          true,
        );
      } else {
        assert.deepEqual(await openAurigaPanel(page), {
          previewAcknowledged: true,
          modelSetupDeferred: true,
        });
        assert.equal(await page.locator('[role=dialog]').count(), 0);
        assert.equal(await page.locator('auriga-team-panel').isVisible(), true);
      }
    } finally {
      await browser?.close();
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    }
  });
}
