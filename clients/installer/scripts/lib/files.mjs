import { readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { listFiles } from '../../runtime/verify.mjs';

export { sha256File, listFiles } from '../../runtime/verify.mjs';

const FORBIDDEN_NAMES =
  /(^|\/)(\.env(\..*)?|.*\.pem|.*\.key|id_rsa|credentials?\.json|.*\.p12|.*\.pfx)$/i;
const SECRET_PATTERNS = [
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/,
  /\bsk-[A-Za-z0-9]{20,}/,
  /\beyJ[A-Za-z0-9_-]{15,}\.[A-Za-z0-9_-]{15,}\.[A-Za-z0-9_-]{10,}/,
  /\bBearer\s+[A-Za-z0-9._~+/-]{24,}/,
  /AURIGA_[A-Z_]*(TOKEN|SECRET|PASSWORD)\s*=\s*\S{8,}/,
];

/** Throws when staged files that we own look like they contain secrets. */
export function findSecrets(
  dir,
  skip = () => false,
  maxBytes = 256 * 1024 * 1024,
) {
  const hits = [];
  for (const file of listFiles(dir)) {
    if (skip(`/${file}`)) continue;
    if (FORBIDDEN_NAMES.test(file)) {
      hits.push(`${file}: forbidden file name`);
      continue;
    }
    // Our own files are never skipped for size: too large to scan is a failure.
    if (statSync(join(dir, file)).size > maxBytes) {
      hits.push(`${file}: too large to scan (${maxBytes} bytes limit)`);
      continue;
    }
    const text = readFileSync(join(dir, file), 'latin1');
    for (const pattern of SECRET_PATTERNS)
      if (pattern.test(text)) hits.push(`${file}: matches ${pattern}`);
  }
  return hits;
}

export function assertNoSecrets(dir, skip, maxBytes) {
  const hits = findSecrets(dir, skip, maxBytes);
  if (hits.length > 0)
    throw new Error(`secrets found in package:\n${hits.join('\n')}`);
}
