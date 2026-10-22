import type { components, paths } from '../generated/iam';
import type { StepInput } from '../generated/steps';
import type { ApiClient } from './client';

export type LoginProvider = components['schemas']['LoginProvider'];
export type LoginTransaction = components['schemas']['LoginTransaction'];
export type CreateLoginTransaction =
  components['schemas']['CreateLoginTransaction'];
export type Me = components['schemas']['Me'];
export type QrChallenge = components['schemas']['QrChallenge'];

type ProvidersResponse =
  paths['/login/providers']['get']['responses']['200']['content']['application/json'];

const IAM = '/api/iam/v1';

export function createIamApi(client: ApiClient) {
  const anonymousTransactions = new Set<string>();
  async function remember(
    transaction: Promise<LoginTransaction>,
    anonymous: boolean,
  ) {
    const tx = await transaction;
    if (anonymous) anonymousTransactions.add(tx.id);
    if (tx.step.type === 'complete') {
      client.resetSession();
      anonymousTransactions.clear();
    }
    return tx;
  }
  return {
    listLoginProviders(purpose: 'login' | 'link' = 'login') {
      const q = purpose === 'login' ? '' : `?purpose=${purpose}`;
      return client.get<ProvidersResponse>(`${IAM}/login/providers${q}`);
    },
    createLoginTransaction(body: CreateLoginTransaction) {
      const anonymous = body.purpose === 'login';
      if (anonymous) {
        client.resetSession();
        anonymousTransactions.clear();
      }
      return remember(
        client.post<LoginTransaction>(`${IAM}/login/transactions`, body, {
          anonymousLogin: anonymous,
        }),
        anonymous,
      );
    },
    getLoginTransaction(txId: string) {
      return client
        .get<LoginTransaction>(`${IAM}/login/transactions/${txId}`)
        .then((tx) => {
          if (tx.step.type === 'complete') {
            client.resetSession();
            anonymousTransactions.clear();
          }
          if (tx.purpose === 'login' && tx.status === 'pending')
            anonymousTransactions.add(tx.id);
          return tx;
        });
    },
    advanceLoginTransaction(txId: string, input: StepInput) {
      return remember(
        client.post<LoginTransaction>(
          `${IAM}/login/transactions/${txId}/advance`,
          input,
          { anonymousLogin: anonymousTransactions.has(txId) },
        ),
        anonymousTransactions.has(txId),
      );
    },
    cancelLoginTransaction(txId: string) {
      const anonymousLogin = anonymousTransactions.delete(txId);
      return client.delete<void>(`${IAM}/login/transactions/${txId}`, {
        anonymousLogin,
      });
    },
    getMe() {
      return client.get<Me>(`${IAM}/me`);
    },
    async logout() {
      client.resetSession();
      anonymousTransactions.clear();
      try {
        await client.post<void>(`${IAM}/logout`);
      } finally {
        client.resetSession();
      }
    },
    getQrChallenge(challengeId: string) {
      return client.get<QrChallenge>(`${IAM}/login/qr/${challengeId}`);
    },
    confirmQrChallenge(challengeId: string, matchCode: string) {
      return client.post<void>(`${IAM}/login/qr/${challengeId}/confirm`, {
        matchCode,
      });
    },
    rejectQrChallenge(challengeId: string) {
      return client.post<void>(`${IAM}/login/qr/${challengeId}/reject`);
    },
  };
}

export type IamApi = ReturnType<typeof createIamApi>;
