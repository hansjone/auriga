import type { StepInput } from '../generated/steps';
import {
  MOCK_INVITE_CODE,
  MOCK_PASSWORD,
  MOCK_QR_CHALLENGE,
  MOCK_TOTP,
  OIDC_PROVIDER_ID,
  TEAM_PROVIDER_ID,
  completeStep,
  failCredentialsStep,
  makeTransaction,
  mockMe,
  mockMembers,
  mockProviders,
  passwordStep,
  totpStep,
} from './data';

type TxState = {
  phase: 'password' | 'totp' | 'done' | 'failed';
  providerId: string;
  returnTo: string;
  audience: 'portal' | 'admin';
};

const txs = new Map<string, TxState>();
let sessionAudience: 'portal' | 'admin' | null = null;
let csrf = 'mock-csrf-token';

function json(data: unknown, status = 200, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'Content-Type': 'application/json',
      'Set-Cookie': `auriga_csrf=${csrf}; Path=/`,
      ...headers,
    },
  });
}

function problem(
  status: number,
  code: string,
  title: string,
  detail?: string,
): Response {
  return new Response(JSON.stringify({ title, status, code, detail }), {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  });
}

function newId(): string {
  return crypto.randomUUID();
}

function parseUrl(input: RequestInfo | URL): URL {
  if (input instanceof URL) return input;
  const raw = typeof input === 'string' ? input : input.url;
  return new URL(raw, 'http://localhost');
}

async function readJson<T>(req: Request): Promise<T | undefined> {
  try {
    return (await req.json()) as T;
  } catch {
    return undefined;
  }
}

