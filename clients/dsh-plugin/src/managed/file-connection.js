import { EventEmitter } from 'node:events';
import { randomUUID, createHash } from 'node:crypto';
import {
  constants,
  openSync,
  closeSync,
  fstatSync,
  lstatSync,
  readFileSync,
  existsSync,
} from 'node:fs';
import { dirname, isAbsolute, join, parse } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { performance } from 'node:perf_hooks';
import { parseStrictJson } from '../network/trust/jcs.js';
import { verifyBrokerPackage } from '../local/broker-package.js';

const fail = (/** @type {string} */ code = 'common.unauthenticated') =>
  Object.assign(new Error(code), { code });

/** Renderer-owned private material. Check the complete path before opening it.
 * @param {string} file */
function readPrivate(file) {
  if (!isAbsolute(file)) throw fail();
  for (let path = file; ; path = dirname(path)) {
    if (lstatSync(path).isSymbolicLink()) throw fail();
    if (path === parse(path).root) break;
  }
  const fd = openSync(file, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
  try {
    const stat = fstatSync(fd);
    if (
      !stat.isFile() ||
      stat.nlink !== 1 ||
      stat.size < 1 ||
      stat.size > 65536 ||
      (process.platform !== 'win32' && (stat.mode & 0o077) !== 0)
    )
      throw fail();
    return readFileSync(fd, 'utf8');
  } finally {
    closeSync(fd);
  }
}

/** Load the identical fixed executor used by the private broker. No helper or
 * IPC process is started. The source path is for an unpackaged developer tree.
 * @returns {Promise<any>} */
async function implementation() {
  const packaged = fileURLToPath(new URL('../../shell-auth/', import.meta.url));
  let base;
  let coreFile;
  if (existsSync(join(packaged, 'broker-manifest.json'))) {
    base = (await verifyBrokerPackage(packaged, 'managed')).base;
    coreFile = 'network-core.bundle.mjs';
  } else {
    base = fileURLToPath(new URL('../../../shell-auth/', import.meta.url));
    if (
      !existsSync(join(base, 'build.mjs')) ||
      !existsSync(new URL('../../scripts/package-ui.mjs', import.meta.url))
    )
      throw fail('common.unavailable');
    coreFile = 'network-core.mjs';
  }
  const module = (/** @type {string} */ name) =>
    import(pathToFileURL(join(base, name)).href);
  const [business, host, platform, schema, core] = await Promise.all([
    module('business.mjs'),
    module('host.mjs'),
    module('platform.mjs'),
    module('schema.mjs'),
    module(coreFile),
  ]);
  return { ...business, ...host, ...platform, ...schema, core };
}

/** Trusted in-process managed transport. Its identity comes from the private
 * managed-device DTO and a verified short-lived device JWT. It does not project
 * a human identity-provider id or pretend to be an OS-authenticated peer.
 * @param {{file:string,directory:string,tenantId:string,ownerId:string,instanceId:string,platformOrigin:string,modelFile?:string}} options */
export async function createManagedFileConnection(options) {
  const shared = await implementation();
  const raw = readPrivate(options.file);
  const material = shared.requireValid(
    'ManagedDeviceCredential',
    parseStrictJson(Buffer.from(raw)),
  );
  if (
    material.tenantId !== options.tenantId ||
    material.ownerId !== options.ownerId ||
    material.instanceId !== options.instanceId ||
    Date.parse(material.expiresAt) <= Date.now()
  )
    throw fail();
  const config = shared.trustedConfig({
    ...material,
    clientId: 'auriga-managed-network',
  });
  const platform = shared.createPlatform(config);
  /** @type {any} */ const authority = new EventEmitter();
  /** @type {any} */ let verified;
  /** @type {any} */ let callbacks = {};
  /** @type {Promise<void>|undefined} */ let refreshing;
  /** @type {ReturnType<typeof setInterval>|undefined} */ let timer;
  let closed = false,
    rejected = false,
    state = 'unreachable',
    revision = 0,
    generation = 0;
  let monotonicUntil = 0,
    retryAt = 0,
    failures = 0;
  const epoch = randomUUID();
  const readModelStamp = () => {
    try {
      return options.modelFile
        ? createHash('sha256')
            .update(readPrivate(options.modelFile))
            .digest('hex')
        : null;
    } catch {
      return null;
    }
  };
  let modelStamp = readModelStamp();
  /** @type {Set<()=>void>} */ const invalidations = new Set();
  const subscriptions = new Set();
  const controllers = new Set();
  const identity = {
    tenantId: material.tenantId,
    memberId: material.ownerId,
    deviceId: material.deviceId,
  };
  Object.defineProperties(authority, {
    revision: { get: () => revision },
    generation: { get: () => generation },
    tokenUntil: { get: () => verified?.until ?? 0 },
    sourceUntil: { get: () => Date.parse(material.expiresAt) },
  });
  const currentMaterial = () => {
    try {
      return (
        readPrivate(options.file) === raw &&
        Date.parse(material.expiresAt) > Date.now()
      );
    } catch {
      return false;
    }
  };
  const publish = (/** @type {string} */ next, force = false) => {
    if (state === next && !force) return;
    state = next;
    revision++;
    generation++;
    authority.emit('changed');
    for (const stop of invalidations) stop();
    callbacks.onInvalidate?.();
    callbacks.onChange?.(snapshot());
  };
  const snapshot = () => {
    if (!closed && !currentMaterial()) publish('revoked');
    else if (
      !closed &&
      verified &&
      (Date.now() >= verified.until || performance.now() >= monotonicUntil)
    )
      publish('unreachable');
    const nextModelStamp = readModelStamp();
    if (nextModelStamp !== modelStamp) {
      modelStamp = nextModelStamp;
      if (!closed && state === 'authenticated') publish(state, true);
    }
    return {
      protocolVersion: 1,
      shellEpoch: epoch,
      revision,
      state: closed ? 'revoked' : state,
      expiresAt: new Date(
        Math.max(
          Date.now() + 1,
          Math.min(
            Date.now() + 30000,
            verified?.until ?? Infinity,
            Date.parse(material.expiresAt),
          ),
        ),
      ).toISOString(),
      ...(state === 'authenticated' && !closed ? { identity } : {}),
    };
  };
  Object.assign(authority, {
    platform,
    epoch,
    record: { tokens: {} },
    snapshot,
    guard(
      /** @type {string} */ expectedEpoch = epoch,
      /** @type {number} */ expectedRevision = revision,
    ) {
      if (closed || rejected || snapshot().state !== 'authenticated')
        throw fail();
      if (expectedEpoch !== epoch || expectedRevision !== revision)
        throw fail('common.conflict');
      return { epoch, revision, generation, identity, claims: verified.claims };
    },
    assert(/** @type {any} */ context) {
      authority.guard(context.epoch, context.revision);
      if (context.generation !== generation) throw fail();
      if (
        context.modelRaw &&
        (!options.modelFile ||
          readPrivate(options.modelFile) !== context.modelRaw)
      )
        throw fail();
    },
    async tokenFor(
      /** @type {string} */ _audience,
      /** @type {any} */ context,
      /** @type {string} */ operation,
    ) {
      authority.assert(context);
      if (
        [
          'listAiModels',
          'createAiChatCompletion',
          'createAiChatCompletionStream',
        ].includes(operation)
      ) {
        if (!options.modelFile) throw fail('common.forbidden');
        const modelRaw = readPrivate(options.modelFile);
        const model = shared.requireValid(
          'ManagedModelCredential',
          parseStrictJson(Buffer.from(modelRaw)),
        );
        const parts = model.credential.split('.');
        if (
          model.tenantId !== material.tenantId ||
          model.ownerId !== material.ownerId ||
          model.instanceId !== material.instanceId ||
          model.baseUrl !== config.platformOrigin + '/ai/v1' ||
          parts.slice(1, 5).join('.') !==
            [
              material.tenantId,
              material.ownerId,
              material.instanceId,
              material.deviceId,
            ].join('.') ||
          new Set(model.models.map((/** @type {any} */ item) => item.id))
            .size !== model.models.length ||
          model.models.some(
            (/** @type {any} */ item) =>
              item.contextWindow !== undefined &&
              item.maxOutputTokens !== undefined &&
              item.maxOutputTokens >= item.contextWindow,
          )
        )
          throw fail('common.forbidden');
        context.modelRaw = modelRaw;
        return model.credential;
      }
      return verified.token;
    },
  });
  class ManagedHost extends shared.HostChannel {
    constructor(/** @type {any} */ broker, /** @type {any} */ streams) {
      super(broker, streams);
      this.peer = {
        id: randomUUID(),
        role: 'managed-backend',
        cache: new Map(),
        tombstones: new Set(),
      };
      this.peers.set(this.peer.id, this.peer);
    }
    checkPeer(/** @type {any} */ peer) {
      if (peer !== this.peer || this.peers.get(peer?.id) !== peer)
        throw fail('common.forbidden');
    }
  }
  const business = new shared.BusinessProxy(
    authority,
    globalThis.fetch,
    undefined,
    {
      networkDirectory: join(options.directory, 'managed-network'),
      networkCore: shared.core,
      hostFactory: (/** @type {any} */ broker, /** @type {any} */ streams) =>
        new ManagedHost(broker, streams),
    },
  );
  // C's network cache expects its installation directory to exist.
  const { mkdir } = await import('node:fs/promises');
  await mkdir(join(options.directory, 'managed-network'), {
    recursive: true,
    mode: 0o700,
  });
  const owner = business.host.peer.id;
  async function refresh() {
    if (refreshing) return refreshing;
    refreshing = (async () => {
      if (closed || rejected || !currentMaterial()) {
        publish('revoked');
        return;
      }
      if (verified && verified.until - Date.now() > 60000) return;
      if (Date.now() < retryAt) return;
      const expected = generation;
      const controller = new AbortController();
      controllers.add(controller);
      try {
        const result = await platform.request(
          new URL(config.issuer).pathname.replace(/\/$/, '') + '/oauth2/token',
          {
            form: true,
            signal: controller.signal,
            body: {
              grant_type: 'urn:auriga:params:oauth:grant-type:managed-device',
              client_id: 'auriga-managed-network',
              instance_id: material.instanceId,
              owner_id: material.ownerId,
              credential: material.credential,
            },
          },
        );
        if (
          result.token_type !== 'Bearer' ||
          result.scope !== 'auriga.connect' ||
          !Number.isSafeInteger(result.expires_in) ||
          result.expires_in < 1 ||
          result.expires_in > 300
        )
          throw fail();
        const claims = await platform.verifyDevice(result.access_token, {
          tid: material.tenantId,
          sub: material.ownerId,
          did: material.deviceId,
        });
        if (claims.exp - claims.iat > 300 || claims.exp <= claims.iat)
          throw fail();
        if (closed || expected !== generation || !currentMaterial()) return;
        const until = Math.min(
          claims.exp * 1000,
          Date.now() + result.expires_in * 1000,
          Date.parse(material.expiresAt),
        );
        verified = { token: result.access_token, claims, until };
        authority.claims = claims;
        authority.record.tokens.access_token = result.access_token;
        monotonicUntil = performance.now() + Math.max(0, until - Date.now());
        retryAt = failures = 0;
        publish('authenticated');
      } catch (error) {
        if (closed || expected !== generation) return;
        const status = /** @type {any} */ (error)?.status;
        if ([401, 403].includes(status)) {
          rejected = true;
          publish('revoked');
        } else if (!verified || verified.until <= Date.now())
          publish('unreachable');
        retryAt =
          Date.now() + Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
      } finally {
        controllers.delete(controller);
      }
    })().finally(() => {
      refreshing = undefined;
    });
    return refreshing;
  }
  const ipc = {
    async call(/** @type {any} */ message) {
      if (message.call) return business.call(message, owner);
      if (message.path === '/host/contexts')
        return {
          body: await business.host.command(message.body, business.host.peer),
        };
      if (message.path === '/host/credentials') throw fail('common.forbidden');
      throw fail('common.validation_failed');
    },
    async subscribeStream(
      /** @type {any} */ message,
      /** @type {any} */ receive,
    ) {
      subscriptions.add(business.streams.subscribe(message, owner, receive));
    },
    streamCommand(/** @type {any} */ message) {
      return business.streams.command(message, owner);
    },
    close() {
      if (closed) return;
      closed = true;
      clearInterval(timer);
      for (const controller of controllers) controller.abort();
      for (const stop of subscriptions) stop();
      subscriptions.clear();
      business.close();
      for (const stop of invalidations) stop();
      invalidations.clear();
    },
  };
  return {
    config: { ...config, providerId: undefined },
    operations: JSON.parse(
      shared.contractFile('plugin/shell-business-operations.json'),
    ).operations,
    validate: shared.requireValid,
    ipc,
    get stale() {
      return !currentMaterial();
    },
    stateFactory(/** @type {any} */ options) {
      callbacks = options;
      return {
        current: snapshot,
        subscribeInvalidation(/** @type {()=>void} */ listener) {
          invalidations.add(listener);
          return () => invalidations.delete(listener);
        },
        fence() {
          const context = authority.guard();
          return () => authority.assert(context);
        },
        async connect() {
          await refresh();
          callbacks.onChange?.(snapshot());
          timer = setInterval(() => {
            snapshot();
            void refresh();
          }, 1000);
          timer.unref();
        },
        async command(/** @type {string} */ action) {
          if (action !== 'logout') throw fail('common.forbidden');
          rejected = true;
          publish('revoked');
        },
        disconnect() {
          publish('unreachable');
        },
        close: () => ipc.close(),
      };
    },
  };
}
