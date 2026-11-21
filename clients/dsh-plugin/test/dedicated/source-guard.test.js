import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  checkPlatformMigration,
  readPluginSources,
} from '../../../../contracts/test/auth-k-source-scan.mjs';

const contract = new URL('../../../../contracts/plugin/', import.meta.url);
const inventory = JSON.parse(
  readFileSync(new URL('shell-auth-call-sites.json', contract), 'utf8'),
);
const operations = JSON.parse(
  readFileSync(new URL('shell-business-operations.json', contract), 'utf8'),
).operations;
const broker = { ...inventory, migrationState: 'broker', calls: [] };
const files = readPluginSources();
const bootstrap = 'dedicated/bootstrap.js';
const browser = 'dedicated/browser.js';
const host = 'dedicated/host.js';
const provider = 'dedicated/plugin.js';
const binding = 'dedicated/intent.js';
const bridge = 'dedicated/node-bridge.js';
const profile = 'dedicated/profile.js';
const check = (sources) => checkPlatformMigration(broker, operations, sources);

test('AUTH-K accepts only the frozen dedicated carrier with the real unique-binding owner derivation', () => {
  assert.doesNotThrow(() => check(files));
  assert.doesNotThrow(() =>
    check({
      ...files,
      [bootstrap]: files[bootstrap].replace(
        '独立实例响应无效',
        '独立实例响应暂不可用',
      ),
    }),
  );
  assert.doesNotThrow(() =>
    check({
      ...files,
      [bootstrap]:
        files[bootstrap] +
        '\n// token/cookie words in a comment are not credentials\n',
    }),
  );
  assert.doesNotThrow(() =>
    check(
      Object.fromEntries(
        Object.entries(files).filter(
          ([file]) => !file.startsWith('dedicated/'),
        ),
      ),
    ),
  );
  for (const companion of [browser, host, provider, binding, bridge, profile]) {
    const missing = { ...files };
    delete missing[companion];
    assert.throws(() => check(missing), /dedicated browser/, companion);
  }
});

test('AUTH-K dedicated browser rejects external/owner paths, dynamic carrier and any identity override', async (t) => {
  const target = 'new URL(`auriga/dedicated/browser/${kind}`, base)';
  for (const [name, before, after] of [
    [
      'absolute origin',
      target,
      "new URL('https://untrusted.invalid/rpc', base)",
    ],
    [
      'protocol-relative origin',
      target,
      "new URL('//untrusted.invalid/rpc', base)",
    ],
    ['original owner API', target, "new URL('/api/session/prompt', base)"],
    ['original owner connect', target, "new URL('/connect', base)"],
    [
      'shared context',
      'auriga/dedicated/browser/${kind}',
      'auriga/shared/browser/${kind}',
    ],
    ['caller URL', target, 'new URL(payload.url, base)'],
    ['endpoint URL', target, 'new URL(endpoint, base)'],
    ['caller host', "new URL('./', document.baseURI)", 'new URL(payload.host)'],
    [
      'external base',
      "new URL('./', document.baseURI)",
      "new URL('https://untrusted.invalid/')",
    ],
    [
      'dynamic kind',
      "send('rpc', channel, endpoint, payload, signal)",
      'send(payload.kind, channel, endpoint, payload, signal)',
    ],
    [
      'extra carrier',
      "'stream',\n          channel,",
      "'owner',\n          channel,",
    ],
    ['channel override', "if (channel !== '/api')", 'if (false)'],
    ['method override', "method: 'POST'", 'method: payload.method'],
    ['credential mode', "credentials: 'same-origin'", "credentials: 'include'"],
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
      'header spread',
      "headers: { 'content-type': 'application/json' }",
      "headers: { ...payload.headers, 'content-type': 'application/json' }",
    ],
    [
      'member/host body',
      'JSON.stringify({ channel, endpoint, payload })',
      'JSON.stringify({ channel, endpoint, payload, memberId: payload.member, host: payload.host })',
    ],
    [
      'service JWT capture',
      'const base =',
      'const token = globalThis.serviceJwt; const base =',
    ],
    [
      'caller endpoint',
      "send('rpc', channel, endpoint, payload, signal)",
      "send('rpc', channel, payload.endpoint, payload, signal)",
    ],
  ])
    await t.test(name, () => {
      assert.ok(files[bootstrap].includes(before), before);
      assert.throws(
        () =>
          check({
            ...files,
            [bootstrap]: files[bootstrap].replace(before, after),
          }),
        /dedicated browser/,
      );
    });
});

