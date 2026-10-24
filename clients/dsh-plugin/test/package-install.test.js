import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL, fileURLToPath } from 'node:url';

const source = fileURLToPath(new URL('../', import.meta.url));
test('package declares an installable root bundle and separate library/client exports', async () => {
  const pkg = JSON.parse(await readFile(join(source, 'package.json'), 'utf8'));
  assert.equal(pkg.dsh.bundle.patch, './cordis.patch.yml');
  assert.equal(pkg.exports['.'], './src/plugin.js');
  assert.equal(pkg.exports['./library'], './src/index.js');
  assert.equal(pkg.exports['./client'], './src/client.js');
  assert.ok(pkg.files.includes('cordis.patch.yml'));
  assert.match(
    await readFile(join(source, 'cordis.patch.yml'), 'utf8'),
    /name: auriga-dsh\s/,
  );
});

test(
  'Desktop rc.3 real installBundle: prepared directory or release tar, actual Node/browser routes',
  { skip: !process.env.AURIGA_DESKTOP_DSH_ROOT, timeout: 120000 },
  async () => {
    const root = process.env.AURIGA_DESKTOP_DSH_ROOT;
    const req = createRequire(pathToFileURL(join(root, 'package.json')));
    const load = (name) => import(pathToFileURL(req.resolve(name)).href);
    const [
      { boot, initProfile, readProfilePatches },
      Manager,
      Web,
      Credentials,
      Connection,
      Timer,
      Hmr,
      Modules,
    ] = await Promise.all([
      load('@deepseek-ai/dsh-app-boot'),
      load('@deepseek-ai/dsh-plugin-manager'),
      load('@deepseek-ai/dsh-host-webserver'),
      load('@deepseek-ai/dsh-credentials-local'),
      load('@deepseek-ai/dsh-client-connection'),
      load('@deepseek-ai/cordis-plugin-timer'),
      load('@deepseek-ai/dsh-hmr'),
      load('@deepseek-ai/dsh-client-modules'),
    ]);
    const managerPackage = JSON.parse(
      await readFile(
        req.resolve('@deepseek-ai/dsh-plugin-manager/package.json'),
        'utf8',
      ),
    );
    assert.equal(managerPackage.version, '0.2.1-rc.3');
    console.log(`HOST=${managerPackage.version} NODE=${process.versions.node}`);
    const home = await mkdtemp(join(tmpdir(), 'auriga-packlink-'));
    let ctx;
    try {
      const dir = join(home, 'profile'),
        anchor = join(home, 'package.json');
      await writeFile(
        anchor,
        '{"name":"isolated-installation","dependencies":{}}',
      );
      initProfile(dir, []);
      await writeFile(join(dir, 'cordis.yml'), '[]\n');
      const profile = {
        name: 'test',
        dir,
        home,
        cwd: home,
        installAnchor: anchor,
        patchPath: join(dir, 'cordis.patch.yml'),
        overlays: [],
        startedBundles: [],
        telemetryDisabledEnv: undefined,
        packageManager: {
          command: process.execPath,
          args: [process.env.AURIGA_DESKTOP_PNPM_CLI],
          env: { ELECTRON_RUN_AS_NODE: '1' },
        },
      };
      ctx = await boot(
        'test',
        join(dir, 'cordis.yml'),
        readProfilePatches('test', profile),
        (host) => {
          host.provide('profileContext', profile);
          host.provide('appReady', {
            onReady(listener) {
              listener();
              return () => {};
            },
          });
        },
      );
      await ctx.plugin(Web.default, { host: '127.0.0.1', port: 0 });
      await ctx.plugin(Credentials.default, {
        path: join(home, 'credentials.yaml'),
        watch: false,
      });
      await ctx.plugin(Connection);
      await ctx.plugin(Manager.default);
      await ctx.plugin(Timer.default);
      await ctx.plugin(Hmr.default, { root: [], ignored: [], debounce: 0 });
      await ctx.hmr.runExclusive(async () => {});
      await ctx.plugin(Modules.default ?? Modules.ClientModuleRegistry);
      if (!process.env.AURIGA_PACKAGE_INSTALL_SPEC) {
        const bad = join(home, 'missing-bundle');
        await mkdir(bad);
        const original = JSON.parse(
          await readFile(join(source, 'package.json'), 'utf8'),
        );
        delete original.dsh.bundle;
        await writeFile(join(bad, 'package.json'), JSON.stringify(original));
        const rejected = await ctx.pluginManager.installBundle('link:' + bad);
        console.log(
          'MISSING_BUNDLE',
          JSON.stringify({
            application: rejected.application,
            error: rejected.error,
            packageExit: rejected.packageResult?.exitCode,
          }),
        );
        assert.equal(rejected.application, 'failed');
        assert.equal(rejected.error?.code, 'not-bundle');
      }
      const spec = process.env.AURIGA_PACKAGE_INSTALL_SPEC ?? 'link:' + source;
      const installed = await ctx.pluginManager.installBundle(spec, {
        registry: 'https://registry.npmjs.org/',
      });
      console.log('REAL_INSTALL', JSON.stringify(installed));
      assert.equal(installed.application, 'applied');
      const rows = await ctx.pluginManager.listPlugins();
      const row = rows.find((r) => r.moduleName === 'auriga-dsh');
      assert.ok(row);
      assert.equal(row.enabled, true);
      assert.equal(row.fiberPhase, 'active');
      const bundles = await ctx.pluginManager.listBundles();
      assert.equal(bundles.find((b) => b.name === 'auriga-dsh')?.enabled, true);
      const graph = ctx.clientModules.graph();
      console.log('CLIENT_GRAPH', JSON.stringify(graph));
      assert.ok(graph.entries.some((r) => r.id === 'auriga-dsh'));
      assert.match(
        ctx.clientModules.clientPath('auriga-dsh'),
        /src[/\\]client\.js$/,
      );
      const base = `http://127.0.0.1:${ctx.webServer.port}`;
      const exchange = await fetch(
        ctx.connection.authenticatedUrl(base + '/'),
        { redirect: 'manual' },
      );
      // No frontend-static seat in this module-level profile: exchange directly using native authorizeIndex.
      if (exchange.status !== 303) {
        const stop = ctx.webServer.register({
          kind: 'exact',
          path: '/',
          handler(req, res) {
            if (ctx.connection.authorizeIndex(req, res))
              res.end('authenticated');
          },
        });
        try {
          await checkRoutes(ctx, base);
        } finally {
          stop();
        }
      } else await checkRoutes(ctx, base);
    } finally {
      await ctx?.fiber.dispose();
      await rm(home, { recursive: true, force: true });
    }
  },
);

async function checkRoutes(ctx, base) {
  const exchange = await fetch(ctx.connection.authenticatedUrl(base + '/'), {
    redirect: 'manual',
  });
  assert.equal(exchange.status, 303);
  const cookie = exchange.headers.get('set-cookie').split(';')[0];
  const bootstrap = await fetch(base + '/auriga/bootstrap', {
    headers: { cookie },
  });
  assert.equal(bootstrap.status, 200);
  assert.equal((await bootstrap.json()).status, 'unconfigured');
  const ui = await fetch(base + '/auriga/local-panel.js', {
    headers: { cookie },
  });
  assert.equal(ui.status, 200);
  assert.match(await ui.text(), /auriga-team-panel/);
  const row = ctx.clientModules
    .graph()
    .entries.find((r) => r.id === 'auriga-dsh');
  const browser = await fetch(new URL(row.url, base + '/'), {
    headers: { cookie },
  });
  assert.equal(browser.status, 200);
  assert.match(await browser.text(), /auriga-dsh\/client/);
  console.log(
    'REAL_ROUTES: Node bootstrap, packaged UI and collected browser entry all 200',
  );
}
