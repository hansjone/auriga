// 结构门禁，不执行插件代码；局限和保留通道规则见 shell-auth-migration.md。
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const base = fileURLToPath(
  new URL('../../clients/dsh-plugin/src/', import.meta.url),
);

function withoutComments(source) {
  return source.replace(
    /(['"`])(?:\\[\s\S]|(?!\1)[^\\])*?\1|\/\*[\s\S]*?\*\/|(?<!\\)\/\/[^\r\n]*/g,
    (value) => (value.startsWith('//') || value.startsWith('/*') ? ' ' : value),
  );
}
function routeFeatures(source) {
  return [
    ...new Set(
      [
        ...withoutComments(source).matchAll(
          /\/(?:api\/[A-Za-z][A-Za-z0-9_-]*\/v[0-9]+|ai\/v1|ai\/mcp|ai\/team\/node|oauth2\/)(?:[A-Za-z0-9_./?=&:%-]|\$\{[^}]+\})*/g,
        ),
      ].map((m) =>
        m[0].replace(
          /\$\{\s*([^}]+?)\s*\}/g,
          (_, expression) => '${' + expression.replace(/\s+/g, '') + '}',
        ),
      ),
    ),
  ];
}
function transportFeatures(source) {
  const code = withoutComments(source),
    features = new Set();
  for (const match of code.matchAll(
    /\b(fetch|controlledFetch|tracedFetch|traceFetch|fetchImpl|fetcher|undiciFetch|customFetch)\s*\(/g,
  ))
    features.add('call:' + match[1]);
  for (const match of code.matchAll(
    /\bnew\s+(WebSocket|XMLHttpRequest|EventSource)\s*\(/g,
  ))
    features.add('new:' + match[1]);
  for (const match of code.matchAll(
    /(?:\bfrom\s*|\brequire\s*\(\s*|\bimport\s*\(\s*|\bimport\s*)(['"])((?:node:)?(?:https?|net|tls|http2)|undici|axios|ws)\1/g,
  ))
    features.add('import:' + match[2]);
  return [...features];
}
function checkBrokerCalls(source, operations, file) {
  const code = withoutComments(source);
  if (
    /\b(?:const|let|var)\s+(?!broker\b)[A-Za-z_$][\w$]*\s*=\s*broker\b(?!\s*[.(?])|\bimport\s*\{[^}]*\bbroker\s+as\s+|\b(?:const|let|var)\s*\{[^}]*\bcall\b[^}]*\}\s*=\s*broker\b/.test(
      code,
    )
  )
    throw new Error('broker alias forbidden: ' + file);
  for (const match of code.matchAll(
    /\bbroker\s*\.\s*call\s*\(\s*([\s\S]*?)(?:,|\))/g,
  )) {
    const literal = /^(['"])([^'"]+)\1$/.exec(match[1].trim());
    if (!literal) throw new Error('dynamic broker operation: ' + file);
    if (!operations.some((o) => o.operation === literal[2]))
      throw new Error('unknown broker operation: ' + literal[2]);
  }
}

// A finite local node relay is not a platform HTTP client. Validate its actual request shape
// and trusted Linux provider together; a retained transport entry alone is insufficient.
function checkSharedNodeRelay(files) {
  const file = 'shared/node-transport.js',
    provider = 'shared/ports-plugin.js';
  const compact = (text) =>
    withoutComments(text ?? '')
      .replace(/\s+/g, '')
      .replaceAll('"', "'");
  const code = compact(files[file]),
    source = compact(files[provider]);
  const allowed = [
    'health',
    'resolve',
    'register',
    'lookup',
    'resourceSnapshot',
    'beginTask',
    'issueCapability',
    'finishTask',
    'authorizeCapability',
    'readCredentials',
    'enterpriseOutbound',
    'messageContext',
    'model',
    'send',
    'receive',
  ];
  const operations = /constoperations=newSet\(\[([^\]]*)\]\);/.exec(code);
  if (
    !operations ||
    operations[1] !== allowed.map((name) => "'" + name + "',").join('') ||
    !code.includes(
      "if(!operations.has(operation))fail('common.validation_failed');",
    ) ||
    (code.match(/\brequest\b/g) ?? []).length !== 2 ||
    (code.match(/'node:http'/g) ?? []).length !== 1 ||
    (code.match(/\bsocketPath\b/g) ?? []).length !== 2 ||
    !code.includes("import{request}from'node:http';") ||
    !code.includes('exportfunctioncreateSharedNodeTransport(socketPath){') ||
    !code.includes(
      "constoutgoing=request({socketPath,path:'/_auriga/shared-rpc/'+operation,method:'POST',headers:{'content-type':'application/json','content-length':String(bytes.length),},signal,});",
    )
  )
    throw new Error(
      'shared node relay must remain a finite socket-only POST transport',
    );
  for (const guard of [
    "process.platform!=='linux'",
    'constinitial=read();',
    'constroot=`/var/lib/auriga/pools/${value.physicalInstanceId}`;',
    "config.configFile!==root+'/control/shared-host.json'",
    "value.nodeSocket!==root+'/control/shared-node.sock'",
    '!stat.isFile()||(stat.mode&0o022)!==0||stat.size>262144',
    '!uuid.test(value.physicalInstanceId)',
    'constuuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;',
    "if(!lstatSync(initial.nodeSocket).isSocket())fail('common.unavailable');",
    'constbridge=createSharedNodeTransport(initial.nodeSocket);',
  ])
    if (!source.includes(guard))
      throw new Error(
        'shared node relay lost trusted provider guard: ' + guard,
      );
  if ((source.match(/\bcreateSharedNodeTransport\b/g) ?? []).length !== 2)
    throw new Error('shared node relay provider alias forbidden');
  if (
    /\b(?:initial|value)\.(?:nodeSocket|physicalInstanceId|tenantId)(?:\+=|\|\|=|\?\?=|=(?!=))/.test(
      source,
    )
  )
    throw new Error(
      'shared node relay trusted identity/socket override forbidden',
    );
  for (const [name, text] of Object.entries(files))
    if (
      name !== file &&
      name !== provider &&
      /\bcreateSharedNodeTransport\b/.test(withoutComments(text))
    )
      throw new Error(
        'shared node relay is callable only by its trusted provider: ' + name,
      );
}

export function checkPlatformMigration(inventory, operations, sources) {
  if (!['legacy', 'broker'].includes(inventory.migrationState))
    throw new Error('invalid migrationState');
  const files = sources ?? readPluginSources();
  if (inventory.migrationState === 'legacy') {
    const allowed = new Set(
      inventory.calls.flatMap((c) =>
        routeFeatures(c.evidence).map((route) => c.file + '\n' + route),
      ),
    );
    for (const [file, source] of Object.entries(files))
      for (const route of routeFeatures(source)) {
        if (!allowed.has(file + '\n' + route))
          throw new Error('new direct platform route: ' + file + ':' + route);
      }
    return;
  }
  if (inventory.calls.length)
    throw new Error('broker migration retains legacy inventory');
  const channels = new Map();
  for (const channel of inventory.retainedChannels ?? []) {
    if (
      !channel.file ||
      channel.file.includes('..') ||
      channels.has(channel.file) ||
      !channel.channel ||
      !channel.reason ||
      !channel.design ||
      !Array.isArray(channel.transports) ||
      !Array.isArray(channel.routePatterns)
    )
      throw new Error('invalid retained channel');
    for (const pattern of channel.routePatterns) {
      if (!pattern.startsWith('^') || !pattern.endsWith('$'))
        throw new Error('unbounded retained route');
      new RegExp(pattern);
    }
    channels.set(channel.file, channel);
  }
  for (const [file, source] of Object.entries(files)) {
    const channel = channels.get(file);
    for (const route of routeFeatures(source)) {
      if (
        !channel?.routePatterns.some((pattern) =>
          new RegExp(pattern).test(route),
        )
      )
        throw new Error('direct platform route: ' + file + ':' + route);
    }
    for (const transport of transportFeatures(source)) {
      if (!channel?.transports.includes(transport))
        throw new Error('direct transport: ' + file + ':' + transport);
    }
    checkBrokerCalls(source, operations, file);
  }
  if (files['shared/node-transport.js']) {
    if (
      channels.get('shared/node-transport.js')?.channel !==
        'shared-unix-node-relay' ||
      channels.get('shared/ports-plugin.js')?.channel !==
        'shared-unix-node-provider'
    )
      throw new Error(
        'shared node relay requires its exact documented channel pair',
      );
    checkSharedNodeRelay(files);
  }
}

export function readPluginSources() {
  const out = {};
  function walk(dir) {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.name === 'generated') continue;
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (/\.(?:[cm]?js|tsx?)$/.test(e.name))
        out[path.relative(base, p).replaceAll('\\', '/')] = fs.readFileSync(
          p,
          'utf8',
        );
    }
  }
  walk(base);
  return out;
}
