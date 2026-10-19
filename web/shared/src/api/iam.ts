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
  return {
    listLoginProviders(purpose: 'login' | 'link' = 'login') {
      const q = purpose === 'login' ? '' : `?purpose=${purpose}`;
      return client.get<ProvidersResponse>(`${IAM}/login/providers${q}`);
    },
    createLoginTransaction(body: CreateLoginTransaction) {
      return client.post<LoginTransaction>(`${IAM}/login/transactions`, body);
    },
    getLoginTransaction(txId: string) {
      return client.get<LoginTransaction>(`${IAM}/login/transactions/${txId}`);
    },
    advanceLoginTransaction(txId: string, input: StepInput) {
      return client.post<LoginTransaction>(
        `${IAM}/login/transactions/${txId}/advance`,
        input,
      );
    },
    cancelLoginTransaction(txId: string) {
      return client.delete<void>(`${IAM}/login/transactions/${txId}`);
    },
    getMe() {
      return client.get<Me>(`${IAM}/me`);
    },
    logout() {
      return client.post<void>(`${IAM}/logout`);
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
