import { createPlatform } from '../local/platform.js';
import { modelDirectory } from '../ai/catalog.js';
import { operation } from '../ai/transport.js';
import { check } from '../ai/guard.js';
import { logEvent } from '../observability/index.js';

/* global AbortSignal */
export const MANAGED_MODEL_REF = 'AURIGA_MANAGED_MODEL';
const NS = 'llm-pi-ai';
const PROVIDER = 'auriga-managed';
const LEGACY_AUTH =
  "'Bearer ' + JSON.parse(process.getBuiltinModule('node:fs').readFileSync(\"/run/auriga/model-credential.json\", 'utf8')).credential";
const owners = new WeakMap();
const raw = (/** @type {any} */ service) =>
  service?.[Symbol.for('cordis.original')] ?? service;
const forbidden = () =>
  Object.assign(
    new Error('Auriga managed model connection is supplied by the platform.'),
    {
      code: 'common.forbidden',
    },
  );

/** Native discovery returns candidates; only the native editor adopts/saves them.
 * The reserved reference is resolved by the owning pi-ai server fiber, never stored.
 * @param {{loadMaterial:()=>Promise<any>,requestCatalog?:(material:any,signal:AbortSignal)=>Promise<any>,timeoutMs?:number}} options */
export function createManagedModels({
  loadMaterial,
  requestCatalog,
  timeoutMs = 4000,
}) {
  let closed = false;
  /** @type {(() => void)|undefined} */
  let detach;
  /** @type {Promise<void>} */
  let ready = Promise.resolve();

  return {
    get ready() {
      return ready;
    },
    /** @param {any} llm @param {any} settings @param {any} editor @param {any} credentials */
    attach(llm, settings, editor, credentials) {
      if (closed) throw forbidden();
      detach?.();
      const target = raw(llm),
        keys = raw(credentials);
      // Rebinding owns fresh effects; an old disposer can only release its owner.
      owners.get(target)?.();
      owners.get(keys)?.();
      const lifetime = new AbortController();
      let active = true;
      const alive = () => {
        if (!active || closed || lifetime.signal.aborted) throw forbidden();
      };
      const descriptor = () =>
        settings
          .describe({ redactSecrets: true })
          .find((/** @type {any} */ row) => row.ns === NS);
      /** @param {any} material */
      const route = (material) => {
        const providers = descriptor()?.value?.providers;
        const managed = providers?.[PROVIDER];
        if (
          !managed ||
          managed.apiKeyEnv !== MANAGED_MODEL_REF ||
          managed.baseURL !== material.value.baseUrl ||
          managed.api !== 'openai-completions' ||
          Object.keys(managed.headers ?? {}).length > 0 ||
          managed.compat?.supportsStore !== false ||
          Object.entries(providers).some(
            ([id, value]) =>
              id !== PROVIDER &&
              /** @type {any} */ (value)?.apiKeyEnv === MANAGED_MODEL_REF,
          )
        )
          throw forbidden();
      };
      /** @param {AbortSignal} [signal] */
      const probe = (signal) =>
        operation(
          AbortSignal.any([
            lifetime.signal,
            AbortSignal.timeout(timeoutMs),
            ...(signal ? [signal] : []),
          ]),
        );
      /** @param {any} port */
      async function initialize(port) {
        const op = probe();
        try {
          const material = await op.wait(loadMaterial());
          alive();
          const entry = port
            .configuration()
            .find(
              (/** @type {any} */ row) => row.entry.options.id === NS,
            )?.entry;
          check(entry, 'NO_ADAPTER');
          const config = entry.options.config?.providers?.[PROVIDER];
          const authorization = config?.headers?.Authorization;
          const legacy =
            authorization &&
            typeof authorization === 'object' &&
            Object.keys(config.headers).length === 1 &&
            Object.keys(authorization).length === 1 &&
            authorization.__jsExpr === LEGACY_AUTH;
          if (!config || legacy || (!config.apiKeyEnv && !config.headers)) {
            await op.wait(
              port.edit(entry, (/** @type {any} */ current) => {
                alive();
                const next = globalThis.structuredClone(current);
                const provider = next.providers?.[PROVIDER] ?? {};
                if (
                  provider.headers &&
                  provider.headers.Authorization?.__jsExpr !== LEGACY_AUTH
                )
                  throw forbidden();
                const safe = { ...provider };
                delete safe.headers;
                next.providers = {
                  ...next.providers,
                  [PROVIDER]: {
                    ...safe,
                    displayName: provider.displayName ?? 'Auriga',
                    api: 'openai-completions',
                    baseURL: material.value.baseUrl,
                    apiKeyEnv: MANAGED_MODEL_REF,
                    compat: { ...provider.compat, supportsStore: false },
                    models:
                      provider.models ??
                      material.value.models.map((/** @type {any} */ model) => ({
                        id: model.id,
                        name: model.id,
                        contextWindow: model.contextWindow,
                        maxTokens: model.maxOutputTokens,
                      })),
                  },
                };
                return next;
              }),
            );
          }
          alive();
          route(material);
          const selection = settings
            .describe({ redactSecrets: true })
            .find((/** @type {any} */ row) => row.ns === 'agent-default-model');
          if (
            selection &&
            !Object.keys(selection.user ?? {}).length &&
            descriptor()?.value?.providers?.[PROVIDER]?.models?.some(
              (/** @type {any} */ model) => model.id === 'auto',
            )
          ) {
            try {
              await op.wait(
                settings.mutate(
                  'agent-default-model',
                  [
                    { op: 'set', path: ['provider'], value: PROVIDER },
                    { op: 'set', path: ['model'], value: 'auto' },
                    { op: 'unset', path: ['reasoningEffort'] },
                  ],
                  selection.revision,
                ),
              );
            } catch (error) {
              if (
                !['SETTINGS_CONFLICT', 'settings/conflict'].includes(
                  /** @type {any} */ (error)?.code,
                )
              )
                throw error;
            }
          }
        } finally {
          op.close();
        }
      }
      let prepared = false;
      /** @type {Promise<void>|undefined} */
      let preparing;
      const prepare = () => {
        alive();
        if (prepared) return Promise.resolve();
        if (preparing) return preparing;
        const pending = initialize(editor)
          .then(() => {
            alive();
            prepared = true;
          })
          .finally(() => {
            if (preparing === pending) preparing = undefined;
          });
        preparing = ready = pending;
        return pending;
      };
      const original = target.discoverModels;
      const resolve = keys.resolve,
        describe = keys.describe;
      check(
        typeof original === 'function' &&
          typeof resolve === 'function' &&
          typeof describe === 'function',
        'NO_ADAPTER',
      );
      /** @this {any} @param {string} ns @param {any} request @param {AbortSignal} [signal] */
      async function discover(ns, request, signal) {
        if (ns !== NS || request?.provider !== PROVIDER)
          return original.call(this, ns, request, signal);
        const op = probe(signal);
        try {
          await op.wait(prepare());
          const material = await op.wait(loadMaterial());
          alive();
          route(material);
          if (
            (request.baseURL !== undefined &&
              request.baseURL !== material.value.baseUrl) ||
            (request.api !== undefined &&
              request.api !== 'openai-completions') ||
            request.apiKey !== undefined
          )
            throw forbidden();
          const platform = createPlatform({
            platformOrigin: material.origin,
            issuer: material.origin,
            clientId: 'auriga-managed-network',
            providerId: 'managed',
          });
          const wire = await op.wait(
            requestCatalog
              ? requestCatalog(material, op.signal)
              : platform.request('/ai/v1/models', {
                  token: material.value.credential,
                  signal: op.signal,
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
          alive();
          return models;
        } finally {
          op.close();
        }
      }
      /** @this {any} @param {string} ref */
      async function resolveManaged(ref) {
        if (ref !== MANAGED_MODEL_REF) return resolve.call(this, ref);
        if (this?.ctx?.fiber?.entry?.options?.id !== NS) throw forbidden();
        const op = probe();
        try {
          const material = await op.wait(loadMaterial());
          alive();
          route(material);
          return { value: material.value.credential, source: 'auriga-managed' };
        } finally {
          op.close();
        }
      }
      /** @this {any} @param {string} ref */
      async function describeManaged(ref) {
        if (ref !== MANAGED_MODEL_REF) return describe.call(this, ref);
        const op = probe();
        try {
          const material = await op.wait(loadMaterial());
          alive();
          route(material);
          return {
            configured: true,
            writable: false,
            source: 'auriga-managed',
          };
        } catch {
          return { configured: false, writable: false };
        } finally {
          op.close();
        }
      }
      target.discoverModels = discover;
      keys.resolve = resolveManaged;
      keys.describe = describeManaged;
      const cleanup = () => {
        if (!active) return;
        active = false;
        lifetime.abort();
        if (target.discoverModels === discover)
          target.discoverModels = original;
        if (keys.resolve === resolveManaged) keys.resolve = resolve;
        if (keys.describe === describeManaged) keys.describe = describe;
        if (owners.get(target) === cleanup) owners.delete(target);
        if (owners.get(keys) === cleanup) owners.delete(keys);
      };
      owners.set(target, cleanup);
      owners.set(keys, cleanup);
      detach = cleanup;
      ready = prepare();
      // Discovery still awaits the original failure; startup also records it.
      void ready.catch((error) => {
        logEvent(
          'WARN',
          'Managed model initialization failed',
          { phase: 'native_config' },
          error,
        );
      });
      return cleanup;
    },
    close() {
      if (closed) return;
      closed = true;
      detach?.();
    },
  };
}
