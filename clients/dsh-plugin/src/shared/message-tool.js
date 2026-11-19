// @ts-nocheck
import { Buffer } from 'node:buffer';
import { createHash } from 'node:crypto';
import { inputSchema, encoding } from '../generated/network/contract.js';
import validateInput from '../generated/network/validate-input.js';
import { parseAddressText } from '../network/host/address.js';
import { snapshotInput } from '../network/tools/json-input.js';
import { mapSendReceipt } from '../network/tools/send.js';
import { executionSession } from './task-context.js';
import { fail } from './protocol.js';

/** A stable original v2 idempotency key from the public Host call identity. */
function messageId(sessionId, callId) {
  if (typeof callId !== 'string' || !callId || callId.length > 512) fail();
  const bytes = createHash('sha256')
    .update('Auriga.SharedMessage.Tool.v1\0' + sessionId + '\0' + callId)
    .digest()
    .subarray(0, 16);
  bytes[6] = (bytes[6] & 15) | 128;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = bytes.toString('hex');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/** This registers SEND only. Receive remains a trusted, durable background path. */
export function installSharedMessageTool(ctx, { settings }) {
  return ctx.tools.register({
    name: 'auriga_send_message',
    description: '从当前已授权会话向 Auriga 会话地址发送消息',
    parameters: {
      type: 'object',
      properties: {
        to: JSON.parse(JSON.stringify(inputSchema.properties.target)),
        body: JSON.parse(JSON.stringify(inputSchema.properties.body)),
      },
      required: ['to', 'body'],
      additionalProperties: false,
    },
    output: {
      schema: { type: 'object', additionalProperties: true },
      render: (_args, value) => [{ type: 'text', text: JSON.stringify(value) }],
    },
    async execute(args, execution) {
      const input = snapshotInput(args);
      if (
        !input ||
        typeof input !== 'object' ||
        Object.keys(input).sort().join(',') !== 'body,to' ||
        !validateInput({ target: input.to, body: input.body }) ||
        Buffer.byteLength(JSON.stringify(input.body)) > encoding.maxBodyBytes
      )
        fail('common.validation_failed');
      const sessionId = executionSession(ctx, execution);
      const target = parseAddressText(
        input.to,
        settings().messageTrust?.realm ?? null,
      );
      if (target.node === '0' || target.local === '0')
        fail('common.validation_failed');
      if (execution.signal?.aborted || !ctx.aurigaShared?.downstream?.send)
        fail('common.unavailable');
      const result = await ctx.aurigaShared.downstream.send(
        sessionId,
        {
          messageId: messageId(sessionId, execution.callId),
          target: {
            realm: target.realm,
            node: target.node,
            local: target.local,
          },
          body: input.body,
        },
        execution.signal,
        execution,
      );
      if (
        execution.signal?.aborted ||
        executionSession(ctx, execution) !== sessionId
      )
        fail();
      return mapSendReceipt(result);
    },
  });
}
