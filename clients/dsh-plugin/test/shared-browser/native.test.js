import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
import { generateKeyPairSync, randomUUID, sign } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { Readable } from 'node:stream';
import { EventEmitter } from 'node:events';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { createNativeBrowser } from '../../src/shared-browser/native.js';
import { createBrowserHandler } from '../../src/shared-browser/plugin.js';
import { canonical } from '../../src/shared/protocol.js';

const { createSharedRuntime } = await import(
  process.env.AURIGA_SHARED_RUNTIME_MODULE
    ? pathToFileURL(process.env.AURIGA_SHARED_RUNTIME_MODULE).href
    : '../../src/shared/runtime.js'
);

const root = process.env.AURIGA_DSH_HOST_ROOT;
const skip = root
  ? false
  : 'AURIGA_DSH_HOST_ROOT must point at the locked rc.3 public package installation';
const request = (endpoint, args) => ({
  channel: '/api',
  endpoint,
  payload: { args },
});

async function fixture(t) {
  const require = createRequire(join(root, 'package.json'));
  const load = (name) => import(pathToFileURL(require.resolve(name)).href);
  const [
    { Context },
    { Peer },
    { RemoteAccessService },
    { default: Registry },
    { default: Gateway },
  ] = await Promise.all([
    load('@deepseek-ai/cordis'),
    load('@deepseek-ai/dsh-client-connection'),
    load('@deepseek-ai/dsh-api-remote-access'),
    load('@deepseek-ai/dsh-typert-registry'),
    load('@deepseek-ai/dsh-api-gateway'),
  ]);
  const ctx = new Context();
  t.after(() => ctx.fiber.dispose());
  new Registry(ctx);
  const remoteAccess = new RemoteAccessService(ctx);
  const gateway = new Gateway(ctx, {
    websocketHeartbeatIntervalMs: 1000,
    streamInboxBytes: 262144,
  });
  const key = generateKeyPairSync('ed25519');
  const physicalInstanceId = randomUUID(),
    tenantId = randomUUID();
  const states = [0, 1].map(() => ({
    binding: {
      physicalInstanceId,
      tenantId,
      memberId: randomUUID(),
      placementId: randomUUID(),
      mode: 'shared',
      epoch: 1,
      revision: 1,
    },
    platformSessionId: randomUUID(),
    authGeneration: 1,
    credentialVersion: 1,
    active: true,
    allowRawAgentToken: true,
    authorizationSetDigest: 'sha256:' + 'a'.repeat(64),
  }));
  const rows = new Map();
  let omitGlobal = false;
  const audit = [];
  const runtime = createSharedRuntime({
    physicalInstanceId,
    identityKeys: new Map([['test', key.publicKey]]),
    capabilityKeys: new Map(),
    now: () => 1000,
    ports: {
      resolve: async (binding) =>
        states.find(
          (state) => state.binding.placementId === binding.placementId,
        ),
      lookup: async (id) => rows.get(id),
      register: async (binding) => {
        audit.push('register');
        rows.set(binding.sessionId, binding);
        return binding;
      },
    },
  });
  t.after(() => runtime.close());
  const issue = (
    index,
    method = 'POST',
    canonicalPath = '/auriga/shared/browser/rpc',
  ) => {
    const state = states[index];
    const claims = {
      schemaVersion: 2,
      issuer: 'auriga.edge',
      audience: 'auriga.shared.ingress',
      binding: state.binding,
      platformSessionId: state.platformSessionId,
      method,
      canonicalPath,
      jti: randomUUID(),
      issuedAt: 1000,
      expiresAt: 1030,
    };
    const input = [
      { alg: 'EdDSA', kid: 'test', typ: 'auriga-shared-identity+jwt' },
      claims,
    ]
      .map((value) => Buffer.from(canonical(value)).toString('base64url'))
      .join('.');
    return (
      input +
      '.' +
      sign(null, Buffer.from(input), key.privateKey).toString('base64url')
    );
  };
  const peers = [];
  const host = {
    admitBrowser: async (token, req) =>
      runtime.admit(token, req, (principal) => {
        const peer = new Peer(ctx, { principal, interactions: 'answer' });
        peers.push(peer);
        return peer;
      }),
    create: (peer) =>
      runtime.create(peer, {
        prepare: async (id) => {
          audit.push('prepare');
          return { id };
        },
        publish: async () => {
          audit.push('publish');
        },
        discard: async () => {},
      }),
    authorize: runtime.authorize,
    refresh: async (peer) => {
      const own = [];
      for (const id of rows.keys()) {
        try {
          await runtime.authorize(peer, 'session/page', id);
          own.push(id);
        } catch (error) {
          if (error.code !== 'shared.binding_mismatch') throw error;
        }
      }
      return own;
    },
    messagingSessions: async (peer) =>
      (await host.refresh(peer)).map((sessionId) => ({
        sessionId,
        label: '本人会话',
      })),
    messagingSettings: async (peer, input) => {
      await runtime.authorize(peer, 'session/page', input.sessionId);
      return {
        sessionId: input.sessionId,
        available: true,
        desiredReceiveEnabled: input.enabled === true,
      };
    },
  };
  t.after(async () => {
    for (const peer of peers) await peer.dispose();
  });
  remoteAccess.registerPolicy('auriga-shared-member', {
    bindings: (principal) =>
      runtime
        .bindings(principal)
        .filter((binding) => !omitGlobal || binding.resource.kind !== 'global'),
  });
  return {
    ctx,
    states,
    issue,
    host,
    gateway,
    remoteAccess,
    runtime,
    audit,
    peer: (index) =>
      host.admitBrowser(issue(index), {
        method: 'POST',
        canonicalPath: '/auriga/shared/browser/rpc',
      }),
    browser: createNativeBrowser({ host, gateway, remoteAccess }),
    omitGlobals: (omit) => {
      omitGlobal = omit;
      remoteAccess.invalidate();
    },
  };
}

