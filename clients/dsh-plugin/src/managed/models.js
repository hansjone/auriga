import { createPlatform } from '../local/platform.js';
import { modelDirectory } from '../ai/catalog.js';
import { operation } from '../ai/transport.js';
import { check } from '../ai/guard.js';
import { logEvent } from '../observability/index.js';
import { performance } from 'node:perf_hooks';
/* global AbortSignal */

const NS = 'llm-pi-ai';
const PROVIDER = 'auriga-managed';
const PREFERENCE_FIELDS = [
  'name',
  'contextWindow',
  'maxTokens',
  'input',
  'reasoningEfforts',
];
const conflict = (/** @type {any} */ error) =>
  ['SETTINGS_CONFLICT', 'settings/conflict'].includes(error?.code);
const same = (/** @type {any} */ a, /** @type {any} */ b) =>
  JSON.stringify(a) === JSON.stringify(b);

/** Authenticated platform facts remain separate from native user preferences.
 * No instance material is rewritten and no adapter is unregistered.
 * @param {{loadMaterial:()=>Promise<any>, requestCatalog?:(material:any,signal:AbortSignal)=>Promise<any>,intervalMs?:number,timeoutMs?:number}} options */
export function createManagedModels({
  loadMaterial,
  requestCatalog,
  intervalMs = 30000,
  timeoutMs = 4000,
}) {
  let closed = false,
    epoch = 0;
  /** @type {any} */
  let native;
  /** @type {any} */
  let lastMaterial;
  /** @type {Promise<any>|undefined} */
  let requestPending;
  /** @type {Promise<any>|undefined} */
  let refreshPending;
  /** @type {(() => void)|undefined} */
  let detachNative;
  /** @type {ReturnType<typeof setTimeout>|undefined} */
  let timer;
  let lifetime = new AbortController();
  /** @type {Map<string,any>|undefined} */
  let previous;
  /** Last effective native projection, distinct from requested user preferences.
   * Platform clamping must never become a new user preference.
   * @type {Map<string,any>|undefined} */
  let projected;
  /** @type {Map<string,any>} */
  let preferences = new Map();
  let excluded = new Set();
  let status = {
    registered: false,
    available: false,
    authorized: false,
    directoryAvailable: false,
    stale: false,
    state: 'managed_model_unavailable',
    lastSuccessAt: /** @type {number|null} */ (null),
  };

  function stopTimer() {
    clearTimeout(timer);
    timer = undefined;
  }
  function schedule() {
    stopTimer();
    if (!closed && native) {
      timer = setTimeout(() => {
        void refresh();
      }, intervalMs);
      timer.unref?.();
    }
  }
  /** @param {AbortSignal} [caller] */
  function read(caller) {
    if (closed) return Promise.reject(new Error('stopped'));
    if (requestPending) return requestPending;
    const probe = operation(
      AbortSignal.any([
        lifetime.signal,
        AbortSignal.timeout(timeoutMs),
        ...(caller ? [caller] : []),
      ]),
    );
    const pending = (async () => {
      const material = await probe.wait(loadMaterial());
      lastMaterial = material;
      const platform = createPlatform({
        platformOrigin: material.origin,
        issuer: material.origin,
        clientId: 'auriga-managed-network',
        providerId: 'managed',
      });
      const wire = await probe.wait(
        requestCatalog
          ? requestCatalog(material, probe.signal)
          : platform.request('/ai/v1/models', {
              token: material.value.credential,
              signal: probe.signal,
            }),
      );
      const directory = modelDirectory(wire);
      check(directory.length <= 256, 'SERVER');
      const models = directory.map((item) => {
        check(
          item.context_window !== undefined &&
            item.max_output_tokens !== undefined,
          'SERVER',
        );
        return {
          id: item.id,
          name: item.id,
          contextWindow: item.context_window,
          maxTokens: item.max_output_tokens,
        };
      });
      return { material, models };
    })().finally(() => {
      probe.close();
      if (requestPending === pending) requestPending = undefined;
    });
    requestPending = pending;
    return pending;
  }

  /** @param {any[]} remote @param {any[]} current */
  function merge(remote, current) {
    const byId = new Map(current.map((model) => [model.id, model]));
    const remoteById = new Map(remote.map((model) => [model.id, model]));
    const requested = new Map(globalThis.structuredClone([...preferences]));
    const hidden = new Set(excluded);
    for (const id of projected?.keys() ?? []) {
      if (!byId.has(id)) hidden.add(id);
    }
    for (const user of current) {
      hidden.delete(user.id);
      const baseline = projected?.get(user.id) ??
        previous?.get(user.id) ??
        remoteById.get(user.id) ?? { id: user.id, name: user.id };
      const preference = requested.get(user.id) ?? {};
      for (const field of PREFERENCE_FIELDS) {
        if (same(user[field], baseline[field])) continue;
        if (user[field] === undefined) delete preference[field];
        else preference[field] = globalThis.structuredClone(user[field]);
      }
      if (Object.keys(preference).length) requested.set(user.id, preference);
      else requested.delete(user.id);
    }
    const models = remote
      .filter((model) => !hidden.has(model.id))
      .map((model) => {
        const preference = requested.get(model.id) ?? {};
        const result = { ...model, ...preference, id: model.id };
        for (const field of ['contextWindow', 'maxTokens']) {
          result[field] =
            Number.isSafeInteger(preference[field]) && preference[field] > 0
              ? Math.min(preference[field], model[field])
              : model[field];
        }
        result.maxTokens = Math.min(result.maxTokens, result.contextWindow - 1);
        return result;
      });
    return { models, preferences: requested, excluded: hidden };
  }

  /** @param {any} port @param {any[]} models @param {any} material @param {number} generation @param {AbortSignal} signal */
  async function publish(port, models, material, generation, signal) {
    if (!previous) {
      previous = new Map(
        material.value.models.map((/** @type {any} */ model) => [
          model.id,
          {
            id: model.id,
            name: model.id,
            contextWindow: model.contextWindow,
            maxTokens: model.maxOutputTokens,
          },
        ]),
      );
      projected = new Map(previous);
    }
    /** Candidate preference state commits only with the successful CAS projection.
     * @type {ReturnType<typeof merge>|undefined} */
    let candidate;
    for (let attempt = 0; attempt < 2; attempt++) {
      if (closed || signal.aborted || generation !== epoch) return;
      const descriptor = port.settings
        .describe({ redactSecrets: true })
        .find((/** @type {any} */ item) => item.ns === NS);
      const current = descriptor?.value?.providers?.[PROVIDER]?.models;
      check(Array.isArray(current), 'NO_ADAPTER');
      candidate = merge(models, current);
      const next = candidate.models;
      if (same(next, current)) break;
      try {
        await port.settings.mutate(
          NS,
          [{ op: 'set', path: ['providers', PROVIDER, 'models'], value: next }],
          descriptor.revision,
        );
        break;
      } catch (error) {
        if (attempt || !conflict(error)) throw error;
      }
    }
    if (closed || signal.aborted || generation !== epoch) return;
    if (candidate) {
      preferences = candidate.preferences;
      excluded = candidate.excluded;
      projected = new Map(
        globalThis
          .structuredClone(candidate.models)
          .map((model) => [model.id, model]),
      );
    }
    previous = new Map(models.map((model) => [model.id, model]));
    // Never replace a saved personal/global choice, including a legacy pool.
    const descriptors = port.settings.describe({ redactSecrets: true });
    const selection = descriptors.find(
      (/** @type {any} */ item) => item.ns === 'agent-default-model',
    );
    const live = descriptors.find((/** @type {any} */ item) => item.ns === NS)
      ?.value?.providers?.[PROVIDER]?.models;
    if (
      selection &&
      !Object.keys(selection.user ?? {}).length &&
      live?.some((/** @type {any} */ model) => model.id === 'auto')
    ) {
      try {
        await port.settings.mutate(
          'agent-default-model',
          [
            { op: 'set', path: ['provider'], value: PROVIDER },
            { op: 'set', path: ['model'], value: 'auto' },
            { op: 'unset', path: ['reasoningEffort'] },
          ],
          selection.revision,
        );
      } catch (error) {
        if (!conflict(error)) throw error;
      }
    }
  }

  /** @param {any} service */
  async function nativeModels(service) {
    const probe = operation(
      AbortSignal.any([lifetime.signal, AbortSignal.timeout(timeoutMs)]),
    );
    try {
      return await probe.wait(
        Promise.resolve().then(() => service.listModels(PROVIDER)),
      );
    } finally {
      probe.close();
    }
  }

  /** Status reads may request a check before the optional settings seam binds.
   * @param {any} [service] */
  function refresh(service = native?.llm) {
    if (closed) return Promise.resolve();
    if (refreshPending) return refreshPending;
    const generation = epoch,
      port = native;
    const pending = (async () => {
      const started = performance.now();
      const probe = operation(
        AbortSignal.any([lifetime.signal, AbortSignal.timeout(timeoutMs)]),
      );
      let phase = 'native';
      try {
        check(typeof service?.listModels === 'function', 'NO_ADAPTER');
        const existing = port
          ? undefined
          : await probe.wait(nativeModels(service));
        phase = 'catalog';
        const { material, models } = await probe.wait(read());
        if (closed || generation !== epoch) return;
        if (port)
          await probe.wait(
            publish(port, models, material, generation, probe.signal),
          );
        if (closed || generation !== epoch) return;
        phase = 'native';
        const list = port ? await probe.wait(nativeModels(service)) : existing;
        if (closed || generation !== epoch) return;
        const available =
          Array.isArray(list) &&
          list.some((entry) =>
            models.some((/** @type {any} */ model) => model.id === entry.id),
          );
        status = {
          registered: Array.isArray(list),
          available,
          authorized: models.length > 0,
          directoryAvailable: true,
          stale: false,
          state: available ? 'model_ready' : 'managed_model_unavailable',
          lastSuccessAt: Date.now(),
        };
      } catch (error) {
        if (closed || generation !== epoch) return;
        logEvent(
          'WARN',
          'Managed model status failed',
          {
            instance_id: lastMaterial?.value.instanceId ?? null,
            credential_version: lastMaterial?.value.credentialVersion ?? null,
            phase,
            elapsed_ms: Math.floor(performance.now() - started),
          },
          error,
        );
        const denied = [401, 403].includes(/** @type {any} */ (error)?.status);
        status = {
          ...status,
          available: false,
          authorized: false,
          directoryAvailable: false,
          stale: status.lastSuccessAt !== null,
          state: denied
            ? 'managed_model_forbidden'
            : 'managed_model_unavailable',
        };
      } finally {
        probe.close();
      }
    })().finally(() => {
      if (refreshPending === pending) {
        refreshPending = undefined;
        schedule();
      }
    });
    refreshPending = pending;
    return pending;
  }

  return {
    refresh,
    /** All host operations stay on documented public service methods.
     * @param {any} llm @param {any} settings */
    attach(llm, settings) {
      if (closed) return () => {};
      detachNative?.();
      const generation = ++epoch;
      native = { llm, settings };
      const original = llm.discoverModels;
      /** @param {string} ns @param {any} request @param {AbortSignal} [signal] */
      const discover = async (ns, request, signal) => {
        if (ns !== NS || request?.provider !== PROVIDER)
          return original.call(llm, ns, request, signal);
        const probe = operation(
          AbortSignal.any([
            lifetime.signal,
            ...(signal ? [signal] : []),
            AbortSignal.timeout(timeoutMs),
          ]),
        );
        try {
          const material = await probe.wait(loadMaterial());
          if (
            (request.baseURL !== undefined &&
              request.baseURL !== material.value.baseUrl) ||
            (request.api !== undefined &&
              request.api !== 'openai-completions') ||
            request.apiKey !== undefined
          ) {
            throw Object.assign(
              new Error(
                'Auriga managed model connection is supplied by the platform.',
              ),
              { code: 'common.forbidden' },
            );
          }
          return (await probe.wait(read(signal))).models;
        } finally {
          probe.close();
        }
      };
      if (typeof original === 'function') llm.discoverModels = discover;
      void refresh();
      detachNative = () => {
        if (generation !== epoch) return;
        epoch++;
        native = undefined;
        stopTimer();
        lifetime.abort();
        lifetime = new AbortController();
        requestPending = refreshPending = undefined;
        status = {
          ...status,
          registered: false,
          available: false,
          authorized: false,
          directoryAvailable: false,
          state: 'managed_model_unavailable',
        };
        if (llm.discoverModels === discover) llm.discoverModels = original;
      };
      return detachNative;
    },
    get status() {
      return { ...status };
    },
    close() {
      if (closed) return;
      detachNative?.();
      closed = true;
      epoch++;
      stopTimer();
      lifetime.abort();
      status = {
        ...status,
        registered: false,
        available: false,
        authorized: false,
        directoryAvailable: false,
        state: 'stopped',
      };
    },
  };
}
