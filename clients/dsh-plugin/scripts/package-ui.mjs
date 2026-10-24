/** Release preparation only; published installs never run a build or contact the platform. */
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = new URL('../../../', import.meta.url);
const require = createRequire(new URL('package.json', root));
const vite = join(dirname(require.resolve('vite/package.json')), 'bin/vite.js');
const result = spawnSync(process.execPath, [vite, 'build'], {
  cwd: fileURLToPath(new URL('web/panel/', root)),
  stdio: 'inherit',
});
if (result.status !== 0) process.exit(result.status ?? 1);
const source = await readFile(
  new URL('web/panel/dist/auriga-team-panel.js', root),
);
const pkg = JSON.parse(
  await readFile(new URL('web/panel/package.json', root), 'utf8'),
);
const output = new URL('../ui/', import.meta.url);
await mkdir(output, { recursive: true });
await writeFile(new URL('auriga-team-panel.js', output), source);
await writeFile(
  new URL('metadata.json', output),
  JSON.stringify({
    source: 'package',
    version: pkg.version,
    integrity: 'sha384-' + createHash('sha384').update(source).digest('base64'),
  }) + '\n',
);