test(
  'real rc.3 Peer and Gateway stream retain member access and cannot answer owner events',
  { skip },
  async (t) => {
    const f = await fixture(t),
      a = await f.peer(0),
      b = await f.peer(1);
    assert.equal(a.principal.kind, 'auriga-shared-member');
    assert.equal(a.interactions, 'answer');
    const binding = await f.host.create(a);
    await f.host.create(b);
    const stop = new AbortController();
    const { source } = await f.browser.open(
      a,
      request('remoteAccess/follow', {}),
      stop.signal,
    );
    const iterator = source[Symbol.asyncIterator]();
    const first = (await iterator.next()).value;
    assert.equal(first.owner, false);
    assert.deepEqual(
      first.grants
        .filter((grant) => grant.resource.kind === 'session')
        .map((grant) => grant.resource.id),
      [binding.sessionId],
    );
    stop.abort();
    await iterator.return();
    await assert.rejects(
      f.browser.call(
        a,
        request('$events/result', {}),
        new AbortController().signal,
      ),
    );
    const ownerAnswer = await f.gateway.dispatchRpc(
      '$events/result',
      { args: {} },
      new AbortController().signal,
      a,
    );
    assert.equal(ownerAnswer.ok, false);
    assert.equal(ownerAnswer.error.code, 'gateway/invocation-unavailable');
  },
);

