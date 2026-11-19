import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHostSdkLoader } from '../../src/shared/host-sdk.js';
import {
  SDK_INSTALLATION_ANCHOR,
  createInstalledSdkResolver,
} from '../../src/shared/profile-resolution.js';
import { createSharedDownstream } from '../../src/shared/downstream.js';
import { createProductionSharedPorts } from '../../src/shared/production-ports.js';

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');
const skip = !root && 'Requires the pinned rc.3 public SDK';

test('Host SDK loads exactly two approved public exports once and retains their class identity', async () => {
  class Peer {}
  class LlmAdapter {}
  const resolved = [],
    imported = [];
  const loader = createHostSdkLoader({
    resolveModule(name) {
      resolved.push(name);
      return name === '@deepseek-ai/dsh-llm'
        ? 'file:///fixture/llm.js'
        : 'file:///fixture/connection.js';
    },
    async importModule(url) {
      imported.push(url);
      return url.endsWith('llm.js') ? { LlmAdapter } : { Peer };
    },
  });
  const [first, second] = await Promise.all([loader(), loader()]);
  assert.equal(first, second);
  assert.equal(first.Peer, Peer);
  assert.equal(first.LlmAdapter, LlmAdapter);
  assert.equal(Object.isFrozen(first), true);
  assert.deepEqual(resolved, [
    '@deepseek-ai/dsh-client-connection',
    '@deepseek-ai/dsh-llm',
  ]);
  assert.equal(imported.length, 2);
});

test('missing approved SDK fails closed without retrying an Auriga dev dependency', async () => {
  let imports = 0;
  const loader = createHostSdkLoader({
    resolveModule() {
      throw new Error('approved SDK absent');
    },
    importModule() {
      imports++;
      assert.fail('no fallback');
    },
  });
  const first = loader();
  await assert.rejects(first, /approved SDK absent/);
  assert.equal(loader(), first);
  await assert.rejects(loader(), /approved SDK absent/);
  assert.equal(imports, 0);
});

test('missing public classes and mismatched Peer factories are rejected', async () => {
  const loader = createHostSdkLoader({
    resolveModule: (name) => name,
    importModule: async () => ({}),
  });
  await assert.rejects(loader(), /Approved Host SDK is unavailable/);
  assert.throws(() => createSharedDownstream({ runtime: {}, ports: {} }), {
    code: 'common.unavailable',
  });
  assert.throws(
    () =>
      createProductionSharedPorts(
        {},
        {
          Peer: class One {},
          sdk: { Peer: class Two {}, LlmAdapter: class {} },
        },
      ),
    { code: 'common.validation_failed' },
  );
  for (const sdk of [null, false, {}])
    assert.throws(
      () => createProductionSharedPorts({}, { Peer: class {}, sdk }),
      { code: 'common.validation_failed' },
    );
});

test('shared modules have no bare runtime SDK imports left under the separate Auriga install anchor', () => {
  for (const file of ['downstream.js', 'ports-plugin.js', 'plugin.js']) {
    const source = readFileSync(
      new URL('../../src/shared/' + file, import.meta.url),
      'utf8',
    );
    assert.doesNotMatch(source, /(?:from\s*|import\s*\()\s*['"]@deepseek-ai\//);
  }
  const ports = readFileSync(
    new URL('../../src/shared/ports-plugin.js', import.meta.url),
    'utf8',
  );
  assert.match(ports, /await loadHostSdk\(\)/);
  assert.match(ports, /Peer: sdk\.Peer/);
  const host = readFileSync(
    new URL('../../src/shared/plugin.js', import.meta.url),
    'utf8',
  );
  assert.match(host, /sdk: ports\.sdk/);
});

test(
  'actual rc.3 Peer and model base are the same classes used by the approved public SDK',
  { skip },
  async () => {
    const hostRequire = createRequire(join(root, 'package.json'));
    const load = async (name) =>
      import(pathToFileURL(hostRequire.resolve(name)).href);
    const resolveModule = createInstalledSdkResolver({
      requireFrom(anchor) {
        assert.equal(anchor, SDK_INSTALLATION_ANCHOR);
        return hostRequire;
      },
      realpath(path) {
        return path === dirname(SDK_INSTALLATION_ANCHOR)
          ? realpathSync(root)
          : realpathSync(path);
      },
    });
    const sdk = await createHostSdkLoader({ resolveModule })();
    const [{ Peer }, { LlmAdapter, LlmRuntime }, { Context }] =
      await Promise.all([
        load('@deepseek-ai/dsh-client-connection'),
        load('@deepseek-ai/dsh-llm'),
        load('@deepseek-ai/cordis'),
      ]);
    assert.equal(sdk.Peer, Peer);
    assert.equal(sdk.LlmAdapter, LlmAdapter);
    const ctx = new Context();
    const downstream = createSharedDownstream({ runtime: {}, ports: {}, sdk });
    const peer = new sdk.Peer(ctx, {
      principal: { kind: 'authenticated', id: 'sdk-fixture' },
      interactions: 'none',
    });
    let detach;
    try {
      new LlmRuntime(ctx);
      assert.ok(peer instanceof Peer);
      assert.ok(downstream.adapter instanceof LlmAdapter);
      assert.equal(
        Object.getPrototypeOf(Object.getPrototypeOf(downstream.adapter)),
        LlmAdapter.prototype,
      );
      assert.equal(
        downstream.adapter.providerInfo('auriga-shared').id,
        'auriga-shared',
      );
      detach = ctx.llm.registerAdapter(['auriga-shared'], downstream.adapter);
      assert.equal(typeof detach, 'function');
    } finally {
      detach?.();
      downstream.close();
      await peer.dispose();
      await ctx.fiber.dispose();
    }
  },
);
