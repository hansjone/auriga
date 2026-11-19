import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  checkPlatformMigration,
  readPluginSources,
} from '../../../../contracts/test/auth-k-source-scan.mjs';

const inventory = JSON.parse(
  readFileSync(
    new URL(
      '../../../../contracts/plugin/shell-auth-call-sites.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
const operations = JSON.parse(
  readFileSync(
    new URL(
      '../../../../contracts/plugin/shell-business-operations.json',
      import.meta.url,
    ),
    'utf8',
  ),
).operations;
const broker = { ...inventory, migrationState: 'broker', calls: [] };
const files = readPluginSources();
const bootstrap = 'shared-browser/bootstrap.js';
const native = 'shared-browser/native.js';
const handler = 'shared-browser/plugin.js';
const check = (sources) => checkPlatformMigration(broker, operations, sources);

test('AUTH-K recognizes only the actual fixed native browser composition while allowing unrelated UI edits', () => {
  assert.doesNotThrow(() => check(files));
  assert.doesNotThrow(() =>
    check({
      ...files,
      [bootstrap]: files[bootstrap].replace("'消息接收'", "'本人消息接收'"),
    }),
  );
  assert.doesNotThrow(() =>
    check({
      ...files,
      [bootstrap]:
        files[bootstrap] +
        '\n// token / cookie words in a comment are not a transport\n',
    }),
  );
  for (const companion of [native, handler]) {
    const missing = { ...files };
    delete missing[companion];
    assert.throws(() => check(missing), /shared browser/);
  }
});

test('AUTH-K browser mutations cannot add another origin, owner URL, dynamic endpoint or credential header', async (t) => {
  const target = 'new URL(`auriga/shared/browser/${kind}`, base)';
  const mutations = [
    ['absolute URL', target, "new URL('https://untrusted.invalid/rpc', base)"],
    [
      'protocol relative URL',
      target,
      "new URL('//untrusted.invalid/rpc', base)",
    ],
    ['owner API URL', target, "new URL('/api/session/prompt', base)"],
    ['owner connect URL', target, "new URL('/connect', base)"],
    ['dynamic URL', target, 'new URL(endpoint, base)'],
    [
      'dynamic route',
      'auriga/shared/browser/${kind}',
      'auriga/shared/browser/${endpoint}',
    ],
    [
      'external base',
      "new URL('./', document.baseURI)",
      "new URL('https://untrusted.invalid/')",
    ],
    ['caller base', "new URL('./', document.baseURI)", 'new URL(payload.url)'],
    ['method override', "method: 'POST'", 'method: payload.method'],
    [
      'cross-origin credential policy',
      "credentials: 'same-origin'",
      "credentials: 'include'",
    ],
    [
      'Identity header',
      "headers: { 'content-type': 'application/json' }",
      "headers: { 'content-type': 'application/json', 'Auriga-Identity': payload.identity }",
    ],
    [
      'Authorization header',
      "headers: { 'content-type': 'application/json' }",
      "headers: { 'content-type': 'application/json', Authorization: payload.token }",
    ],
    [
      'headers spread',
      "headers: { 'content-type': 'application/json' }",
      "headers: { 'content-type': 'application/json', ...payload.headers }",
    ],
    [
      'body spread',
      'JSON.stringify({ channel, endpoint, payload })',
      'JSON.stringify({ channel, endpoint, payload, ...payload })',
    ],
    [
      'dynamic rpc carrier',
      "send('rpc', channel, endpoint, payload, signal)",
      'send(payload.kind, channel, endpoint, payload, signal)',
    ],
    [
      'unknown carrier',
      "send('stream', channel, endpoint, payload, signal)",
      "send('owner', channel, endpoint, payload, signal)",
    ],
    [
      'arbitrary endpoint',
      "send('rpc', channel, endpoint, payload, signal)",
      "send('rpc', channel, 'credentials/read', payload, signal)",
    ],
    [
      'dynamic endpoint override',
      "send('rpc', channel, endpoint, payload, signal)",
      "send('rpc', channel, payload.endpoint, payload, signal)",
    ],
    [
      'unknown message method',
      "read('messagingSessions'",
      "read('credentials'",
    ],
    ['owner host claim', 'ownsHost: false', 'ownsHost: true'],
    [
      'capture service token',
      'const rpc = {',
      'const token = globalThis.serviceJwt; const rpc = {',
    ],
  ];
  for (const [name, before, after] of mutations)
    await t.test(name, () => {
      assert.ok(files[bootstrap].includes(before), before);
      assert.throws(
        () =>
          check({
            ...files,
            [bootstrap]: files[bootstrap].replace(before, after),
          }),
        /shared browser/,
      );
    });
});

test('AUTH-K browser allowance has no filename, function-name or extra-transport exemption', () => {
  for (const extra of [
    "fetcher('https://untrusted.invalid');",
    "globalThis.fetch('/api/session/prompt');",
    "new WebSocket('wss://untrusted.invalid');",
    'const alias = fetcher; alias(payload.url);',
    "rpc.call('/api', 'credentials/read', { args: {} });",
    "send('rpc', '/api', 'credentials/read', {}, undefined);",
  ])
    assert.throws(
      () => check({ ...files, [bootstrap]: files[bootstrap] + '\n' + extra }),
      /shared browser/,
    );
  assert.throws(
    () =>
      check({
        ...files,
        'shared-browser/unlisted.js': 'fetcher(payload.url);',
      }),
    /direct transport/,
  );
  assert.throws(
    () => check({ ...files, 'unlisted.js': 'fetcher(payload.url);' }),
    /direct transport/,
  );
  assert.throws(
    () =>
      checkPlatformMigration(
        {
          ...broker,
          retainedChannels: [
            ...broker.retainedChannels,
            {
              file: bootstrap,
              channel: 'browser',
              reason: 'test',
              design: 'test',
              transports: ['call:fetcher'],
              routePatterns: [],
            },
          ],
        },
        operations,
        { ...files, [bootstrap]: files[bootstrap] + '\nfetcher(payload.url);' },
      ),
    /shared browser/,
  );
});

test('AUTH-K binds browser endpoint data to the actual finite signed-member server dispatcher', async (t) => {
  const mutations = [
    [
      native,
      "['session/projections', 'session/projections'],",
      "['session/projections', 'session/projections'], ['credentials/read', 'credentials/read'],",
    ],
    [
      native,
      "['session/follow', 'session/follow']",
      "['session/follow', 'session/follow'], ['mcp/call', 'mcp/call']",
    ],
    [
      native,
      "['$events', 'remoteAccess/follow', 'session/control']",
      "['$events', 'remoteAccess/follow', 'session/control', '$events/result']",
    ],
    [native, "if (endpoint === '$events/result')", 'if (false)'],
    [
      native,
      'const binding = await host.create(peer, {});',
      "const binding = await gateway.invoke({ namespace: 'session', method: 'create' });",
    ],
    [
      native,
      'const selected = await prepare(peer, envelope, false);',
      'const selected = envelope;',
    ],
    [
      native,
      'result = await gateway.dispatchRpc(',
      'result = await gateway.ownerDispatch(',
    ],
    [
      handler,
      "const prefix = '/auriga/shared/browser';",
      "const prefix = '/api';",
    ],
    [
      handler,
      "const token = req.headers['auriga-identity'];",
      'const token = req.headers.authorization;',
    ],
    [
      handler,
      'peer = await host.admitBrowser(token, {',
      'peer = await ctx.connection.admitConnection(token, {',
    ],
    [handler, "if (peer.principal.kind === 'owner')", 'if (false)'],
    [
      handler,
      'native.call(peer, envelope, signal)',
      'native.call(ctx.connection.browserPeer, envelope, signal)',
    ],
  ];
  for (const [file, before, after] of mutations)
    await t.test(before, () => {
      assert.ok(files[file].includes(before), before);
      assert.throws(
        () => check({ ...files, [file]: files[file].replace(before, after) }),
        /shared browser/,
      );
    });
});
