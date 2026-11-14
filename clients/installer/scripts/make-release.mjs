/** CI produces unsigned artifacts. This script never accepts a private key. */
import { createHash, createPublicKey } from 'node:crypto';
import { writeFileSync, readFileSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { sha256File } from '../runtime/verify.mjs';
import { unsignedRelease } from './sign-release.mjs';

export function makeRelease(options) {
  const body = unsignedRelease(options);
  if (options.exe && sha256File(options.exe) !== body.sha256)
    throw new Error('release artifact must be the setup exe');
  const commit = spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' });
  const sourceCommit = commit.stdout.trim();
  if (commit.status !== 0 || !/^[a-f0-9]{40}$/.test(sourceCommit))
    throw new Error('source commit unavailable');
  const keyFile = join(
    options.payload,
    'auriga/runtime/release-public-key.txt',
  );
  const record = {
    event: options.event ?? process.env.GITHUB_EVENT_NAME ?? null,
    ref: options.ref ?? process.env.GITHUB_REF ?? null,
    workflow: '.github/workflows/installer.yml',
    sourceOnMain:
      options.sourceOnMain === true ||
      process.env.DESKTOP_SOURCE_ON_MAIN === 'true',
    publicKeyFingerprint: existsSync(keyFile)
      ? createHash('sha256')
          .update(
            createPublicKey(readFileSync(keyFile)).export({
              type: 'spki',
              format: 'der',
            }),
          )
          .digest('hex')
      : null,
    exeSha256: options.exe ? sha256File(options.exe) : null,
    sourceCommit,
    desktopVersion: body.version,
    packageId: body.packageId,
    components: body.components,
    sha256: body.sha256,
    sizeBytes: body.sizeBytes,
    builtAt: new Date().toISOString(),
    workflowRun: options.workflowRun ?? null,
  };
  for (const [name, value] of [
    ['desktop-release.unsigned.json', body],
    ['desktop-build.json', record],
  ])
    writeFileSync(
      join(options.output, name),
      JSON.stringify(value, null, 2) + '\n',
      { flag: 'wx' },
    );
  writeFileSync(join(options.output, 'desktop.sha256'), body.sha256 + '\n', {
    flag: 'wx',
  });
  return record;
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  const options = Object.fromEntries(
    process.argv.slice(2).map((arg) => {
      const index = arg.indexOf('=');
      if (index < 1) throw new Error('expected name=value');
      return [arg.slice(0, index), arg.slice(index + 1)];
    }),
  );
  makeRelease(options);
}
