import {
  createHash,
  createPrivateKey,
  createPublicKey,
  sign,
} from 'node:crypto';
import { Buffer } from 'node:buffer';
import { constants, lstat, open } from 'node:fs/promises';
import { dirname, isAbsolute, join, parse } from 'node:path';
import { createFileLedgerStore } from '../host/store.js';
import { openV2TrustStore } from './v2-store.js';
import { detach, jcs, reject, structure } from './v2.js';

/** Installation files are selected only by the process/renderer. Neither a
 * control package nor a settings request can select a key, pin or directory.
 * @param {string} file @param {number} limit */
async function privateFile(file, limit) {
  if (!isAbsolute(file)) reject('scope_denied');
  for (let path = file; ; path = dirname(path)) {
    const stat = await lstat(path);
    if (stat.isSymbolicLink()) reject('scope_denied');
    if (path === parse(path).root) break;
  }
  const parent = await lstat(dirname(file));
  if (process.platform !== 'win32' && parent.mode & 0o077)
    reject('scope_denied');
  const before = await lstat(file);
  const handle = await open(
    file,
    constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
  );
  try {
    const stat = await handle.stat();
    if (
      !stat.isFile() ||
      stat.nlink !== 1 ||
      stat.ino !== before.ino ||
      stat.dev !== before.dev ||
      stat.size < 1 ||
      stat.size > limit ||
      (process.platform !== 'win32' && stat.mode & 0o077)
    )
      reject('scope_denied');
    const bytes = Buffer.alloc(stat.size + 1);
    const { bytesRead } = await handle.read(bytes, 0, bytes.length, 0);
    if (bytesRead !== stat.size) reject('scope_denied');
    return bytes.subarray(0, bytesRead);
  } finally {
    await handle.close();
  }
}

/** @param {string} encoded @param {'ed25519'|'x25519'} algorithm */
function canonicalPublic(encoded, algorithm) {
  const bytes = Buffer.from(encoded, 'base64url');
  const key = createPublicKey({ key: bytes, format: 'der', type: 'spki' });
  if (
    key.asymmetricKeyType !== algorithm ||
    bytes.toString('base64url') !== encoded ||
    key.export({ format: 'der', type: 'spki' }).toString('base64url') !==
      encoded
  )
    reject('scope_denied');
  return key;
}

/** Uses K's seven-field pin without inventing an enrollment identity.
 * @param {any} pinsInput @param {string|undefined} target @param {Buffer} privateDer @param {any} [expected] */
export function installationIdentity(pinsInput, target, privateDer, expected) {
  /** @type {import('../../generated/network/auto-renewal-v2.js').InstallationPins} */
  const pins = structure('InstallationPins', detach(pinsInput));
  const ids = new Set(),
    targets = new Set();
  for (const pin of pins.pins) {
    if (ids.has(pin.identity_id) || targets.has(pin.target_id))
      reject('scope_denied');
    ids.add(pin.identity_id);
    targets.add(pin.target_id);
    canonicalPublic(pin.signing_spki, 'ed25519');
    canonicalPublic(pin.sealing_spki, 'x25519');
  }
  const selected =
    target ??
    expected?.target_id ??
    (pins.pins.length === 1 ? pins.pins[0].target_id : undefined);
  const pin = pins.pins.find((p) => p.target_id === selected);
  if (
    !pin ||
    (expected &&
      (expected.tenant_id !== pins.tenant_id ||
        expected.realm !== pins.realm ||
        expected.target_id !== pin.target_id))
  )
    reject('scope_denied');
  const key = createPrivateKey({
    key: privateDer,
    format: 'der',
    type: 'pkcs8',
  });
  if (
    key.asymmetricKeyType !== 'ed25519' ||
    !key.export({ format: 'der', type: 'pkcs8' }).equals(privateDer) ||
    createPublicKey(key)
      .export({ format: 'der', type: 'spki' })
      .toString('base64url') !== pin.signing_spki
  )
    reject('scope_denied');
  const scope = Object.freeze({
    tenant_id: pins.tenant_id,
    realm: pins.realm,
    target_id: pin.target_id,
  });
  return {
    scope,
    /** @type {import('../runtime-types.js').ControlIdentity} */
    controlIdentity: {
      target_id: pin.target_id,
      identity_id: pin.identity_id,
      async sign(bytes) {
        const prefix = Buffer.from('Auriga.NetworkControlReceipt.v2\0');
        const input = Buffer.from(bytes);
        if (!input.subarray(0, prefix.length).equals(prefix))
          reject('scope_denied');
        const body = detach(input.subarray(prefix.length));
        if (
          body.tenant_id !== scope.tenant_id ||
          body.realm !== scope.realm ||
          body.target_id !== scope.target_id ||
          body.identity_id !== pin.identity_id ||
          Object.hasOwn(body, 'identity_auth') ||
          !Buffer.from(jcs(body)).equals(input.subarray(prefix.length))
        )
          reject('scope_denied');
        const auth = {
          alg: /** @type {const} */ ('Ed25519'),
          kid: pin.identity_id,
          signature: sign(null, input, key).toString('base64url'),
        };
        structure('InstallationReceipt', { ...body, identity_auth: auth });
        return auth;
      },
    },
  };
}

/** @param {{config:any,directory:string,env?:NodeJS.ProcessEnv,getScope?:()=>any}} options */
export function createControlInstallation({
  config,
  directory,
  env = process.env,
  getScope,
}) {
  const pinsFile = env.AURIGA_NETWORK_INSTALLATION_PINS_FILE;
  const keyFile = env.AURIGA_NETWORK_INSTALLATION_KEY_FILE;
  const target = env.AURIGA_NETWORK_INSTALLATION_TARGET_ID;
  const permitFile = env.AURIGA_NETWORK_MIGRATION_PERMIT_FILE;
  async function load(/** @type {any} */ expected) {
    if (!pinsFile && !keyFile) return undefined;
    if (!pinsFile || !keyFile || !config.network) reject('scope_denied');
    const identity = installationIdentity(
      detach(await privateFile(pinsFile, 32 * 1024 * 1024)),
      target,
      await privateFile(keyFile, 4096),
      expected,
    );
    if (identity.scope.realm !== config.network.realm) reject('scope_denied');
    return {
      ...identity,
      migrationPermit: permitFile
        ? detach(await privateFile(permitFile, 65536))
        : undefined,
    };
  }
  return {
    get available() {
      return !!(pinsFile && keyFile && config.network);
    },
    /** Called after the device credential has been verified, before connecting. */
    prepare: load,
    /** Offline owner import, using exactly the normal device trust namespace. */
    async importPackage(/** @type {any} */ input) {
      const identity = await load(getScope?.());
      if (!identity) throw new Error('installation identity unavailable');
      const { scope, controlIdentity, migrationPermit } = identity;
      const storageKey = createHash('sha256')
        .update(
          JSON.stringify([config.issuer, scope.tenant_id, scope.target_id]),
        )
        .digest('hex');
      const cache = await createFileLedgerStore({
        dir: join(directory, 'devices', storageKey, 'trust'),
      }).open();
      try {
        const trust = await openV2TrustStore({
          cache,
          roots: config.network.roots,
          realm: scope.realm,
          tenant_id: scope.tenant_id,
          identity_id: controlIdentity.identity_id,
          migrationPermit,
        });
        return await trust.importControlPackage(input, controlIdentity);
      } finally {
        await cache.close();
      }
    },
  };
}
