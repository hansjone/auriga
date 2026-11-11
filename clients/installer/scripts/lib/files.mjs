import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, sep } from 'node:path';

export function sha256File(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

/** All files below dir as sorted posix-style relative paths. */
export function listFiles(dir) {
  const out = [];
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else out.push(relative(dir, full).split(sep).join('/'));
    }
  };
  walk(dir);
  return out.sort();
}

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
export function findSecrets(dir, skip = () => false) {
  const hits = [];
  for (const file of listFiles(dir)) {
    if (skip(`/${file}`)) continue;
    if (FORBIDDEN_NAMES.test(file)) {
      hits.push(`${file}: forbidden file name`);
      continue;
    }
    if (statSize(join(dir, file)) > 2 * 1024 * 1024) continue;
    const text = readFileSync(join(dir, file), 'latin1');
    for (const pattern of SECRET_PATTERNS)
      if (pattern.test(text)) hits.push(`${file}: matches ${pattern}`);
  }
  return hits;
}

function statSize(file) {
  return statSync(file).size;
}

export function assertNoSecrets(dir, skip) {
  const hits = findSecrets(dir, skip);
  if (hits.length > 0)
    throw new Error(`secrets found in package:\n${hits.join('\n')}`);
}
