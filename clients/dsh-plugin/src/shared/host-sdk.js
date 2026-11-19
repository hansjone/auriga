// @ts-nocheck
import { resolveInstalledSdkModule } from './profile-resolution.js';

/** Internal code factory, never a browser/YAML/env-selected module loader. */
export function createHostSdkLoader({
  resolveModule = resolveInstalledSdkModule,
  importModule = (url) => import(url),
} = {}) {
  let pending;
  return () => {
    pending ??= Promise.all(
      ['@deepseek-ai/dsh-client-connection', '@deepseek-ai/dsh-llm'].map(
        async (name) => importModule(resolveModule(name)),
      ),
    ).then(([connection, llm]) => {
      if (
        typeof connection.Peer !== 'function' ||
        typeof llm.LlmAdapter !== 'function'
      )
        throw new Error('Approved Host SDK is unavailable');
      return Object.freeze({
        Peer: connection.Peer,
        LlmAdapter: llm.LlmAdapter,
      });
    });
    return pending;
  };
}

// Both public classes come from the same immutable /opt/dsh installation.
// A failed installed export stays failed; no devDependency/rc.2 fallback.
export const loadHostSdk = createHostSdkLoader();
