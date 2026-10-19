import type { LoginProvider, LoginTransaction, Me } from '../api/iam';
import type { Member } from '../api/org';
import type { ClientStep, FormStep } from '../generated/steps';

export const TEAM_PROVIDER_ID = '01936f00-0000-7000-8000-000000000001';
export const OIDC_PROVIDER_ID = '01936f00-0000-7000-8000-000000000002';
export const MOCK_ACCOUNT_ID = '01936f00-0000-7000-8000-000000000010';
export const MOCK_TENANT_ID = '01936f00-0000-7000-8000-000000000020';
export const MOCK_SESSION_ID = '01936f00-0000-7000-8000-000000000030';

/** Demo password / TOTP for the happy path. */
export const MOCK_PASSWORD = 'correct-horse';
export const MOCK_TOTP = '123456';
export const MOCK_INVITE_CODE = 'invite-demo';
export const MOCK_QR_CHALLENGE = 'qr-demo-challenge';

export const mockProviders: LoginProvider[] = [
  {
    id: TEAM_PROVIDER_ID,
    connectorType: 'team-account',
    displayName: { en: 'Team account', 'zh-CN': '团队账号' },
    buttonText: { en: 'Sign in with password', 'zh-CN': '口令登录' },
    order: 0,
    primaryStep: 'form',
  },
  {
    id: OIDC_PROVIDER_ID,
    connectorType: 'oidc',
    displayName: { en: 'Example IdP', 'zh-CN': '示例身份源' },
    buttonText: { en: 'Continue with IdP', 'zh-CN': '使用外部身份源' },
    order: 1,
    primaryStep: 'redirect',
  },
];

export function passwordStep(error?: FormStep['error']): FormStep {
  return {
    type: 'form',
    formId: 'password',
    title: { en: 'Sign in', 'zh-CN': '登录' },
    fields: [
      {
        name: 'username',
        kind: 'text',
        label: { en: 'Username', 'zh-CN': '用户名' },
        required: true,
        autocomplete: 'username',
      },
      {
        name: 'password',
        kind: 'password',
        label: { en: 'Password', 'zh-CN': '口令' },
        required: true,
        autocomplete: 'current-password',
      },
    ],
    submitLabel: { en: 'Continue', 'zh-CN': '继续' },
    ...(error ? { error } : {}),
  };
}

export function totpStep(): FormStep {
  return {
    type: 'form',
    formId: 'totp',
    title: { en: 'Authenticator code', 'zh-CN': '验证器代码' },
    description: {
      en: 'Enter the 6-digit code from your authenticator.',
      'zh-CN': '输入验证器中的 6 位数字。',
    },
    fields: [
      {
        name: 'code',
        kind: 'otp',
        label: { en: 'Code', 'zh-CN': '验证码' },
        required: true,
        autocomplete: 'one-time-code',
        minLength: 6,
        maxLength: 6,
        pattern: '^[0-9]{6}$',
      },
    ],
    submitLabel: { en: 'Verify', 'zh-CN': '验证' },
  };
}

export function completeStep(redirectTo = '/'): ClientStep {
  return { type: 'complete', redirectTo };
}

export function failCredentialsStep(): ClientStep {
  return {
    type: 'fail',
    code: 'iam.invalid_credentials',
    message: {
      en: 'Invalid username or password.',
      'zh-CN': '用户名或口令不正确。',
    },
    retryable: true,
  };
}

export function makeTransaction(
  id: string,
  step: ClientStep,
  providerId = TEAM_PROVIDER_ID,
): LoginTransaction {
  const provider = mockProviders.find((p) => p.id === providerId)!;
  return {
    id,
    status: step.type === 'complete' ? 'completed' : 'pending',
    purpose: 'login',
    provider: {
      id: provider.id,
      connectorType: provider.connectorType,
      displayName: provider.displayName,
    },
    // OpenAPI-generated ClientStep is stricter on optional defaults than steps.d.ts.
    step: step as LoginTransaction['step'],
    expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
  };
}

export function mockMe(audience: 'portal' | 'admin' = 'portal'): Me {
  return {
    account: {
      id: MOCK_ACCOUNT_ID,
      tenantId: MOCK_TENANT_ID,
      displayName: 'Alice',
      email: 'alice@example.com',
      status: 'active',
      createdAt: '2026-01-01T00:00:00Z',
      lastLoginAt: new Date().toISOString(),
    },
    session: {
      id: MOCK_SESSION_ID,
      audience,
      current: true,
      providerId: TEAM_PROVIDER_ID,
      mfa: true,
      createdAt: '2026-01-01T00:00:00Z',
      lastSeenAt: new Date().toISOString(),
      expiresAt: new Date(Date.now() + 86400_000).toISOString(),
    },
    identities: [],
    mfa: { totp: true, passkeys: 0, required: true },
  };
}

export const mockMembers: Member[] = [
  {
    id: MOCK_ACCOUNT_ID,
    displayName: 'Alice',
    email: 'alice@example.com',
    memberType: 'member',
    status: 'active',
    groupIds: [],
    roleKeys: ['admin'],
    createdAt: '2026-01-01T00:00:00Z',
  },
  {
    id: '01936f00-0000-7000-8000-000000000011',
    displayName: 'Bob',
    email: 'bob@example.com',
    memberType: 'member',
    status: 'active',
    groupIds: [],
    roleKeys: ['member'],
    createdAt: '2026-02-01T00:00:00Z',
  },
  {
    id: '01936f00-0000-7000-8000-000000000012',
    displayName: 'Carol',
    email: 'carol@example.com',
    memberType: 'external',
    status: 'disabled',
    groupIds: [],
    roleKeys: [],
    createdAt: '2026-03-01T00:00:00Z',
    disabledAt: '2026-09-01T00:00:00Z',
  },
];
