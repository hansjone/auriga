function unsafePath(value: string): boolean {
  return Array.from(value).some((char) => {
    const code = char.charCodeAt(0);
    return code <= 32 || code === 127 || char === '\\';
  });
}

/** An application-local path; reject browser URL normalization to another host. */
export function loginReturnTo(value: unknown): string | undefined {
  if (
    typeof value !== 'string' ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    unsafePath(value)
  )
    return undefined;
  try {
    const pathname = decodeURIComponent(value.split(/[?#]/, 1)[0]!);
    if (pathname.startsWith('//') || unsafePath(pathname)) return undefined;
    const base = 'https://portal.example.invalid';
    if (new URL(value, base).origin !== base) return undefined;
    return value;
  } catch {
    return undefined;
  }
}
