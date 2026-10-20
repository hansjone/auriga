// @ts-nocheck
/**
 * Stable team SDK surface for other DSH plugins: ctx.get('auriga').
 * Prototype name ctx.get('dshTeam') is not provided.
 */

export const SDK_VERSION = 1;

/**
 * @param {{
 *   getIdentity?: () => { uid: string, displayName?: string, role?: string } | null,
 *   notify?: (message: { level?: string, title?: string, body?: string }) => Promise<void> | void,
 *   kb?: { query: (...args: unknown[]) => Promise<unknown> },
 *   files?: {
 *     list: (path?: string) => Promise<unknown>,
 *     read: (path: string) => Promise<Buffer>,
 *     write: (path: string, data: unknown, opts?: object) => Promise<unknown>,
 *     usage: () => Promise<unknown>,
 *   },
 * }} [opts]
 */
export function createAurigaSdk(opts = {}) {
  const kb = opts.kb ?? {
    async query() {
      throw Object.assign(new Error('kb unavailable'), {
        code: 'common.unavailable',
      });
    },
  };

  const files = opts.files ?? {
    async list() {
      throw Object.assign(new Error('files unavailable'), {
        code: 'common.unavailable',
      });
    },
    async read() {
      throw Object.assign(new Error('files unavailable'), {
        code: 'common.unavailable',
      });
    },
    async write() {
      throw Object.assign(new Error('files unavailable'), {
        code: 'common.unavailable',
      });
    },
    async usage() {
      throw Object.assign(new Error('files unavailable'), {
        code: 'common.unavailable',
      });
    },
  };

  return {
    version: SDK_VERSION,
    identity() {
      const id = opts.getIdentity?.() || null;
      if (!id) {
        throw Object.assign(new Error('not authenticated'), {
          code: 'common.unauthenticated',
        });
      }
      return {
        uid: id.uid,
        displayName: id.displayName || id.uid,
        role: id.role || 'member',
      };
    },
    kb,
    async notify(message) {
      if (typeof opts.notify !== 'function')
        return { ok: false, skipped: true };
      await opts.notify(message || {});
      return { ok: true };
    },
    files,
  };
}

/**
 * @param {object} ctx
 * @param {Parameters<typeof createAurigaSdk>[0]} [opts]
 */
export function provideAurigaSdk(ctx, opts = {}) {
  const sdk = createAurigaSdk(opts);
  ctx.provide?.('auriga', sdk);
  return sdk;
}
