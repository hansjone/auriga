import test from 'node:test';
import assert from 'node:assert/strict';
import Ajv from 'ajv/dist/2020.js';
import { readFile } from 'node:fs/promises';
import { createWorld, peer, self } from '../host/fixtures.js';
import validateInput from '../../../src/generated/network/validate-input.js';
import { createAgentSendTool } from '../../../src/network/tools/index.js';

// Independently compile the original schemas; do not validate generated output
// against another copy of the generated schema.
const ajv = new Ajv({ strict: false, validateFormats: false });
for (const file of ['address', 'envelope', 'send']) {
  ajv.addSchema(
    JSON.parse(
      await readFile(
        new URL(
          `../../../../../contracts/network/${file}.schema.json`,
          import.meta.url,
        ),
        'utf8',
      ),
    ),
  );
}
const schemaId =
  'https://auriga.example.com/contracts/network/send.schema.json';
const originalInput = ajv.getSchema(`${schemaId}#/$defs/AgentSendToolInput`);
const originalRequest = ajv.getSchema(`${schemaId}#/$defs/SubmitRequest`);

test('standalone input validator agrees with original schema for bodies, reserved fields and integer boundaries', () => {
  const target = 'aur1:2.3f.1';
  const body = { subtype: 'text', text: 'hello' };
  const cases = [
    null,
    {},
    { target, body },
    { target, body, thread: 'thread-1' },
    ...[
      'from',
      'sender_kind',
      'message_class',
      'authorization_ref',
      'source_binding',
      'human_intent_id',
      'flags',
      'ext',
      'to',
    ].map((key) => ({ target, body, [key]: true })),
    ...[
      'text',
      'progress',
      'status',
      'artifact_ref',
      'control',
      'conversation_opened',
    ].map((subtype) => ({ target, body: { ...body, subtype } })),
    ...['0', '18446744073709551615', '18446744073709551616', '01', '-1'].map(
      (revision) => ({
        target,
        body: { subtype: 'progress', revision, state: 'running' },
      }),
    ),
    ...['host', 'model'].map((provenance) => ({
      target,
      body: { ...body, provenance },
    })),
  ];
  for (const input of cases)
    assert.equal(
      validateInput(input),
      originalInput(input),
      JSON.stringify(input),
    );
});

test('prepared request validates against the original SubmitRequest contract without stamped envelope fields', async (t) => {
  const w = await createWorld();
  t.after(() => w.ledger.close());
  w.host.addSession('native', { cwd: 'D:/example' });
  await w.adapter.bindExistingSession({
    opId: 'bind',
    hostSessionId: 'native',
    cwd: 'D:/example',
  });
  const tool = createAgentSendTool({
    ledger: w.ledger,
    localAddress: w.adapter.localAddress,
    sourceFor: () => ({
      address: self('1'),
      senderKind: 'agent',
      sourceBinding: 'source-1',
      producerEpoch: '1',
      sendAllowed: true,
      expiresAt: Date.now() + 60_000,
      firstContactAllowed: true,
    }),
    resolveTarget: ({ local }) => peer(local),
    rateLimit: { messages: 3, windowMs: 1_000 },
    releaseResult: async () => {},
  });
  for (const thread of [undefined, 'thread-1']) {
    const input = {
      target: 'aur1:3f.1',
      body: { subtype: 'text', text: '你好' },
      ...(thread ? { thread } : {}),
    };
    const request = await tool.prepare(input, {
      agent: { session: { id: 'native' } },
    });
    assert.equal(
      originalRequest(request),
      true,
      JSON.stringify(originalRequest.errors),
    );
    for (const [key, value] of Object.entries({
      from: self('1'),
      sender_kind: 'agent',
      message_class: 'unicast',
      authorization_ref: 'forged',
    })) {
      assert.equal(originalRequest({ ...request, [key]: value }), false, key);
    }
  }
  const firstContact = await tool.prepare(
    { target: 'aur1:3f.0', body: { subtype: 'text', text: 'hello' } },
    { agent: { session: { id: 'native' } } },
  );
  assert.equal(firstContact.kind, 'first_contact');
  assert.equal(firstContact.contact_scope, 'conversation');
  assert.equal(
    originalRequest(firstContact),
    true,
    JSON.stringify(originalRequest.errors),
  );
});
