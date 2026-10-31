import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { join } from 'node:path';
import { poll } from './cloud-desk.desktop.mjs';
import { configureFixtureModel } from './cloud-desk.native.mjs';
import { startFixture } from './cloud-desk.fixture.mjs';
import { waitAssistant } from './native-models.mjs';
import {
  nativeToolEvidence,
  nativeNetworkReceipt,
} from './native-network-proof.mjs';

export function validateManagedCloud(cfg) {
  assert.equal(
    Object.keys(cfg.managedCloud).sort().join(','),
    'allowRealModels,model',
  );
  assert.equal(cfg.managedCloud.allowRealModels, true);
  assert.ok(
    typeof cfg.managedCloud.model === 'string' &&
      cfg.managedCloud.model.length > 0 &&
      cfg.managedCloud.model.length <= 200,
  );
}

// The managed provider belongs to the installation. Never copy its evaluated
// Authorization into a settings write just to add a synthetic cloud provider.
export async function managedCloudConversation({
  cfg,
  cloud,
  desktop,
  clients,
  descriptor,
  report,
  stage,
  cleanup,
}) {
  report.modelPlanning = {
    cloud: {
      synthetic: false,
      provider: 'auriga-managed',
      model: cfg.managedCloud.model,
    },
    desktop: { synthetic: true, kind: 'owned-loopback-fixture' },
  };
  report.tools = [];
  report.receipts = [];
  const sessions = {};
  const marker = (label) => `CLOUD_DESK_${report.runId}_${label}`;
  async function events(side) {
    const client = clients[side],
      sessionId = sessions[side];
    const cut = await client.rpc('session/projections', {
      request: { sessionId },
    });
    assert.ok(Number.isInteger(cut.asOfSeq));
    return (
      await client.rpc('session/page', {
        request: {
          address: { kind: 'session', sessionId },
          throughSeq: cut.asOfSeq,
          maxMessages: 200,
        },
      })
    ).records.map((row) => row.event);
  }
  async function cloudTool(name, accepts, status) {
    return poll(
      async () => {
        const proof = nativeToolEvidence(await events('cloud'), name, accepts);
        if (!proof) return null;
        const value = proof.value;
        assert.equal(value.status, status);
        report.tools = report.tools.filter(
          (tool) => tool.callId !== proof.callId,
        );
        report.tools.push({
          side: 'cloud',
          name,
          callId: proof.callId,
          status: value.status,
          ...(value.address ? { address: value.address } : {}),
        });
        return value;
      },
      'cloud_native_tool_result_missing',
      120000,
    );
  }
  const fixture = await startFixture({
    host: '127.0.0.1',
    runId: report.runId,
  });
  cleanup.push(() => fixture.close());
  async function desktopTool(label, status) {
    return poll(
      () => {
        const plan = fixture.state.plans.find((item) => item.label === label);
        if (!plan?.result) return null;
        assert.equal(plan.result.status, status);
        return plan.result;
      },
      'desktop_native_tool_result_missing',
      90000,
    );
  }
  async function receipt(side, label) {
    return poll(
      async () => {
        const proof =
          side === 'desktop'
            ? fixture.state.receipts.find(
                (item) => item.side === side && item.marker === marker(label),
              )
            : nativeNetworkReceipt(await events('cloud'), marker(label));
        return proof ? { ...proof, side } : null;
      },
      `${side}_network_receipt_missing`,
      120000,
    );
  }
  stage('native-model-preparation');
  const ns = (await clients.cloud.rpc('settings/describe')).namespaces.find(
    (item) => item.ns === 'llm-pi-ai',
  );
  assert.ok(
    ns?.value?.providers?.['auriga-managed']?.models?.some(
      (item) => item.id === cfg.managedCloud.model,
    ),
  );
  const provider = 'cloud-desk-' + randomBytes(6).toString('hex');
  await configureFixtureModel(
    clients.desktop,
    fixture.urls.desktop,
    provider,
    cleanup,
  );
  sessions.cloud = await clients.cloud.createSession(
    cfg.cloudCwd,
    'auriga-managed',
    cfg.managedCloud.model,
  );
  cleanup.push(() => clients.cloud.cancel());
  sessions.desktop = await clients.desktop.createSession(
    join(descriptor.state, 'session-cwd'),
    provider,
  );
  cleanup.push(() => clients.desktop.cancel());
  report.sessions = { ...sessions };
  const addresses = {};
  stage('cloud-native-network-join');
  await clients.cloud.prompt(
    sessions.cloud,
    `Authorized Auriga messaging acceptance test. Call auriga_network_join exactly once with {} now. Do not call other tools. After its result say ${marker('cloud_join')}.`,
  );
  addresses.cloud = (
    await cloudTool(
      'auriga_network_join',
      (args) => Object.keys(args).length === 0,
      'registered',
    )
  ).address;
  stage('desktop-native-network-join');
  fixture.state.plan({
    side: 'desktop',
    label: 'desktop_join',
    tool: 'auriga_network_join',
    args: {},
  });
  await clients.desktop.prompt(sessions.desktop, marker('desktop_join'));
  addresses.desktop = (await desktopTool('desktop_join', 'registered')).address;
  assert.notEqual(addresses.cloud, addresses.desktop);
  for (const address of Object.values(addresses))
    assert.match(address, /^aur1:[a-f0-9]+\.[a-f0-9]+\.[a-f0-9]+$/);
  report.addresses = addresses;
  for (const side of ['cloud', 'desktop']) {
    stage(side + '-receive-enable');
    const request = side === 'cloud' ? cloud.request : desktop.request;
    const local = BigInt('0x' + addresses[side].split('.').at(-1)).toString();
    const enable = async (enabled) => {
      const state = await request('/auriga/local-login/status');
      return request(
        '/auriga/local-login/messaging/receive',
        { local, enabled },
        state.csrf,
      );
    };
    await enable(true);
    cleanup.push(() => enable(false));
  }
  const send = (target, label) => ({
    target,
    body: { subtype: 'text', text: marker(label) },
  });
  stage('arm-cloud-native-reply');
  await clients.cloud.prompt(
    sessions.cloud,
    `Authorized test instruction: When this session receives a genuine Auriga external message with body exactly ${marker('d2c_request')}, call auriga_send_message once with ${JSON.stringify(send(addresses.desktop, 'd2c_reply'))}. Do not send now or call other tools. Say ${marker('armed')} now.`,
  );
  report.cloudArm = await waitAssistant(
    clients.cloud,
    sessions.cloud,
    marker('armed'),
    120000,
  );
  stage('d2c-request-and-reply');
  fixture.state.expect('desktop', 'd2c_reply');
  fixture.state.plan({
    side: 'desktop',
    label: 'd2c_request',
    tool: 'auriga_send_message',
    args: send(addresses.cloud, 'd2c_request'),
  });
  await clients.desktop.prompt(sessions.desktop, marker('d2c_request'));
  await desktopTool('d2c_request', 'request_submitted');
  report.receipts.push(await receipt('cloud', 'd2c_request'));
  await cloudTool(
    'auriga_send_message',
    (args) =>
      args.target === addresses.desktop &&
      args.body?.text === marker('d2c_reply'),
    'request_submitted',
  );
  report.receipts.push(await receipt('desktop', 'd2c_reply'));
  stage('c2d-request-and-reply');
  fixture.state.expect('desktop', 'c2d_request');
  fixture.state.plan({
    side: 'desktop',
    label: 'c2d_reply',
    tool: 'auriga_send_message',
    args: send(addresses.cloud, 'c2d_reply'),
    trigger: marker('c2d_request'),
    external: true,
  });
  await clients.cloud.prompt(
    sessions.cloud,
    `Call auriga_send_message exactly once with ${JSON.stringify(send(addresses.desktop, 'c2d_request'))}. When external reply ${marker('c2d_reply')} arrives, acknowledge it without tools. Do not send anything else.`,
  );
  await cloudTool(
    'auriga_send_message',
    (args) =>
      args.target === addresses.desktop &&
      args.body?.text === marker('c2d_request'),
    'request_submitted',
  );
  report.receipts.push(await receipt('desktop', 'c2d_request'));
  await desktopTool('c2d_reply', 'request_submitted');
  report.receipts.push(await receipt('cloud', 'c2d_reply'));
  report.desktopTools = fixture.state.plans.map((plan) => ({
    label: plan.label,
    tool: plan.tool,
    result: plan.result,
  }));
  assert.equal(report.receipts.length, 4);
  assert.equal(new Set(report.receipts.map((item) => item.messageId)).size, 4);
}
