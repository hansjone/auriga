// 契约测试只读扫描，不随业务生成器更新覆盖清单。
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
const base = fileURLToPath(
  new URL('../../clients/dsh-plugin/src/', import.meta.url),
);
export function platformSourceDigests(files) {
  return [...new Set(files)].sort().map((file) => ({
    file,
    sha256: createHash('sha256')
      .update(
        fs.readFileSync(path.join(base, file), 'utf8').replaceAll('\r\n', '\n'),
      )
      .digest('hex'),
  }));
}
export function platformCallSites() {
  function walk(dir) {
    return fs
      .readdirSync(dir, { withFileTypes: true })
      .flatMap((e) =>
        e.isDirectory()
          ? e.name === 'generated'
            ? []
            : walk(path.join(dir, e.name))
          : e.name.endsWith('.js')
            ? [path.join(dir, e.name)]
            : [],
      );
  }
  return walk(base)
    .flatMap((file) =>
      fs
        .readFileSync(file, 'utf8')
        .split(/\r?\n/)
        .filter(
          (line) =>
            /\/(?:api\/(?:iam|resource|workspace|ai|collab)\/v1|ai\/v1|ai\/mcp|ai\/team\/node|oauth2\/)/.test(
              line,
            ) &&
            !line.trim().startsWith('//') &&
            !line.trim().startsWith('*'),
        )
        .map((line) => ({
          file: path.relative(base, file).replaceAll('\\', '/'),
          evidence: line.trim(),
        })),
    )
    .sort((a, b) =>
      (a.file + '\n' + a.evidence).localeCompare(b.file + '\n' + b.evidence),
    );
}

// Implementation may delete inventoried routes during migration; source hashes are historical only.
export function checkPlatformMigration(inventory, operations, sources) {
  if (!['legacy', 'broker'].includes(inventory.migrationState))
    throw new Error('invalid migrationState');
  const allowed = new Set(
    inventory.calls.map((c) => c.file + '\n' + c.evidence),
  );
  const files = sources ?? readPluginSources();
  const calls = sourceRoutes(files);
  if (inventory.migrationState === 'legacy') {
    for (const c of calls)
      if (!allowed.has(c.file + '\n' + c.evidence))
        throw new Error('new direct platform route: ' + c.file);
    return;
  }
  if (inventory.calls.length || calls.length)
    throw new Error('broker migration retains direct routes');
  for (const [file, source] of Object.entries(files)) {
    // Ban direct transports even when the target path is assembled dynamically.
    if (
      /\b(?:fetch|controlledFetch|tracedFetch|WebSocket|XMLHttpRequest|EventSource)\b|(?:node:)?(?:https?|net|tls|http2)['"]|\b(?:axios|undici)\b/.test(
        source,
      )
    )
      throw new Error('direct transport: ' + file);
    for (const match of source.matchAll(
      /\bbroker\.call\s*\(\s*(['"])([^'"]+)\1/g,
    ))
      if (!operations.some((o) => o.operation === match[2]))
        throw new Error('unknown broker operation: ' + match[2]);
    for (const match of source.matchAll(
      /\bbroker\.call\s*\(\s*([\s\S]*?)(?:,|\))/g,
    )) {
      const literal = /^(['"])([^'"]+)\1$/.exec(match[1].trim());
      if (!literal) throw new Error('dynamic broker operation: ' + file);
      if (!operations.some((o) => o.operation === literal[2]))
        throw new Error('unknown broker operation: ' + literal[2]);
    }
  }
}
function readPluginSources() {
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
function sourceRoutes(sources) {
  return Object.entries(sources).flatMap(([file, s]) =>
    s
      .split(/\r?\n/)
      .filter(
        (l) =>
          /\/(?:api\/(?:iam|resource|workspace|ai|collab)\/v1|ai\/v1|ai\/mcp|ai\/team\/node|oauth2\/)/.test(
            l,
          ) &&
          !l.trim().startsWith('//') &&
          !l.trim().startsWith('*'),
      )
      .map((l) => ({ file, evidence: l.trim() })),
  );
}
