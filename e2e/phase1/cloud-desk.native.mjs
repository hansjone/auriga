import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { authenticatorCode } from '../network/support/dual-cli.mjs';
import { poll } from './cloud-desk.desktop.mjs';

function failure(code) {
  return Object.assign(new Error(code), { code });
}

export function nativeClient(request) {
  const sessions = [];
  const rpc = async (method, args = {}) => {
    const rpcId = randomUUID();
    const result = await request('/api/' + method, {
      type: 'client-request',
      rpcId,
      method,
      payload: { args },
    });
    if (result.type !== 'server-response' || result.rpcId !== rpcId)
      throw failure('native_rpc_identity_mismatch');
    if (result.result?.ok !== true) {
      const error = failure('native_rpc_failed');
      error.rpcMethod = method;
      const remoteCode = result.result?.error?.code;
      if (/^[A-Za-z0-9_.:-]{1,80}$/.test(remoteCode ?? ''))
        error.remoteCode = remoteCode;
      throw error;
    }
    return result.result.value;
  };
  return {
    rpc,
    async activePlugin() {
      const rows = await rpc('pluginManager/listPlugins');
      const plugin = rows.find(
        (p) =>
          p.moduleName === 'auriga-dsh' &&
          p.enabled &&
          p.fiberPhase === 'active',
      );
      assert.ok(plugin, 'Auriga must be active in the real native inventory');
      return {
        entryId: plugin.entryId,
        moduleName: plugin.moduleName,
        enabled: true,
        fiberPhase: 'active',
      };
    },
    async createSession(cwd, provider) {
      const { sessionId } = await rpc('session/create', {
        request: { cwd, agentPreset: 'standard' },
      });
      assert.equal(typeof sessionId, 'string');
      sessions.push(sessionId);
      const { selected } = await rpc('session/selectModel', {
        request: { sessionId, provider, model: 'cloud-desk-fixture' },
      });
      assert.equal(selected.provider, provider);
      assert.equal(selected.model, 'cloud-desk-fixture');
      return sessionId;
    },
    async prompt(sessionId, marker) {
      assert.ok(sessions.includes(sessionId));
      assert.equal(
        (
          await rpc('session/prompt', {
            request: {
              sessionId,
              requestId: randomUUID(),
              mode: 'queue',
              content: [{ type: 'text', text: marker }],
            },
          })
        ).accepted,
        true,
      );
    },
    async cancel() {
      for (const sessionId of sessions)
        await rpc('session/cancel', { request: { sessionId } });
    },
  };
}

export async function diagnoseNativeSession(client, sessionId) {
  const cut = await client.rpc('session/projections', {
    request: { sessionId },
  });
  if (!Number.isInteger(cut?.asOfSeq)) return { projectionAvailable: false };
  const page = await client.rpc('session/page', {
    request: {
      address: { kind: 'session', sessionId },
      throughSeq: cut.asOfSeq,
      maxMessages: 40,
    },
  });
  const errors = new Set();
  const known = [
    'ECONNREFUSED',
    'EHOSTUNREACH',
    'ENETUNREACH',
    'ETIMEDOUT',
    'MODEL_NOT_CONFIGURED',
    'MODEL_NOT_FOUND',
    'MODEL_UNAVAILABLE',
    'PROVIDER_NOT_FOUND',
    'fetch failed',
    'Connection error',
    'permission denied',
    'Unknown tool',
    'apiKey',
  ];
  const scan = (value, depth = 0) => {
    if (depth > 20) return;
    if (typeof value === 'string') {
      for (const code of known) if (value.includes(code)) errors.add(code);
    } else if (Array.isArray(value))
      for (const item of value) scan(item, depth + 1);
    else if (value && typeof value === 'object')
      for (const item of Object.values(value)) scan(item, depth + 1);
  };
  for (const row of page.records) scan(row.event?.data);
  return {
    projectionAvailable: true,
    asOfSeq: cut.asOfSeq,
    eventTypes: [
      ...new Set(
        page.records
          .map((row) => row.event?.type)
          .filter((type) => /^[a-zA-Z0-9_/-]{1,80}$/.test(type ?? '')),
      ),
    ],
    errorClasses: [...errors],
  };
}

