import { test } from 'node:test';
import assert from 'node:assert/strict';
import { surfaceHost, flush, boot, response } from './shell-host.mjs';

for (const mode of ['managed', 'standalone']) {
  test(`local transport stays fixed and independent of platform fetch in ${mode}`, async () => {
    const bootstrap =
      mode === 'managed'
        ? boot()
        : {
            mode,
            protocol: 1,
            status: 'unconfigured',
            pluginVersion: '0.2.3',
            fetchBase: null,
            localPanel: {
              source: 'package',
              entry: 'auriga/local-panel.js',
              version: '2.4.6',
              integrity: 'sha384-YQ==',
            },
          };
    const calls = [];
    const host = surfaceHost(async (url, init) => {
      calls.push({ url: String(url), init });
      return response(200, bootstrap);
    });
    try {
      const main = host.mount('main');
      await flush();
      const context = main.root.querySelector('auriga-team-panel').context;
      assert.equal(typeof context.localResources, 'function');
      if (mode === 'standalone') assert.equal(context.fetch, undefined);
      await context.localResources('/enabled', {
        method: 'POST',
        body: '{}',
        headers: { authorization: 'must-strip', cookie: 'must-strip' },
      });
      const call = calls.at(-1);
      assert.equal(
        call.url,
        'https://instance.example.com/i/example/auriga/local-resources/enabled',
      );
      assert.equal(call.init.credentials, 'same-origin');
      assert.equal(call.init.redirect, 'error');
      assert.equal(call.init.headers['x-auriga-local-resources'], '1');
      assert.equal(call.init.headers.authorization, undefined);
      assert.equal(call.init.headers.cookie, undefined);
      const before = calls.length;
      for (const path of [
        'https://example.com',
        '/../enabled',
        '/api/iam/v1/me',
        '/detail?url=example',
      ])
        await assert.rejects(context.localResources(path), {
          name: 'TypeError',
        });
      assert.equal(calls.length, before);
      main.close();
      host.dispose();
    } finally {
      host.close();
    }
  });
}
