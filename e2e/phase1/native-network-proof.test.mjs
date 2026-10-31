import test from 'node:test';
import assert from 'node:assert/strict';
import {
  nativeToolEvidence,
  nativeNetworkReceipt,
  nativeAssistantEvidence,
} from './native-network-proof.mjs';

test('managed reply arming waits past an earlier completed turn and requires a fresh assistant marker with usage', () => {
  const old = [{ type: 'turn/end', data: {} }];
  const reply = {
    type: 'assistant/message',
    data: {
      message: { content: [{ type: 'text', text: 'current-arm' }] },
      usage: { inputTokens: 20, outputTokens: 3 },
    },
  };
  assert.equal(nativeAssistantEvidence(old, 'current-arm'), null);
  assert.equal(
    nativeAssistantEvidence([...old, reply], 'current-arm').inputTokens,
    20,
  );
  for (const data of [
    { ...reply.data, interrupted: true },
    { ...reply.data, usage: { inputTokens: 0, outputTokens: 0 } },
    {
      ...reply.data,
      message: { content: [{ type: 'text', text: 'old-arm' }] },
    },
  ])
    assert.equal(
      nativeAssistantEvidence([{ ...reply, data }], 'current-arm'),
      null,
    );
});

test('native network tool proof requires matching call identity, arguments and a non-error result', () => {
  const call = {
    type: 'tool/call',
    data: { name: 'auriga_network_join', callId: 'call-1', arguments: '{}' },
  };
  const end = {
    type: 'tool/result',
    data: {
      message: {
        toolCallId: 'call-1',
        content: [
          {
            type: 'text',
            text: '{"status":"registered","address":"aur1:1.2.3"}',
          },
        ],
      },
    },
  };
  const verify = (events) =>
    nativeToolEvidence(
      events,
      'auriga_network_join',
      (args) => Object.keys(args).length === 0,
    );
  assert.equal(verify([call, end]).value.address, 'aur1:1.2.3');
  assert.equal(verify([{ type: 'assistant/message', data: end.data }]), null);
  assert.equal(
    verify([
      call,
      {
        ...end,
        data: { message: { ...end.data.message, toolCallId: 'other' } },
      },
    ]),
    null,
  );
  assert.equal(
    verify([
      call,
      { ...end, data: { message: { ...end.data.message, isError: true } } },
    ]),
    null,
  );
  assert.equal(
    verify([
      {
        ...call,
        data: { ...call.data, arguments: '{"target":"model-controlled"}' },
      },
      end,
    ]),
    null,
  );
});

test('native receipt requires the exact external body and its durable native request identity', () => {
  const received = {
    type: 'user/message',
    data: {
      content: [
        {
          type: 'text',
          text: '[Auriga external message message-1]\nunique-marker',
        },
      ],
      source: { kind: 'user', rpcId: 'message-1' },
    },
  };
  assert.equal(
    nativeNetworkReceipt([received], 'unique-marker').messageId,
    'message-1',
  );
  for (const event of [
    { ...received, type: 'assistant/message' },
    {
      ...received,
      data: {
        ...received.data,
        source: { kind: 'user', rpcId: 'manual-prompt' },
      },
    },
    {
      ...received,
      data: {
        ...received.data,
        content: [
          { type: 'text', text: 'Quoted ' + received.data.content[0].text },
        ],
      },
    },
    {
      ...received,
      data: {
        ...received.data,
        content: [
          { type: 'text', text: received.data.content[0].text + '-other' },
        ],
      },
    },
  ])
    assert.equal(nativeNetworkReceipt([event], 'unique-marker'), null);
});
