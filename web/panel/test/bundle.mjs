import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import assert from 'node:assert/strict';
const dom = new JSDOM('<!doctype html><html><body></body></html>', {
  runScripts: 'outside-only',
});
const source = await readFile(
  new URL('../dist/auriga-team-panel.js', import.meta.url),
  'utf8',
);
// Evaluate the built, self-contained ES module in a fresh document, with its exports removed.
assert.ok(!/^import\s/m.test(source), 'the panel must bundle its runtime');
dom.window.eval(source.replace(/export\s*\{[^}]*\};?\s*$/, ''));
const panel = dom.window.document.createElement('auriga-team-panel');
let requests = 0;
panel.context = {
  locale: 'en',
  theme: 'light',
  mode: 'managed',
  protocol: 1,
  fetch: () => {
    requests++;
    throw new Error('Managed resource requests are prohibited');
  },
};
dom.window.document.body.append(panel);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(panel.shadowRoot?.textContent.includes('Team panel'));
assert.ok(
  panel.shadowRoot?.querySelector('style')?.textContent.includes('--auriga-bg'),
);
assert.equal(
  panel.shadowRoot?.querySelector('aside')?.getAttribute('data-theme'),
  'light',
);
assert.ok(panel.shadowRoot?.textContent.includes('managed environment'));
const tabs = panel.shadowRoot.querySelectorAll('nav button');
assert.equal(tabs.length, 2);
tabs[1].click();
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(
  panel.shadowRoot.textContent.includes(
    'installation inventory is unavailable',
  ),
);
assert.equal(requests, 0, 'built panel must not call a managed member API');
panel.remove();
dom.window.close();
console.log(
  'Built ES module mounts in a blank document with Shadow DOM and theme styles.',
);
