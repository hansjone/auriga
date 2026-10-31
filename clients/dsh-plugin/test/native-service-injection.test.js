import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { generateKeyPairSync, sign, randomBytes } from 'node:crypto';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { ReadableStream } from 'node:stream/web';
import { createVault } from '../src/local/vault.js';

test(
  'real rc.3 module apply: conditional injection owns tools; missing/removed providers keep the parent door closed',
  { skip: !process.env.AURIGA_DESKTOP_DSH_ROOT, timeout: 30000 },
  async () => {
    const req = createRequire(
        pathToFileURL(
          join(process.env.AURIGA_DESKTOP_DSH_ROOT, 'package.json'),
        ),
      ),
      load = (name) =>
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
      { Service },
    ] = await Promise.all([
      load('dsh-app-boot'),
      load('dsh-host-webserver'),
      load('dsh-credentials-local'),
      load('dsh-client-connection'),
      load('dsh-system-prompt'),
      load('dsh-tools'),
      load('dsh-session-projection'),
      load('dsh-session'),
      load('cordis'),
    ]);
    const Plugin = await import(
      process.env.AURIGA_TEST_PLUGIN_ENTRY ??
        new URL('../src/plugin.js', import.meta.url)
    );
    assert.deepEqual(Plugin.inject, ['webServer', 'connection']);
    const home = await mkdtemp(join(tmpdir(), 'auriga-injection-native-')),
      dir = join(home, 'profile'),
      names = [
        'auriga_list_endpoints',
        'auriga_open_thread',
        'auriga_reply_thread',
        'auriga_read_thread',
      ];
    const { privateKey, publicKey } = generateKeyPairSync('ec', {
        namedCurve: 'prime256v1',
      }),
      jwk = {
        ...publicKey.export({ format: 'jwk' }),
        kid: 'ephemeral-test',
        use: 'sig',
        alg: 'ES256',
      };
    const transportCalls = [],
      identity = {
        tid: '11111111-1111-4111-8111-111111111111',
        sub: '22222222-2222-4222-8222-222222222222',
        did: '33333333-3333-4333-8333-333333333333',
      };
    let origin;
    // Explicit cryptographic/transport fixture, not IAM/password/TOTP evidence.
    const remote = createServer(async (request, response) => {
      const json = (status, body) => {
        response.writeHead(status, { 'content-type': 'application/json' });
        response.end(JSON.stringify(body));
      };
      if (request.method === 'GET' && request.url === '/oauth2/jwks')
        return json(200, { keys: [jwk] });
      let text = '';
      for await (const part of request) text += part;
      let body;
      try {
        body = JSON.parse(text);
      } catch {
        return json(400, {});
      }
      transportCalls.push(body.method ?? request.url);
      if (request.headers.origin !== undefined) return json(403, {});
      if (request.url === '/ai/mcp') {
        const accept = request.headers.accept ?? '';
        if (
          !accept.includes('application/json') ||
          !accept.includes('text/event-stream')
        )
          return json(406, {});
        const result = (value) =>
          json(200, { jsonrpc: '2.0', id: body.id, result: value });
        if (body.method === 'initialize')
          return result({
            protocolVersion: '2025-06-18',
            capabilities: { tools: {} },
            serverInfo: { name: 'test-peer', version: '1' },
          });
        if (body.method === 'notifications/initialized') {
          response.writeHead(202);
          return response.end();
        }
        if (body.method === 'tools/list')
          return result({
            tools: names.map((name) => ({
              name,
              inputSchema: {
                type: 'object',
                properties: {},
                additionalProperties: false,
              },
            })),
          });
        if (body.method === 'ping') return result({});
        return json(400, {});
      }
      if (request.url === '/ai/team/node/register')
        return json(200, {
          endpoint: {
            id: '44444444-4444-4444-8444-444444444444',
            receiveEnabled: true,
          },
        });
      if (request.url === '/ai/team/node/pull')
        return json(200, { page: { items: [] } });
      return json(404, {});
    });
    await new Promise((resolve) => remote.listen(0, '127.0.0.1', resolve));
    origin = 'http://127.0.0.1:' + remote.address().port;
    let ctx,
      plugin,
      provider,
      streamReader,
      aborted = false;
    const streamAborted = Promise.withResolvers();
    console.log('ISOLATED_INJECTION_PROFILE', home);
    try {
      initProfile(dir, []);
      await writeFile(join(dir, 'cordis.yml'), '[]\n');
      const profile = {
        name: 'injection-test',
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
        '{"name":"isolated-injection","dependencies":{}}',
      );
      ctx = await boot(
        'injection-test',
        join(dir, 'cordis.yml'),
        readProfilePatches('injection-test', profile),
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
      // A real Cordis Service supplies dependency capabilities only. Neither
      // method is a fake successful session controller operation.
      class ControllerCapabilities extends Service {
        constructor(owner) {
          super(owner, 'sessionController');
        }
        create() {
          throw new Error('controller calls outside injection probe');
        }
        prompt() {
          throw new Error('controller calls outside injection probe');
        }
      }
      const base = 'http://127.0.0.1:' + ctx.webServer.port;
      ctx.webServer.register({
        kind: 'exact',
        path: '/',
        handler(request, response) {
          if (ctx.connection.authorizeIndex(request, response))
            response.end('native app');
        },
      });
      const launch = await fetch(ctx.connection.authenticatedUrl(base + '/'), {
        redirect: 'manual',
      });
      assert.equal(launch.status, 303);
      const nativeCookie = launch.headers.get('set-cookie').split(';')[0];
      ctx.connection.fetch.register({
        path: '/api/injection-stream',
        methods: ['GET'],
        requestBody: 'buffered',
        fetch(request) {
          return new Response(
            new ReadableStream({
              start(controller) {
                controller.enqueue(new TextEncoder().encode('ready'));
                request.signal.addEventListener(
                  'abort',
                  () => {
                    aborted = true;
                    streamAborted.resolve();
                    try {
                      controller.close();
                    } catch {
                      /* Closed stream. */
                    }
                  },
                  { once: true },
                );
              },
            }),
          );
        },
      });
      const config = {
        connect: {
          platformOrigin: origin,
          issuer: origin,
          clientId: 'test-native-client',
          providerId: 'team',
          allowLocalHttp: true,
        },
      };
      // A preverified credential is sealed through the real vault. Resume still
      // performs actual ES256/JWKS validation and the complete HTTP MCP handshake.
      const now = Math.floor(Date.now() / 1000),
        claims = {
          iss: origin,
          aud: 'auriga',
          ptyp: 'device',
          ctx: 'device',
          scope: 'auriga.connect',
          ...identity,
          iat: now,
          exp: now + 600,
        };
      const encoded = [{ alg: 'ES256', kid: jwk.kid }, claims]
        .map((value) =>
          Buffer.from(JSON.stringify(value)).toString('base64url'),
        )
        .join('.');
      const accessToken =
        encoded +
        '.' +
        sign('sha256', Buffer.from(encoded), {
          key: privateKey,
          dsaEncoding: 'ieee-p1363',
        }).toString('base64url');
      const vault = await createVault(join(dir, 'auriga-connect'));
      await vault.update((s) => {
        s.credential = {
          accessToken,
          refreshToken: randomBytes(32).toString('base64url'),
          claims,
        };
      });
      vault.close();
      if (process.env.AURIGA_TEST_PLUGIN_ENTRY)
        provider = await ctx.plugin(ControllerCapabilities);
      plugin = await ctx.plugin(Plugin, config);
      async function status(cookieValue, expected) {
        const deadline = Date.now() + 5000;
        for (;;) {
          const response = await fetch(base + '/auriga/local-login/status', {
            headers: { cookie: cookieValue },
          });
          if (response.status === expected) return response;
          await response.arrayBuffer();
          if (Date.now() > deadline)
            assert.fail('conditional runtime did not reach status ' + expected);
          await delay(10);
        }
      }
      if (!process.env.AURIGA_TEST_PLUGIN_ENTRY) {
        const pending = await status(nativeCookie, 503);
        assert.equal(pending.status, 503);
        assert.equal(
          (
            await fetch(base + '/api/injection-stream', {
              headers: { cookie: nativeCookie, origin: base },
            })
          ).status,
          401,
        );
        assert.equal(
          (
            await fetch(base + '/', {
              headers: { cookie: nativeCookie },
              redirect: 'manual',
            })
          ).status,
          200,
          'the native shell boots while business providers remain unavailable',
        );
        assert.equal(transportCalls.length, 0);
        provider = await ctx.plugin(ControllerCapabilities);
      }
      await ctx.plugin({
        name: 'injection-red-control',
        inject: ['connection'],
        apply(owner) {
          assert.equal(typeof ctx.get('tools').register, 'function');
          assert.equal(typeof ctx.get('sessions').flush, 'function');
          assert.equal(typeof ctx.get('sessionController').prompt, 'function');
          for (const name of ['tools', 'sessions', 'sessionController']) {
            let value;
            try {
              value = owner[name];
            } catch (error) {
              assert.match(error.message, /without inject/);
            }
            assert.equal(value, undefined);
          }
        },
      });
      const ready = await status(nativeCookie, 200),
        state = await ready.json(),
        attemptCookie = ready.headers.get('set-cookie').split(';')[0];
      assert.equal(state.loggedIn, false);
      const resumed = await fetch(base + '/auriga/local-login/login', {
        method: 'POST',
        headers: {
          cookie: nativeCookie + '; ' + attemptCookie,
          origin: base,
          'content-type': 'application/json',
          'X-Auriga-CSRF': state.csrf,
        },
        body: '{"action":"resume"}',
      });
      assert.equal(
        resumed.status,
        200,
        'formal module apply could not reach native services/startTeam',
      );
      const body = await resumed.json();
      assert.equal(body.step.type, 'complete');
      // Identity admission completes independently of the asynchronous legacy
      // Team transport. Require its actual handshake before checking tools.
      const teamDeadline = Date.now() + 5000;
      while (transportCalls.length < 4 && Date.now() < teamDeadline)
        await delay(10);
      assert.deepEqual(transportCalls.slice(0, 4), [
        'initialize',
        'notifications/initialized',
        'tools/list',
        '/ai/team/node/register',
      ]);
      const localCookie = resumed.headers
          .getSetCookie()
          .find((value) => value.startsWith('auriga_local_session='))
          .split(';')[0],
        authenticated = nativeCookie + '; ' + localCookie;
      while (
        names.some((name) => !ctx.get('tools').get(name)) &&
        Date.now() < teamDeadline
      )
        await delay(10);
      for (const name of names)
        assert.equal(typeof ctx.get('tools').get(name).execute, 'function');
      const stream = await fetch(base + '/api/injection-stream', {
        headers: { cookie: authenticated, origin: base },
      });
      assert.equal(stream.status, 200);
      streamReader = stream.body.getReader();
      assert.equal((await streamReader.read()).done, false);
      await provider.dispose();
      provider = undefined;
      assert.equal(
        (await streamReader.read().catch(() => ({ done: true }))).done,
        true,
      );
      await Promise.race([
        streamAborted.promise,
        delay(2000).then(() => {
          assert.fail(
            'native stream request did not abort after provider disposal',
          );
        }),
      ]);
      assert.equal(aborted, true);
      await status(nativeCookie, 503);
      assert.equal(
        (
          await fetch(base + '/api/injection-stream', {
            headers: { cookie: authenticated, origin: base },
          })
        ).status,
        401,
      );
      for (const name of names)
        assert.equal(ctx.get('tools').get(name), undefined);
      provider = await ctx.plugin(ControllerCapabilities);
      const restored = await status(nativeCookie, 200);
      assert.equal((await restored.json()).loggedIn, false);
      assert.equal(
        (
          await fetch(base + '/api/injection-stream', {
            headers: { cookie: authenticated, origin: base },
          })
        ).status,
        401,
        'old browser session survived provider restart',
      );
      for (const name of names)
        assert.equal(
          ctx.get('tools').get(name),
          undefined,
          'restoring providers must not auto-register Team tools before sign-in',
        );
      await plugin.dispose();
      plugin = undefined;
      await provider.dispose();
      provider = undefined;
      const standalone = await ctx.plugin(Plugin);
      const standaloneIndex = await fetch(base + '/', {
        headers: { cookie: nativeCookie },
        redirect: 'manual',
      });
      assert.equal(standaloneIndex.status, 200);
      assert.equal(await standaloneIndex.text(), 'native app');
      await standalone.dispose();
      console.log(
        'RC3_CONDITIONAL_OWNER_PASS: formal module lifecycle/start/tool owner/provider removal/closed restore; transport credentials are test-only, not real IAM evidence',
      );
    } finally {
      await streamReader?.cancel().catch(() => {});
      await plugin?.dispose();
      await provider?.dispose();
      await ctx?.fiber.dispose();
      await new Promise((resolve) => remote.close(resolve));
      console.log('INJECTION_PROFILE_EVIDENCE_RETAINED', home);
    }
  },
);