test(
  'native create keeps trusted prepare-register-publish ordering and rejects owner/cwd/foreign reuse',
  { skip },
  async (t) => {
    const f = await fixture(t),
      a = await f.peer(0),
      b = await f.peer(1),
      signal = new AbortController().signal;
    const result = await f.browser.call(
      a,
      request('session/create', { request: {} }),
      signal,
    );
    assert.equal(result.ok, true);
    assert.deepEqual(f.audit, ['prepare', 'register', 'publish']);
    const foreign = await f.host.create(b);
    for (const input of [
      { cwd: '/' },
      { memberId: b.principal.memberId },
      { sessionId: foreign.sessionId },
      { agentPreset: '' },
      { agentPreset: 'shell', cwd: '/' },
    ])
      await assert.rejects(
        f.browser.call(
          a,
          request('session/create', { request: input }),
          signal,
        ),
      );
    assert.equal((await f.browser.snapshot(a)).owner, false);
    const token = f.issue(0);
    await f.host.admitBrowser(token, {
      method: 'POST',
      canonicalPath: '/auriga/shared/browser/rpc',
    });
    await assert.rejects(
      f.host.admitBrowser(token, {
        method: 'POST',
        canonicalPath: '/auriga/shared/browser/rpc',
      }),
    );
  },
);

test(
  'finite native mapping checks session ownership before and after real protocol invocation',
  { skip },
  async (t) => {
    const f = await fixture(t),
      a = await f.peer(0),
      b = await f.peer(1),
      signal = new AbortController().signal;
    const own = await f.host.create(a),
      other = await f.host.create(b);
    const seen = [];
    // Business-method fixture only: Peer, Identity admission, current authority,
    // runtime binding and RemoteAccess above remain the real public modules.
    const gateway = {
      ...f.gateway,
      dispatchRpc: async (...args) => {
        seen.push(args);
        return { ok: true, value: 'native-business-result' };
      },
    };
    const browser = createNativeBrowser({
      host: f.host,
      gateway,
      remoteAccess: f.remoteAccess,
    });
    const envelope = request('session/page', {
      request: {
        address: { kind: 'session', sessionId: own.sessionId },
        throughSeq: -1,
      },
    });
    assert.equal(
      (await browser.call(a, envelope, signal)).value,
      'native-business-result',
    );
    assert.equal(seen[0][3], a);
    await assert.rejects(
      browser.call(
        a,
        request('session/page', {
          request: { address: { kind: 'session', sessionId: other.sessionId } },
        }),
        signal,
      ),
    );
    await assert.rejects(
      browser.call(a, request('terminal/run', { request: {} }), signal),
    );
    gateway.dispatchRpc = async () => {
      f.states[0].active = false;
      return { ok: true, value: 'late' };
    };
    await assert.rejects(browser.call(a, envelope, signal));
  },
);

test(
  'real Gateway forwards only own native session events and refuses global owner capabilities',
  { skip },
  async (t) => {
    const f = await fixture(t),
      a = await f.peer(0),
      b = await f.peer(1);
    const own = await f.host.create(a),
      other = await f.host.create(b);
    const require = createRequire(join(root, 'package.json'));
    const { apply } = await import(
      pathToFileURL(require.resolve('@deepseek-ai/dsh-api-remotes')).href
    );
    apply(f.ctx);
    const stop = new AbortController();
    const { source } = await f.browser.open(
      a,
      request('$events', {}),
      stop.signal,
    );
    const iterator = source[Symbol.asyncIterator]();
    const ready = (await iterator.next()).value;
    assert.equal(ready.type, 'ready');
    assert.ok(ready.host.home.includes(a.principal.memberId));
    const next = iterator.next();
    f.ctx.emit('api-session/status', other.sessionId, true);
    f.ctx.emit('credentials/record-updated', 'OWNER-CREDENTIAL-CANARY');
    f.ctx.emit('cordis/request-run', 'OWNER-EXECUTION-CANARY');
    f.ctx.emit('api-session/status', own.sessionId, true);
    const ownEvent = await Promise.race([
      next,
      new Promise((_, reject) =>
        setTimeout(() => reject(new Error('own event missing')), 1000),
      ),
    ]);
    assert.deepEqual(ownEvent.value, {
      type: 'emit',
      event: 'api-session/status',
      args: [own.sessionId, true],
    });
    for (const endpoint of [
      '$events/result',
      'credentials/read',
      'mcp/call',
      'pluginManager/install',
      'terminal/create',
    ])
      await assert.rejects(
        f.browser.call(a, request(endpoint, {}), stop.signal),
      );
    stop.abort();
    await iterator.return();
  },
);

