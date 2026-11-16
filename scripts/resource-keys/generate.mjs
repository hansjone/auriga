/** Local-only resource root bootstrap. Private bytes never leave protected files. */
import { generateKeyPairSync, randomUUID, createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { lstatSync, mkdirSync, realpathSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

function privateDirectory(directory) {
  // mkdir is exclusive even when two generators select the same output.
  // On Windows there are no private bytes until the DACL is protected.
  mkdirSync(directory, { mode: 0o700 });
  if (process.platform !== 'win32') {
    return;
  }
  const script = fileURLToPath(
    new URL('./private-directory.ps1', import.meta.url),
  );
  const result = spawnSync(
    'powershell.exe',
    ['-NoProfile', '-NonInteractive', '-File', script],
    {
      env: { ...process.env, AURIGA_RESOURCE_KEY_DIRECTORY: directory },
      encoding: 'utf8',
    },
  );
  if (result.status !== 0) throw new Error('Cannot protect new key directory');
}

export function generateKeys(output, kidPrefix = `resource-${randomUUID()}`) {
  if (
    typeof kidPrefix !== 'string' ||
    !/^[A-Za-z0-9._-]{1,110}$/.test(kidPrefix)
  )
    throw new Error('Invalid resource kid prefix');
  const requested = resolve(output);
  // Refuse aliases before creating anything. Canonical parent casing is also
  // required by Java's toRealPath equality check on Windows.
  for (let parent = dirname(requested); ; parent = dirname(parent)) {
    if (lstatSync(parent).isSymbolicLink())
      throw new Error('Indirect or symbolic key parent');
    if (parent === dirname(parent)) break;
  }
  const directory = join(
    realpathSync.native(dirname(requested)),
    basename(requested),
  );
  privateDirectory(directory);
  privateDirectory(join(directory, 'platform'));
  privateDirectory(join(directory, 'offline'));
  const trust = {};
  for (const role of ['release', 'revocation', 'downgrade']) {
    const { privateKey, publicKey } = generateKeyPairSync('ed25519');
    const bytes = privateKey.export({ type: 'pkcs8', format: 'der' });
    try {
      writeFileSync(
        join(
          directory,
          role === 'downgrade' ? 'offline' : 'platform',
          `${role}.pk8`,
        ),
        bytes,
        {
          mode: 0o600,
          flag: 'wx',
        },
      );
    } finally {
      bytes.fill(0);
    }
    trust[role] = {
      kid: `${kidPrefix}-${role}`,
      publicKeySpkiBase64: publicKey
        .export({ type: 'spki', format: 'der' })
        .toString('base64'),
    };
  }
  const json = JSON.stringify(trust);
  writeFileSync(join(directory, 'resource-roots.json'), json, {
    mode: 0o600,
    flag: 'wx',
  });
  writeFileSync(
    join(directory, 'platform.properties'),
    [
      `auriga.resource.release-kid=${trust.release.kid}`,
      'auriga.resource.release-key-file=/run/secrets/auriga-resource/release.pk8',
      `auriga.resource.revocation-kid=${trust.revocation.kid}`,
      'auriga.resource.revocation-key-file=/run/secrets/auriga-resource/revocation.pk8',
      '',
    ].join('\n'),
    { mode: 0o600, flag: 'wx' },
  );
  return {
    directory,
    trust,
    resourceRootsSha256: createHash('sha256').update(json).digest('hex'),
  };
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.slice(2);
    const options = {};
    for (let i = 0; i < args.length; i += 2) {
      if (
        !['--out', '--kid-prefix'].includes(args[i]) ||
        !args[i + 1] ||
        options[args[i]]
      )
        throw new Error(
          'Usage: node generate.mjs --out <new-directory> [--kid-prefix <prefix>]',
        );
      options[args[i]] = args[i + 1];
    }
    if (!options['--out']) throw new Error('--out is required');
    const { directory, resourceRootsSha256 } = generateKeys(
      options['--out'],
      options['--kid-prefix'],
    );
    console.log(JSON.stringify({ directory, resourceRootsSha256 }));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
