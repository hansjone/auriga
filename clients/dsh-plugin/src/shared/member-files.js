// @ts-nocheck
import { realpath } from 'node:fs/promises';
import { isAbsolute, relative, resolve, sep } from 'node:path';
import { fail } from './protocol.js';

export function memberWorkspace(binding) {
  return `/var/lib/auriga/pools/${binding.physicalInstanceId}/members/${binding.tenantId}/${binding.memberId}/${binding.epoch}/workspace`;
}

function under(root, candidate) {
  const suffix = relative(root, candidate);
  return (
    suffix !== '..' && !suffix.startsWith('..' + sep) && !isAbsolute(suffix)
  );
}

/** Entry restriction only. Same-UID hard links and filesystem races remain possible. */
export async function memberFilePath(root, input) {
  if (typeof input !== 'string' || !input || input.includes('\0'))
    fail('common.validation_failed');
  const base = resolve(root),
    candidate = resolve(base, input);
  if (!under(base, candidate)) fail('shared.binding_mismatch');
  let actualRoot, actualPath;
  try {
    [actualRoot, actualPath] = await Promise.all([
      realpath(base),
      realpath(candidate),
    ]);
  } catch {
    fail('shared.binding_mismatch');
  }
  if (!under(actualRoot, actualPath)) fail('shared.binding_mismatch');
  return actualPath;
}
