import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtemp, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import * as Plugin from '../src/plugin.js';
import { registerTeamTools } from '../src/local/team.js';

test(
  'actual rc.3 native admission and local Desk: launch cookie alone never authenticates platform session',
  { skip: !process.env.AURIGA_DESKTOP_DSH_ROOT, timeout: 60000 },
  async () => {
    const req = createRequire(
      pathToFileURL(join(process.env.AURIGA_DESKTOP_DSH_ROOT, 'package.json')),
    );
    const load = (name) =>
      import(pathToFileURL(req.resolve('@deepseek-ai/' + name)).href);
    const [
      { boot, initProfile, readProfilePatches },
      Web,
      Credentials,
      Connection,
      Prompt,
      Tools,
      Projections,
      Sessions,
    ] = await Promise.all([
      load('dsh-app-boot'),
      load('dsh-host-webserver'),
      load('dsh-credentials-local'),
      load('dsh-client-connection'),
      load('dsh-system-prompt'),
      load('dsh-tools'),
      load('dsh-session-projection'),
      load('dsh-session'),
    ]);
    assert.equal(
      JSON.parse(
        await readFile(
          req.resolve('@deepseek-ai/dsh-client-connection/package.json'),
          'utf8',
        ),
      ).version,
      '0.2.1-rc.3',
    );
    const home = await mkdtemp(join(tmpdir(), 'auriga-localconnect-native-')),
      dir = join(home, 'profile');
    let ctx;
    console.log('ISOLATED_PROFILE', home);
    try {
      initProfile(dir, []);
      await writeFile(join(dir, 'cordis.yml'), '[]\n');
      const profile = {
        name: 'localconnect',
        dir,
        home,
        cwd: home,
        installAnchor: join(home, 'package.json'),
        patchPath: join(dir, 'cordis.patch.yml'),
        overlays: [],
        startedBundles: [],
      };
      await writeFile(
        profile.installAnchor,
        '{"name":"isolated-localconnect","dependencies":{}}',
      );
      ctx = await boot(
        'localconnect',
        join(dir, 'cordis.yml'),
        readProfilePatches('localconnect', profile),
        (root) => {
          root.provide('profileContext', profile);
          root.provide('appReady', {
            onReady(fn) {
              fn();
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
      await ctx.plugin(Prompt.default);
      await ctx.plugin(Tools.default, { mode: 'native' });
      await ctx.plugin(Projections.default);
      await ctx.plugin(Sessions.default);
      const memorySession = ctx.sessions.create('native-persistence-negative');
      assert.equal(await ctx.sessions.flush(memorySession), false);
      console.log(
        'RC3_PERSISTENCE_FENCE_PASS: actual session store flush returns false with no durable provider; ACK adapter must reject it',
      );
      const unregisterTools = registerTeamTools(
        ctx.tools,
        [
          {
            name: 'auriga_read_thread',
            description: 'Native adapter contract probe',
            inputSchema: {
              type: 'object',
              additionalProperties: false,
              properties: {
                threadId: { type: 'string' },
                limit: { type: 'integer' },
              },
              required: ['threadId', 'limit'],
            },
          },
        ],
        async (name, args, exec) => {
          assert.equal(name, 'auriga_read_thread');
          assert.equal(args.limit, 1);
          assert.equal(exec.callId, 'native-probe');
          assert.equal(exec.rootCallId, 'native-probe');
          return {
            structuredContent: { page: { items: [] } },
            content: [{ type: 'text', text: 'native-output-rendered' }],
          };
        },
      );
      const output = await ctx.tools.execute({
        name: 'auriga_read_thread',
        callId: 'native-probe',
        arguments: { threadId: 'schema-probe', limit: 1 },
        signal: new AbortController().signal,
      });
      assert.equal(output.isError, false);
      assert.deepEqual(output.content, [
        { type: 'text', text: 'native-output-rendered' },
      ]);
      unregisterTools();
      console.log(
        'RC3_TOOLS_ADAPTER_PASS: actual registry/schema/execute/output signature, no IAM or remote Team success claimed',
      );
      const base = 'http://127.0.0.1:' + ctx.webServer.port;
      const stop = ctx.webServer.register({
        kind: 'exact',
        path: '/',
        handler(request, response) {
          if (ctx.connection.authorizeIndex(request, response))
            response.end('native app');
        },
      });
      await ctx.plugin(Plugin, {
        connect: {
          platformOrigin: 'https://portal.example.com',
          issuer: 'https://portal.example.com',
          clientId: 'auriga-dsh',
          providerId: 'team',
        },
      });
      const exchange = await fetch(
        ctx.connection.authenticatedUrl(base + '/'),
        { redirect: 'manual' },
      );
      assert.equal(exchange.status, 303);
      const nativeCookie = exchange.headers.get('set-cookie').split(';')[0];
      const index = await fetch(base + '/', {
        headers: { cookie: nativeCookie },
        redirect: 'manual',
      });
      assert.equal(index.status, 303);
      assert.equal(index.headers.get('location'), '/auriga/local-login');
      assert.equal(
        (await fetch(base + '/api/', { headers: { cookie: nativeCookie } }))
          .status,
        401,
      );
      const login = await fetch(base + '/auriga/local-login', {
        headers: { cookie: nativeCookie },
      });
      assert.equal(login.status, 200);
      assert.match(await login.text(), /assets\/adapter.js/);
      for (const file of ['adapter.js', 'index.js', 'styles.js'])
        assert.equal(
          (
            await fetch(base + '/auriga/local-login/assets/' + file, {
              headers: { cookie: nativeCookie },
            })
          ).status,
          200,
        );
      const status = await fetch(base + '/auriga/local-login/status', {
          headers: { cookie: nativeCookie },
        }),
        state = await status.json();
      assert.equal(state.loggedIn, false);
      assert.equal(state.connected, false);
      assert.equal(typeof state.csrf, 'string');
      assert.equal(
        (
          await fetch(base + '/auriga/local-login/status', {
            headers: {
              cookie: nativeCookie,
              origin: 'https://other.example.com',
            },
          })
        ).status,
        403,
      );
      const cookie =
        nativeCookie + '; ' + status.headers.get('set-cookie').split(';')[0];
      const noCsrf = await fetch(base + '/auriga/local-login/login', {
        method: 'POST',
        headers: { cookie, origin: base, 'content-type': 'application/json' },
        body: '{"action":"begin"}',
      });
      assert.equal(noCsrf.status, 403);
      const forged = await fetch(base + '/auriga/local-login/login', {
        method: 'POST',
        headers: {
          cookie,
          origin: base,
          'content-type': 'application/json',
          'X-Auriga-CSRF': state.csrf,
        },
        body: '{"action":"complete","actor":"alice"}',
      });
      assert.equal(forged.status, 400);
      const still = await fetch(base + '/api/', { headers: { cookie } });
      assert.equal(still.status, 401);
      stop();
      console.log(
        'RC3_NATIVE_GATE_PASS: real native cookie + Desk HTTP/assets/CSRF/forged complete denied; no IAM success simulated',
      );
    } finally {
      await ctx?.fiber.dispose();
      console.log('PROFILE_EVIDENCE_RETAINED', home);
    }
  },
);
