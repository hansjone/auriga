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
const wire = (source) => ({
  type: 'string',
  contentEncoding: 'base64',
  contentMediaType: 'application/json',
  pattern: '^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$',
  maxLength: 87384,
  description:
    '解码后最多65536字节，必须严格验证 ' +
    source +
    '；保留原始受签字节，不经生成模型重序列化。',
});
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
      for (let p of [...(item.parameters ?? []), ...(op.parameters ?? [])]) {
        if (p.$ref) {
          const [f, frag] = p.$ref.split('#');
          p = read(path.posix.join(path.posix.dirname(file), f));
          for (const part of frag.split('/').slice(1)) p = p[part];
        }
        if (!['path', 'query'].includes(p.in)) continue;
        // 本机设备/实例身份由 broker 派生，不能由插件指定。
        if (['clientId', 'tenantId', 'ownerId', 'memberId'].includes(p.name))
          continue;
        properties[p.name] = expand(p.schema, file);
        if (p.required) required.push(p.name);
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
      operations.push({
        operation,
        method,
        route,
        source: file,
        permission: op['x-permission'] ?? 'auriga.connect',
        audience: file.includes('/ai.') ? 'auriga.ai-gateway' : 'auriga',
        stream: !output || operation === 'createAiChatCompletion',
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
    obj({ wire: wire('network/keyset.schema.json#/$defs/SignedKeyset') }),
  ],
  [
    'getNetworkKeyAnnouncement',
    obj({}),
    obj({
      wire: wire('network/protocol.schema.json#/$defs/KeysetAnnouncement'),
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
    permission: 'auriga.connect',
    stream: operation.includes('open'),
    retry: 'never',
    route:
      operation === 'getNetworkKeys'
        ? '/api/collab/v1/network/keys'
        : operation === 'getNetworkKeyAnnouncement'
          ? '/api/collab/v1/network/keys/announcement'
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
defs.ShellAuthStreamCommand = obj(
  {
    streamId: uuid,
    shellEpoch: uuid,
    expectedRevision: integer,
    action: { type: 'string', enum: ['credit', 'cancel', 'close'] },
    creditBytes: { type: 'integer', minimum: 0, maximum: 1048576 },
  },
  ['streamId', 'shellEpoch', 'expectedRevision', 'action'],
);
defs.ShellAuthStreamEvent = obj(
  {
    streamId: uuid,
    shellEpoch: uuid,
    revision: integer,
    sequence: integer,
    type: { type: 'string', enum: ['data', 'end', 'closed', 'error'] },
    data: { type: 'string', contentEncoding: 'base64', maxLength: 87384 },
    code: {
      type: 'string',
      enum: [
        'common.conflict',
        'common.forbidden',
        'common.unauthenticated',
        'common.unavailable',
        'common.rate_limited',
      ],
    },
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
];
defs.ShellAuthStreamEvent.allOf = [
  {
    if: { properties: { type: { const: 'data' } }, required: ['type'] },
    then: { required: ['data'], properties: { data: {} } },
    else: { not: { required: ['data'], properties: { data: {} } } },
  },
  {
    if: { properties: { type: { const: 'error' } }, required: ['type'] },
    then: { required: ['code'], properties: { code: {} } },
  },
];
for (const operation of operations) {
  operation.scope = 'auriga.connect';
  if (
    operation.operation.startsWith('team_') ||
    operation.operation.startsWith('network_') ||
    operation.operation === 'listNetworkSystems'
  )
    operation.permission = 'auriga.connect.collab.use';
  if (['listAiModels', 'createAiChatCompletion'].includes(operation.operation))
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
