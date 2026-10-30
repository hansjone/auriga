import test from 'node:test';
import assert from 'node:assert/strict';
import { startFixture } from './cloud-desk.fixture.mjs';
import {
  assertConversation,
  nativeClient,
  configureFixtureModel,
  diagnoseNativeSession,
  loginDesktop,
} from './cloud-desk.native.mjs';
import { validateRunConfig } from './cloud-desk.mjs';
import {
  accessOrigin,
  assertCloudEntry,
  assessManagedPlugin,
  loginOtpField,
  loopbackBrowserArgs,
} from './cloud-desk.browser.mjs';

const tools = [{ type: 'function', function: { name: 'auriga_send_message' } }];
const message = (text) => ({ role: 'user', content: text });
const external = (marker, id = '019f0000-0000-7000-8000-000000000001') =>
  message(`[Auriga external message ${id}]\n${marker}`);

test('resume preserves the existing desktop identity and rejects another authenticated subject', async () => {
  const request = async () => ({
    loggedIn: true,
    available: true,
    account: { subject: 'same-member' },
  });
  assert.equal(
    (
      await loginDesktop(
        request,
        { accountId: 'same-member' },
        { resume: true },
      )
    ).resumed,
    true,
  );
  await assert.rejects(
    loginDesktop(request, { accountId: 'another-member' }, { resume: true }),
  );
  await assert.rejects(loginDesktop(request, { accountId: 'same-member' }));
});

test('a saved desktop device resumes through the public login action instead of replacing it', async () => {
  let resumed = false;
  const request = async (path, body, csrf) => {
    if (path === '/auriga/local-login/status')
      return {
        loggedIn: resumed,
        available: true,
        resumeAvailable: !resumed,
        csrf: 'local-csrf',
        account: resumed ? { subject: 'same-member' } : null,
      };
    assert.deepEqual(body, { action: 'resume' });
    assert.equal(csrf, 'local-csrf');
    resumed = true;
    return { step: { type: 'complete' } };
  };
  assert.equal(
    (
      await loginDesktop(
        request,
        { accountId: 'same-member' },
        { resume: true },
      )
    ).resumed,
    true,
  );
});

async function fixture(t) {
  const f = await startFixture({ runId: 'test-run' });
  t.after(() => f.close());
  return f;
}

test('only actual external input at the recipient establishes receipt; an initiating prompt does not', async (t) => {
  const f = await fixture(t);
  f.state.expect('cloud', 'request');
  const marker = f.state.marker('request');
  f.state.respond('cloud', { messages: [message(marker)], tools });
  assert.equal(f.state.snapshot().receipts.length, 0);
  f.state.respond('desktop', { messages: [external(marker)], tools });
  assert.equal(f.state.snapshot().receipts.length, 0);
  f.state.respond('cloud', { messages: [external(marker)], tools });
  assert.equal(f.state.snapshot().receipts.length, 1);
});

test('recipient reply is a real advertised tool call, with a verified original tool result required', async (t) => {
  const f = await fixture(t);
  const request = f.state.marker('request');
  f.state.plan({
    side: 'cloud',
    label: 'reply',
    trigger: request,
    external: true,
    tool: 'auriga_send_message',
    args: {
      target: 'aur1:1.2.3',
      body: { subtype: 'text', text: f.state.marker('reply') },
    },
  });
  const call = f.state.respond('cloud', {
    messages: [external(request)],
    tools,
  }).tool_calls[0];
  assert.equal(call.function.name, 'auriga_send_message');
  assert.equal(JSON.parse(call.function.arguments).target, 'aur1:1.2.3');
  assert.equal(f.state.snapshot().tools[0].result, null);
  f.state.respond('cloud', {
    messages: [
      external(request),
      {
        role: 'tool',
        tool_call_id: 'foreign',
        content: '{"status":"request_submitted"}',
      },
    ],
    tools,
  });
  assert.equal(f.state.snapshot().tools[0].result, null);
  f.state.respond('cloud', {
    messages: [
      external(request),
      {
        role: 'tool',
        tool_call_id: call.id,
        content: '{"status":"request_submitted"}',
      },
    ],
    tools,
  });
  assert.equal(f.state.snapshot().tools[0].result.status, 'request_submitted');
});

