import { describe, expect, it } from 'vitest';
import { loginReturnTo } from '../src/login-return';

describe('UAC login return destination', () => {
  it.each([
    '/',
    '/account/security',
    '/apps?tab=recent#selected',
    '/%E8%B4%A6%E5%8F%B7',
  ])('retains a local destination %s', (value) => {
    expect(loginReturnTo(value)).toBe(value);
  });
  it.each([
    undefined,
    null,
    ['/account'],
    '',
    'https://other.example/',
    '//other.example/',
    '/\\other.example/',
    '/%2fother.example/',
    '/%5cother.example/',
    '/account\n',
    '/%0aaccount',
    '/%broken',
  ])('rejects unsafe or ambiguous input %s', (value) => {
    expect(loginReturnTo(value)).toBeUndefined();
  });
});
