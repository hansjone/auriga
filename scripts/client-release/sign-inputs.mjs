/** Connected maintainer: independently signed Windows addition to PR373 devkit. */
import { Buffer } from 'node:buffer';
import { createPrivateKey, createPublicKey, sign } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { assertPrivateKey } from './keys.mjs';
import { CLIENT_TARGET, clientPlatform } from './platform.mjs';
import { digest, verifyInputs, ROOT } from './inputs.mjs';
import { stampRequires } from './compat.mjs';
import { sha256File } from '../../clients/installer/runtime/verify.mjs';

export function signInputs(
  directory,
  keyFile,
  version,
  { checkTools = true, root = ROOT } = {},
) {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$/.test(version ?? ''))
    throw new Error('缺少有效 Windows 附件发行版本');
  for (const name of [
    'client-inputs-release.json',
    'SHA256SUMS',
    'SHA256SUMS.sig',
    'release-public.pem',
  ])
    if (existsSync(join(directory, name)))
      throw new Error('输入附件签名材料已存在，禁止覆盖');
  const manifestSha256 = sha256File(join(directory, 'windows-client.json'));
  const kit = verifyInputs(directory, manifestSha256, root, { checkTools });
  assertPrivateKey(keyFile);
  const key = createPrivateKey(readFileSync(keyFile));
  if (key.asymmetricKeyType !== 'ed25519')
    throw new Error('发行私钥必须为 Ed25519');
  const publicKey = createPublicKey(key);
  const metadata = stampRequires('client-inputs-release.json', {
    schemaVersion: 1,
    version,
    platform: CLIENT_TARGET,
    lockSha256: kit.lockSha256,
    manifestSha256,
    buildNodeSha256: sha256File(join(directory, clientPlatform().buildNode)),
  });
  const bytes = JSON.stringify(metadata, null, 2) + '\n';
  const sums = `${digest(bytes)}  client-inputs-release.json\n${manifestSha256}  windows-client.json\n`;
  for (const [name, content] of [
    ['client-inputs-release.json', bytes],
    ['release-public.pem', publicKey.export({ type: 'spki', format: 'pem' })],
    ['SHA256SUMS', sums],
    [
      'SHA256SUMS.sig',
      sign(null, Buffer.from(sums), key).toString('base64') + '\n',
    ],
  ])
    writeFileSync(join(directory, name), content, { flag: 'wx' });
  return {
    ...metadata,
    issuerFingerprint: digest(
      publicKey.export({ type: 'spki', format: 'der' }),
    ),
  };
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const { values: o } = parseArgs({
      options: {
        devkit: { type: 'string' },
        'key-file': { type: 'string' },
        version: { type: 'string' },
      },
    });
    if (!o.devkit || !o['key-file'])
      throw new Error('需 --devkit、--key-file、--version');
    console.log(
      JSON.stringify(signInputs(o.devkit, o['key-file'], o.version), null, 2),
    );
  } catch {
    console.error(
      'Windows 输入附件签名失败，请检查输入清单、发行私钥权限和版本。',
    );
    process.exitCode = 1;
  }
}
