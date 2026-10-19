const CSRF_COOKIES = ['__Host-auriga_csrf', 'auriga_csrf'] as const;

/** Read CSRF token from document cookies (Host-prefixed first). */
export function readCsrfCookie(
  cookieSource: string = typeof document !== 'undefined' ? document.cookie : '',
): string | undefined {
  const parts = cookieSource.split(';').map((p) => p.trim());
  for (const name of CSRF_COOKIES) {
    const prefix = `${name}=`;
    const hit = parts.find((p) => p.startsWith(prefix));
    if (hit) return decodeURIComponent(hit.slice(prefix.length));
  }
  return undefined;
}

export function isWriteMethod(method: string): boolean {
  return ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method.toUpperCase());
}
