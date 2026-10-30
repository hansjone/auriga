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
    if (result.result?.ok !== true) throw failure('native_rpc_failed');
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
        request: { cwd, agentPreset: 'minimal' },
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

export async function loginDesktop(request, member) {
  let status = await request('/auriga/local-login/status');
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
  }
  assert.equal(result.step.type, 'complete');
  status = await poll(async () => {
    const value = await request('/auriga/local-login/status');
    return value.loggedIn && value.available ? value : null;
  }, 'desktop_login_not_ready');
  return { loggedIn: status.loggedIn, available: status.available };
}

export function assertConversation(report, state) {
  assert.equal(report.cloud?.ready, true);
  assert.equal(report.cloud?.mode, 'managed');
  assert.equal(report.cloud?.browserAuthenticated, true);
  assert.equal(report.desktop?.installation, 'native-cli-tarball');
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