// Edits only a unique fixture provider, using the public compare-and-swap settings API.
// No pre-existing secret or provider is copied back from its redacted settings view.
export async function configureFixtureModel(
  client,
  baseURL,
  provider,
  cleanup,
) {
  assert.match(provider, /^cloud-desk-[a-f0-9]{12}$/);
  const url = new URL(baseURL);
  assert.equal(url.protocol, 'http:');
  const ref = 'CLOUD_DESK_FIXTURE_' + provider.slice(-12).toUpperCase();
  const view = async () => {
    const described = await client.rpc('settings/describe');
    assert.equal(
      described.writable,
      true,
      'native profile must permit normal model settings',
    );
    const namespace = described.namespaces.find((v) => v.ns === 'llm-pi-ai');
    assert.ok(namespace, 'public llm-pi-ai settings must be available');
    return namespace;
  };
  const original = await view();
  assert.equal(
    original.value?.providers?.[provider],
    undefined,
    'fixture provider must be fresh',
  );
  await client.rpc('credentials/set', { ref, value: 'local-test-only' });
  cleanup.push(() => client.rpc('credentials/unset', { ref }));
  await client.rpc('settings/mutate', {
    ns: original.ns,
    expectedRevision: original.revision,
    ops: [
      {
        op: 'set',
        path: ['providers', provider],
        value: {
          displayName: 'Local cloud/desktop acceptance fixture',
          api: 'openai-completions',
          apiKeyEnv: ref,
          baseURL,
          models: [
            {
              id: 'cloud-desk-fixture',
              contextWindow: 262144,
              maxTokens: 4096,
            },
          ],
        },
      },
    ],
  });
  cleanup.push(async () => {
    const current = await view();
    await client.rpc('settings/mutate', {
      ns: current.ns,
      expectedRevision: current.revision,
      ops: [{ op: 'unset', path: ['providers', provider] }],
    });
  });
}

export async function loginDesktop(
  request,
  member,
  { resume = false, reauthenticate = false } = {},
) {
  let status = await request('/auriga/local-login/status');
  let reauthenticated = false;
  if (resume && !status.loggedIn && status.resumeAvailable) {
    assert.ok(member.accountId);
    const result = await request(
      '/auriga/local-login/login',
      { action: 'resume' },
      status.csrf,
    );
    assert.equal(result.step?.type, 'complete');
    status = await poll(async () => {
      const current = await request('/auriga/local-login/status');
      return current.loggedIn && current.available ? current : null;
    }, 'desktop_resume_not_ready');
  }
  if (resume && status.loggedIn) {
    assert.ok(member.accountId);
    assert.equal(
      status.account?.subject,
      member.accountId,
      'resume only the same authenticated test member',
    );
    if (!reauthenticate)
      return {
        loggedIn: true,
        available: status.available,
        accountId: status.account.subject,
        resumed: true,
      };
    const signedOut = await request(
      '/auriga/local-login/logout',
      {},
      status.csrf,
    );
    assert.equal(signedOut.loggedIn, false);
    status = await poll(
      async () => {
        const current = await request('/auriga/local-login/status');
        return current.loggedIn === false &&
          current.resumeAvailable === false &&
          current.remoteRevocation === 'complete'
          ? current
          : null;
      },
      'desktop_revocation_not_complete',
      90000,
    );
    reauthenticated = true;
  }
  assert.equal(
    status.loggedIn,
    false,
    'desktop starts without another identity',
  );
  const begin = await request(
    '/auriga/local-login/login',
    { action: 'begin' },
    status.csrf,
  );
  assert.equal(begin.step.type, 'form');
  let result = await request(
    '/auriga/local-login/login',
    {
      action: 'advance',
      input: {
        type: 'form',
        formId: begin.step.formId,
        values: { username: member.username, password: member.password },
      },
    },
    status.csrf,
  );
  if (result.step?.formId === 'first_totp') {
    const secret = result.step.fields
      .find((f) => f.name === 'enrollment')
      ?.label.en.match(/([A-Z2-7]{32})$/)?.[1];
    assert.ok(
      secret,
      'authenticator enrollment comes only from the real login transaction',
    );
    await poll(
      () => 30000 - (Date.now() % 30000) > 5000,
      'authenticator_window',
    );
    result = await request(
      '/auriga/local-login/login',
      {
        action: 'advance',
        input: {
          type: 'form',
          formId: 'first_totp',
          values: { otp: authenticatorCode(secret) },
        },
      },
      status.csrf,
    );
  } else if (['totp', 'platform_totp'].includes(result.step?.formId)) {
    const fields = result.step.fields.filter((field) => field.kind === 'otp');
    assert.equal(fields.length, 1);
    assert.ok(['otp', 'code'].includes(fields[0].name) && member.totpSecret);
    await poll(
      () => 30000 - (Date.now() % 30000) > 5000,
      'authenticator_window',
    );
    result = await request(
      '/auriga/local-login/login',
      {
        action: 'advance',
        input: {
          type: 'form',
          formId: result.step.formId,
          values: { [fields[0].name]: authenticatorCode(member.totpSecret) },
        },
      },
      status.csrf,
    );
  }
  assert.equal(result.step.type, 'complete');
  status = await poll(async () => {
    const value = await request('/auriga/local-login/status');
    return value.loggedIn && value.available ? value : null;
  }, 'desktop_login_not_ready');
  if (member.accountId) assert.equal(status.account?.subject, member.accountId);
  return {
    loggedIn: status.loggedIn,
    available: status.available,
    accountId: status.account?.subject,
    ...(reauthenticated ? { reauthenticated: true } : {}),
  };
}

