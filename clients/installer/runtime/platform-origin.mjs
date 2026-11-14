import { isIP } from 'node:net';

/** HTTP is limited to a literal IP explicitly confirmed at first binding. */
export function updateOrigin(origin, binding = {}) {
  const url = new URL(origin);
  const ip = isIP(url.hostname.replace(/^\[|\]$/g, '')) !== 0;
  if (
    url.origin !== origin ||
    url.username ||
    url.password ||
    (url.protocol !== 'https:' &&
      !(
        url.protocol === 'http:' &&
        ip &&
        binding.accessMode === 'ip' &&
        binding.confirmedOrigin === origin
      ))
  )
    throw new Error(
      'updates require HTTPS or an explicitly confirmed IP platform origin',
    );
  return origin;
}
