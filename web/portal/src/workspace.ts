import type {
  ApiClient,
  WorkspaceComponents,
  WorkspacePaths,
} from '@auriga/shared';

export type Instance = WorkspaceComponents['schemas']['Instance'];
type MyInstances =
  WorkspacePaths['/me/instances']['get']['responses'][200]['content']['application/json'];
type OpenResult =
  WorkspacePaths['/me/instance/open']['post']['responses'][200]['content']['application/json'];
const base = '/api/workspace/v1';

export function createWorkspaceApi(client: ApiClient) {
  return {
    list: (signal: AbortSignal) =>
      client.get<MyInstances>(`${base}/me/instances`, { signal }),
    open: (signal: AbortSignal) =>
      client.post<OpenResult>(`${base}/me/instance/open`, undefined, {
        signal,
      }),
    get: (id: string, signal: AbortSignal) =>
      client.get<Instance>(`${base}/instances/${encodeURIComponent(id)}`, {
        signal,
      }),
  };
}

export function instanceEntry(id: string) {
  // Never consume entryUrl from a response: edge owns ticket exchange and routing.
  if (!id || id === '.' || id === '..') throw new Error('Invalid instance ID');
  return `/open/${encodeURIComponent(id)}`;
}

export function enterInstance(id: string) {
  window.location.assign(instanceEntry(id));
}
