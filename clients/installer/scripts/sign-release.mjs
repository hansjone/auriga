/** Offline release review and signing. Never prints private key material or its path. */
import {
  readFileSync,
  writeFileSync,
  statSync,
  rmSync,
  mkdtempSync,
} from 'node:fs';
import {
  createPrivateKey,
  sign,
  createPublicKey,
  createHash,
} from 'node:crypto';
import { Buffer } from 'node:buffer';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { extractArchive } from '../runtime/update.mjs';
import { resolve, join } from 'node:path';
import { canonical, verifyRelease, RELEASE_KID } from '../runtime/release.mjs';
import { verifyPackage, sha256File } from '../runtime/verify.mjs';

export function unsignedRelease({
  payload,
  archive,
  sequence,
  minimumVersion,
  notAfter,
  notes = '',
  publishedAt = new Date().toISOString(),
}) {
  const manifest = verifyPackage(payload);
  return {
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
    components: {
      dshVersion: manifest.upstream.version,
      nodeVersion: manifest.upstream.nodeVersion,
      pluginVersion: manifest.plugin.version,
    },
  };
}

export function reviewRelease(options) {
  const body = options.manifest
    ? JSON.parse(readFileSync(options.manifest, 'utf8'))
    : unsignedRelease(options);
  if (
    body.signature ||
    body.sha256 !== sha256File(options.archive) ||
    body.sizeBytes !== statSync(options.archive).size
  )
    throw new Error('archive differs from unsigned manifest');
  let record;
  if (options.manifest) {
    record = JSON.parse(readFileSync(options.buildRecord, 'utf8'));
    if (
      record.sha256 !== body.sha256 ||
      (record.exeSha256 && record.exeSha256 !== body.sha256) ||
      record.sizeBytes !== body.sizeBytes ||
      record.packageId !== body.packageId ||
      record.desktopVersion !== body.version ||
      canonical(record.components) !== canonical(body.components) ||
      !/^[a-f0-9]{40}$/.test(record.sourceCommit)
    )
      throw new Error('build record mismatch');
  }
  let publicKeyFingerprint = null;
  if (record) {
    if (
      !['push', 'workflow_dispatch'].includes(record.event) ||
      record.ref !== `refs/tags/desktop-v${body.version}` ||
      record.workflow !== '.github/workflows/installer.yml' ||
      record.sourceOnMain !== true ||
      !/^[0-9]+$/.test(record.workflowRun ?? '')
    )
      throw new Error('only desktop tag builds on main may be signed');
    const temporary = mkdtempSync(join(tmpdir(), 'desktop-review-'));
    try {
      const payload = extractArchive(
        options.archive,
        join(temporary, 'unpacked'),
      );
      const actual = verifyPackage(payload);
      if (
        actual.id !== body.packageId ||
        actual.bundleVersion !== body.version ||
        canonical(body.components) !==
          canonical({
            dshVersion: actual.upstream.version,
            nodeVersion: actual.upstream.nodeVersion,
            pluginVersion: actual.plugin.version,
          })
      )
        throw new Error('archive package identity mismatch');
      const publicKey = createPublicKey(
        readFileSync(join(payload, 'auriga/runtime/release-public-key.txt')),
      );
      if (publicKey.asymmetricKeyType !== 'ed25519')
        throw new Error('invalid package release key');
      publicKeyFingerprint = createHash('sha256')
        .update(publicKey.export({ type: 'spki', format: 'der' }))
        .digest('hex');
      if (record.publicKeyFingerprint !== publicKeyFingerprint)
        throw new Error('package public key differs from build record');
    } finally {
      rmSync(temporary, { recursive: true, force: true });
    }
  }
  const reviewSha256 = createHash('sha256')
    .update(canonical({ body, record: record ?? null }))
    .digest('hex');
  return {
    body,
    summary: {
      reviewSha256,
      packageId: body.packageId,
      version: body.version,
      sha256: body.sha256,
      sizeBytes: body.sizeBytes,
      components: body.components,
      sourceCommit: record?.sourceCommit ?? null,
      workflow: record?.workflow ?? null,
      workflowRun: record?.workflowRun ?? null,
      event: record?.event ?? null,
      ref: record?.ref ?? null,
      publicKeyFingerprint,
      sequence: body.sequence,
      notAfter: body.notAfter,
      minimumVersion: body.minimumVersion,
      publishedAt: body.publishedAt,
      notes: body.notes,
    },
  };
}

export function signRelease(options) {
  const { body, summary } = reviewRelease(options);
  if (options.confirmSha256 !== summary.reviewSha256)
    throw new Error('review SHA-256 confirmation required');
  const key = createPrivateKey(readFileSync(options.keyFile));
  if (key.asymmetricKeyType !== 'ed25519')
    throw new Error('Ed25519 key required');
  if (
    summary.publicKeyFingerprint &&
    createHash('sha256')
      .update(createPublicKey(key).export({ type: 'spki', format: 'der' }))
      .digest('hex') !== summary.publicKeyFingerprint
  )
    throw new Error('signing key differs from package trust root');
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
    currentVersion: body.minimumVersion,
  });
  return envelope;
}

if (resolve(process.argv[1] ?? '') === fileURLToPath(import.meta.url)) {
  try {
    const options = {};
    const argv = process.argv.slice(2);
    for (let index = 0; index < argv.length; index++) {
      const arg = argv[index];
      if (arg === '--review-only') {
        options.reviewOnly = true;
        continue;
      }
      if (arg === '--confirm-sha256') {
        options.confirmSha256 = argv[++index];
        continue;
      }
      const separator = arg.indexOf('=');
      if (separator < 1) throw new Error('expected name=value');
      const name = arg.slice(0, separator);
      options[name === '--confirm-sha256' ? 'confirmSha256' : name] = arg.slice(
        separator + 1,
      );
    }
    if (!options.manifest || !options.buildRecord)
      throw new Error('CI manifest and build record required');
    const review = reviewRelease(options).summary;
    const checkpoint = options.reviewState ?? options.output + '.review.json';
    if (options.reviewOnly) {
      if (!options.output)
        throw new Error('output required for review checkpoint');
      writeFileSync(
        checkpoint,
        JSON.stringify({ reviewSha256: review.reviewSha256 }),
        { mode: 0o600 },
      );
      console.log(JSON.stringify(review, null, 2));
    } else {
      if (
        JSON.parse(readFileSync(checkpoint, 'utf8')).reviewSha256 !==
        review.reviewSha256
      )
        throw new Error('repeat review required');
      writeFileSync(
        options.output,
        JSON.stringify(signRelease(options), null, 2) + '\n',
        { flag: 'wx' },
      );
      rmSync(checkpoint);
      console.log('发行清单已签名');
    }
  } catch {
    console.error('发行审阅或签名失败，请检查制品、构建记录及确认摘要。');
    process.exitCode = 1;
  }
}
