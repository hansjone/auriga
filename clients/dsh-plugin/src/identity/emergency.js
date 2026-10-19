import { failResult } from './errors.js';
import {
  DUMMY_PASSWORD_HASH,
  parseAndValidatePasswordHash,
  same,
  verifyPassword,
  verifyTotp,
} from './crypto.js';

/**
 * Built-in emergency account connector (type=emergency).
 *
 * Config (already decrypted):
 * - username: string
 * - passwordHash: Argon2id PHC string (strength enforced)
 * - totpSecret: Base32 secret
 *
 * @param {{ now?: () => Date, onTotpAccepted?: (counter: number) => void|Promise<void> }} [deps]
 */
export function createEmergencyConnector(deps = {}) {
  const now = deps.now ?? (() => new Date());

  return {
    descriptor() {
      return {
        type: 'emergency',
        spiVersion: '1',
        displayName: { en: 'Emergency account', 'zh-CN': '应急账号' },
        steps: ['form'],
        configSchema: {
          type: 'object',
          additionalProperties: false,
          required: ['username', 'passwordHash', 'totpSecret'],
          properties: {
            username: { type: 'string', minLength: 1 },
            passwordHash: { type: 'string', minLength: 1, 'x-secret': true },
            totpSecret: { type: 'string', minLength: 1, 'x-secret': true },
          },
        },
        multipleInstances: false,
      };
    },

    /** @param {Record<string, unknown>} config */
    validateConfig(config) {
      if (
        typeof config.username !== 'string' ||
        !config.username ||
        typeof config.passwordHash !== 'string' ||
        typeof config.totpSecret !== 'string' ||
        !config.totpSecret
      )
        throw Object.assign(new Error('invalid emergency config'), {
          code: 'common.validation_failed',
        });
      parseAndValidatePasswordHash(config.passwordHash);
    },

    /** @param {Record<string, unknown>} _ctx @param {Record<string, unknown>} _config */
    begin(_ctx, _config) {
      return {
        step: {
          type: 'form',
          formId: 'emergency',
          title: { en: 'Emergency sign-in', 'zh-CN': '应急账号登录' },
          fields: [
            {
              name: 'username',
              kind: 'text',
              required: true,
              label: { en: 'Username', 'zh-CN': '用户名' },
              autocomplete: 'username',
            },
            {
              name: 'password',
              kind: 'password',
              required: true,
              label: { en: 'Password', 'zh-CN': '口令' },
              autocomplete: 'current-password',
            },
            {
              name: 'otp',
              kind: 'otp',
              required: true,
              label: { en: 'Authenticator code', 'zh-CN': '验证码' },
              autocomplete: 'one-time-code',
            },
          ],
          submitLabel: { en: 'Sign in', 'zh-CN': '登录' },
        },
      };
    },

    /**
     * @param {Record<string, unknown>} ctx
     * @param {{ username: string, passwordHash: string, totpSecret: string, lastCounter?: number }} config
     * @param {Record<string, unknown>} input
     */
    async advance(ctx, config, input) {
      if (input.type !== 'form' || input.formId !== 'emergency')
        return failResult('iam.invalid_credentials');
      const values = /** @type {Record<string, string|boolean>} */ (
        input.values ?? {}
      );
      const username = String(values.username ?? '');
      const password = String(values.password ?? '');
      const otp = String(values.otp ?? '');
      const state = /** @type {{ lastCounter?: number }|null} */ (ctx.state);
      const lastCounter =
        typeof config.lastCounter === 'number'
          ? config.lastCounter
          : (state?.lastCounter ?? -1);

      const usernameOk = same(username, config.username);
      // Always run Argon2 so wrong usernames do not short-circuit on timing.
      const passwordOk = verifyPassword(
        password,
        usernameOk ? config.passwordHash : DUMMY_PASSWORD_HASH,
      );
      if (!usernameOk || !passwordOk)
        return failResult('iam.invalid_credentials');

      const counter = verifyTotp(
        config.totpSecret,
        otp,
        now(),
        Number(lastCounter),
      );
      if (counter < 0) return failResult('iam.invalid_credentials');
      if (deps.onTotpAccepted) await deps.onTotpAccepted(counter);

      return {
        step: {
          type: 'done',
          profile: {
            subject: config.username,
            displayName: config.username,
            mfaPerformed: true,
            authTime: now().toISOString(),
          },
        },
        state: { lastCounter: counter },
      };
    },
  };
}

export const emergencyConnector = createEmergencyConnector();
