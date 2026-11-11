import { readFile } from 'node:fs/promises';
import { Buffer } from 'node:buffer';
import { JSDOM } from 'jsdom';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
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
dom.window.fetch = () => {
  requests++;
  throw new Error('The built panel must not bypass the host channel');
};
panel.context = {
  locale: 'en',
  theme: 'light',
  mode: 'managed',
  protocol: 1,
  apiBase: 'https://platform.example.com',
};
dom.window.document.body.append(panel);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(panel.shadowRoot?.textContent.includes('Team panel'));
assert.ok(
  panel.shadowRoot
    ?.querySelector('style')
    ?.textContent.includes('--dsw-alias-bg-layer-1'),
);
assert.equal(
  panel.shadowRoot?.querySelector('aside')?.getAttribute('data-theme'),
  'light',
);
assert.ok(panel.shadowRoot?.textContent.includes('managed environment'));
assert.equal(panel.shadowRoot.querySelectorAll('.card').length, 0);
assert.equal(
  panel.shadowRoot.querySelector('[role="alert"]'),
  null,
  'missing transport must show its availability notice without pretending to read',
);
assert.equal(
  requests,
  0,
  'missing host transport must never use browser fetch',
);
for (const [status, message] of [
  [401, 'Your session has expired'],
  [403, 'This account cannot read these resources'],
]) {
  let hostRequests = 0;
  panel.context = {
    ...panel.context,
    fetch: async (path, init) => {
      hostRequests++;
      assert.equal(path, '/api/resource/v1/packages?limit=50');
      assert.equal(init.method, 'GET');
      return Response.json(
        { detail: 'private-authorization-detail' },
        { status },
      );
    },
  };
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(hostRequests, 1);
  assert.ok(
    panel.shadowRoot
      .querySelector('[role="alert"]')
      .textContent.includes(message),
  );
  assert.equal(panel.shadowRoot.querySelectorAll('.card').length, 0);
  assert.ok(!panel.shadowRoot.textContent.includes('No plugins are visible'));
  assert.ok(
    !panel.shadowRoot.textContent.includes('private-authorization-detail'),
  );
  assert.equal(
    requests,
    0,
    'failed authorization must never fall back to browser fetch',
  );
}
panel.context = { ...panel.context, theme: 'dark' };
assert.equal(panel.shadowRoot.querySelector('aside').dataset.theme, 'dark');
assert.equal(
  dom.window.document.querySelector('style'),
  null,
  'styles remain inside Shadow DOM',
);
const tabs = panel.shadowRoot.querySelectorAll('nav button');
assert.equal(tabs.length, 2);
tabs[1].click();
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(
  panel.shadowRoot.textContent.includes(
    'installation inventory is unavailable',
  ),
);
assert.equal(
  requests,
  0,
  'local inventory must not fall back to browser fetch',
);
panel.remove();
dom.window.close();
console.log(
  'Built ES module mounts in a blank document with Shadow DOM and theme styles.',
);
// Exercise the real client adapter with the built element, including its cookie policy.
// The host harness uses native VM modules, so keep that flag local to this child.
execFileSync(
  process.execPath,
  [
    '--experimental-vm-modules',
    fileURLToPath(new URL('./bundle-managed.mjs', import.meta.url)),
  ],
  { stdio: 'inherit' },
);

const panelPackage = JSON.parse(
  await readFile(new URL('../package.json', import.meta.url), 'utf8'),
);
assert.equal(
  Object.keys(panelPackage.dependencies ?? {}).length,
  0,
  'native panel has no runtime package dependencies',
);
assert.doesNotMatch(
  source,
  /\bvue\b|@vue|defineCustomElement|__v_isRef|createApp/,
  'native bundle must not contain a Vue runtime',
);
assert.ok(
  Buffer.byteLength(source) < 40_000,
  'native panel must stay below its 40 kB uncompressed budget',
);
console.log(
  JSON.stringify({
    native: true,
    bytes: Buffer.byteLength(source),
    runtimeDependencies: [],
    noVue: true,
  }),
);
