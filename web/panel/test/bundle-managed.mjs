import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setTimeout, clearTimeout } from 'node:timers';
import { surfaceHost, flush, response } from './shell-host.mjs';

// Contract fixtures exercise the shipped artifact and actual client adapter,
// without claiming an authorized production backend or a real DSH acceptance run.
const packageId = '01900000-0000-7000-8000-000000000001';
const versionId = '01900000-0000-7000-8000-000000000002';
const item = {
  id: packageId,
  tenantId: '01900000-0000-7000-8000-000000000003',
  ownerId: '01900000-0000-7000-8000-000000000004',
  name: 'Built managed plugin',
  kind: 'plugin',
  level: 'l2',
  visibility: 'tenant',
  groupIds: [],
  revision: 1,
  createdAt: '2026-10-25T00:00:00Z',
  enabled: true,
  defaultVersionId: versionId,
};
const version = {
  id: versionId,
  packageId,
  version: '1.2.3',
  digest: `sha256:${'a'.repeat(64)}`,
  sizeBytes: 100,
  mediaType: 'application/gzip',
  state: 'published',
  revision: 1,
  createdAt: '2026-10-25T00:00:00Z',
};
const base = '/api/resource/v1/packages';
const calls = [];
let authorized = true;
const host = surfaceHost(
  async (url, init) => {
    const path = new URL(url).pathname;
    if (path.endsWith('/auriga/bootstrap')) return response();
    calls.push({ url: new URL(url), init });
    assert.equal(init.method, 'GET');
    assert.equal(init.credentials, 'omit');
    const headers = new globalThis.Headers(init.headers);
    assert.equal(headers.has('cookie'), false);
    assert.equal(headers.has('authorization'), false);
    if (!authorized)
      return Response.json({ detail: 'private-detail' }, { status: 401 });
    if (path === base) return Response.json({ items: [item] });
    if (path === `${base}/${packageId}`) return Response.json(item);
    if (path === `${base}/${packageId}/versions`)
      return Response.json({ items: [version] });
    assert.fail(`unexpected host request: ${path}`);
  },
  { defined: false, locale: 'en', hostTheme: 'dark', osTheme: 'light' },
);

let main;
try {
  host.document.cookie = 'auriga_portal=browser-cookie; path=/';
  main = host.mount('main');
  await flush();
  const script = host.document.querySelector('script');
  assert.ok(script, 'the real shell must request its verified panel module');
  const source = await readFile(
    new URL('../dist/auriga-team-panel.js', import.meta.url),
    'utf8',
  );
  vm.runInNewContext(source.replace(/export\s*\{[^}]*\};?\s*$/, ''), {
    HTMLElement: host.window.HTMLElement,
    customElements: host.window.customElements,
    document: host.document,
    CustomEvent: host.window.CustomEvent,
    AbortController: globalThis.AbortController,
    URLSearchParams: globalThis.URLSearchParams,
    setTimeout,
    clearTimeout,
  });
  host.completeScript(
    script,
    host.window.customElements.get('auriga-team-panel'),
  );
  await flush();
  const panel = main.root.querySelector('auriga-team-panel');
  assert.ok(
    panel?.shadowRoot,
    'the real shell must mount the built Custom Element',
  );
  const root = panel.shadowRoot;
  assert.equal(root.querySelector('aside').dataset.theme, 'dark');
  assert.equal(root.querySelectorAll('.card').length, 1);
  assert.ok(root.querySelector('.card').textContent.includes(item.name));
  assert.ok(root.textContent.includes('Local installation · Unknown'));
  assert.deepEqual(
    calls.map((call) => call.url.pathname + call.url.search),
    [base + '?limit=50'],
  );
  const details = [...root.querySelectorAll('button')].find(
    (button) => button.textContent === 'View details',
  );
  assert.ok(details);
  details.click();
  await flush();
  assert.equal(root.querySelector('.detail h2').textContent, item.name);
  assert.ok(root.querySelector('.versions').textContent.includes('1.2.3'));
  assert.ok(root.querySelector('.versions').textContent.includes('Published'));
  assert.deepEqual(
    calls.slice(1).map((call) => call.url.pathname),
    [`${base}/${packageId}`, `${base}/${packageId}/versions`],
  );
  // Even an explicit caller override cannot switch the managed adapter to cookies.
  await panel.context.fetch(base, { method: 'GET', credentials: 'include' });
  assert.equal(calls.at(-1).init.credentials, 'omit');
  authorized = false;
  const refresh = [...root.querySelectorAll('button')].find(
    (button) => button.textContent === 'Refresh',
  );
  refresh.click();
  await flush();
  assert.ok(
    root
      .querySelector('[role="alert"]')
      .textContent.includes('Your session has expired'),
  );
  assert.equal(
    root.querySelector('.detail'),
    null,
    'failed authorization clears the old resource',
  );
  assert.ok(!root.textContent.includes(item.name));
  assert.ok(!root.textContent.includes('private-detail'));
  main.close();
  main = undefined;
  host.dispose();
  assert.equal(host.counts.timers.size, 0);
  console.log(
    'Built managed catalogue/detail/version render through the real cookie-free client adapter; authorization failure clears data.',
  );
} finally {
  main?.close();
  host.close();
}