test('AUTH-K dedicated admission requires the actual profile, full E/W binding and current fences', async (t) => {
  for (const [name, file, before, after] of [
    [
      'cookie admission',
      browser,
      "host.admit(req.headers['auriga-identity']",
      'host.admit(req.headers.cookie',
    ],
    [
      'borrow owner',
      browser,
      "const row = await host.admit(req.headers['auriga-identity'], {",
      'const row = await ctx.connection.admitConnection(req, {',
    ],
    [
      'stream bound lifetime',
      browser,
      'row.native.signal,',
      'payload.ownerSignal,',
    ],
    [
      'idle current fence',
      browser,
      'await host.check(row);',
      '// removed current fence',
    ],
    [
      'stream current fence',
      browser,
      'for await (const value of source) {\n          await host.check(row);',
      'for await (const value of source) {',
    ],
    [
      'E identity gate',
      host,
      'const member = await runtime.admit(token, request,',
      'const member = await config.admit(token, request,',
    ],
    [
      'W current fence',
      host,
      'await runtime.refresh(member);',
      '// no online authority',
    ],
    [
      'complete owner binding',
      host,
      'sameBinding(placementBinding(member.principal), binding);',
      '// no binding comparison',
    ],
    [
      'member-specific context',
      host,
      'const key = canonical(member.principal);',
      'const key = binding.physicalInstanceId;',
    ],
    [
      'session owner',
      host,
      "await runtime.authorize(member, 'session/page', id);",
      'return id;',
    ],
    [
      'K register/publish',
      host,
      'await runtime.create(row.member, ports.sessions, {})',
      "await ctx.typertGateway.invoke({ namespace: 'session', method: 'create', peer: row.native })",
    ],
    [
      'unknown native endpoint',
      host,
      '!ctx.typertGateway.claimsEndpoint(endpoint)',
      'false',
    ],
    [
      'action admission',
      host,
      'await prepare(row, input, false);',
      '// removed prepare',
    ],
    ['binding body override', host, "  'memberId',", ''],
    [
      'shared runtime',
      provider,
      "expectedMode: 'dedicated'",
      "expectedMode: 'shared'",
    ],
    [
      'installation binding',
      provider,
      'value.binding = normalizeBinding(value.binding);',
      'value.binding = config.binding;',
    ],
    [
      'trusted installation path',
      provider,
      "config.configFile !== root + '/control/dedicated-host.json'",
      'false',
    ],
    [
      'caller socket',
      provider,
      'createSharedNodeTransport(initial.nodeSocket)',
      'createSharedNodeTransport(config.socketPath)',
    ],
    [
      'dedicated profile proof',
      provider,
      'assertDedicatedProfile(context, expected, resources)',
      'true',
    ],
    [
      'current lookup binding',
      provider,
      'sameBinding(placementBinding(result), binding);',
      '// no lookup fence',
    ],
    [
      'fake dedicated mode',
      binding,
      "if (binding.mode !== 'dedicated')",
      'if (false)',
    ],
    [
      'old epoch comparison',
      binding,
      'canonical(left) !== canonical(right)',
      'left.memberId !== right.memberId',
    ],
    [
      'bridge input owner',
      bridge,
      'sameBinding(placementBinding(body.binding), binding)',
      'sameBinding(binding, binding)',
    ],
    [
      'bridge current fence',
      bridge,
      "if (operation !== 'finishTask') settings();",
      'settings;',
    ],
    [
      'shared profile context',
      profile,
      "profile?.name !== 'auriga-dedicated'",
      "profile?.name !== 'auriga-shared'",
    ],
    ['shared service exclusion', profile, "        'auriga-dsh-shared',", ''],
  ])
    await t.test(name, () => {
      assert.ok(files[file].includes(before), before);
      assert.throws(
        () => check({ ...files, [file]: files[file].replace(before, after) }),
        /dedicated browser/,
      );
    });
  const ownerPeer =
    "native: ports.createPeer({\n            principal: Object.freeze({ kind: 'owner' }),\n            interactions: 'answer',\n          }),";
  assert.ok(files[host].includes(ownerPeer));
  assert.throws(
    () =>
      check({
        ...files,
        [host]: files[host].replace(
          ownerPeer,
          'native: ctx.connection.browserPeer,',
        ),
      }),
    /dedicated browser/,
  );
});

test('AUTH-K dedicated allowance never exempts another transport or Node relay caller', () => {
  for (const extra of [
    "fetcher('https://untrusted.invalid');",
    "globalThis.fetch('/api/session/prompt');",
    "new WebSocket('wss://untrusted.invalid');",
    'const alias = fetcher; alias(payload.url);',
    "send('rpc', '/api', 'credentials/read', {}, undefined);",
    "globalThis.__DSH_TRANSPORT__.rpc.call('/api', 'session/create', {});",
  ])
    assert.throws(
      () => check({ ...files, [bootstrap]: files[bootstrap] + '\n' + extra }),
      /dedicated browser/,
    );
  for (const file of ['dedicated/unlisted.js', 'unlisted.js']) {
    assert.throws(
      () => check({ ...files, [file]: 'fetcher(payload.url);' }),
      /direct transport/,
    );
    assert.throws(
      () =>
        check({
          ...files,
          [file]:
            "import { createSharedNodeTransport as relay } from './shared/node-transport.js'; relay(input.path);",
        }),
      /shared node relay/,
    );
  }
  assert.throws(
    () =>
      check({
        ...files,
        [provider]:
          files[provider] + '\ncreateSharedNodeTransport(input.path);',
      }),
    /dedicated browser/,
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
              channel: 'dedicated',
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
    /dedicated browser/,
  );
});
