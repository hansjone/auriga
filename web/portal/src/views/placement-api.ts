import type { ApiClient, WorkspaceComponents } from '@auriga/shared';
import { readPlacementConfig } from './placement-config';
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

export function placementRequestKey() {
  return Array.from(
    globalThis.crypto.getRandomValues(new Uint8Array(16)),
    (byte) => byte.toString(16).padStart(2, '0'),
  ).join('');
}
const uuid = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
const exchangePath = new RegExp(`^/(?:i/${uuid}/)?_auriga/placement-ticket$`);
export function exchangeTarget(value: string) {
  const url = new URL(value, location.origin);
  const { instanceParentDomain } = readPlacementConfig();
  const poolLabel = url.hostname.split('.')[0];
  const trustedPool =
    !!instanceParentDomain &&
    url.protocol === 'https:' &&
    !url.port &&
    new RegExp(`^${uuid}$`).test(poolLabel) &&
    url.hostname === `${poolLabel}.${instanceParentDomain}` &&
    url.pathname === '/_auriga/placement-ticket';
  if (
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    (url.origin !== location.origin && !trustedPool) ||
    !exchangePath.test(url.pathname)
  )
    throw new Error('shared.invalid_entry');
  return url;
}
export async function exchangePlacement(
  value: PlacementOpen,
  signal: AbortSignal,
  browser: { fetch: typeof fetch; navigate: (url: string) => void } = {
    fetch: globalThis.fetch,
    navigate: (url) => location.assign(url),
  },
) {
  if (
    value.placement.state !== 'ready' ||
    !['shared', 'dedicated'].includes(value.placement.mode) ||
    value.expiresAt <= Date.now() / 1000
  )
    throw new Error('shared.not_ready');
  const url = exchangeTarget(value.exchangeUrl);
  // JSON POST only; no query string, storage, DOM input or logging of the ticket.
  const response = await browser.fetch(url, {
    method: 'POST',
    credentials: 'include',
    redirect: 'manual',
    cache: 'no-store',
    signal,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ticket: value.ticket }),
  });
  // Fetch deliberately hides a manual redirect's status and Location. Edge's
  // ticket contract uses 302 on success; navigate to the already trusted root.
  if (response.type !== 'opaqueredirect')
    throw new Error('shared.exchange_failed');
  const root = url.pathname.replace(/_auriga\/placement-ticket$/, '');
  const destination = new URL(root, url.origin);
  signal.throwIfAborted();
  browser.navigate(destination.href);
}
