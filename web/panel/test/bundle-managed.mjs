import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setTimeout, clearTimeout } from 'node:timers';
import { surfaceHost, flush, response } from './shell-host.mjs';

// Exercise the shipped artifact and actual client adapter with contract fixtures,
// without claiming an authorized backend or a real DSH acceptance run.
const packageId = '01900000-0000-7000-8000-000000000001';
const versionId = '01900000-0000-7000-8000-000000000002';
const item = {
  id: packageId,
  tenantId: '01900000-0000-7000-8000-000000000003',
  ownerId: '01900000-0000-7000-8000-000000000004',
  name: 'Built managed plugin',
  kind: 'plugin',
  revision: 1,
  enabled: true,
  defaultVersionId: versionId,
};
const version = {
  id: versionId,
  packageId,
  kind: 'plugin',
  version: '1.2.3',
  digest: `sha256:${'a'.repeat(64)}`,
  state: 'published',
  manifest: {
    kind: 'plugin',
    name: item.name,
    version: '1.2.3',
    description: 'Signed managed capability',
    author: { displayName: 'Example author' },
    license: 'MIT',
    permissions: { tools: [], outboundHosts: [], dataAccess: [] },
    plugin: { packageName: '@example/managed-plugin' },
  },
};
const installed = {
  clientId: '01900000-0000-7000-8000-000000000005',
  installationRevision: 2,
  selectionRevision: 7,
  generation: 9,
  state: 'applied',
  reportSource: 'member',
  resource: {
    identity: {
      tenantId: item.tenantId,
      packageId,
      versionId,
      kind: 'plugin',
      version: version.version,
      manifestDigest: version.digest,
    },
    envelope: { statement: { name: item.name } },
    enabled: true,
    desiredState: 'ENABLED',
    installationMode: 'optional',
  },
};
// The bootstrap fixture supplies fetchBase='/'; the real adapter resolves the
// panel's relative catalog channel against that same-origin host base.
const base = '/catalog';
const calls = [];
let authorizationStatus = 200;
let legacyInventoryRequests = 0;
let installationResult = {
  status: 200,
  body: { items: [], sharedAvailable: false },
};
const host = surfaceHost(
  async (url, init) => {
    const path = new URL(url).pathname;
    if (path.endsWith('/auriga/bootstrap')) return response();
    if (path.endsWith('/auriga/shell/status'))
      return Response.json({
        loggedIn: true,
        connected: true,
        account: { subject: 'fixture-member' },
        network: { connected: true, state: 'connected' },
      });
    assert.equal(new URL(url).origin, 'https://instance.example.com');
    calls.push({ url: new URL(url), init });
    assert.equal(init.method, 'GET');
    assert.equal(init.credentials, 'omit');
    const headers = new globalThis.Headers(init.headers);
    assert.equal(headers.has('cookie'), false);
    assert.equal(headers.has('authorization'), false);
    if (authorizationStatus !== 200)
      return Response.json(
        { detail: 'private-detail' },
        { status: authorizationStatus },
      );
    if (path === `${base}/packages`) return Response.json({ items: [item] });
    if (path === `${base}/packages/${packageId}/versions/${versionId}`)
      return Response.json(version);
    if (path === `${base}/packages/${packageId}/versions`)
      return Response.json({ items: [version] });
    if (path === `${base}/installations`) {
      if (installationResult instanceof Error) throw installationResult;
      return Response.json(installationResult.body, {
        status: installationResult.status,
      });
    }
    assert.fail(`unexpected host request: ${path}`);
  },
  {
    defined: false,
    locale: 'en',
    hostTheme: 'dark',
    osTheme: 'light',
    remote: {
      pluginInventory: {
        async list() {
          legacyInventoryRequests++;
          throw new Error(
            'The unified panel must not read the legacy inventory',
          );
        },
      },
    },
  },
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
  const button = (label) => {
    const found = [...root.querySelectorAll('button')].find(
      (element) => element.textContent === label,
    );
    assert.ok(found, label);
    return found;
  };
  assert.equal(root.querySelector('aside').dataset.theme, 'dark');
  assert.equal(root.querySelectorAll('.card').length, 1);
  const card = root.querySelector('.resource-card');
  assert.equal(
    card.querySelector('.resource-heading h3').textContent,
    item.name,
  );
  assert.ok(
    [...card.querySelectorAll('.resource-meta .resource-badge')].some(
      (badge) => badge.textContent === 'Setup required',
    ),
  );
  assert.equal(
    card.querySelector('.resource-actions .primary').disabled,
    true,
    'unavailable shared installation must keep install disabled',
  );
  assert.deepEqual(
    calls.map((call) => call.url.pathname + call.url.search),
    [
      `${base}/packages?limit=50&kind=plugin`,
      `${base}/installations`,
      `${base}/packages/${packageId}/versions/${versionId}`,
    ],
  );
  button('Select version / update').click();
  await flush();
  assert.equal(
    root.querySelector('.resource-heading h3').textContent,
    item.name,
  );
  const builtVersion = root.querySelector('.resource-version');
  assert.equal(builtVersion.querySelector('h4').textContent, version.version);
  assert.equal(
    builtVersion.querySelector('.resource-badge').textContent,
    'published',
  );
  assert.ok(builtVersion.textContent.includes('@example/managed-plugin'));
  assert.equal(builtVersion.querySelector('.primary').disabled, true);
  assert.equal(
    calls.at(-1).url.pathname + calls.at(-1).url.search,
    `${base}/packages/${packageId}/versions?limit=200`,
  );
  // Even an explicit caller override cannot switch the managed adapter to cookies.
  await panel.context.fetch('catalog/packages?limit=50&kind=plugin', {
    method: 'GET',
    credentials: 'include',
  });
  assert.equal(calls.at(-1).init.credentials, 'omit');

  installationResult = {
    status: 200,
    body: { items: [installed], sharedAvailable: true },
  };
  const requestsBeforeInventory = calls.length;
  button('My resources').click();
  await flush();
  assert.equal(calls.length, requestsBeforeInventory + 1);
  assert.equal(calls.at(-1).url.pathname, `${base}/installations`);
  assert.equal(root.querySelectorAll('.card').length, 1);
  assert.ok(root.textContent.includes(item.name));
  assert.ok(root.textContent.includes('applied'));
  assert.equal(button('Remove').disabled, false);
  for (const failure of [
    {
      status: 503,
      body: {
        code: 'private-inventory-code',
        detail: 'private-inventory-error',
      },
    },
    new Error('private-inventory-transport-error'),
  ]) {
    installationResult = failure;
    button('Refresh').click();
    await flush();
    assert.ok(
      root
        .querySelector('[role="alert"]')
        .textContent.includes('Cannot reach the platform'),
    );
    assert.equal(root.querySelectorAll('.card').length, 0);
    assert.ok(!root.textContent.includes('private-inventory'));
  }
  installationResult = {
    status: 200,
    body: { items: [installed], sharedAvailable: true },
  };
  button('Refresh').click();
  await flush();
  assert.equal(
    root.querySelectorAll('.card').length,
    1,
    'a later successful catalog read recovers installation state',
  );
  for (const [status, hint] of [
    [403, 'Your account or device is not authorized'],
    [401, 'Platform sign-in required'],
  ]) {
    authorizationStatus = 200;
    button('Resource market').click();
    await flush();
    button('Refresh').click();
    await flush();
    button('Select version / update').click();
    await flush();
    authorizationStatus = status;
    button('Refresh').click();
    await flush();
    if (status === 401)
      assert.ok(
        main.root.textContent.includes(hint),
        'the built panel must notify the real shell to request sign-in',
      );
    else
      assert.ok(
        root.querySelector('[role="alert"]').textContent.includes(hint),
      );
    const currentRoot =
      main.root.querySelector('auriga-team-panel')?.shadowRoot;
    assert.ok(currentRoot, 'the shell retains its mounted resource panel');
    assert.equal(
      currentRoot.querySelector('.resource-version'),
      null,
      'failed authorization clears the old resource',
    );
    assert.equal(currentRoot.querySelectorAll('.card').length, 0);
    assert.ok(!currentRoot.textContent.includes(item.name));
    assert.ok(!currentRoot.textContent.includes('private-detail'));
  }
  assert.ok(
    calls.every((call) => call.url.pathname.startsWith(`${base}/`)),
    'the panel only uses the unified host channel',
  );
  assert.equal(
    legacyInventoryRequests,
    0,
    'the unified panel must not fall back to legacy inventory',
  );
  main.close();
  main = undefined;
  host.dispose();
  assert.equal(host.counts.timers.size, 0);
  console.log(
    'Built managed catalog and versions use the cookie-free client adapter; installation recovery and 401/403 failures preserve the host boundary.',
  );
} finally {
  main?.close();
  host.close();
}
