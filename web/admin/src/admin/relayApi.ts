import type { ApiClient } from '@auriga/shared';
import type { components } from '../../../shared/src/generated/ai';
import { apiClient } from '../api';
type S = components['schemas'];
export type RelayProvider = S['RelayProvider'];
export type RelayProviderInput = S['RelayProviderInput'];
export type RelayPool = S['RelayPool'];
export type RelayKey = S['RelayKey'];
export type RelayKeyInput = S['RelayKeyInput'];
export type Usage = S['Usage'];
const base = '/api/ai/v1/admin/relay';
const id = encodeURIComponent;
export function createRelayApi(client: ApiClient) {
  const post = <T>(
    path: string,
    body: unknown,
    key: string,
    signal?: AbortSignal,
  ) =>
    client.post<T>(base + path, body, {
      signal,
      headers: { 'Idempotency-Key': key },
    });
  const all = async <
    P extends { items: unknown[]; nextCursor?: string | null },
  >(
    path: string,
    signal?: AbortSignal,
  ): Promise<P> => {
    const items: unknown[] = [],
      seen = new Set<string>();
    let cursor: string | null | undefined;
    do {
      const query = new URLSearchParams({ limit: '200' });
      if (cursor) query.set('cursor', cursor);
      const page = await client.get<P>(base + path + '?' + query, {
        cache: 'no-store',
        signal,
      });
      items.push(...page.items);
      cursor = page.nextCursor;
      if (cursor && seen.has(cursor)) throw new Error('Repeated relay cursor');
      if (cursor) seen.add(cursor);
    } while (cursor);
    return { items } as P;
  };
  return {
    providers: (signal?: AbortSignal) =>
      all<S['RelayProviderPage']>('/providers', signal),
    providerModels: (providerId: string, signal?: AbortSignal) =>
      client.get<S['RelayProviderModelPage']>(
        base + '/providers/' + id(providerId) + '/models',
        { cache: 'no-store', signal },
      ),
    pools: (signal?: AbortSignal) => all<S['RelayPoolPage']>('/pools', signal),
    keys: (signal?: AbortSignal) =>
      all<S['RelayKeyPage']>('/virtual-keys', signal),
    saveProvider: (
      providerId: string,
      input: RelayProviderInput,
      key: string,
      signal?: AbortSignal,
    ) =>
      providerId
        ? client.put<RelayProvider>(
            base + '/providers/' + id(providerId),
            input,
            { signal },
          )
        : post<RelayProvider>('/providers', input, key, signal),
    credential: (
      providerId: string,
      secret: string,
      key: string,
      signal?: AbortSignal,
    ) =>
      post<RelayProvider>(
        '/providers/' + id(providerId) + '/keys',
        { secret },
        key,
        signal,
      ),
    savePool: (input: RelayPool, key: string, signal?: AbortSignal) =>
      post<RelayPool>('/pools', input, key, signal),
    createKey: (input: RelayKeyInput, key: string, signal?: AbortSignal) =>
      post<S['RelayKeyIssued']>('/virtual-keys', input, key, signal),
    updateKey: (keyId: string, input: RelayKeyInput, signal?: AbortSignal) =>
      client.put<RelayKey>(base + '/virtual-keys/' + id(keyId), input, {
        signal,
      }),
    usage: (
      kind: 'accountId' | 'keyId',
      value: string,
      cursor?: string,
      signal?: AbortSignal,
    ) => {
      const query = new URLSearchParams({ [kind]: value, limit: '50' });
      if (cursor) query.set('cursor', cursor);
      return client.get<S['UsagePage']>(base + '/logs?' + query, {
        cache: 'no-store',
        signal,
      });
    },
  };
}
export const relayApi = createRelayApi(apiClient);
