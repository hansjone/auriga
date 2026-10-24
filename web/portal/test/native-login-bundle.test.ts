// @vitest-environment node
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { build } from 'vite';
import { describe, expect, it } from 'vitest';

describe('native entry delivery', () => {
  it('builds both real HTML entries and resolves a framework-free native JS/CSS graph', async () => {
    const portalRoot = fileURLToPath(new URL('../', import.meta.url));
    const result = await build({
      root: portalRoot,
      configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)),
      logLevel: 'silent',
      build: { write: false },
    });
    if (Array.isArray(result) || !('output' in result))
      throw new Error('Expected a single Rollup build output');
    const files = new Map(result.output.map((file) => [file.fileName, file]));
    expect(files.has('index.html')).toBe(true);
    const htmlFile = files.get('instance-login.html');
    if (htmlFile?.type !== 'asset') throw new Error('Native HTML missing');
    const html = String(htmlFile.source);
    const links = [...html.matchAll(/(?:src|href)="([^"]+)"/g)].map(
      (match) => match[1],
    );
    expect(links.some((link) => link.endsWith('.js'))).toBe(true);
    expect(links.some((link) => link.endsWith('.css'))).toBe(true);
    const visited = new Set<string>();
    const moduleIds: string[] = [];
    function visit(name: string) {
      if (visited.has(name)) return;
      visited.add(name);
      const file = files.get(name);
      expect(file, `bundled asset ${name}`).toBeDefined();
      if (file?.type === 'chunk') {
        moduleIds.push(...file.moduleIds);
        for (const imported of [...file.imports, ...file.dynamicImports])
          visit(imported);
      }
    }
    for (const link of links) {
      expect(link.startsWith('/assets/')).toBe(true);
      visit(link.slice(1));
    }
    expect(
      moduleIds.some((id) =>
        id
          .replaceAll('\\', '/')
          .endsWith('/clients/dsh-plugin/src/login/index.js'),
      ),
    ).toBe(true);
    expect(
      moduleIds.some((id) =>
        id.replaceAll('\\', '/').includes('/shared/src/api/iam.ts'),
      ),
    ).toBe(true);
    expect(
      moduleIds.filter((id) =>
        /(?:node_modules|\/shared\/src\/).*(?:vue|react|pinia|element-plus|\/steps\/)/i.test(
          id,
        ),
      ),
    ).toEqual([]);
    const dockerfile = await readFile(
      new URL('../../../deploy/compose/Dockerfile.edge', import.meta.url),
      'utf8',
    );
    expect(dockerfile).toMatch(
      /^COPY web\/portal\/index\.html web\/portal\/instance-login\.html web\/portal\/vite\.config\.ts \.\/web\/portal\/$/m,
    );
  }, 60_000);
});
