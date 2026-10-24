// @vitest-environment node
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { build } from 'vite';
import { describe, expect, it } from 'vitest';

describe('native logout delivery', () => {
  it('builds all three HTML entries with a native session-only logout graph and complete Docker input', async () => {
    const result = await build({
      root: fileURLToPath(new URL('../', import.meta.url)),
      configFile: fileURLToPath(new URL('../vite.config.ts', import.meta.url)),
      logLevel: 'silent',
      build: { write: false },
    });
    if (Array.isArray(result) || !('output' in result))
      throw new Error('Expected a single Rollup build output');
    const files = new Map(result.output.map((file) => [file.fileName, file]));
    for (const entry of ['index.html', 'instance-login.html', 'logout.html'])
      expect(files.has(entry)).toBe(true);
    const html = files.get('logout.html');
    if (html?.type !== 'asset') throw new Error('Logout HTML missing');
    const links = [
      ...String(html.source).matchAll(/(?:src|href)="([^"]+)"/g),
    ].map((match) => match[1]);
    expect(links.some((link) => link.endsWith('.js'))).toBe(true);
    expect(links.some((link) => link.endsWith('.css'))).toBe(true);
    const visited = new Set<string>();
    const modules: string[] = [];
    function visit(name: string) {
      if (visited.has(name)) return;
      visited.add(name);
      const file = files.get(name);
      expect(file, `actual logout dependency ${name}`).toBeDefined();
      if (file?.type === 'chunk') {
        modules.push(...file.moduleIds.map((id) => id.replaceAll('\\', '/')));
        for (const imported of [...file.imports, ...file.dynamicImports])
          visit(imported);
      }
    }
    for (const link of links) {
      expect(link.startsWith('/assets/')).toBe(true);
      visit(link.slice(1));
    }
    expect(modules.some((id) => id.endsWith('/native-logout/page.ts'))).toBe(
      true,
    );
    expect(modules.some((id) => id.endsWith('/shared/src/api/iam.ts'))).toBe(
      true,
    );
    expect(
      modules.filter(
        (id) =>
          /(?:node_modules|\/shared\/src\/).*(?:vue|react|pinia|element-plus|\/steps\/)/i.test(
            id,
          ) ||
          id.includes('/native-login/') ||
          id.endsWith('/portal/src/workspace.ts'),
      ),
    ).toEqual([]);
    const dockerfile = await readFile(
      new URL('../../../deploy/compose/Dockerfile.edge', import.meta.url),
      'utf8',
    );
    expect(dockerfile).toMatch(
      /^COPY web\/portal\/logout\.html \.\/web\/portal\/$/m,
    );
    expect(dockerfile).toMatch(/^COPY web\/portal\/src \.\/web\/portal\/src$/m);
  }, 60_000);
});
