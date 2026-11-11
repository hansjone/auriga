import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createPublicKey, verify } from 'node:crypto';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const [vectorsFile, clientVerifyFile] = process.argv.slice(2);
if (!vectorsFile || !clientVerifyFile)
  throw new Error(
    'Pass Java vector JSON and the MARKET-CLIENT resources/verify.js path',
  );
const { verifyResourceArchive } = await import(
  pathToFileURL(resolve(clientVerifyFile)).href
);
const { canonicalize } = await import(
  new URL('../network/trust/jcs.js', pathToFileURL(resolve(clientVerifyFile)))
);
const vectors = JSON.parse(await readFile(vectorsFile, 'utf8'));
for (const vector of vectors) {
  const envelope = vector.envelope;
  const key = createPublicKey({
    key: Buffer.from(vector.publicKey, 'base64'),
    format: 'der',
    type: 'spki',
  });
  const archive = Buffer.from(vector.archive, 'base64');
  const expected = { ...envelope, sizeBytes: archive.length };
  delete expected.signature;
  const verified = verifyResourceArchive(
    Buffer.from(JSON.stringify(envelope)),
    archive,
    {
      expected,
      releaseKeys: new Map([[envelope.signature.kid, key]]),
      now: Date.parse(envelope.statement.issuedAt) + 1,
    },
  );
  assert.equal(verified.statement.issuedAt, envelope.statement.issuedAt);
  assert(
    verify(
      null,
      Buffer.from(canonicalize(vector.legacyBody)),
      key,
      Buffer.from(vector.legacySignature, 'base64'),
    ),
  );
}
console.log(
  `Java envelope and legacy signatures verified by MARKET-CLIENT for ${vectors.length} time vectors`,
);
