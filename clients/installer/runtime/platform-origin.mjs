import { isIP } from 'node:net';

function parse(input) {
  if (typeof input !== 'string' || input.length > 2048)
    throw new Error('invalid platform address');
  const url = new URL(input.trim());
  if (
    url.username ||
    url.password ||
    !['https:', 'http:'].includes(url.protocol)
  )
    throw new Error('platform address must use HTTP(S) without credentials');
  return url;
}

function privateHttp(input) {
  const url = parse(input);
  // Validate the original literal before WHATWG URL rewrites decimal/octal IPs.
  const host = input
    .trim()
    .match(/^http:\/\/(\[[^\]]+\]|[^/:?#]+)(?::\d+)?\/?$/i)?.[1];
  const literal = host?.replace(/^\[|\]$/g, '');
  const kind = literal ? isIP(literal) : 0;
  const octets = literal?.split('.').map(Number);
  const local =
    kind === 4
      ? octets[0] === 127 ||
        octets[0] === 10 ||
        (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) ||
        (octets[0] === 192 && octets[1] === 168)
      : kind === 6 &&
        (url.hostname === '[::1]' || /^\[f[cd][\da-f]{2}:/i.test(url.hostname));
  if (url.protocol !== 'http:' || !host || !local)
    throw new Error(
      'HTTP platform must be a private or loopback literal IP without a path',
    );
  return url.origin;
}

/** Installation input may contain an HTTPS path; only the origin is persisted. */
export function normalizePlatformBinding(input, binding = {}) {
  const url = parse(input);
  const origin = url.protocol === 'https:' ? url.origin : privateHttp(input);
  if (
    url.protocol === 'http:' &&
    (binding.accessMode !== 'ip' || !binding.confirmedOrigin)
  )
    throw new Error(
      'HTTP platform requires IP mode and explicit address confirmation; reinstall with confirmation parameters',
    );
  if (binding.confirmedOrigin !== undefined) {
    const confirmed = parse(binding.confirmedOrigin);
    const confirmedOrigin =
      confirmed.protocol === 'https:'
        ? confirmed.origin
        : privateHttp(binding.confirmedOrigin);
    if (confirmedOrigin !== origin)
      throw new Error('platform address differs from explicit confirmation');
  }
  return url.protocol === 'http:'
    ? { origin, accessMode: 'ip', confirmedOrigin: origin }
    : { origin };
}

/** Runtime accepts only an already canonical, installation-owned origin. */
export function updateOrigin(origin, binding = {}) {
  const normalized = normalizePlatformBinding(origin, binding);
  if (normalized.origin !== origin)
    throw new Error(
      'update origin must be canonical; normalize it during installation',
    );
  return normalized.origin;
}
