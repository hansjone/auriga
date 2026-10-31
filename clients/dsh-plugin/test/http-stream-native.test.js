import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtemp, writeFile, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { ReadableStream } from 'node:stream/web';
import { TextDecoder } from 'node:util';
import { setTimeout as delay } from 'node:timers/promises';
import { installLocalAdmission } from '../src/local/host.js';
import { createHttpResponseTracker } from './http-response-fixture.js';
import { createConnectRoutes } from '../src/local/routes.js';
const cookie = (req, name) =>
  (req.headers.cookie ?? '')
    .split(';')
    .map((x) => x.trim())
    .find((x) => x.startsWith(name + '='))
    ?.slice(name.length + 1);

test(
  'actual rc.3 HTTP stream closes at the local-session boundary; late events and unrelated streams stay isolated',
  { skip: !process.env.AURIGA_DESKTOP_DSH_ROOT, timeout: 30000 },
  async (t) => {
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
    ] = await Promise.all([
      load('dsh-app-boot'),
      load('dsh-host-webserver'),
      load('dsh-credentials-local'),
      load('dsh-client-connection'),
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
    const home = await mkdtemp(join(tmpdir(), 'auriga-http-stream-native-')),
      dir = join(home, 'profile');
    let ctx, admission;
    const controllers = new Map(),
      abortSignals = new Map(),
      readers = new Set(),
      aborted = new Set(),
      sessions = new Map(),
      attempts = new Map();
    let hookCalls = 0;
    console.log('ISOLATED_HTTP_PROFILE', home);
    try {
      initProfile(dir, []);
      await writeFile(join(dir, 'cordis.yml'), '[]\n');
      const profile = {
        name: 'http-stream-test',
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
        '{"name":"isolated-stream-test","dependencies":{}}',
      );
      ctx = await boot(
        'http-stream-test',
        join(dir, 'cordis.yml'),
        readProfilePatches('http-stream-test', profile),
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
      const base = 'http://127.0.0.1:' + ctx.webServer.port;
      ctx.webServer.register({
        kind: 'exact',
        path: '/',
        handler(request, response) {
          if (ctx.connection.authorizeIndex(request, response))
            response.end('native');
        },
      });
      const exchange = await fetch(
        ctx.connection.authenticatedUrl(base + '/'),
        { redirect: 'manual' },
      );
      assert.equal(exchange.status, 303);
      const nativeCookie = exchange.headers.get('set-cookie').split(';')[0];
      // Only the post-IAM local-session authority is a fixture. The native cookie,
      // gate, request waterfall, Fetch route, bridge, response and HTTP client are real.
      const resolve = (request) =>
        sessions.get(cookie(request, 'stream-session')) ?? null;
      const responses = createHttpResponseTracker(resolve);
      const revoke = (id) => {
        const row = sessions.get(id);
        if (row) {
          sessions.delete(id);
          responses.close(row.key);
        }
      };
      let ownerCtx;
      await ctx.plugin({
        name: 'http-session-owner-probe',
        inject: ['connection'],
        apply(owner) {
          ownerCtx = owner;
        },
      });
      const runtime = {
        resolve,
        bindSocket() {
          return true;
        },
        checkCsrf: (request) => {
          const session = resolve(request);
          const csrf =
            session?.csrf ?? attempts.get(cookie(request, 'stream-attempt'));
          return !!csrf && csrf === request.headers['x-auriga-csrf'];
        },
        installHttpTracking() {
          return ownerCtx.on(
            'connection/request',
            (request, response, next) => {
              hookCalls++;
              assert.equal(typeof next, 'function');
              assert.equal(typeof response.destroy, 'function');
              if (!responses.bind(request, response)) {
                response.writeHead(401);
                response.end();
                return;
              }
              return next();
            },
          );
        },
        async logout(request) {
          const id = cookie(request, 'stream-session');
          if (id) revoke(id);
          else attempts.delete(cookie(request, 'stream-attempt'));
          return {
            body: { loggedIn: false },
            setCookie: 'stream-session=; Path=/; Max-Age=0',
          };
        },
      };
      admission = installLocalAdmission(ownerCtx.connection, runtime, {
        protocol: ctx.webServer.protocol,
      });
      ctx.webServer.register({
        kind: 'prefix',
        path: '/auriga/local-login',
        handler: createConnectRoutes({ runtime, admission }),
      });
      ctx.connection.fetch.register({
        path: '/api/test-stream',
        methods: ['GET'],
        requestBody: 'buffered',
        fetch(request) {
          const name = new URL(request.url).searchParams.get('name');
          let controller;
          return new Response(
            new ReadableStream({
              start(value) {
                controller = value;
                controllers.set(name, value);
                const stopped = Promise.withResolvers();
                abortSignals.set(name, stopped.promise);
                value.enqueue(new TextEncoder().encode('data: before\n\n'));
                request.signal.addEventListener(
                  'abort',
                  () => {
                    aborted.add(name);
                    stopped.resolve();
                    try {
                      controller.close();
                    } catch {
                      /* Already closed by client teardown. */
                    }
                  },
                  { once: true },
                );
              },
            }),
            { headers: { 'content-type': 'text/event-stream' } },
          );
        },
      });
      ctx.connection.fetch.register({
        path: '/api/test-once',
        methods: ['GET'],
        requestBody: 'buffered',
        fetch() {
          return new Response('finished');
        },
      });
      const emit = (name, text) => {
        if (aborted.has(name)) return false;
        controllers
          .get(name)
          .enqueue(new TextEncoder().encode('data: ' + text + '\n\n'));
        return true;
      };
      async function open(id, name) {
        const response = await fetch(base + '/api/test-stream?name=' + name, {
          headers: {
            cookie: nativeCookie + '; stream-session=' + id,
            origin: base,
          },
        });
        assert.equal(response.status, 200);
        const reader = response.body.getReader();
        readers.add(reader);
        assert.match(
          new TextDecoder().decode((await reader.read()).value),
          /before/,
        );
        return reader;
      }
      for (const reason of ['logout', 'expiry', 'online401'])
        await t.test(reason, async () => {
          const a = reason + '-A',
            b = reason + '-B';
          sessions.set(a, { key: a, csrf: 'csrf-A' });
          sessions.set(b, { key: b, csrf: 'csrf-B' });
          attempts.set(reason + '-anonymous', 'csrf-anonymous');
          const streamA = await open(a, a),
            streamB = await open(b, b);
          try {
            const once = await fetch(base + '/api/test-once', {
              headers: {
                cookie: nativeCookie + '; stream-session=' + a,
                origin: base,
              },
            });
            assert.equal(await once.text(), 'finished');
            const anonymous = await fetch(
              base + '/api/test-stream?name=anonymous',
              {
                headers: {
                  cookie:
                    nativeCookie + '; stream-attempt=' + reason + '-anonymous',
                  origin: base,
                },
              },
            );
            assert.equal(anonymous.status, 401);
            const anonymousLogout = await fetch(
              base + '/auriga/local-login/logout',
              {
                method: 'POST',
                headers: {
                  cookie:
                    nativeCookie + '; stream-attempt=' + reason + '-anonymous',
                  origin: base,
                  'content-type': 'application/json',
                  'X-Auriga-CSRF': 'csrf-anonymous',
                },
                body: '{}',
              },
            );
            assert.equal(anonymousLogout.status, 200);
            assert.equal(emit(a, 'live-after-anonymous-cancel'), true);
            assert.match(
              new TextDecoder().decode((await streamA.read()).value),
              /live-after-anonymous-cancel/,
            );
            if (reason === 'logout') {
              const logout = await fetch(base + '/auriga/local-login/logout', {
                method: 'POST',
                headers: {
                  cookie: nativeCookie + '; stream-session=' + a,
                  origin: base,
                  'content-type': 'application/json',
                  'X-Auriga-CSRF': 'csrf-A',
                },
                body: '{}',
              });
              assert.equal(logout.status, 200);
              assert.equal((await logout.json()).loggedIn, false);
            } else revoke(a); // Same post-authority invalidation boundary as expiry/401.
            const nextA = streamA.read().catch(() => ({ done: true }));
            emit(a, 'late-after-' + reason);
            emit(b, 'other-session-live');
            const receivedA = await nextA;
            assert.equal(
              receivedA.done,
              true,
              'revoked HTTP subscription received a late event',
            );
            await Promise.race([
              abortSignals.get(a),
              delay(2000, undefined, { ref: false }).then(() => {
                assert.fail('native bridge abort did not settle');
              }),
            ]);
            assert.equal(
              aborted.has(a),
              true,
              'native bridge request was not aborted by response close',
            );
            const receivedB = await streamB.read();
            assert.equal(receivedB.done, false);
            assert.match(
              new TextDecoder().decode(receivedB.value),
              /other-session-live/,
            );
            assert.ok(hookCalls > 0);
            assert.equal(attempts.has(reason + '-anonymous'), false);
            assert.equal(sessions.has(b), true);
            const fresh = await fetch(base + '/api/test-once', {
              headers: {
                cookie: nativeCookie + '; stream-session=' + b,
                origin: base,
              },
            });
            assert.equal(await fresh.text(), 'finished');
            const stale = await fetch(base + '/api/test-once', {
              headers: {
                cookie: nativeCookie + '; stream-session=' + a,
                origin: base,
              },
            });
            assert.equal(stale.status, 401);
          } finally {
            await streamA.cancel().catch(() => {});
            await streamB.cancel().catch(() => {});
            readers.delete(streamA);
            readers.delete(streamB);
            revoke(a);
            revoke(b);
          }
        });
      console.log(
        'RC3_HTTP_RESPONSE_CLOSE_PASS: exact native waterfall, bridge abort, no late HTTP events; no IAM/full-profile logout success claim',
      );
    } finally {
      admission?.dispose();
      for (const reader of readers) await reader.cancel().catch(() => {});
      await ctx?.fiber.dispose();
      console.log('HTTP_PROFILE_EVIDENCE_RETAINED', home);
    }
  },
);