test('a missing native tool fails explicitly, including rc.2/managed capability gaps', async (t) => {
  const f = await fixture(t);
  f.state.plan({
    side: 'cloud',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  assert.throws(
    () =>
      f.state.respond('cloud', {
        messages: [message(f.state.marker('join'))],
        tools: [],
      }),
    { code: 'native_tool_unavailable' },
  );
  assert.equal(f.state.snapshot().tools[0].result, null);
});

test('PTC invokes the real tools namespace without supplying source identity', async (t) => {
  const f = await fixture(t);
  f.state.plan({
    side: 'desktop',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  const reply = f.state.respond('desktop', {
    messages: [message(f.state.marker('join'))],
    tools: [{ function: { name: 'run_code' } }],
  });
  assert.equal(reply.tool_calls[0].function.name, 'run_code');
  const args = JSON.parse(reply.tool_calls[0].function.arguments);
  assert.match(args.code, /tools\.auriga_network_join\(\{\}\)/);
});

test('repeated model history is deduplicated by network message id, but a second delivery id is rejected', async (t) => {
  const f = await fixture(t);
  f.state.expect('desktop', 'reply');
  const body = { messages: [external(f.state.marker('reply'))], tools };
  f.state.respond('desktop', body);
  f.state.respond('desktop', body);
  assert.equal(f.state.snapshot().receipts.length, 1);
  assert.throws(
    () =>
      f.state.respond('desktop', {
        ...body,
        messages: [
          external(
            f.state.marker('reply'),
            '019f0000-0000-7000-8000-000000000002',
          ),
        ],
      }),
    { code: 'duplicate_network_message' },
  );
});

test('local HTTP fixture supports streaming OpenAI responses and rejects unrelated control URLs', async (t) => {
  const f = await fixture(t);
  const denied = await globalThis.fetch(f.origin + '/control/not-the-key', {
    method: 'POST',
    body: '{}',
  });
  assert.equal(denied.status, 404);
  f.state.plan({
    side: 'desktop',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  const response = await globalThis.fetch(
    f.urls.desktop + '/chat/completions',
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        model: 'cloud-desk-fixture',
        stream: true,
        messages: [message(f.state.marker('join'))],
        tools: [{ function: { name: 'auriga_network_join' } }],
      }),
    },
  );
  assert.equal(response.status, 200);
  const stream = await response.text();
  assert.match(stream, /auriga_network_join/);
  assert.match(stream, /data: \[DONE\]/);
});

test('a submitted send or a local echo cannot satisfy cloud/desktop conversation acceptance', () => {
  const state = { failures: [], tools: [], receipts: [] };
  const report = {
    cloud: { ready: true, mode: 'managed', browserAuthenticated: true },
    desktop: {
      installation: 'native-cli-tarball',
      dshVersion: '0.2.1-rc.3',
      packageSHA256: 'a'.repeat(64),
    },
  };
  assert.throws(() => assertConversation(report, state));
  state.tools = ['cloud', 'desktop'].map((side) => ({
    side,
    label: `${side}_join`,
    tool: 'auriga_network_join',
    issued: true,
    result: {
      status: 'registered',
      address: `aur1:1.2.${side === 'cloud' ? '3' : '4'}`,
    },
  }));
  for (const [side, label] of [
    ['desktop', 'd2c_request'],
    ['cloud', 'd2c_reply'],
    ['cloud', 'c2d_request'],
    ['desktop', 'c2d_reply'],
  ])
    state.tools.push({
      side,
      label,
      tool: 'auriga_send_message',
      issued: true,
      result: { status: 'request_submitted' },
    });
  assert.throws(() => assertConversation(report, state));
  state.receipts = [
    ['cloud', 'd2c_request'],
    ['desktop', 'd2c_reply'],
    ['desktop', 'c2d_request'],
    ['cloud', 'c2d_reply'],
  ].map(([side, label], index) => ({
    side,
    marker: `CLOUD_DESK_test-run_${label}`,
    messageId: `message-${index}`,
    via: 'native-model-input',
  }));
  state.runId = 'test-run';
  assertConversation(report, state);
  report.cloud.browserAuthenticated = false;
  assert.throws(() => assertConversation(report, state));
  Object.assign(report.cloud, {
    memberAuthenticated: true,
    authentication: 'public-iam-cookie-redirect',
    httpsVerified: true,
  });
  assertConversation(report, state);
  report.cloud.httpsVerified = false;
  assert.throws(() => assertConversation(report, state));
  report.cloud.httpsVerified = true;
  state.receipts[0].side = 'desktop';
  assert.throws(() => assertConversation(report, state));
});

test('native RPC rejects mismatched response identity and never exposes returned error text', async () => {
  const client = nativeClient(async (_path, body) => ({
    type: 'server-response',
    rpcId: body.rpcId,
    result: { ok: false, error: { message: 'sensitive server body' } },
  }));
  await assert.rejects(
    client.rpc('session/create', {}),
    (error) =>
      error.code === 'native_rpc_failed' &&
      !error.message.includes('sensitive'),
  );
  const wrong = nativeClient(async () => ({
    type: 'server-response',
    rpcId: 'foreign',
    result: { ok: true, value: {} },
  }));
  await assert.rejects(wrong.rpc('session/create', {}), {
    code: 'native_rpc_identity_mismatch',
  });
});

test('shared-stack login or ACTIVE cannot stand in for the installation network configuration', () => {
  const access = {
    mode: 'domain',
    portalOrigin: { scheme: 'http', host: 'portal.example.com', port: 8080 },
    adminOrigin: { scheme: 'http', host: 'admin.example.com', port: 8084 },
  };
  assert.throws(() => validateRunConfig({}, access), {
    code: 'installation_network_config_missing',
  });
  const cfg = {
    connect: {
      platformOrigin: 'http://portal.example.com:8080',
      network: {
        realm: '1',
        wssUrl: 'wss://network.example.com/network',
        roots: [{}],
      },
    },
  };
  assert.throws(() => validateRunConfig(cfg, access), {
    code: 'desktop_platform_origin_not_supported',
  });
});

test('a model endpoint must be an actual private IP, never a DNS name shaped like one', () => {
  const access = {
    mode: 'ip',
    portalOrigin: { scheme: 'https', host: '127.0.0.1', port: 8443 },
    adminOrigin: { scheme: 'https', host: '127.0.0.1', port: 8444 },
  };
  const cfg = {
    connect: {
      platformOrigin: 'https://127.0.0.1:8443',
      issuer: 'https://127.0.0.1:8443',
      providerId: 'actual-provider',
      clientId: 'actual-client',
      network: {
        realm: '1',
        roots: [{}],
        wssUrl: 'wss://127.0.0.1:8443/network',
      },
    },
    cloudCwd: '/workspace',
    desktopDescriptor: 'desktop.json',
    enrollmentFile: 'new-stack.json',
    fixture: { cloudHost: '127.0.0.1', desktopHost: '10.evil.example.com' },
  };
  assert.throws(() => validateRunConfig(cfg, access));
  cfg.fixture.desktopHost = '192.168.0.2';
  validateRunConfig(cfg, access);
  cfg.connect.platformOrigin = 'http://10.evil.example.com';
  assert.throws(() => validateRunConfig(cfg, access), {
    code: 'desktop_platform_origin_not_supported',
  });
});

test('entry checks require the actual instance and preserve the IP path prefix', () => {
  const access = {
    mode: 'ip',
    portalOrigin: { scheme: 'https', host: '127.0.0.1', port: 8443 },
  };
  assertCloudEntry(
    'https://127.0.0.1:8443/i/real-instance/',
    access,
    'real-instance',
  );
  assert.throws(() =>
    assertCloudEntry(
      'https://127.0.0.1:8443/i/another/',
      access,
      'real-instance',
    ),
  );
  assert.throws(() =>
    accessOrigin({ scheme: 'http', host: 'user:pass@example.com', port: 80 }),
  );
});

test('fixture model edits and cleanup use fresh CAS revisions and only their own provider path', async () => {
  const writes = [],
    cleanup = [];
  let revision = 1;
  const client = {
    async rpc(method, args) {
      if (method === 'settings/describe')
        return {
          writable: true,
          namespaces: [
            {
              ns: 'llm-pi-ai',
              revision: revision++,
              value: { providers: { existing: { apiKey: '[redacted]' } } },
            },
          ],
        };
      writes.push({ method, args });
    },
  };
  await configureFixtureModel(
    client,
    'http://127.0.0.1:1234/fixture/v1',
    'cloud-desk-012345abcdef',
    cleanup,
  );
  for (const action of cleanup.reverse()) await action();
  const edits = writes.filter((w) => w.method === 'settings/mutate');
  assert.deepEqual(
    edits.map((w) => w.args.expectedRevision),
    [1, 2],
  );
  assert.deepEqual(
    edits.map((w) => w.args.ops[0].op),
    ['set', 'unset'],
  );
  for (const edit of edits)
    assert.deepEqual(edit.args.ops[0].path, [
      'providers',
      'cloud-desk-012345abcdef',
    ]);
  assert.equal(JSON.stringify(writes).includes('[redacted]'), false);
  assert.equal(writes.at(-1).method, 'credentials/unset');
});

test('a signed-panel 502 does not mask a healthy managed Node plugin or bypass failed authentication', () => {
  const health = {
    status: 200,
    body: { status: 'ok', ready: true, protocol: 1, pluginVersion: 'test' },
  };
  assert.deepEqual(assessManagedPlugin(health, { status: 502 }), {
    mode: 'managed',
    pluginVersion: 'test',
    panelStatus: 502,
    panelAvailable: false,
    nodeHealthy: true,
  });
  assert.throws(() => assessManagedPlugin(health, { status: 401 }));
  assert.throws(() => assessManagedPlugin({ status: 503 }, { status: 502 }));
});

test('existing member MFA uses the real OTP field, including platform_totp code', () => {
  assert.equal(
    loginOtpField({ formId: 'totp', fields: [{ kind: 'otp', name: 'otp' }] }),
    'otp',
  );
  assert.equal(
    loginOtpField({
      formId: 'platform_totp',
      fields: [{ kind: 'otp', name: 'code' }],
    }),
    'code',
  );
  assert.throws(() =>
    loginOtpField({ fields: [{ kind: 'text', name: 'password' }] }),
  );
});

test('browser-only loopback routing explicitly selects direct proxy and leaves system routing alone', () => {
  const access = {
    mode: 'domain',
    portalOrigin: { host: 'portal.example.com' },
    adminOrigin: { host: 'admin.example.com' },
    instances: { parentDomain: 'i.example.com' },
  };
  assert.deepEqual(loopbackBrowserArgs(access, false), []);
  const args = loopbackBrowserArgs(access, true);
  assert.ok(args.includes('--proxy-server=direct://'));
  assert.ok(args.includes('--proxy-bypass-list=*'));
  assert.ok(args.some((v) => v.includes('MAP portal.example.com 127.0.0.1')));
});

test('native failure evidence excludes model payloads and capability URLs', async () => {
  const client = {
    async rpc(method) {
      if (method === 'session/projections') return { asOfSeq: 8 };
      return {
        records: [
          {
            event: {
              type: 'llm/attempt-failed',
              data: {
                prompt: 'private prompt',
                error: {
                  message:
                    'Connection error at http://127.0.0.1/private-capability',
                },
              },
            },
          },
        ],
      };
    },
  };
  const result = await diagnoseNativeSession(client, 'own-test-session');
  assert.deepEqual(result.errorClasses, ['Connection error']);
  assert.equal(JSON.stringify(result).includes('private'), false);
});

test('conversation capability checks use the normal standard Agent preset', async () => {
  const requests = [];
  const client = nativeClient(async (_path, body) => {
    requests.push(body);
    return {
      type: 'server-response',
      rpcId: body.rpcId,
      result: {
        ok: true,
        value:
          body.method === 'session/create'
            ? { sessionId: 'own-session' }
            : {
                selected: {
                  provider: 'local-fixture',
                  model: 'cloud-desk-fixture',
                },
              },
      },
    };
  });
  await client.createSession('/own-workspace', 'local-fixture');
  assert.equal(requests[0].payload.args.request.agentPreset, 'standard');
});

test('auxiliary title input containing the marker or quoted external header is not an Agent command or receipt', async (t) => {
  const f = await fixture(t);
  f.state.plan({
    side: 'cloud',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  const title = {
    max_tokens: 64,
    messages: [message('User message 1:\n' + f.state.marker('join'))],
    tools: [],
  };
  assert.doesNotThrow(() => f.state.respond('cloud', title));
  assert.equal(f.state.snapshot().failures.length, 0);
  f.state.expect('cloud', 'request');
  f.state.respond('cloud', {
    max_tokens: 64,
    messages: [
      message(
        'User message 1:\n' + external(f.state.marker('request')).content,
      ),
    ],
    tools: [],
  });
  assert.equal(f.state.snapshot().receipts.length, 0);
});

test('Agent context framing does not hide a genuine tool-capability failure', async (t) => {
  const f = await fixture(t);
  f.state.plan({
    side: 'cloud',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  assert.throws(
    () =>
      f.state.respond('cloud', {
        max_tokens: 4096,
        messages: [message('Agent context\n' + f.state.marker('join'))],
        tools: [],
      }),
    { code: 'native_tool_unavailable' },
  );
});

test('native trailing context does not displace the latest actual user command', async (t) => {
  const f = await fixture(t);
  f.state.plan({
    side: 'cloud',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  const response = f.state.respond('cloud', {
    max_tokens: 4096,
    messages: [
      message(f.state.marker('join')),
      message('Additional native runtime context'),
    ],
    tools: [{ function: { name: 'auriga_network_join' } }],
  });
  assert.equal(response.tool_calls?.[0]?.function.name, 'auriga_network_join');
});

test('fixture diagnostics retain only valid tool names and never raw model input', async (t) => {
  const f = await fixture(t);
  f.state.plan({
    side: 'cloud',
    label: 'join',
    tool: 'auriga_network_join',
    args: {},
  });
  assert.throws(
    () =>
      f.state.respond('cloud', {
        max_tokens: 4096,
        messages: [message('private model context\n' + f.state.marker('join'))],
        tools: [{ function: { name: 'credential/should-not-persist' } }],
      }),
    { code: 'native_tool_unavailable' },
  );
  const evidence = JSON.stringify(f.state.snapshot());
  assert.equal(evidence.includes('private model context'), false);
  assert.equal(evidence.includes('should-not-persist'), false);
});
