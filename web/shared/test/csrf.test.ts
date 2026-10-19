import { describe, expect, it } from 'vitest';
import { isWriteMethod, readCsrfCookie } from '../src/api/csrf';

describe('readCsrfCookie', () => {
  it('prefers __Host-auriga_csrf', () => {
    expect(
      readCsrfCookie(
        'auriga_csrf=fallback; __Host-auriga_csrf=host-token; other=1',
      ),
    ).toBe('host-token');
  });

  it('falls back to auriga_csrf', () => {
    expect(readCsrfCookie('foo=1; auriga_csrf=plain-token')).toBe(
      'plain-token',
    );
  });

  it('returns undefined when missing', () => {
    expect(readCsrfCookie('session=abc')).toBeUndefined();
  });
});

describe('isWriteMethod', () => {
  it('detects write verbs', () => {
    expect(isWriteMethod('post')).toBe(true);
    expect(isWriteMethod('GET')).toBe(false);
  });
});
