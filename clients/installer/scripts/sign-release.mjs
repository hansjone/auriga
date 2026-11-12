/** Offline only. No private material or key path is emitted. */
import { readFileSync, writeFileSync, statSync } from 'node:fs';
import { createPrivateKey, sign, createPublicKey } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { canonical, verifyRelease, RELEASE_KID } from '../runtime/release.mjs';
import { verifyPackage, sha256File } from '../runtime/verify.mjs';

export function signRelease({
  payload,
  archive,
  keyFile,
  sequence,
  minimumVersion,
  notAfter,
  notes = '',
  publishedAt = new Date().toISOString(),
}) {
  const manifest = verifyPackage(payload);
  const key = createPrivateKey(readFileSync(keyFile));
  if (key.asymmetricKeyType !== 'ed25519')
    throw new Error('Ed25519 key required');
  const body = {
    schema: 1,
    product: 'Auriga Desktop',
    version: manifest.bundleVersion,
    packageId: manifest.id,
    channel: 'stable',
    platform: 'win-x64',
    sizeBytes: statSync(archive).size,
    sha256: sha256File(archive),
    minimumVersion,
    publishedAt,
    notAfter,
    sequence: Number(sequence),
    notes,
  };
  const envelope = {
    ...body,
    signature: {
      alg: 'Ed25519',
      kid: RELEASE_KID,
      value: sign(null, Buffer.from(canonical(body)), key).toString(
        'base64url',
      ),
    },
  };
  verifyRelease(envelope, {
    publicKey: createPublicKey(key),
    currentVersion: minimumVersion,
  });
  return envelope;
}

if (process.argv[1]?.endsWith('sign-release.mjs')) {
  try {
    const args = Object.fromEntries(
      process.argv.slice(2).map((arg) => {
        const i = arg.indexOf('=');
        if (i < 1) throw new Error('expected name=value');
        return [arg.slice(0, i), arg.slice(i + 1)];
      }),
    );
    writeFileSync(
      args.output,
      JSON.stringify(signRelease(args), null, 2) + '\n',
      { flag: 'wx' },
    );
    console.log('发行清单已签名');
  } catch {
    console.error('签名失败；请检查参数、包和离线钥文件（未输出私钥路径）');
    process.exitCode = 1;
  }
}
