import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import Ajv from 'ajv/dist/2020.js';
import {
  encodeReady,
  encodeReceipt,
  decodeJson,
} from '../../../src/network/session/frames.js';
import { self } from '../host/fixtures.js';
import { matches } from '../../../src/network/trust/keyset.js';

const ajv = new Ajv({ strict: false, validateFormats: false });
for (const file of ['address', 'envelope', 'source-control', 'protocol'])
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
test('assembled READY and durable inbox RECEIPT satisfy the original wire schemas', () => {
  const ready = decodeJson(
    encodeReady({
      requestId: 'ready-1',
      channelNonce: 'nonce-1',
      resumeCursor: '0',
      localHighWater: '0',
      registrationRevision: '0',
      receiptCheckpoint: {
        checkpoint_seq: '0',
        checkpoint_revision: '0',
        summary_digest: 'sha256:' + '0'.repeat(64),
      },
    }),
  );
  const receipt = decodeJson(
    encodeReceipt({
      requestId: 'receipt-1',
      operationId: 'op-1',
      epoch: '7',
      deliveryId: 'delivery-1',
      mailSeq: '1',
      results: [
        {
          recipientId: 'recipient-1',
          status: 'host_accepted',
          expectedStateVersion: '1',
          acceptedBy: 'system',
          acceptanceMode: 'inbox_only',
          messageDigest: 'sha256:' + '1'.repeat(64),
          hostRequestId: 'recipient-1',
          target: self('0'),
          evidenceDigest: 'sha256:' + '2'.repeat(64),
        },
      ],
    }),
  );
  for (const [type, frame] of [
    ['Ready', ready],
    ['Receipt', receipt],
  ]) {
    const check = ajv.getSchema(
      `https://auriga.example.com/contracts/network/protocol.schema.json#/$defs/${type}`,
    );
    assert.equal(check(frame), true, JSON.stringify(check.errors));
  }
});
test('keyset validation rejects unsupported schema keywords and types', () => {
  for (const schema of [
    { type: 'string', minItems: 1 },
    { type: 'integer' },
    { anyOf: [] },
    { type: 'object', properties: { absent: { minItems: 1 } } },
  ])
    assert.throws(
      () => matches('value', schema),
      (error) => error.code === 'network.key_unavailable',
    );
});
