import {
  existsSync,
  readFileSync,
  openSync,
  writeSync,
  fsyncSync,
  closeSync,
  renameSync,
  unlinkSync,
} from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { createRequire } from 'node:module';
import { contractFile, valid } from './schema.mjs';
import { boundedBody } from './platform.mjs';
import { safePath } from './vault.mjs';
const packaged = existsSync(
  new URL('./network-core.bundle.mjs', import.meta.url),
);
const loadCore = () =>
  import(packaged ? './network-core.bundle.mjs' : './network-core.mjs');
const fail = () =>
  Object.assign(new Error('network authority unavailable'), {
    code: 'common.forbidden',
  });
let validate;
function schema(file, name, value) {
  if (!validate) {
    const loader = createRequire(
      new URL(
        packaged ? './package.json' : '../dsh-plugin/package.json',
        import.meta.url,
      ),
    );
    const Ajv = loader(
        packaged ? './vendor-ajv.cjs' : 'ajv/dist/2020.js',
      ).default,
      ajv = new Ajv({ strict: false });
    ajv.addFormat('date-time', (s) => Number.isFinite(Date.parse(s)));
    for (const f of [
      'address',
      'envelope',
      'source-control',
      'protocol',
      'keyset',
      'send',
    ]) {
      const object = JSON.parse(contractFile('network/' + f + '.schema.json'));
      ajv.addSchema(object);
    }
    validate = (f, n, v) =>
      ajv.getSchema(
        'https://auriga.example.com/contracts/network/' +
          f +
          '.schema.json#/$defs/' +
          n,
      )(v);
  }
  if (!validate(file, name, value)) throw fail();
  return value;
}
const wire = (value, parse) => {
  if (typeof value !== 'string') throw fail();
  const bytes = Buffer.from(value, 'base64');
  if (bytes.length > 65536 || bytes.toString('base64') !== value) throw fail();
  return { bytes, value: parse(bytes) };
};
export class NetworkProxy {
  /** @param {any} broker @param {any} host @param {any} streams @param {{directory?:string,config?:any,core?:any,fetcher?:typeof globalThis.fetch}} options */
  constructor(
    broker,
    host,
    streams,
    { directory, config, core, fetcher = globalThis.fetch } = {},
  ) {
    this.broker = broker;
    this.host = host;
    this.streams = streams;
    this.directory = directory;
    this.config = config;
    this.core = core;
    this.fetcher = fetcher;
    this.links = new Map();
    this.trustStores = new Map();
    this.changed = () => {
      for (const link of this.links.values()) this.closeLink(link);
      this.links.clear();
    };
    broker.on('changed', this.changed);
  }
  async authority(owner, id) {
    const context = this.host.resolve(owner, id);
    return context.kind === 'tool'
      ? this.host.resolve(owner, context.parentExecutionContextId, 'session')
      : context;
  }
  check(record) {
    this.broker.assert(record.context);
    this.host.checkPeer(this.host.peers.get(record.owner));
    if (record.closed) throw fail();
  }
  async execute(definition, input, context, owner, signal) {
    const core = this.core ?? (await loadCore());
    if (definition.operation === 'network_open') {
      let registeredHere = false;
      const execution = this.host.resolve(
        owner,
        input.executionContextId,
        'session',
      );
      let record = this.links.get(owner);
      if (!record) {
        if (!this.config || !this.directory)
          throw new Error('trusted network installation required');
        record = {
          owner,
          context,
          core,
          closed: false,
          controller: new AbortController(),
          pending: new Map(),
          sources: new Map(),
          streams: new Set(),
          queue: Promise.resolve(),
          sequence: 0,
          queuedBytes: 0,
          queuedFrames: 0,
        };
        this.links.set(owner, record);
        record.opening = this.open(record, signal);
        registeredHere = true;
      }
      await record.opening;
      signal.throwIfAborted();
      this.host.resolve(owner, input.executionContextId, 'session');
      this.check(record);
      const registration = record.registration;
      if (
        !valid('ShellAuthNetworkRegistration', registration) ||
        registration.tenant !== context.identity.tenantId ||
        registration.device !== context.identity.deviceId ||
        registration.realm !== this.config.realm ||
        BigInt(registration.realm) > 18446744073709551615n ||
        BigInt(registration.node) > 18446744073709551615n ||
        registration.systemAddress !==
          `aur1:${BigInt(registration.realm).toString(16)}.${BigInt(registration.node).toString(16)}.0`
      )
        throw fail();
      const created = registeredHere && registration.created;
      if (registeredHere) record.registration = { ...registration, created: false };
      const stream = this.streams.create(context, owner, () => {
        record.streams.delete(stream.id);
        if (!record.streams.size) this.closeLink(record);
      });
      stream.executionContextId = execution.id;
      record.streams.add(stream.id);
      return {
        statusCode: 200,
        body: {
          streamId: stream.id,
          registration: { ...registration, created },
          binding: {
            executionContextId: execution.id,
            shellEpoch: context.epoch,
            revision: context.revision,
          },
        },
      };
    }
    if (definition.operation === 'network_send') {
      const stream = this.streams.get(input.streamId, owner),
        record = this.links.get(owner);
      if (!record || !record.streams.has(stream.id)) throw fail();
      this.check(record);
      this.host.resolve(owner, stream.executionContextId);
      const frame = wire(input.frameWire, core.parseStrictJson);
      schema('protocol', 'Upstream', frame.value);
      for (const [key, value] of record.pending)
        if (value.until <= performance.now()) record.pending.delete(key);
      if (
        record.pending.size >= 128 ||
        record.pending.has(frame.value.request_id)
      )
        throw fail();
      if (frame.value.type === 'ready') {
        if (record.ready) throw fail();
        record.ready = {
          requestId: frame.value.request_id,
          channelNonce: frame.value.channel_nonce,
          features: frame.value.features,
        };
      } else if (
        !record.ready ||
        !record.epoch ||
        frame.value.epoch !== record.epoch
      )
        throw fail();
      if (frame.value.type === 'source_revoke') {
        const source = record.sources.get(frame.value.source_binding);
        const execution = this.host.resolve(owner, stream.executionContextId);
        if (source && source.hostSessionId !== execution.hostSessionId)
          throw fail();
        if (source) {
          source.state = 'stopped';
          source.stopped = true;
        }
      }
      record.pending.set(frame.value.request_id, {
        until: performance.now() + 30000,
        executionId: stream.executionContextId,
        frame: frame.value,
      });
      signal.throwIfAborted();
      this.host.resolve(owner, stream.executionContextId);
      await record.link.send(frame.bytes);
      this.check(record);
      signal.throwIfAborted();
      this.host.resolve(owner, stream.executionContextId);
      return { statusCode: 200, body: { sequence: record.sequence++ } };
    }
    if (definition.operation === 'network_submit') {
      const execution = await this.authority(owner, input.executionContextId),
        record = this.links.get(owner);
      if (!record) throw fail();
      this.check(record);
      const request = wire(input.requestWire, core.parseStrictJson);
      schema('send', 'SubmitRequest', request.value);
      const source = record.sources.get(request.value.source.source_binding);
      if (
        !source ||
        source.hostSessionId !== execution.hostSessionId ||
        source.expires <= Date.now() ||
        source.state !== 'active'
      )
        throw fail();
      const token = await this.broker.tokenFor(definition.audience, context);
      this.check(record);
      await this.authority(owner, input.executionContextId);
      if (source.expires <= Date.now() || source.state !== 'active')
        throw fail();
      signal.throwIfAborted();
      const response = await this.fetcher(
        new URL(
          '/api/ai/v1/network/submit',
          this.broker.platform.config.platformOrigin,
        ),
        {
          method: 'POST',
          redirect: 'error',
          credentials: 'omit',
          signal,
          headers: {
            Authorization: `Bearer ${token}`,
            'Content-Type': 'application/json',
          },
          body: request.bytes,
        },
      );
      const raw = await boundedBody(response, 65536);
      this.check(record);
      await this.authority(owner, input.executionContextId);
      if (source.expires <= Date.now() || source.state !== 'active')
        throw fail();
      if (!response.ok) throw new Error('network submit unavailable');
      schema('send', 'SendResult', core.parseStrictJson(raw));
      signal.throwIfAborted();
      return {
        statusCode: response.status,
        body: { resultWire: raw.toString('base64') },
      };
    }
    if (
      ['getNetworkKeys', 'getNetworkKeyAnnouncement'].includes(
        definition.operation,
      )
    ) {
      const record = this.links.get(owner);
      if (!record) throw fail();
      this.check(record);
      const raw = await this.readFixed(definition.route, context, signal);
      const value = core.parseStrictJson(raw);
      if (definition.operation === 'getNetworkKeys') {
        schema('keyset', 'SignedKeyset', value);
        await record.trust.update(raw);
      } else {
        schema('protocol', 'KeysetAnnouncement', value);
        await record.trust.announce(raw);
      }
      this.check(record);
      signal.throwIfAborted();
      return { statusCode: 200, body: { wire: raw.toString('base64') } };
    }
    throw fail();
  }
  async open(record, signal) {
    try {
      const provider = record.core.createDeviceProvider({
        config: {
          ...this.broker.platform.config,
          providerId: 'discover',
          network: this.config,
        },
        getDevice: async () => {
          this.check(record);
          return {
            accessToken: this.broker.record.tokens.access_token,
            claims: this.broker.claims,
          };
        },
      });
      const prepared = await provider.prepare({ signal });
      this.check(record);
      record.prepared = prepared;
      const file = safePath(
        join(this.directory, prepared.storageKey + '.json'),
      );
      const cache = {
        load: async () =>
          existsSync(file) ? JSON.parse(readFileSync(file, 'utf8')) : undefined,
        commit: async (value) => {
          const temp = file + '.' + randomUUID() + '.tmp';
          try {
            const fd = openSync(temp, 'wx', 0o600);
            try {
              writeSync(fd, JSON.stringify(value));
              fsyncSync(fd);
            } finally {
              closeSync(fd);
            }
            renameSync(temp, file);
          } finally {
            if (existsSync(temp)) unlinkSync(temp);
          }
        },
      };
      record.storageKey = prepared.storageKey;
      let openingTrust = this.trustStores.get(prepared.storageKey);
      if (!openingTrust) {
        openingTrust = record.core.openTrustStore({
          cache,
          roots: prepared.roots,
          realm: this.config.realm,
          requireAnnouncement: true,
          refresh: async () => {
            const active = [...this.links.values()].find(
              (link) => !link.closed && link.storageKey === prepared.storageKey,
            );
            if (!active) throw fail();
            this.check(active);
            await active.prepared.refresh(
              active.trust,
              active.controller.signal,
            );
          },
        });
        this.trustStores.set(prepared.storageKey, openingTrust);
        openingTrust.catch(() => {
          if (this.trustStores.get(prepared.storageKey) === openingTrust)
            this.trustStores.delete(prepared.storageKey);
        });
      }
      const trust = await openingTrust;
      record.trust = trust;
      await prepared.refresh(trust, signal);
      record.registration = await prepared.register(signal);
      const ticket = await prepared.getTicket(signal);
      this.check(record);
      record.until = ticket.expiresAtMs;
      record.link = new record.core.NatsLink({
        url: prepared.url,
        ...ticket.subjects,
        auth: ticket.auth,
        onMessage: (bytes) => {
          if (record.closed) return;
          if (
            record.queuedBytes + bytes.length > 1048576 ||
            record.queuedFrames >= 128
          ) {
            this.closeLink(record);
            return;
          }
          record.queuedBytes += bytes.length;
          record.queuedFrames++;
          record.queue = record.queue
            .then(() => this.receive(record, Buffer.from(bytes)))
            .catch(() => this.closeLink(record))
            .finally(() => {
              record.queuedBytes -= bytes.length;
              record.queuedFrames--;
            });
        },
        onClosed: () => this.closeLink(record),
      });
      await record.link.open();
      this.check(record);
      record.expiry = setTimeout(
        () => this.closeLink(record),
        Math.max(0, record.until - Date.now()),
      );
      record.expiry.unref();
    } catch (error) {
      this.closeLink(record);
      throw error;
    }
  }
  async readFixed(route, context, signal) {
    const token = await this.broker.tokenFor('auriga', context);
    this.broker.assert(context);
    const response = await this.fetcher(
      new URL(route, this.broker.platform.config.platformOrigin),
      {
        redirect: 'error',
        credentials: 'omit',
        signal,
        headers: { Authorization: `Bearer ${token}` },
      },
    );
    const raw = await boundedBody(response, 65536);
    this.broker.assert(context);
    if (!response.ok) throw new Error('network keys unavailable');
    return raw;
  }
  async receive(record, bytes) {
    this.check(record);
    if (bytes.length > 65536) throw fail();
    const frame = record.core.parseStrictJson(bytes);
    schema('protocol', 'Downstream', frame);
    const requestId =
      frame.type === 'fetch_result'
        ? frame.page.request_id
        : (frame.request_id ?? null);
    if (
      requestId &&
      (!record.pending.has(requestId) ||
        record.pending.get(requestId).until <= performance.now())
    )
      throw fail();
    if (frame.type === 'ready_result') {
      if (
        requestId !== record.ready?.requestId ||
        frame.node_identity.realm !== record.registration.realm ||
        frame.node_identity.node !== record.registration.node ||
        frame.required_features.some(
          (feature) => !record.ready.features.includes(feature),
        )
      )
        throw fail();
      record.epoch = frame.epoch;
      record.incarnation = frame.node_identity.incarnation;
    }
    if (!record.ready || !record.epoch || !record.incarnation) throw fail();
    const context = {
      realm: record.registration.realm,
      node: record.registration.node,
      nodeIncarnation: record.incarnation,
      epoch: record.epoch,
      channelNonce: record.ready.channelNonce,
      requestId,
    };
    if (frame.type === 'fetch_result') {
      const page = await record.core.verifyFetchPage({
        header: frame.page,
        items: frame.items,
        trust: record.trust,
        context,
        maxBytes: record.pending.get(requestId)?.frame.max_bytes,
      });
      if (page.rejections.length) throw fail();
    } else
      await record.core.verifyEnvelopeWithRefresh(frame, {
        trust: record.trust,
        context,
      });
    this.check(record);
    if (frame.type === 'head' && frame.keyset_announcement) {
      await record.trust.announce(frame.keyset_announcement);
      await record.prepared.refresh(record.trust, record.controller.signal);
      this.check(record);
    }
    if (frame.source?.source_binding) {
      const request = record.pending.get(requestId);
      if (!request) throw fail();
      const execution = this.host.resolve(record.owner, request.executionId),
        source = frame.source;
      const previous = record.sources.get(source.source_binding);
      if (previous && previous.hostSessionId !== execution.hostSessionId)
        throw fail();
      if (record.sources.size >= 512 && !previous) throw fail();
      if (
        previous?.revision &&
        BigInt(source.revision) < BigInt(previous.revision)
      )
        throw fail();
      let expires = 0;
      if (frame.type === 'source_get_result') {
        const sent = request.frame,
          witness = sent.native_witness;
        if (
          sent.type !== 'source_get' ||
          sent.source_binding !== source.source_binding ||
          frame.witness_id !== witness.witness_id ||
          ['binding_id', 'binding_revision'].some(
            (k) => witness[k] !== source.native_binding[k],
          ) ||
          Object.keys(source.registration).some(
            (k) => source.registration[k] !== sent.registration[k],
          ) ||
          Date.parse(frame.observed_at) > Date.now() + 30000 ||
          Date.parse(frame.observed_at) < Date.now() - 30000
        )
          throw fail();
        expires = Math.min(
          Date.parse(source.expires_at),
          Date.parse(frame.valid_until),
          Date.parse(witness.valid_until),
          Date.parse(frame.server_auth.not_after),
        );
        if (
          source.state === 'active' &&
          (!Number.isFinite(expires) ||
            expires <= Date.now() ||
            Date.parse(source.not_before) > Date.now() ||
            expires > Date.now() + 30000)
        )
          throw fail();
      }
      // Mutation receipts never grant SEND authority. Only a fresh signed GET
      // observation of the named native binding can temporarily enable submit.
      record.sources.set(source.source_binding, {
        hostSessionId: execution.hostSessionId,
        expires,
        state:
          frame.type === 'source_get_result' && !previous?.stopped
            ? source.state
            : 'stopped',
        revision: source.revision,
        stopped: previous?.stopped ?? false,
      });
    }
    if (frame.type === 'source_revoke_result') {
      const source = record.sources.get(frame.source_binding);
      if (source) {
        source.state = 'stopped';
        source.stopped = true;
      }
    }
    for (const id of [...record.streams]) {
      const stream = this.streams.streams.get(id);
      if (stream && !stream.terminal) {
        this.host.resolve(record.owner, stream.executionContextId);
        this.streams.push(stream, bytes);
      }
    }
  }
  closeLink(record) {
    if (record.closed) return;
    record.closed = true;
    record.controller.abort();
    clearTimeout(record.expiry);
    record.link?.close().catch(() => {});
    if (this.links.get(record.owner) === record)
      this.links.delete(record.owner);
    record.pending.clear();
    for (const id of record.streams) {
      const stream = this.streams.streams.get(id);
      if (stream) this.streams.end(stream, 'error', 'common.unavailable');
    }
    record.sources.clear();
  }
  close() {
    this.changed();
    this.trustStores.clear();
    this.broker.off('changed', this.changed);
  }
}
