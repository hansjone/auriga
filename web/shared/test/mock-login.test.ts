import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApiClient } from '../src/api/client';
import { createIamApi } from '../src/api/iam';
import {
  disableAurigaMock,
  enableAurigaMock,
  resetMockState,
} from '../src/mock/enable';
import { MOCK_PASSWORD, MOCK_TOTP, TEAM_PROVIDER_ID } from '../src/mock/data';

beforeEach(async () => {
  resetMockState();
  await enableAurigaMock();
});

afterEach(() => {
  disableAurigaMock();
});

describe('mock multi-step login', () => {
  it('provider → password → totp → complete', async () => {
    const iam = createIamApi(createApiClient());
    const providers = await iam.listLoginProviders();
    expect(providers.items.length).toBeGreaterThan(0);

    let tx = await iam.createLoginTransaction({
      providerId: TEAM_PROVIDER_ID,
      purpose: 'login',
      returnTo: '/',
    });
    expect(tx.step.type).toBe('form');
    if (tx.step.type !== 'form') throw new Error('expected form');
    expect(tx.step.formId).toBe('password');

    tx = await iam.advanceLoginTransaction(tx.id, {
      type: 'form',
      formId: 'password',
      values: { username: 'alice', password: MOCK_PASSWORD },
    });
    expect(tx.step.type).toBe('form');
    if (tx.step.type !== 'form') throw new Error('expected totp');
    expect(tx.step.formId).toBe('totp');

    tx = await iam.advanceLoginTransaction(tx.id, {
      type: 'form',
      formId: 'totp',
      values: { code: MOCK_TOTP },
    });
    expect(tx.step).toEqual({ type: 'complete', redirectTo: '/' });
  });

  it('failure path returns form error then fail-hard', async () => {
    const iam = createIamApi(createApiClient());
    let tx = await iam.createLoginTransaction({
      providerId: TEAM_PROVIDER_ID,
      purpose: 'login',
    });
    tx = await iam.advanceLoginTransaction(tx.id, {
      type: 'form',
      formId: 'password',
      values: { username: 'alice', password: 'wrong' },
    });
    expect(tx.step.type).toBe('form');
    if (tx.step.type === 'form') {
      expect(tx.step.error?.code).toBe('iam.invalid_credentials');
    }

    tx = await iam.advanceLoginTransaction(tx.id, {
      type: 'form',
      formId: 'password',
      values: { username: 'alice', password: 'fail-hard' },
    });
    expect(tx.step.type).toBe('fail');
  });
});
