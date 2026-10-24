const RECORD_KEY = 'auriga-dsh/standalone-owner';
const INTERNAL_ERROR = 'owner admission unavailable';

/**
 * @typedef {{ providerId: string, subject: string }} Owner
 * @typedef {{ version: 1, owner: Owner, lastCounter: number }} OwnerState
 * @typedef {{
 *   providerId: string,
 *   profile: { subject: string, displayName?: string, email?: string, mfaPerformed?: boolean, authTime?: string },
 *   emergencyCounter?: number,
 * }} AdmissionInput
 */

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function object(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** @param {Record<string, unknown>} value @param {string[]} keys */
function exactKeys(value, keys) {
  const actual = Object.keys(value);
  return (
    actual.length === keys.length &&
    keys.every((key) => Object.hasOwn(value, key))
  );
}

/** @param {unknown} value @returns {value is Owner} */
function validOwner(value) {
  return (
    object(value) &&
    exactKeys(value, ['providerId', 'subject']) &&
    typeof value.providerId === 'string' &&
    value.providerId.trim().length > 0 &&
    typeof value.subject === 'string' &&
    value.subject.trim().length > 0
  );
}

/** @param {Owner} left @param {Owner} right */
function sameOwner(left, right) {
  return left.providerId === right.providerId && left.subject === right.subject;
}

/** @param {unknown} record @param {Owner} owner @returns {OwnerState} */
function stateOf(record, owner) {
  if (
    !object(record) ||
    !exactKeys(record, ['kind', 'payload']) ||
    record.kind !== 'grant'
  )
    throw new Error(INTERNAL_ERROR);
  const state = record.payload;
  if (
    !object(state) ||
    !exactKeys(state, ['version', 'owner', 'lastCounter']) ||
    state.version !== 1 ||
    !validOwner(state.owner) ||
    !sameOwner(state.owner, owner) ||
    typeof state.lastCounter !== 'number' ||
    !Number.isSafeInteger(state.lastCounter) ||
    state.lastCounter < -1
  )
    throw new Error(INTERNAL_ERROR);
  return {
    version: 1,
    owner: { ...state.owner },
    lastCounter: state.lastCounter,
  };
}

/**
 * Same-process Node prerequisite. The supplied DSH seam must hold its durable
 * cross-process read/modify/write lock until modifyRecord resolves. No secrets
 * belong in this record; this does not install an HTTP or native DSH gate.
 *
 * @param {{
 *   credentials: { modifyRecord: (key: string, mutate: (current: unknown) => Promise<{ kind: 'grant', payload: OwnerState }|undefined>) => Promise<unknown> },
 *   owner: Owner,
 *   initialize?: boolean,
 * }} options
 */
export async function createOwnerAdmission({
  credentials,
  owner,
  initialize = false,
}) {
  if (
    !validOwner(owner) ||
    typeof credentials?.modifyRecord !== 'function' ||
    typeof initialize !== 'boolean'
  )
    throw new Error(INTERNAL_ERROR);
  const fixedOwner = { ...owner };

  // Catch the complete provider operation, including failures after the write.
  // Never attach a cause: provider errors may contain private paths or payloads.
  /** @param {(current: unknown) => Promise<{ kind: 'grant', payload: OwnerState }|undefined>} mutate */
  async function modify(mutate) {
    try {
      await credentials.modifyRecord(RECORD_KEY, mutate);
    } catch {
      throw new Error(INTERNAL_ERROR);
    }
  }

  await modify(async (current) => {
    if (current === undefined && initialize)
      return {
        kind: 'grant',
        payload: { version: 1, owner: { ...fixedOwner }, lastCounter: -1 },
      };
    stateOf(current, fixedOwner);
    return undefined;
  });

  return {
    async readState() {
      /** @type {OwnerState|undefined} */
      let state;
      await modify(async (current) => {
        state = stateOf(current, fixedOwner);
        return undefined;
      });
      if (!state) throw new Error(INTERNAL_ERROR);
      return state;
    },
    /** @param {AdmissionInput} input */
    async commitAdmission(input) {
      const counter = input?.emergencyCounter;
      if (
        input?.providerId !== fixedOwner.providerId ||
        input?.profile?.subject !== fixedOwner.subject ||
        input?.profile?.mfaPerformed !== true ||
        typeof counter !== 'number' ||
        !Number.isSafeInteger(counter) ||
        counter < 0
      )
        return false;
      let accepted = false;
      await modify(async (current) => {
        const state = stateOf(current, fixedOwner);
        if (counter <= state.lastCounter) return undefined;
        accepted = true;
        return { kind: 'grant', payload: { ...state, lastCounter: counter } };
      });
      return accepted;
    },
  };
}
