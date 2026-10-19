import { createEmergencyConnector } from './emergency.js';
import { createOidcConnector } from './oidc.js';

const SPI_VERSION = '1';

/**
 * @typedef {{
 *   descriptor: () => Record<string, unknown>,
 *   validateConfig: (config: Record<string, unknown>) => void | Promise<void>,
 *   begin: (ctx: Record<string, unknown>, config: Record<string, unknown>) =>
 *     | Record<string, unknown> | Promise<Record<string, unknown>>,
 *   advance: (ctx: Record<string, unknown>, config: Record<string, unknown>, input: Record<string, unknown>) =>
 *     | Record<string, unknown> | Promise<Record<string, unknown>>,
 * }} IdentityConnector
 */

/**
 * @param {{
 *   allowLocalHttp?: boolean,
 *   fetchImpl?: typeof fetch,
 *   lookupAll?: (hostname: string) => Promise<Array<{ address: string, family: number }>>,
 *   now?: () => Date,
 *   importModule?: (specifier: string) => Promise<unknown>,
 * }} [deps]
 */
export function createBuiltinConnectors(deps = {}) {
  /** @type {Map<string, IdentityConnector>} */
  const map = new Map();
  map.set(
    'emergency',
    /** @type {IdentityConnector} */ (
      /** @type {unknown} */ (createEmergencyConnector(deps))
    ),
  );
  map.set(
    'oidc',
    /** @type {IdentityConnector} */ (
      /** @type {unknown} */ (createOidcConnector(deps))
    ),
  );
  return map;
}

/**
 * Load connectors from standalone config.
 *
 * @param {{
 *   connectors?: Array<{
 *     type: string,
 *     id?: string,
 *     module?: string,
 *     config?: Record<string, unknown>,
 *   }>,
 * }} config
 * @param {{
 *   allowLocalHttp?: boolean,
 *   fetchImpl?: typeof fetch,
 *   lookupAll?: (hostname: string) => Promise<Array<{ address: string, family: number }>>,
 *   now?: () => Date,
 *   importModule?: (specifier: string) => Promise<unknown>,
 *   builtins?: Map<string, IdentityConnector>,
 * }} [deps]
 */
export async function loadConnectors(config, deps = {}) {
  const builtins = deps.builtins ?? createBuiltinConnectors(deps);
  const importModule = deps.importModule ?? ((s) => import(s));
  /** @type {Map<string, { id: string, type: string, connector: IdentityConnector, config: Record<string, unknown> }>} */
  const providers = new Map();
  const entries = Array.isArray(config.connectors) ? config.connectors : [];

  // Always ensure emergency is available if listed; oidc/custom only when configured.
  for (const entry of entries) {
    if (!entry || typeof entry.type !== 'string' || !entry.type)
      throw Object.assign(new Error('connector type required'), {
        code: 'common.validation_failed',
      });
    let connector = builtins.get(entry.type);
    if (!connector) {
      if (!entry.module)
        throw Object.assign(
          new Error(`unknown connector type: ${entry.type}`),
          {
            code: 'common.validation_failed',
          },
        );
      const mod =
        /** @type {{ default?: IdentityConnector, createConnector?: Function, connector?: IdentityConnector }} */ (
          await importModule(entry.module)
        );
      connector =
        mod.default ??
        mod.connector ??
        (typeof mod.createConnector === 'function'
          ? mod.createConnector()
          : null);
      if (!connector || typeof connector.descriptor !== 'function')
        throw Object.assign(
          new Error(`invalid connector module: ${entry.module}`),
          {
            code: 'common.validation_failed',
          },
        );
    }
    const descriptor = connector.descriptor();
    if (descriptor.spiVersion !== SPI_VERSION)
      throw Object.assign(
        new Error(`incompatible spiVersion for ${entry.type}`),
        {
          code: 'common.validation_failed',
        },
      );
    if (descriptor.type !== entry.type)
      throw Object.assign(
        new Error(`connector type mismatch for ${entry.type}`),
        {
          code: 'common.validation_failed',
        },
      );
    const cfg = entry.config ?? {};
    await connector.validateConfig(cfg);
    const id = entry.id ?? entry.type;
    if (providers.has(id))
      throw Object.assign(new Error(`duplicate provider id: ${id}`), {
        code: 'common.validation_failed',
      });
    providers.set(id, { id, type: entry.type, connector, config: cfg });
  }

  return {
    providers,
    builtins,
    /** @param {string} id */
    get(id) {
      return providers.get(id);
    },
    list() {
      return [...providers.values()].map((p) => ({
        id: p.id,
        type: p.type,
        descriptor: p.connector.descriptor(),
      }));
    },
  };
}