test(
  'an epoch change during native execution fences the late result',
  { skip },
  async (t) => {
    const f = await fixture(t),
      peer = await f.peer(0),
      binding = await f.host.create(peer);
    const signal = new AbortController().signal;
    const browser = createNativeBrowser({
      host: f.host,
      remoteAccess: f.remoteAccess,
      gateway: {
        dispatchRpc: async () => {
          f.states[0].binding.epoch++;
          return { ok: true, value: 'OLD-EPOCH' };
        },
      },
    });
    await assert.rejects(
      browser.call(
        peer,
        request('session/cancel', {
          request: { sessionId: binding.sessionId },
        }),
        signal,
      ),
    );
  },
);

function response() {
  const res = new EventEmitter();
  res.chunks = [];
  res.headersSent = false;
  res.destroyed = false;
  res.writeHead = (status, headers) => {
    res.status = status;
    res.headers = headers;
    res.headersSent = true;
  };
  res.write = (value) => {
    res.chunks.push(value);
    return true;
  };
  res.flushHeaders = () => {};
  res.end = (value) => {
    if (value) res.chunks.push(value);
    res.ended = true;
  };
  res.destroy = () => {
    res.destroyed = true;
    res.emit('close');
  };
  return res;
}
function incoming(path, token, body, method = 'POST') {
  const req = Readable.from(
    body === undefined ? [] : [Buffer.from(JSON.stringify(body))],
  );
  req.url = path;
  req.method = method;
  req.headers = {
    'content-type': 'application/json',
    cookie: 'native-owner-cookie-canary',
    ...(token ? { 'auriga-identity': token } : {}),
  };
  return req;
}

test(
  'member index serves original native dist, rejects cookie-only admission, and idle streams close on revoke',
  { skip },
  async (t) => {
    const f = await fixture(t);
    const dir = await mkdtemp(join(tmpdir(), 'auriga-shared-browser-'));
    t.after(() => rm(dir, { recursive: true, force: true }));
    const distIndex = join(dir, 'index.html');
    await writeFile(
      distIndex,
      '<html><head></head><body><div id="native-root"></div><script type="module" src="native-entry.js"></script></body></html>',
    );
    const require = createRequire(join(root, 'package.json'));
    const { renderIndexInjections } = await import(
      pathToFileURL(require.resolve('@deepseek-ai/dsh-host-webserver')).href
    );
    const browser = createBrowserHandler(
      {
        aurigaShared: f.host,
        typertGateway: f.gateway,
        remoteAccess: f.remoteAccess,
        webServer: {
          renderIndex: (html) =>
            renderIndexInjections(html, [
              {
                kind: 'global',
                name: '__DSH_REMOTE_ACCESS__',
                value: { type: 'baseline', owner: true },
              },
            ]),
        },
      },
      { distIndex, recheckMs: 10 },
    );
    t.after(() => browser.close());
    const denied = response();
    await browser.handler(incoming('/', undefined, undefined, 'GET'), denied);
    assert.equal(denied.status, 403);
    const index = response();
    await browser.handler(
      incoming('/', f.issue(0, 'GET', '/'), undefined, 'GET'),
      index,
    );
    assert.equal(index.status, 200);
    const html = index.chunks.join('');
    assert.ok(html.includes('src="native-entry.js"'));
    assert.ok(html.includes('__DSH_TRANSPORT__'));
    assert.ok(
      html.lastIndexOf('"owner":false') > html.lastIndexOf('"owner":true'),
    );
    assert.equal(index.headers['Set-Cookie'], undefined);
    const peer = await f.peer(0);
    await f.host.create(peer);
    const path = '/auriga/shared/browser/stream',
      res = response();
    const work = browser.handler(
      incoming(
        path,
        f.issue(0, 'POST', path),
        request('remoteAccess/follow', {}),
      ),
      res,
    );
    await new Promise((resolve) => setTimeout(resolve, 20));
    f.states[0].active = false;
    await Promise.race([
      work,
      new Promise((_, reject) =>
        setTimeout(() => reject(new Error('stale stream remained live')), 1000),
      ),
    ]);
    assert.equal(res.ended, true);
    assert.equal(
      res.chunks.some((line) => line.includes('"type":"end"')),
      false,
    );
  },
);

