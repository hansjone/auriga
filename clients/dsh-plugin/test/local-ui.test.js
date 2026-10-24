import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';

async function fixture(run) {
  const root = await mkdtemp(join(tmpdir(), 'auriga-local-surface-'));
  try {
    await mkdir(join(root, 'src/managed'), { recursive: true });
    await mkdir(join(root, 'ui'));
    await writeFile(
      join(root, 'package.json'),
      JSON.stringify({ type: 'module', version: '0.2.3' }),
    );
    await writeFile(
      join(root, 'src/local-ui.js'),
      await readFile(new URL('../src/local-ui.js', import.meta.url)),
    );
    await writeFile(
      join(root, 'src/managed/protocol.js'),
      await readFile(new URL('../src/managed/protocol.js', import.meta.url)),
    );
    const source = 'export const packaged = true;';
    const integrity =
      'sha384-' + createHash('sha384').update(source).digest('base64');
    await writeFile(join(root, 'ui/auriga-team-panel.js'), source);
    await writeFile(
      join(root, 'ui/metadata.json'),
      JSON.stringify({ version: '2.4.6', integrity }),
    );
    const { createLocalSurfaceHandler } = await import(
      pathToFileURL(join(root, 'src/local-ui.js'))
    );
    await run({ root, source, integrity, createLocalSurfaceHandler });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}
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
test('local packaged routes expose only unconfigured package metadata and fixed same-origin code', async () =>
  fixture(async ({ source, integrity, createLocalSurfaceHandler }) => {
    for (const mode of ['standalone', 'connect']) {
      const handler = createLocalSurfaceHandler({
        mode,
        auth: { requestRejection() {} },
      });
      const boot = result();
      handler({ method: 'GET', url: '/auriga/bootstrap' }, boot);
      const body = JSON.parse(boot.body);
      assert.equal(boot.status, 200);
      assert.equal(body.status, 'unconfigured');
      assert.equal(body.mode, mode);
      assert.equal(body.fetchBase, null);
      assert.equal(body.pluginVersion, '0.2.3');
      assert.equal(body.localPanel.version, '2.4.6');
      assert.equal(body.localPanel.source, 'package');
      assert.equal(body.localPanel.integrity, integrity);
      assert.deepEqual(Object.keys(body).sort(), [
        'fetchBase',
        'localPanel',
        'mode',
        'pluginVersion',
        'protocol',
        'status',
      ]);
      assert.doesNotMatch(boot.body, /platformUrl|token|key|signature/i);
      const code = result();
      handler({ method: 'GET', url: '/auriga/local-panel.js' }, code);
      assert.equal(code.body, source);
      assert.equal(code.headers['x-content-type-options'], 'nosniff');
    }
  }));
test('local routes preserve native authentication, exact paths and read-only methods', async () =>
  fixture(async ({ createLocalSurfaceHandler }) => {
    for (const status of [401, 403]) {
      const handler = createLocalSurfaceHandler({
        mode: 'standalone',
        auth: { requestRejection: () => status },
      });
      const res = result();
      handler({ method: 'GET', url: '/auriga/bootstrap' }, res);
      assert.equal(res.status, status);
      assert.equal(res.body, '');
    }
    const handler = createLocalSurfaceHandler({
      mode: 'standalone',
      auth: { requestRejection() {} },
    });
    for (const [url, method, status] of [
      ['/auriga/bootstrap', 'POST', 405],
      ['/auriga/local-panel.js', 'PUT', 405],
      ['/auriga/../local-panel.js', 'GET', 404],
      ['/auriga/%2e%2e/package.json', 'GET', 404],
      ['/auriga/local-panel.js/', 'GET', 404],
    ]) {
      const res = result();
      handler({ method, url }, res);
      assert.equal(res.status, status);
      assert.equal(res.body, '');
    }
  }));
test('tampered and missing package UI fail closed without private filesystem detail', async () =>
  fixture(async ({ root, createLocalSurfaceHandler }) => {
    const handler = createLocalSurfaceHandler({
      mode: 'standalone',
      auth: { requestRejection() {} },
    });
    await writeFile(join(root, 'ui/auriga-team-panel.js'), 'tampered');
    const bad = result();
    handler({ method: 'GET', url: '/auriga/bootstrap' }, bad);
    assert.equal(bad.status, 503);
    assert.equal(JSON.parse(bad.body).code, 'common.unavailable');
    assert.doesNotMatch(bad.body, /tampered|auriga-local-surface/);
    await rm(join(root, 'ui/metadata.json'));
    const missing = result();
    handler({ method: 'GET', url: '/auriga/local-panel.js' }, missing);
    assert.equal(missing.status, 503);
    assert.doesNotMatch(missing.body, /ENOENT|auriga-local-surface/);
  }));
