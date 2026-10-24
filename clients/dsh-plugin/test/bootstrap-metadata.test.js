import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { EventEmitter } from 'node:events';
import { createManaged } from '../src/modes/managed.js';
import { createAurigaRouteHandler, PanelError } from '../src/managed/panel.js';
import {
  makeIdentityKeys,
  signPanelManifest,
  panelManifestBase,
  INSTANCE_ID,
  OWNER_ID,
} from './managed-helpers.js';

function result() {
  return {
    status: 0,
    headers: {},
    body: '',
    writeHead(status, headers) {
      this.status = status;
      this.headers = headers ?? {};
    },
    end(body = '') {
      this.body = String(body);
    },
  };
}
test('managed bootstrap returns actual package plugin version and independently signed UI version, without runtime secrets', async () => {
  const keys = makeIdentityKeys();
  const manifest = signPanelManifest(
    keys.privateKey,
    keys.kid,
    panelManifestBase(),
  );
  const runtime = await createManaged({
    env: {
      AURIGA_MODE: 'managed',
      AURIGA_INSTANCE_ID: INSTANCE_ID,
      AURIGA_OWNER_ID: OWNER_ID,
      AURIGA_PLATFORM_URL: 'https://private.example.com',
      AURIGA_INSTANCE_TOKEN: 'private-instance-token',
    },
    config: {
      browserOrigin: {
        scheme: 'https',
        host: 'instance.example.com',
        port: 443,
      },
    },
    identityKeys: keys.keySet,
    panelKeys: keys.keySet,
    startHeartbeat: false,
    watchKeys: false,
    fetchImpl: async () => ({
      ok: true,
      text: async () => JSON.stringify(manifest),
    }),
  });
  let handler;
  const dispose = runtime.apply({
    webServer: {
      server: new EventEmitter(),
      register(route) {
        if (route.path === '/auriga') handler = route.handler;
      },
    },
    connection: { requestRejection() {} },
    provide() {},
    inject() {},
    effect(fn) {
      return fn();
    },
  });
  try {
    const res = result();
    await handler({ url: '/auriga/bootstrap', method: 'GET' }, res);
    const body = JSON.parse(res.body);
    const pkg = JSON.parse(
      readFileSync(new URL('../package.json', import.meta.url), 'utf8'),
    );
    assert.equal(res.status, 200);
    assert.equal(body.pluginVersion, pkg.version);
    assert.equal(body.manifest.version, manifest.version);
    assert.notEqual(body.pluginVersion, body.manifest.version);
    assert.equal(body.platformUrl, null);
    assert.equal(res.headers['cache-control'], 'no-store');
    assert.deepEqual(Object.keys(body).sort(), [
      'fetchBase',
      'manifest',
      'mode',
      'panelBase',
      'platformUrl',
      'pluginVersion',
      'protocol',
    ]);
    assert.doesNotMatch(
      res.body,
      /private\.example|private-instance-token|refreshToken|ownerId|instanceId/,
    );
  } finally {
    dispose();
    runtime.close();
  }
});
test('bootstrap still gates authentication before manifest access and supports only GET', async () => {
  for (const status of [401, 403]) {
    let reads = 0;
    const handler = createAurigaRouteHandler({
      mode: 'managed',
      pluginVersion: '0.0.0',
      auth: { requestRejection: () => status },
      getManifest: async () => {
        reads++;
        return {};
      },
    });
    const res = result();
    await handler({ method: 'GET', url: '/auriga/bootstrap' }, res);
    assert.equal(res.status, status);
    assert.equal(reads, 0);
    assert.doesNotMatch(res.body, /pluginVersion/);
  }
  const handler = createAurigaRouteHandler({
    mode: 'managed',
    pluginVersion: '0.0.0',
    auth: { requestRejection() {} },
    getManifest: async () => {
      throw new Error('must not run');
    },
  });
  const res = result();
  await handler({ method: 'POST', url: '/auriga/bootstrap' }, res);
  assert.equal(res.status, 405);
});
test('bootstrap failures preserve signature/protocol error category but suppress private detail', async () => {
  for (const code of [
    'common.unauthenticated',
    'common.validation_failed',
    'common.precondition_failed',
    'plugin.protocol_unsupported',
  ]) {
    const handler = createAurigaRouteHandler({
      mode: 'managed',
      pluginVersion: '0.0.0',
      auth: { requestRejection() {} },
      getManifest: async () => {
        throw new PanelError(
          code,
          'private-instance-token https://private.example.com',
        );
      },
    });
    const res = result();
    await handler({ method: 'GET', url: '/auriga/bootstrap' }, res);
    assert.equal(res.status, 502);
    assert.equal(JSON.parse(res.body).code, code);
    assert.doesNotMatch(res.body, /private-instance-token|private\.example/);
  }
});
