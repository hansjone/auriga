import { randomBytes } from 'node:crypto';
import { createStandaloneCore } from '../identity/core.js';
import { enrollEmergency } from '../modes/standalone.js';

/** Local credentials and the accepted TOTP counter share the encrypted vault.
 * Passwords are returned only by an explicit native-owner reset, never saved.
 * @param {{vault:any,secure:boolean}} options */
export function createLocalEmergency({ vault, secure }) {
  let generation = 0;
  /** @type {Promise<ReturnType<typeof createStandaloneCore> extends Promise<infer T> ? T : never>|undefined} */
  let pending;
  const core = () => {
    if (pending) return pending;
    const saved = vault.snapshot().emergency;
    if (!saved) throw new Error('local emergency account not enrolled');
    const g = generation;
    pending = createStandaloneCore({
      config: {
        secure,
        sameSite: 'Strict',
        connectors: [{ type: 'emergency', config: saved.config }],
        owner: { providers: ['emergency'], subject: saved.config.username },
      },
      async commitAdmission(input) {
        let accepted = false;
        await vault.update((/** @type {any} */ state) => {
          if (g !== generation || state.emergency?.id !== saved.id) return;
          const counter = input.emergencyCounter;
          if (
            typeof counter !== 'number' ||
            counter <= state.emergency.lastCounter
          )
            return;
          state.emergency.lastCounter = counter;
          accepted = true;
        });
        return accepted;
      },
    });
    return pending;
  };
  return {
    available: () => !!vault.snapshot().emergency,
    async reset() {
      const password = randomBytes(24).toString('base64url');
      const config = enrollEmergency('owner', password);
      await vault.update((/** @type {any} */ state) => {
        state.emergency = {
          id: randomBytes(16).toString('hex'),
          config,
          lastCounter: -1,
        };
      });
      generation++;
      pending = undefined;
      return {
        username: config.username,
        password,
        totpSecret: config.totpSecret,
      };
    },
    async begin(/** @type {string} */ clientIp) {
      return (await core()).beginLogin('emergency', { clientIp });
    },
    async advance(
      /** @type {string} */ txId,
      /** @type {any} */ input,
      /** @type {string} */ clientIp,
    ) {
      return (await core()).advanceLogin(txId, input, { clientIp });
    },
  };
}
