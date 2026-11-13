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
