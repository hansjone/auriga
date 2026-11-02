import { open, constants } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { createPlatform } from '../local/platform.js';
import { parseStrictJson } from '../network/trust/jcs.js';
import { logEvent } from '../observability/index.js';
import { performance } from 'node:perf_hooks';
import { operation } from './transport.js';
/* global AbortSignal */

/** Status for the renderer-owned native pi-ai route. No adapter registration,
 * OAuth exchange or public device-token fallback occurs here.
 * @param {{tenantId:string,ownerId:string,instanceId:string,file?:string}} binding */
export function createManagedModelStatus(binding) {
  const file = binding.file ?? '/run/auriga/model-credential.json';
  let closed = false;
  let status = unavailable('managed_model_unavailable');
  /** @type {Promise<void>|undefined} */
  let pending;
  const lifetime = new AbortController();
  /** @param {string} state */
  function unavailable(state) {
    return { registered: false, available: false, authorized: false, state };
  }
  async function material() {
    if (!isAbsolute(file)) throw new Error('private model file required');
    const handle = await open(
      file,
      constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
    );
    try {
      const stat = await handle.stat();
      if (
        !stat.isFile() ||
        stat.size > 1048576 ||
        (process.platform !== 'win32' && (stat.mode & 0o077) !== 0)
      )
        throw new Error('private model file required');
      const value =
        /** @type {import('../generated/managed-model.js').ManagedModelCredential} */ (
          parseStrictJson(Buffer.from(await handle.readFile('utf8')))
        );
      const credentialPrefix = `amc.${binding.tenantId}.${binding.ownerId}.${binding.instanceId}.`;
      if (
        value.tenantId !== binding.tenantId ||
        value.ownerId !== binding.ownerId ||
        value.instanceId !== binding.instanceId ||
        !Number.isSafeInteger(value.credentialVersion) ||
        value.credentialVersion < 1 ||
        typeof value.credential !== 'string' ||
        !value.credential.startsWith(credentialPrefix) ||
        !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$/.test(
          value.credential.slice(credentialPrefix.length),
        ) ||
        !Array.isArray(value.models) ||
        !value.models.length ||
        value.models.length > 256 ||
        value.models.some(
          (m) => typeof m.id !== 'string' || !m.id.trim() || m.id.length > 200,
        )
      )
        throw new Error('invalid model binding');
      const base = new URL(value.baseUrl);
      if (
        !['http:', 'https:'].includes(base.protocol) ||
        base.username ||
        base.password ||
        base.pathname !== '/ai/v1' ||
        base.search ||
        base.hash ||
        base.href !== value.baseUrl
      )
        throw new Error('invalid model origin');
      return { value, origin: base.origin };
    } finally {
      await handle.close();
    }
  }
  return {
    /** Public native LLM service only. @param {any} llm */
    refresh(llm) {
      if (closed) return Promise.resolve();
      if (pending) return pending;
      status = unavailable('managed_model_checking');
      const probe = operation(
        AbortSignal.any([lifetime.signal, AbortSignal.timeout(4000)]),
      );
      pending = (async () => {
        const started = performance.now();
        let phase = 'native',
          version = null;
        try {
          if (typeof llm?.listModels !== 'function')
            throw new Error('provider unavailable');
          phase = 'material';
          const { value, origin } = await probe.wait(material());
          version = value.credentialVersion;
          phase = 'native';
          const native = await probe.wait(
            Promise.resolve().then(() => llm.listModels('auriga-managed')),
          );
          if (
            !Array.isArray(native) ||
            !native.some((m) => value.models.some((v) => v.id === m.id))
          )
            throw new Error('provider unavailable');
          const platform = createPlatform({
            platformOrigin: origin,
            issuer: origin,
            clientId: 'auriga-managed-network',
            providerId: 'managed',
          });
          phase = 'catalog';
          const catalog = await probe.wait(
            platform.request('/ai/v1/models', {
              token: value.credential,
              signal: probe.signal,
            }),
          );
          if (
            catalog?.object !== 'list' ||
            !Array.isArray(catalog.data) ||
            !catalog.data.some(
              (/** @type {any} */ m) =>
                value.models.some((v) => v.id === m.id) &&
                native.some((n) => n.id === m.id),
            )
          )
            throw new Error('authorized model unavailable');
          if (!closed)
            status = {
              registered: true,
              available: true,
              authorized: true,
              state: 'model_ready',
            };
          logEvent('INFO', 'Managed model status', {
            instance_id: binding.instanceId,
            phase: 'ready',
            credential_version: version,
            elapsed_ms: Math.floor(performance.now() - started),
          });
        } catch (error) {
          logEvent(
            'WARN',
            'Managed model status failed',
            {
              instance_id: binding.instanceId,
              phase,
              credential_version: version,
              elapsed_ms: Math.floor(performance.now() - started),
            },
            error,
          );
          if (!closed) status = unavailable('managed_model_unavailable');
        }
      })().finally(() => {
        probe.close();
        pending = undefined;
      });
      return pending;
    },
    get status() {
      return { ...status };
    },
    close() {
      closed = true;
      lifetime.abort();
      status = unavailable('stopped');
    },
  };
}
