import { createHash } from 'node:crypto';
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import {
  PRODUCT,
  computeId,
  PINNED_TREES,
  isPinnedPath,
  listFiles,
  sha256File,
  treeDigest,
} from '../runtime/verify.mjs';

const put = (dir, path, content) => {
  const file = join(dir, ...path.split('/'));
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, content);
};

/**
 * Build a small but fully valid package directory (real manifest, real
 * checksums) so tests run the real verification.
 */
export function buildPayload(base, options = {}) {
  const {
    name = 'p',
    pluginVersion = '1.0.0',
    dshVersion = '0.2.1-rc.3',
    enginesDsh = '>=0.2.1-rc.3',
    nodeContent = 'node-binary',
    coreContent = 'core',
    pnpmVersion = '11.28.5',
  } = options;
  const dir = join(base, `payload-${name}`);
  put(dir, 'upstream/node/node.exe', nodeContent);
  put(dir, 'upstream/app/package.json', '{}');
  put(
    dir,
    'upstream/app/package-lock.json',
    JSON.stringify({
      packages: {
        'node_modules/@deepseek-ai/dsh': { integrity: 'sha512-dsh' },
      },
    }),
  );
  put(
    dir,
    'upstream/app/node_modules/@deepseek-ai/dsh/package.json',
    JSON.stringify({ name: '@deepseek-ai/dsh', version: dshVersion }),
  );
  put(dir, 'upstream/app/node_modules/@deepseek-ai/dsh/index.js', 'module');
  put(dir, 'tools/pnpm/package.json', '{}');
  put(
    dir,
    'tools/pnpm/node_modules/pnpm/package.json',
    JSON.stringify({ name: 'pnpm', version: pnpmVersion }),
  );
  put(dir, 'tools/bin/pnpm.cmd', 'rem pnpm');
  put(dir, 'auriga/plugin/auriga-dsh.tgz', `tgz-${name}-${pluginVersion}`);
  put(
    dir,
    'auriga/plugin/node_modules/auriga-dsh/package.json',
    JSON.stringify({
      name: 'auriga-dsh',
      version: pluginVersion,
      engines: { dsh: enginesDsh },
    }),
  );
  put(dir, 'auriga/plugin/node_modules/@nats-io/nats-core/package.json', '{}');
  put(dir, 'auriga/runtime/core.mjs', coreContent);
  put(dir, 'auriga/launcher/auriga-desktop.cmd', `rem ${name}`);
  put(dir, 'auriga/launcher/rollback.cmd', `rem rollback ${name}`);
  put(dir, 'licenses/NOTICE.txt', 'notice');
  if (options.releasePublicKey)
    put(dir, 'auriga/runtime/release-public-key.txt', options.releasePublicKey);
  const files = listFiles(dir)
    .filter((f) => f !== 'manifest.json' && !isPinnedPath(f))
    .map((f) => ({
      path: f,
      size: readFileSync(join(dir, f)).length,
      sha256: sha256File(join(dir, f)),
    }));
  const manifest = {
    schema: 1,
    id: '',
    bundleVersion: pluginVersion,
    product: PRODUCT,
    target: 'win-x64',
    pnpm: { version: pnpmVersion, integrity: 'sha512-pnpm' },
    upstream: {
      name: '@deepseek-ai/dsh',
      version: dshVersion,
      integrity: 'sha512-dsh',
      lockSha256: sha256File(join(dir, 'upstream/app/package-lock.json')),
      nodeVersion: '24.0.0',
      nodeZipSha256: createHash('sha256').update(nodeContent).digest('hex'),
    },
    plugin: {
      name: 'auriga-dsh',
      version: pluginVersion,
      enginesDsh,
      tgzSha256: sha256File(join(dir, 'auriga/plugin/auriga-dsh.tgz')),
    },
    trees: PINNED_TREES.map((p) => treeDigest(dir, p.slice(0, -1))),
    files,
  };
  manifest.id = computeId(manifest);
  writeFileSync(
    join(dir, 'manifest.json'),
    JSON.stringify(manifest, null, 2) + '\n',
  );
  return { dir, id: manifest.id, manifest };
}

export function tempBase(t) {
  const base = mkdtempSync(join(tmpdir(), 'auriga-installer-'));
  t.after(() => rmSync(base, { recursive: true, force: true }));
  return base;
}