export function assertConversation(report, state) {
  assert.equal(report.cloud?.ready, true);
  assert.equal(report.cloud?.mode, 'managed');
  assert.ok(
    report.cloud?.browserAuthenticated === true ||
      (report.cloud?.memberAuthenticated === true &&
        report.cloud?.authentication === 'public-iam-cookie-redirect' &&
        report.cloud?.httpsVerified === true),
    'authenticated cloud entry required',
  );
  assert.ok(
    ['native-cli-tarball', 'installed-app-tarball'].includes(
      report.desktop?.installation,
    ),
  );
  assert.equal(report.desktop?.dshVersion, '0.2.1-rc.3');
  assert.match(report.desktop?.packageSHA256, /^[a-f0-9]{64}$/);
  assert.deepEqual(state.failures, []);
  const joins = ['cloud', 'desktop'].map((side) => {
    const tool = state.tools.find(
      (t) => t.side === side && t.label === side + '_join',
    );
    assert.equal(tool?.tool, 'auriga_network_join');
    assert.equal(tool?.issued, true);
    assert.equal(tool?.result?.status, 'registered');
    assert.match(tool.result.address, /^aur1:[0-9a-f]+\.[0-9a-f]+\.[0-9a-f]+$/);
    return tool.result.address;
  });
  assert.notEqual(joins[0], joins[1], 'distinct actual source addresses');
  for (const [sender, receiver, label] of [
    ['desktop', 'cloud', 'd2c_request'],
    ['cloud', 'desktop', 'd2c_reply'],
    ['cloud', 'desktop', 'c2d_request'],
    ['desktop', 'cloud', 'c2d_reply'],
  ]) {
    const tool = state.tools.find(
      (t) => t.label === label && t.side === sender,
    );
    assert.equal(tool?.tool, 'auriga_send_message');
    assert.equal(tool?.issued, true);
    assert.equal(tool?.result?.status, 'request_submitted');
    const receipts = state.receipts.filter(
      (r) =>
        r.side === receiver &&
        r.marker === `CLOUD_DESK_${state.runId}_${label}`,
    );
    assert.equal(receipts.length, 1, 'actual recipient model input required');
    assert.equal(receipts[0].via, 'native-model-input');
    assert.match(receipts[0].messageId, /^[A-Za-z0-9-]+$/);
  }
  assert.equal(state.receipts.length, 4);
  assert.equal(new Set(state.receipts.map((r) => r.messageId)).size, 4);
}
