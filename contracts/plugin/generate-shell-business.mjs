// 固定操作投影：只从显式源选取，不能在运行时接受 URL、header 或身份参数。
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import YAML from '../../services/contracts/node_modules/yaml/dist/index.js';
const root = fileURLToPath(new URL('../', import.meta.url));
const defs = {};
const cache = new Map();
const read = (file) => {
  if (!cache.has(file))
    cache.set(file, YAML.parse(fs.readFileSync(path.join(root, file), 'utf8')));
  return cache.get(file);
};
function expand(value, file) {
  if (Array.isArray(value)) return value.map((v) => expand(v, file));
  if (!value || typeof value !== 'object') return value;
  if (value.type === 'string' && value.format === 'binary')
    return { $ref: '#/$defs/ShellAuthUploadReference' };
  if (value.$ref) {
    const [relative, fragment = ''] = value.$ref.split('#');
    const target = relative
      ? path.posix.normalize(
          path.posix.join(path.posix.dirname(file), relative),
        )
      : file;
    const name =
      'ShellAuth' + (target + fragment).replace(/[^a-zA-Z0-9]/g, '_');
    if (!defs[name]) {
      defs[name] = {};
      let source = read(target);
      for (const part of fragment.split('/').slice(1))
        source = source[part.replaceAll('~1', '/').replaceAll('~0', '~')];
      defs[name] = expand(source, target);
    }
    return { $ref: '#/$defs/' + name };
  }
  const result = Object.fromEntries(
    Object.entries(value)
      .filter(
        ([k]) =>
          ![
            '$id',
            '$schema',
            'discriminator',
            'xml',
            'example',
            'nullable',
          ].includes(k) && !k.startsWith('x-'),
      )
      .map(([k, v]) => [k, expand(v, file)]),
  );
  if (result.type === 'integer' && result.maximum > 2147483647)
    result.format = 'int64';
  return result;
}
const obj = (properties, required = Object.keys(properties)) => ({
  type: 'object',
  additionalProperties: false,
  properties,
  required,
});
const uuid = { type: 'string', format: 'uuid' };
const integer = {
  type: 'integer',
  format: 'int64',
  minimum: 0,
  maximum: 9007199254740991,
};
const operations = [];
const arms = [];
const chunk64 = () => ({
  type: 'string',
  contentEncoding: 'base64',
  pattern: '^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$',
  maxLength: 87384,
  allOf: [{ if: { minLength: 87384 }, then: { pattern: '==$' } }],
});
const wire = (source, maxBytes = 65536) => ({
  type: 'string',
  contentEncoding: 'base64',
  contentMediaType: 'application/json',
  pattern: '^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$',
  maxLength: 4 * Math.ceil(maxBytes / 3),
  description:
    '解码后最多' +
    maxBytes +
    '字节，必须严格验证 ' +
    source +
    '；保留原始受签字节，不经生成模型重序列化。',
});
const codes = [
  'common.validation_failed',
  'common.unauthenticated',
  'common.forbidden',
  'common.not_found',
  'common.conflict',
  'common.precondition_failed',
  'common.rate_limited',
  'common.unavailable',
  'common.internal',
  'resource.digest_mismatch',
  'resource.signature_invalid',
  'resource.storage_unavailable',
];
const digest = { type: 'string', pattern: '^[a-f0-9]{64}$' };
defs.ShellAuthUploadReference = obj({
  uploadId: uuid,
  sizeBytes: {
    type: 'integer',
    format: 'int64',
    minimum: 0,
    maximum: 67108864,
  },
  sha256: digest,
});
defs.ShellAuthBusinessError = obj(
  {
    code: { type: 'string', enum: codes },
    upstreamStatus: { type: 'integer', minimum: 400, maximum: 599 },
    retryable: { type: 'boolean' },
    retryAfterSeconds: { type: 'integer', minimum: 0, maximum: 300 },
  },
  ['code', 'retryable'],
);
for (const file of [
  'openapi/resource.yaml',
  'openapi/ai.yaml',
  'openapi/iam-network.yaml',
]) {
  const spec = read(file);
  for (const [route, item] of Object.entries(spec.paths)) {
    if (
      route.includes('/admin') ||
      route.startsWith('/internal') ||
      route.includes('/desktop/') ||
      route.includes('/drafts') ||
      route.includes('/personal-signatures')
    )
      continue;
    for (const method of ['get', 'post', 'patch']) {
      const op = item[method];
      if (!op) continue;
      const operation = op.operationId;
      const properties = {},
        required = [];
      const headerProperties = {},
        headerRequired = [];
      for (let p of [...(item.parameters ?? []), ...(op.parameters ?? [])]) {
        if (p.$ref) {
          const [f, frag] = p.$ref.split('#');
          p = read(path.posix.join(path.posix.dirname(file), f));
          for (const part of frag.split('/').slice(1)) p = p[part];
        }
        if (p.in === 'header') {
          const name = p.name.toLowerCase();
          if (
            ['if-match', 'idempotency-key', 'if-none-match', 'range'].includes(
              name,
            )
          ) {
            headerProperties[name] = expand(p.schema, file);
            if (p.required) headerRequired.push(name);
          }
          continue;
        }
        if (!['path', 'query'].includes(p.in)) continue;
        // 本机设备/实例身份由 broker 派生，不能由插件指定。
        if (['clientId', 'tenantId', 'ownerId', 'memberId'].includes(p.name))
          continue;
        properties[p.name] = expand(p.schema, file);
        if (p.required) required.push(p.name);
      }
      if (file === 'openapi/resource.yaml' && method !== 'get') {
        headerProperties['if-match'] ??= {
          type: 'string',
          minLength: 1,
          maxLength: 128,
        };
        headerProperties['idempotency-key'] ??= {
          type: 'string',
          minLength: 1,
          maxLength: 128,
        };
      }
      if (Object.keys(headerProperties).length) {
        properties.headers = obj(headerProperties, headerRequired);
        if (headerRequired.length) required.push('headers');
      }
      const content = op.requestBody?.content;
      const body =
        content?.['application/json']?.schema ??
        content?.['multipart/form-data']?.schema;
      if (body) {
        properties.body = expand(body, file);
        required.push('body');
      }
      const inputName = 'ShellAuthInput_' + operation;
      defs[inputName] = obj(properties, required);
      const result = Object.entries(op.responses).find(([s]) =>
        /^2/.test(s),
      )?.[1];
      const output = result?.content?.['application/json']?.schema;
      const outputName = 'ShellAuthOutput_' + operation;
      defs[outputName] = output
        ? expand(output, file)
        : obj({ streamId: uuid });
      if (operation === 'createAiChatCompletion') {
        const bodyName = properties.body.$ref.split('/').at(-1);
        const baseBody = structuredClone(defs[bodyName]);
        baseBody.properties.stream = { type: 'boolean' };
        baseBody.allOf = [
          ...(baseBody.allOf ?? []),
          {
            not: {
              properties: { stream: { const: true } },
              required: ['stream'],
            },
          },
        ];
        defs.ShellAuthChatJsonRequest = baseBody;
        properties.body = { $ref: '#/$defs/ShellAuthChatJsonRequest' };
      }
      operations.push({
        operation,
        method,
        route,
        source: file,
        permission: op['x-permission'] ?? 'auriga.connect',
        audience: file.includes('/ai.') ? 'auriga.ai-gateway' : 'auriga',
        stream: !output,
        requestHeaders: Object.keys(headerProperties),
        responseHeaders: [
          'etag',
          'content-type',
          'content-length',
          'content-range',
          'retry-after',
        ],
        retry: method === 'get' ? 'read_only' : 'explicit_idempotency_only',
        input: inputName,
        output: outputName,
      });
      arms.push(
        obj({
          operation: { const: operation, type: 'string' },
          input: { $ref: '#/$defs/' + inputName },
        }),
      );
    }
  }
}
const chat = operations.find((op) => op.operation === 'createAiChatCompletion');
const streamChat = {
  ...chat,
  operation: 'createAiChatCompletionStream',
  input: 'ShellAuthInput_createAiChatCompletionStream',
  output: 'ShellAuthOutput_createAiChatCompletionStream',
  stream: true,
};
const streamInput = structuredClone(defs[chat.input]);
const streamBody = structuredClone(defs.ShellAuthChatJsonRequest);
streamBody.allOf = [
  { not: { properties: { stream: { const: false } }, required: ['stream'] } },
];
streamBody.required = [...new Set([...(streamBody.required ?? []), 'stream'])];
defs.ShellAuthChatStreamRequest = streamBody;
streamInput.properties.body = { $ref: '#/$defs/ShellAuthChatStreamRequest' };
defs[streamChat.input] = streamInput;
defs[streamChat.output] = obj({
  streamId: uuid,
  encoding: { type: 'string', enum: ['sse_utf8'] },
});
operations.push(streamChat);
arms.push(
  obj({
    operation: { type: 'string', const: streamChat.operation },
    input: { $ref: '#/$defs/' + streamChat.input },
  }),
);
// 远程 MCP 与 NATS 通道持有平台凭据，插件仅拿业务流 ID。
for (const [operation, input, output] of [
  [
    'team_initialize',
    obj({ protocolVersion: { type: 'string', enum: ['2025-06-18'] } }),
    obj({ streamId: uuid }),
  ],
  [
    'team_list_tools',
    obj({ streamId: uuid }),
    obj({ tools: { type: 'array', maxItems: 256, items: { type: 'object' } } }),
  ],
  [
    'team_ping',
    obj({ streamId: uuid }),
    obj({ available: { type: 'boolean' } }),
  ],
  ['network_open', obj({ executionContextId: uuid }), obj({ streamId: uuid })],
  [
    'upload_open',
    obj({
      sizeBytes: {
        type: 'integer',
        format: 'int64',
        minimum: 0,
        maximum: 67108864,
      },
      sha256: digest,
      mediaType: {
        type: 'string',
        enum: [
          'application/json',
          'application/gzip',
          'application/octet-stream',
        ],
      },
    }),
    obj({
      uploadId: uuid,
      streamId: uuid,
      maxChunkBytes: { type: 'integer', enum: [65536] },
      creditBytes: { type: 'integer', enum: [1048576] },
    }),
  ],
  [
    'upload_finish',
    obj({ uploadId: uuid }),
    { $ref: '#/$defs/ShellAuthUploadReference' },
  ],
  [
    'network_send',
    obj({ streamId: uuid, frameWire: wire('network/protocol.schema.json') }),
    obj({ sequence: integer }),
  ],
  [
    'network_submit',
    obj({
      executionContextId: uuid,
      requestWire: wire('network/send.schema.json#/$defs/SubmitRequest'),
    }),
    obj({ resultWire: wire('network/send.schema.json#/$defs/SendResult') }),
  ],
  [
    'getNetworkKeys',
    obj({}),
    obj({
      wire: wire(
        'network/auto-renewal-v2.schema.json#/$defs/Publication',
        33554432,
      ),
    }),
  ],
  [
    'getNetworkKeyAnnouncement',
    obj({}),
    obj({
      wire: wire(
        'network/auto-renewal-v2.schema.json#/$defs/SignedKeysetAnnouncementV2',
        33554432,
      ),
    }),
  ],
  [
    'getPublicNetworkControl',
    obj({}),
    obj({
      wire: wire(
        'network/auto-renewal-v2.schema.json#/$defs/SignedRootControl',
        33554432,
      ),
    }),
  ],
  [
    'confirmNetworkControl',
    obj({
      receiptWire: wire(
        'network/auto-renewal-v2.schema.json#/$defs/InstallationReceipt',
      ),
    }),
    obj({
      wire: wire('network/auto-renewal-v2.schema.json#/$defs/IsolationRecord'),
    }),
  ],
  [
    'profile_read',
    obj({}),
    obj({ memberId: uuid, displayName: { type: 'string', maxLength: 256 } }),
  ],
  ['provider_diagnostic', obj({}), obj({ available: { type: 'boolean' } })],
]) {
  const i = 'ShellAuthInput_' + operation,
    o = 'ShellAuthOutput_' + operation;
  defs[i] = input;
  defs[o] = output;
  operations.push({
    operation,
    input: i,
    output: o,
    audience:
      operation.startsWith('team_') || operation === 'network_submit'
        ? 'auriga.ai-gateway'
        : 'auriga',
    permission:
      operation === 'getPublicNetworkControl'
        ? null
        : operation === 'confirmNetworkControl'
          ? 'collab.msgnet.control.install'
          : ['getNetworkKeys', 'getNetworkKeyAnnouncement'].includes(operation)
            ? 'collab.msgnet.read'
            : 'auriga.connect',
    ...([
      'getNetworkKeys',
      'getNetworkKeyAnnouncement',
      'getPublicNetworkControl',
      'confirmNetworkControl',
    ].includes(operation)
      ? {
          authentication:
            operation === 'getPublicNetworkControl' ? 'none' : 'bearer',
          method: operation === 'confirmNetworkControl' ? 'post' : 'get',
        }
      : {}),
    stream: operation.includes('open'),
    retry: 'never',
    route: operation.startsWith('upload_')
      ? 'broker:upload'
      : operation === 'getNetworkKeys'
        ? '/api/collab/v2/network/keys'
        : operation === 'getNetworkKeyAnnouncement'
          ? '/api/collab/v2/network/keys/announcement'
          : operation === 'getPublicNetworkControl'
            ? '/api/collab/v2/network/bootstrap/controls'
            : operation === 'confirmNetworkControl'
              ? '/api/collab/v2/network/controls/receipts'
              : operation.startsWith('team_')
                ? '/ai/mcp'
                : operation === 'network_submit'
                  ? '/api/ai/v1/network/submit'
                  : operation.startsWith('network_')
                    ? 'broker:nats'
                    : operation === 'profile_read'
                      ? '/api/iam/v1/me'
                      : '/api/iam/v1/login/providers',
  });
  arms.push(
    obj({
      operation: { type: 'string', const: operation },
      input: { $ref: '#/$defs/' + i },
    }),
  );
}
for (const [operation, source, definition, route] of [
  ['team_list_endpoints', 'team-mcp.schema.json', 'ListEndpoints', '/ai/mcp'],
  ['team_open_thread', 'team-mcp.schema.json', 'OpenThread', '/ai/mcp'],
  ['team_reply_thread', 'team-mcp.schema.json', 'ReplyThread', '/ai/mcp'],
  ['team_read_thread', 'team-mcp.schema.json', 'ReadThread', '/ai/mcp'],
  ...['Register', 'Bind', 'Pull', 'Ack', 'Revoke'].map((n) => [
    'team_node_' + n.toLowerCase(),
    'team-transport.schema.json',
    n,
    '/ai/team/node/' + n.toLowerCase(),
  ]),
]) {
  const i = 'ShellAuthInput_' + operation,
    o = 'ShellAuthOutput_' + operation;
  defs[i] = obj({
    executionContextId: uuid,
    arguments: expand(
      { $ref: source + '#/$defs/' + definition },
      'plugin/shell-business.schema.json',
    ),
  });
  defs[o] = obj({ streamId: uuid });
  operations.push({
    operation,
    input: i,
    output: o,
    audience: 'auriga.ai-gateway',
    permission: 'auriga.connect',
    stream: true,
    retry: 'explicit_idempotency_only',
    route,
    ...(route === '/ai/mcp' ? { tool: 'auriga_' + operation.slice(5) } : {}),
  });
  arms.push(
    obj({
      operation: { type: 'string', const: operation },
      input: { $ref: '#/$defs/' + i },
    }),
  );
}
defs.ShellAuthBusinessCall = obj(
  {
    protocolVersion: { type: 'integer', const: 1 },
    requestId: uuid,
    shellEpoch: uuid,
    expectedRevision: integer,
    expiresAt: { type: 'string', format: 'date-time' },
    idempotencyKey: uuid,
    call: { oneOf: arms },
  },
  [
    'protocolVersion',
    'requestId',
    'shellEpoch',
    'expectedRevision',
    'expiresAt',
    'call',
  ],
);
defs.ShellAuthBusinessResult = {
  oneOf: operations.map((op) =>
    obj({
      requestId: uuid,
      operation: { type: 'string', const: op.operation },
      output: { $ref: '#/$defs/' + op.output },
    }),
  ),
};
defs.ShellAuthBusinessResult.oneOf.push(
  obj({
    requestId: uuid,
    operation: { type: 'string', enum: operations.map((op) => op.operation) },
    error: { $ref: '#/$defs/ShellAuthBusinessError' },
  }),
);
defs.ShellAuthStreamSubscription = obj({
  streamId: uuid,
  shellEpoch: uuid,
  expectedRevision: integer,
});
defs.ShellAuthStreamCommand = obj(
  {
    streamId: uuid,
    shellEpoch: uuid,
    expectedRevision: integer,
    action: {
      type: 'string',
      enum: ['credit', 'cancel', 'close', 'upload_chunk'],
    },
    creditBytes: { type: 'integer', minimum: 0, maximum: 1048576 },
    sequence: integer,
    data: chunk64(),
  },
  ['streamId', 'shellEpoch', 'expectedRevision', 'action'],
);
defs.ShellAuthStreamEvent = obj(
  {
    streamId: uuid,
    shellEpoch: uuid,
    revision: integer,
    sequence: integer,
    type: {
      type: 'string',
      enum: ['data', 'end', 'closed', 'error', 'upload_ack'],
    },
    data: chunk64(),
    ackSequence: integer,
    creditBytes: { type: 'integer', minimum: 0, maximum: 1048576 },
    code: {
      type: 'string',
      enum: codes,
    },
    error: { $ref: '#/$defs/ShellAuthBusinessError' },
  },
  ['streamId', 'shellEpoch', 'revision', 'sequence', 'type'],
);
defs.ShellAuthStreamCommand.allOf = [
  {
    if: { properties: { action: { const: 'credit' } }, required: ['action'] },
    then: { required: ['creditBytes'], properties: { creditBytes: {} } },
    else: {
      not: { required: ['creditBytes'], properties: { creditBytes: {} } },
    },
  },
  {
    if: {
      properties: { action: { const: 'upload_chunk' } },
      required: ['action'],
    },
    then: {
      required: ['sequence', 'data'],
      properties: { sequence: {}, data: {} },
    },
    else: {
      not: {
        anyOf: [
          { required: ['data'], properties: { data: {} } },
          { required: ['sequence'], properties: { sequence: {} } },
        ],
      },
    },
  },
];
defs.ShellAuthStreamEvent.allOf = [
  {
    if: { properties: { type: { const: 'upload_ack' } }, required: ['type'] },
    then: {
      required: ['ackSequence', 'creditBytes'],
      properties: { ackSequence: {}, creditBytes: {} },
    },
    else: {
      not: {
        anyOf: [
          { required: ['ackSequence'], properties: { ackSequence: {} } },
          { required: ['creditBytes'], properties: { creditBytes: {} } },
        ],
      },
    },
  },
  {
    if: { properties: { type: { const: 'data' } }, required: ['type'] },
    then: { required: ['data'], properties: { data: {} } },
    else: { not: { required: ['data'], properties: { data: {} } } },
  },
  {
    if: { properties: { type: { const: 'error' } }, required: ['type'] },
    then: { required: ['code', 'error'], properties: { code: {}, error: {} } },
    else: {
      not: {
        anyOf: [
          { required: ['code'], properties: { code: {} } },
          { required: ['error'], properties: { error: {} } },
        ],
      },
    },
  },
];
for (const operation of operations) {
  operation.requestHeaders ??= [];
  operation.responseHeaders ??= ['content-type', 'retry-after'];
  const bodySchema = defs[operation.output];
  defs[operation.output] = obj(
    {
      statusCode: { type: 'integer', minimum: 200, maximum: 299 },
      headers: obj(
        Object.fromEntries(
          operation.responseHeaders.map((name) => [
            name,
            { type: 'string', maxLength: 1024 },
          ]),
        ),
        [],
      ),
      body: bodySchema,
    },
    ['statusCode', 'body'],
  );
  operation.scope = 'auriga.connect';
  if (
    operation.operation.startsWith('team_') ||
    operation.operation.startsWith('network_') ||
    operation.operation === 'listNetworkSystems'
  )
    operation.permission = 'auriga.connect.collab.use';
  if (
    [
      'listAiModels',
      'createAiChatCompletion',
      'createAiChatCompletionStream',
    ].includes(operation.operation)
  )
    operation.devicePermission = 'auriga.connect.models.invoke';
}
for (const [name, data] of [
  [
    'shell-business.schema.json',
    {
      $schema: 'https://json-schema.org/draft/2020-12/schema',
      title: 'ShellAuth 固定业务操作',
      $defs: defs,
    },
  ],
  ['shell-business-operations.json', { version: 1, operations }],
]) {
  const target = path.join(root, 'plugin', name),
    text = JSON.stringify(data, null, 2) + '\n';
  if (process.argv.includes('--check')) {
    if (fs.readFileSync(target, 'utf8').replaceAll('\r\n', '\n') !== text)
      throw Error(name + ' 需要重新生成');
  } else fs.writeFileSync(target, text);
}