/** Handle /api/* mock requests; return null to fall through. */
export async function handleMockRequest(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response | null> {
  const url = parseUrl(input);
  if (!url.pathname.startsWith('/api/')) return null;

  const method = (
    init?.method ??
    (typeof input !== 'string' && !(input instanceof URL)
      ? input.method
      : 'GET') ??
    'GET'
  ).toUpperCase();
  const path = url.pathname;
  const req = new Request(url, { ...init, method });

  // --- IAM login providers ---
  if (method === 'GET' && path === '/api/iam/v1/login/providers') {
    return json({ items: mockProviders });
  }

  // --- create transaction ---
  if (method === 'POST' && path === '/api/iam/v1/login/transactions') {
    const body = await readJson<{
      providerId: string;
      returnTo?: string;
      invitationCode?: string;
    }>(req);
    if (!body?.providerId) {
      return problem(400, 'common.validation_failed', 'Validation failed');
    }
    const provider = mockProviders.find((p) => p.id === body.providerId);
    if (!provider) {
      return problem(400, 'common.validation_failed', 'Unknown provider');
    }
    const id = newId();
    const returnTo = body.returnTo || '/';
    // Edge would pick audience by Host; mock infers from returnTo used by each app.
    const audience =
      returnTo.startsWith('/members') ||
      returnTo.startsWith('/groups') ||
      returnTo.startsWith('/roles') ||
      returnTo.startsWith('/providers') ||
      returnTo.startsWith('/settings') ||
      returnTo.startsWith('/admin')
        ? 'admin'
        : 'portal';
    if (provider.id === OIDC_PROVIDER_ID) {
      txs.set(id, {
        phase: 'done',
        providerId: provider.id,
        returnTo,
        audience,
      });
      return json(
        makeTransaction(
          id,
          {
            type: 'redirect',
            url: 'https://idp.example.com/authorize?client_id=auriga',
            method: 'GET',
          },
          provider.id,
        ),
        201,
      );
    }
    txs.set(id, {
      phase: 'password',
      providerId: provider.id,
      returnTo,
      audience,
    });
    return json(makeTransaction(id, passwordStep(), provider.id), 201);
  }

  const txMatch = path.match(
    /^\/api\/iam\/v1\/login\/transactions\/([^/]+)(?:\/(advance))?$/,
  );
  if (txMatch) {
    const txId = txMatch[1]!;
    const action = txMatch[2];
    const state = txs.get(txId);
    if (!state) {
      return problem(404, 'common.not_found', 'Not found');
    }
    if (method === 'GET' && !action) {
      const step =
        state.phase === 'password'
          ? passwordStep()
          : state.phase === 'totp'
            ? totpStep()
            : state.phase === 'failed'
              ? failCredentialsStep()
              : completeStep(state.returnTo);
      return json(makeTransaction(txId, step, state.providerId));
    }
    if (method === 'DELETE' && !action) {
      txs.delete(txId);
      return new Response(null, { status: 204 });
    }
    if (method === 'POST' && action === 'advance') {
      const input = await readJson<StepInput>(req);
      if (!input) {
        return problem(400, 'common.validation_failed', 'Validation failed');
      }
      if (state.phase === 'password' && input.type === 'form') {
        const password = String(input.values.password ?? '');
        if (password === 'fail-hard') {
          state.phase = 'failed';
          return json(
            makeTransaction(txId, failCredentialsStep(), state.providerId),
          );
        }
        if (password !== MOCK_PASSWORD) {
          return json(
            makeTransaction(
              txId,
              passwordStep({
                code: 'iam.invalid_credentials',
                message: {
                  en: 'Invalid username or password.',
                  'zh-CN': '用户名或口令不正确。',
                },
              }),
              state.providerId,
            ),
          );
        }
        state.phase = 'totp';
        return json(makeTransaction(txId, totpStep(), state.providerId));
      }
      if (state.phase === 'totp' && input.type === 'form') {
        const code = String(input.values.code ?? '');
        if (code !== MOCK_TOTP) {
          return json(
            makeTransaction(
              txId,
              {
                ...totpStep(),
                error: {
                  code: 'iam.invalid_otp',
                  message: {
                    en: 'Invalid authenticator code.',
                    'zh-CN': '验证码不正确。',
                  },
                },
              },
              state.providerId,
            ),
          );
        }
        state.phase = 'done';
        sessionAudience = state.audience;
        csrf = `mock-csrf-${sessionAudience}`;
        const headers = {
          'Set-Cookie': [
            `auriga_csrf=${csrf}; Path=/`,
            state.audience === 'admin'
              ? `auriga_admin_session=mock-admin; Path=/`
              : `auriga_session=mock-portal; Path=/`,
          ].join(', '),
        };
        return json(
          makeTransaction(txId, completeStep(state.returnTo), state.providerId),
          200,
          headers,
        );
      }
      if (state.phase === 'failed' && input.type === 'form') {
        state.phase = 'password';
        return json(makeTransaction(txId, passwordStep(), state.providerId));
      }
      return problem(409, 'iam.login_tx_finished', 'Transaction finished');
    }
  }

  if (method === 'GET' && path === '/api/iam/v1/me') {
    if (!sessionAudience) {
      return problem(401, 'common.unauthenticated', 'Unauthenticated');
    }
    return json(mockMe(sessionAudience));
  }

  if (method === 'POST' && path === '/api/iam/v1/logout') {
    sessionAudience = null;
    return new Response(null, { status: 204 });
  }

  const qrGet = path.match(/^\/api\/iam\/v1\/login\/qr\/([^/]+)$/);
  if (method === 'GET' && qrGet) {
    const challengeId = qrGet[1]!;
    if (challengeId !== MOCK_QR_CHALLENGE) {
      return problem(404, 'common.not_found', 'Not found');
    }
    return json({
      challengeId,
      initiator: {
        ip: '192.0.2.10',
        userAgent: 'MockBrowser/1.0',
        location: 'Example City',
      },
      purpose: 'login',
      expiresAt: new Date(Date.now() + 120_000).toISOString(),
    });
  }

  const qrAction = path.match(
    /^\/api\/iam\/v1\/login\/qr\/([^/]+)\/(confirm|reject)$/,
  );
  if (method === 'POST' && qrAction) {
    const challengeId = qrAction[1]!;
    const action = qrAction[2]!;
    if (challengeId !== MOCK_QR_CHALLENGE) {
      return problem(404, 'common.not_found', 'Not found');
    }
    if (action === 'confirm') {
      const body = await readJson<{ matchCode: string }>(req);
      if (body?.matchCode !== '42') {
        return problem(409, 'iam.qr_match_mismatch', 'Match code mismatch');
      }
    }
    return new Response(null, { status: 204 });
  }

  if (method === 'GET' && path === '/api/org/v1/admin/members') {
    if (sessionAudience !== 'admin') {
      return problem(401, 'common.unauthenticated', 'Unauthenticated');
    }
    const q = (url.searchParams.get('q') || '').toLowerCase();
    const limit = Number(url.searchParams.get('limit') || '50');
    const cursor = url.searchParams.get('cursor');
    const items = mockMembers.filter((m) => {
      if (!q) return true;
      return (
        m.displayName.toLowerCase().includes(q) ||
        (m.email || '').toLowerCase().includes(q)
      );
    });
    let start = 0;
    if (cursor) {
      const idx = items.findIndex((m) => m.id === cursor);
      start = idx >= 0 ? idx + 1 : 0;
    }
    const page = items.slice(start, start + limit);
    const next = items[start + limit];
    return json({
      items: page,
      ...(next ? { nextCursor: page[page.length - 1]?.id } : {}),
    });
  }

  const invite = path.match(/^\/api\/org\/v1\/invitations\/preview\/([^/]+)$/);
  if (method === 'GET' && invite) {
    const code = decodeURIComponent(invite[1]!);
    if (code !== MOCK_INVITE_CODE) {
      return json({ valid: false, reason: 'not_found' });
    }
    return json({
      valid: true,
      tenantName: 'Example Team',
      inviterName: 'Alice',
      memberType: 'member',
      email: 'a***@example.com',
      providerIds: [TEAM_PROVIDER_ID],
      expiresAt: new Date(Date.now() + 7 * 86400_000).toISOString(),
    });
  }

  return problem(404, 'common.not_found', 'Not found', path);
}

export function resetMockState(): void {
  txs.clear();
  sessionAudience = null;
  csrf = 'mock-csrf-token';
}

export function setMockSession(audience: 'portal' | 'admin' | null): void {
  sessionAudience = audience;
}
