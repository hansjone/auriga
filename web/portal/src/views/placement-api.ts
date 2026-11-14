import type { ApiClient, WorkspaceComponents } from '@auriga/shared';
export type Placement = WorkspaceComponents['schemas']['DshPlacement'];
export type PlacementOpen = WorkspaceComponents['schemas']['DshPlacementOpen'];
const base = '/api/workspace/v1';
export function createPlacementApi(client: ApiClient) {
  return {
    read: (signal: AbortSignal) =>
      client.get<Placement>(`${base}/me/placement`, {
        signal,
        cache: 'no-store',
      }),
    open: (key: string, signal: AbortSignal) =>
      client.post<PlacementOpen>(`${base}/me/placement/open`, undefined, {
        signal,
        headers: { 'Idempotency-Key': key },
      }),
  };
}
// The portal cannot infer trusted sibling domains. Domain deployments must
// explicitly list their trusted E origins in deployment configuration.
export function exchangeTarget(value: string, origins: readonly string[] = []) {
  const url = new URL(value, location.origin);
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    ![location.origin, ...origins].includes(url.origin) ||
    !/^\/(?:i\/[a-zA-Z0-9-]+\/)?_auriga\/placement-ticket$/.test(url.pathname)
  )
    throw new Error('shared.invalid_entry');
  return url;
}
export async function exchangePlacement(
  value: PlacementOpen,
  signal: AbortSignal,
) {
  if (
    value.placement.state !== 'ready' ||
    !['shared', 'dedicated'].includes(value.placement.mode) ||
    value.expiresAt <= Date.now() / 1000
  )
    throw new Error('shared.not_ready');
  const origins = (import.meta.env.VITE_SHARED_DSH_EXCHANGE_ORIGINS || '')
    .split(',')
    .filter(Boolean);
  const url = exchangeTarget(value.exchangeUrl, origins);
  // JSON POST only; no query string, storage, DOM input or logging of the ticket.
  const response = await fetch(url, {
    method: 'POST',
    credentials: 'include',
    cache: 'no-store',
    signal,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ticket: value.ticket }),
  });
  if (!response.ok) throw new Error('shared.exchange_failed');
  const root = url.pathname.replace(/_auriga\/placement-ticket$/, '');
  const destination = new URL(root, url.origin);
  const final = new URL(response.url);
  if (
    !response.redirected ||
    final.origin !== destination.origin ||
    final.pathname !== destination.pathname ||
    final.search ||
    final.hash
  )
    throw new Error('shared.invalid_entry');
  signal.throwIfAborted();
  location.assign(destination.href);
}
