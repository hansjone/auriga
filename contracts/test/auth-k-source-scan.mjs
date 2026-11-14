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
