import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { createLocalResources } from '../src/local-resources/service.js';
import { createLocalResourceHandler } from '../src/local-resources/http.js';

// Explicit installed host package, never a running DSH_HOME or a source-tree mock.
test(
  'installed DSH rc.2: real Include/Loader/settings/profile enablement and config persistence',
  { skip: !process.env.AURIGA_DSH_PACKAGE_JSON },
  async () => {
    const requireHost = createRequire(
      pathToFileURL(process.env.AURIGA_DSH_PACKAGE_JSON),
    );
    const load = (name) =>
      import(pathToFileURL(requireHost.resolve(name)).href);
    const [
      { boot, initProfile, loadProfileDirectory, readProfilePatches },
      { default: Manager },
      { default: ConfigEditor },
      { default: Settings },
      Connection,
      { default: Credentials },
    ] = await Promise.all([
      load('@deepseek-ai/dsh-app-boot'),
      load('@deepseek-ai/dsh-plugin-manager'),
      load('@deepseek-ai/dsh-config-editor'),
      load('@deepseek-ai/dsh-settings'),
      load('@deepseek-ai/dsh-client-connection'),
      load('@deepseek-ai/dsh-credentials-local'),
    ]);
    const home = await mkdtemp(join(tmpdir(), 'auriga-resource-real-host-'));
    let ctx, server;
    try {
      const dir = join(home, 'profile'),
        anchor = join(home, 'package.json');
      await writeFile(anchor, '{"name":"test-installation","dependencies":{}}');
      initProfile(dir, ['fixture']);
      const fixture = join(dir, 'node_modules/fixture'),
        example = join(dir, 'node_modules/example-plugin');
      await mkdir(fixture, { recursive: true });
      await mkdir(example, { recursive: true });
      await writeFile(
        join(fixture, 'package.json'),
        JSON.stringify({
          name: 'fixture',
          version: '1.0.0',
          dsh: { bundle: { patch: './cordis.patch.yml' } },
        }),
      );
      await writeFile(
        join(fixture, 'cordis.patch.yml'),
        JSON.stringify([
          {
            insert: [
              { id: 'manager', name: 'cordis:manager' },
              { id: 'config-editor', name: 'cordis:editor' },
              { id: 'settings', name: 'cordis:settings' },
              {
                id: 'credentials',
                name: 'cordis:credentials',
                config: { path: join(home, 'credentials.yaml'), watch: false },
              },
              { id: 'connection', name: 'cordis:connection' },
              {
                id: 'example',
                name: 'example-plugin',
                config: { label: 'first', secret: 'never-expose' },
              },
            ],
          },
        ]),
      );
      await writeFile(
        join(example, 'package.json'),
        JSON.stringify({
          name: 'example-plugin',
          version: '1.2.3',
          type: 'module',
          main: './index.mjs',
        }),
      );
      await writeFile(
        join(example, 'index.mjs'),
        `import z from ${JSON.stringify(pathToFileURL(requireHost.resolve('@deepseek-ai/schemastery')).href)};
      export const Config = z.object({ label: z.string().default('first').volatile(), secret: z.string().role('secret').volatile() });
      export function apply(ctx, config) { ctx.provide('exampleProbe', config); }
    `,
      );
      await writeFile(join(dir, 'cordis.yml'), '[]\n');
      const profile = {
        name: 'test',
        dir,
        patchPath: join(dir, 'cordis.patch.yml'),
        installAnchor: anchor,
        cwd: home,
        home,
        overlays: [],
        telemetryDisabledEnv: undefined,
        startedBundles: loadProfileDirectory('test', dir, anchor).layers.map(
          (l) => l.packageName,
        ),
      };
      ctx = await boot(
        'test',
        join(dir, 'cordis.yml'),
        readProfilePatches('test', profile),
        (root) => {
          root.provide('profileContext', profile);
          root.loader.builtins.manager = Manager;
          root.loader.builtins.editor = ConfigEditor;
          root.loader.builtins.settings = Settings;
          root.loader.builtins.credentials = Credentials;
          root.loader.builtins.connection = Connection;
        },
      );
      const service = createLocalResources({
        getHost: () => ctx,
        mode: 'standalone',
      });
      const item = (await service.list()).items.find(
        (row) => row.name === 'example-plugin',
      );
      assert.ok(item);
      assert.equal(item.canToggle, true);
      const detail = await service.detail(item.id);
      assert.deepEqual(detail.fields, [
        { key: 'label', type: 'string', value: 'first' },
      ]);
      assert.doesNotMatch(JSON.stringify(detail), /never-expose|secret/);
      const result = await service.change('config', {
        id: item.id,
        revision: item.revision,
        configRevision: detail.configRevision,
        changes: [{ key: 'label', value: 'changed' }],
      });
      assert.equal(result.item.fields[0].value, 'changed');
      assert.match(await readFile(profile.patchPath, 'utf8'), /changed/);
      assert.equal(ctx.exampleProbe.secret.get(), 'never-expose');
      const disabled = await service.change('enabled', {
        id: item.id,
        revision: result.item.plugin.revision,
        enabled: false,
      });
      assert.equal(disabled.application, 'restart-required');
      assert.match(await readFile(profile.patchPath, 'utf8'), /disabled: true/);
      // Startup-only persistence is not misreported as a live disable.
      assert.equal(disabled.item.plugin.enabled, true);
      const handler = createLocalResourceHandler({
        service,
        auth: ctx.connection,
      });
      server = createServer((req, res) => {
        if (req.url.startsWith('/?token=')) {
          ctx.connection.authorizeIndex(req, res);
          return;
        }
        void handler(req, res);
      });
      server.listen(0, '127.0.0.1');
      await once(server, 'listening');
      const base = `http://127.0.0.1:${server.address().port}`;
      const exchange = await fetch(
        ctx.connection.authenticatedUrl(base + '/'),
        { redirect: 'manual' },
      );
      assert.equal(exchange.status, 303);
      const cookie = exchange.headers.get('set-cookie').split(';')[0];
      const call = (path = '', extra = {}) =>
        fetch(base + '/auriga/local-resources' + path, {
          headers: {
            cookie,
            'x-auriga-local-resources': '1',
            'content-type': 'application/json',
            origin: base,
            ...extra.headers,
          },
          ...extra,
          signal: globalThis.AbortSignal.timeout(5000),
        });
      assert.equal(
        (
          await fetch(base + '/auriga/local-resources', {
            headers: { 'x-auriga-local-resources': '1' },
          })
        ).status,
        401,
      );
      const listed = await call();
      assert.equal(listed.status, 200);
      assert.ok(
        (await listed.json()).items.some((p) => p.name === 'example-plugin'),
      );
      const blocked = await call('/enabled', {
        method: 'POST',
        headers: {
          cookie,
          origin: 'https://example.com',
          'x-auriga-local-resources': '1',
          'content-type': 'application/json',
        },
        body: JSON.stringify({
          id: item.id,
          revision: disabled.item.plugin.revision,
          enabled: true,
        }),
      });
      assert.equal(blocked.status, 403);
      const changed = await call('/enabled', {
        method: 'POST',
        body: JSON.stringify({
          id: item.id,
          revision: disabled.item.plugin.revision,
          enabled: true,
        }),
      });
      assert.equal(changed.status, 200);
      assert.equal((await changed.json()).application, 'restart-required');
      const [{ default: Timer }, { default: Hmr }] = await Promise.all([
        load('@deepseek-ai/cordis-plugin-timer'),
        load('@deepseek-ai/dsh-hmr'),
      ]);
      // The fixture has completed boot; supply the same readiness barrier the launcher owns.
      ctx.provide('appReady', {
        onReady(listener) {
          listener();
          return () => {};
        },
      });
      await ctx.plugin(Timer);
      await ctx.plugin(Hmr, { root: [], ignored: [], debounce: 0 });
      await ctx.hmr.runExclusive(async () => {});
      const fresh = await service.detail(item.id);
      const liveDisabled = await service.change('enabled', {
        id: item.id,
        revision: fresh.plugin.revision,
        enabled: false,
      });
      assert.equal(liveDisabled.application, 'applied');
      assert.equal(liveDisabled.item.plugin.enabled, false);
      const liveEnabled = await service.change('enabled', {
        id: item.id,
        revision: liveDisabled.item.plugin.revision,
        enabled: true,
      });
      assert.equal(liveEnabled.application, 'applied');
      assert.equal(liveEnabled.item.plugin.enabled, true);
      assert.equal(liveEnabled.item.fields[0].value, 'changed');
      console.log(
        'REAL_DSH_HOST: config persisted/read back; enablement saved/restart-required; secrets preserved',
      );
      console.log(
        'REAL_DSH_HTTP: native signed cookie exchange; unauthenticated 401; cross-origin 403; actual profile mutation',
      );
      console.log(
        'REAL_DSH_HMR: real live disable and re-enable; persisted configuration retained',
      );
    } finally {
      if (server) {
        server.closeAllConnections();
        await new Promise((resolve) => server.close(resolve));
      }
      await ctx?.fiber.dispose();
      await rm(home, { recursive: true, force: true });
    }
  },
);