test(
  'real native list/control require only narrow global read role and still filter trusted member bindings',
  { skip },
  async (t) => {
    const f = await fixture(t),
      a = await f.peer(0),
      b = await f.peer(1);
    const own = await f.host.create(a),
      other = await f.host.create(b);
    const require = createRequire(join(root, 'package.json'));
    const load = (name) => import(pathToFileURL(require.resolve(name)).href);
    const [{ SessionController }, { TYPERT }, { TypertRemoteService }] =
      await Promise.all([
        load('@deepseek-ai/dsh-api-session-controller'),
        load('@deepseek-ai/dsh-api-session-controller/typert'),
        load('@deepseek-ai/dsh-typert-protocol'),
      ]);
    f.ctx.typert.register(TYPERT);
    // Pure persistence/projection data fixture. The implementations below are
    // the INSTALLED native SessionController.list/control functions; no native
    // Agent loop, filesystem persistence service, model or process is started.
    class ControllerFixture extends TypertRemoteService {
      constructor(ctx) {
        super(ctx, 'sessionController', { namespace: 'session' });
        this.listState = {
          list: async () =>
            [own, other].map((binding) => ({
              sessionId: binding.sessionId,
              updatedAt: 1000,
              blank: true,
              running: false,
              agentAvailable: true,
            })),
        };
        this.controlState = {
          async *control() {
            yield {
              type: 'baseline',
              value: {
                projections: {
                  [own.sessionId]: { asOfSeq: -1, values: {} },
                  [other.sessionId]: { asOfSeq: -1, values: {} },
                },
              },
            };
            yield {
              type: 'projection',
              sessionId: other.sessionId,
              key: 'test',
              value: 'FOREIGN',
              seq: 0,
            };
            yield {
              type: 'projection',
              sessionId: own.sessionId,
              key: 'test',
              value: 'OWN',
              seq: 0,
            };
          },
        };
      }
      list(...args) {
        return SessionController.prototype.list.apply(this, args);
      }
      control(...args) {
        return SessionController.prototype.control.apply(this, args);
      }
    }
    new ControllerFixture(f.ctx);
    const signal = new AbortController().signal;
    const list = request('session/list', { _request: {} });
    f.omitGlobals(true);
    const denied = await f.browser.call(a, list, signal);
    assert.equal(denied.ok, false);
    assert.equal(denied.error.code, 'remote-access/denied');
    f.omitGlobals(false);
    const allowed = await f.browser.call(a, list, signal);
    assert.equal(allowed.ok, true, JSON.stringify(allowed));
    assert.deepEqual(
      allowed.value.items.map((item) => item.sessionId),
      [own.sessionId],
    );
    const { source } = await f.browser.open(
      a,
      request('session/control', {}),
      signal,
    );
    const frames = [];
    for await (const frame of source) frames.push(frame);
    assert.deepEqual(Object.keys(frames[0].value.projections), [own.sessionId]);
    assert.equal(frames.length, 2);
    assert.equal(frames[1].sessionId, own.sessionId);
    for (const action of [
      'session/create',
      'session/modelCatalog',
      'credentials/read',
      'mcp/call',
      'event:credentials/record-updated',
      'event:cordis/request-run',
    ]) {
      assert.equal(
        f.remoteAccess.decide({
          principal: a.principal,
          action,
          resources: [{ kind: 'global' }],
          args: {},
        }).kind,
        'deny',
        action,
      );
    }
  },
);
