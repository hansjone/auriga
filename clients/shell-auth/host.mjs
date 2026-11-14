import { randomUUID, createHash } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { valid, requireValid } from './schema.mjs';
const codes = {
  invalid_message: 'common.validation_failed',
  not_authenticated: 'common.unauthenticated',
  peer_not_allowed: 'common.forbidden',
  owner_mismatch: 'common.forbidden',
  expired: 'common.forbidden',
  context_revoked: 'common.forbidden',
  raw_token_disabled: 'common.forbidden',
  stale_identity: 'common.conflict',
  capacity_exceeded: 'common.rate_limited',
  credentials_unavailable: 'common.unavailable',
};
export const hostFault = (reason) =>
  Object.assign(new Error(reason), {
    code: codes[reason],
    hostError: {
      code: codes[reason],
      reason,
      retryable: ['capacity_exceeded', 'credentials_unavailable'].includes(
        reason,
      ),
    },
  });
const signature = (message) =>
  createHash('sha256').update(JSON.stringify(message)).digest('hex');
export class HostChannel {
  constructor(
    broker,
    streams,
    {
      readConfig = () => ({ allowRawAgentToken: true }),
      now = Date.now,
      mono = () => performance.now(),
    } = {},
  ) {
    this.broker = broker;
    this.streams = streams;
    this.readConfig = readConfig;
    this.now = now;
    this.mono = mono;
    this.peers = new Map();
    this.contexts = new Map();
    this.versions = new Map();
    this.changed = () => {
      for (const id of [...this.contexts.keys()]) this.drop(id);
      this.versions.clear();
    };
    broker.on('changed', this.changed);
    this.timer = setInterval(() => {
      for (const [id, record] of this.contexts)
        if (record.until <= this.mono() || record.expires <= this.now())
          this.drop(id);
    }, 500);
    this.timer.unref();
  }
  connect(evidence) {
    if (
      !evidence?.verified ||
      evidence.role !== 'plugin-backend' ||
      !evidence.connectionId ||
      !evidence.sid ||
      !evidence.processStart
    )
      throw hostFault('peer_not_allowed');
    const peer = {
      ...evidence,
      id: randomUUID(),
      cache: new Map(),
      usedRequests: new Map(),
      tombstones: new Set(),
    };
    this.peers.set(peer.id, peer);
    return peer;
  }
  checkPeer(peer) {
    if (
      !peer ||
      this.peers.get(peer.id) !== peer ||
      peer.role !== 'plugin-backend'
    )
      throw hostFault('peer_not_allowed');
  }
  disconnect(peer) {
    if (!peer) return;
    for (const [id, r] of this.contexts) if (r.owner === peer.id) this.drop(id);
    this.peers.delete(peer.id);
    for (const stream of this.streams?.streams.values() ?? [])
      if (stream.owner === peer.id) this.streams.end(stream, 'closed');
  }
  envelope(message, peer, schema) {
    this.checkPeer(peer);
    if (!valid(schema, message)) throw hostFault('invalid_message');
    if (this.broker.snapshot().state !== 'authenticated')
      throw hostFault('not_authenticated');
    if (
      message.shellEpoch !== this.broker.epoch ||
      message.expectedRevision !== this.broker.revision
    )
      throw hostFault('stale_identity');
    const remaining = Date.parse(message.expiresAt) - this.now();
    if (remaining <= 0 || remaining > 30000) throw hostFault('expired');
    for (const [id, item] of peer.cache)
      if (item.until <= this.mono()) {
        peer.cache.delete(id);
        peer.usedRequests.set(id, item.hash);
      }
    const previous = peer.cache.get(message.requestId),
      hash = signature(message);
    if (peer.usedRequests.has(message.requestId))
      throw hostFault(
        peer.usedRequests.get(message.requestId) === hash
          ? 'expired'
          : 'stale_identity',
      );
    if (previous && previous.hash !== hash) throw hostFault('stale_identity');
    if (previous && previous.until <= this.mono()) throw hostFault('expired');
    if (!previous && (peer.cache.size >= 512 || peer.usedRequests.size >= 4096))
      throw hostFault('capacity_exceeded');
    const guard = this.broker.guard(
      message.shellEpoch,
      message.expectedRevision,
    );
    const entry = previous ?? {
      hash,
      until: this.mono() + remaining,
      expires: Date.parse(message.expiresAt),
    };
    peer.cache.set(message.requestId, entry);
    return { guard, entry };
  }
  assert(peer, guard, entry) {
    this.checkPeer(peer);
    if (entry.until <= this.mono() || entry.expires <= this.now())
      throw hostFault('expired');
    try {
      this.broker.assert(guard);
    } catch {
      throw hostFault('stale_identity');
    }
  }
  result(message, fields) {
    return {
      protocolVersion: 1,
      requestId: message.requestId,
      shellEpoch: this.broker.epoch,
      revision: this.broker.revision,
      ...fields,
    };
  }
  resolve(owner, id, kind) {
    const peer = this.peers.get(owner);
    this.checkPeer(peer);
    const r = this.contexts.get(id);
    if (!r || r.owner !== owner) throw hostFault('context_revoked');
    if (r.until <= this.mono() || r.expires <= this.now()) {
      this.drop(id);
      throw hostFault('expired');
    }
    try {
      this.broker.assert(r.guard);
    } catch {
      throw hostFault('stale_identity');
    }
    if (kind && r.kind !== kind) throw hostFault('owner_mismatch');
    if (r.parentExecutionContextId)
      this.resolve(owner, r.parentExecutionContextId, 'session');
    return r;
  }
  drop(id) {
    const record = this.contexts.get(id);
    if (!record) return;
    for (const [child, r] of this.contexts)
      if (r.parentExecutionContextId === id) this.drop(child);
    this.contexts.delete(id);
    const peer = this.peers.get(record.owner);
    if (peer) {
      peer.tombstones.add(id);
      if (peer.tombstones.size > 256)
        peer.tombstones.delete(peer.tombstones.values().next().value);
    }
    for (const stream of [...(this.streams?.streams.values() ?? [])])
      if (stream.executionContextId === id) this.streams.end(stream, 'closed');
  }
  async command(message, peer) {
    let result;
    try {
      const { guard, entry } = this.envelope(
        message,
        peer,
        'ShellAuthHostContextCommand',
      );
      if (entry.result) {
        this.assert(peer, guard, entry);
        return entry.result;
      }
      if (message.action === 'register') {
        if (
          [...this.contexts.values()].filter((r) => r.owner === peer.id)
            .length >= 128
        )
          throw hostFault('capacity_exceeded');
        const context = message.context;
        if (context.kind === 'tool') {
          const parent = this.resolve(
            peer.id,
            context.parentExecutionContextId,
            'session',
          );
          if (parent.hostSessionId !== context.hostSessionId)
            throw hostFault('owner_mismatch');
        }
        const parent =
          context.kind === 'tool'
            ? this.resolve(peer.id, context.parentExecutionContextId, 'session')
            : undefined;
        const expires = Math.min(
          Date.parse(context.expiresAt),
          this.now() + 600000,
          this.broker.tokenUntil,
          this.broker.sourceUntil ?? Infinity,
          parent?.expires ?? Infinity,
        );
        if (!Number.isFinite(expires) || expires <= this.now())
          throw hostFault('expired');
        const id = randomUUID(),
          record = {
            ...structuredClone(context),
            id,
            owner: peer.id,
            guard,
            expires,
            until: this.mono() + expires - this.now(),
            endpointId: undefined,
            bindingId: undefined,
          };
        this.contexts.set(id, record);
        result = this.result(message, {
          action: 'register',
          context: {
            executionContextId: id,
            ownerId: peer.id,
            hostSessionId: context.hostSessionId,
            kind: context.kind,
            expiresAt: new Date(expires).toISOString(),
          },
        });
      } else {
        if (!peer.tombstones.has(message.executionContextId)) {
          this.resolve(peer.id, message.executionContextId);
          this.drop(message.executionContextId);
        }
        result = this.result(message, {
          action: 'revoke',
          revokedExecutionContextId: message.executionContextId,
        });
      }
      this.assert(peer, guard, entry);
      entry.result = result;
    } catch (e) {
      result = this.result(message, {
        action: ['register', 'revoke'].includes(message?.action)
          ? message.action
          : 'register',
        error: e.hostError ?? hostFault('credentials_unavailable').hostError,
      });
    }
    return requireValid('ShellAuthHostContextResult', result);
  }
  rawAllowed() {
    try {
      const config = this.readConfig();
      if (!config || typeof config !== 'object') return false;
      return (
        config.allowRawAgentToken === undefined ||
        config.allowRawAgentToken === true
      );
    } catch {
      return false;
    }
  }
  async credentials(message, peer) {
    let result;
    try {
      const { guard, entry } = this.envelope(
        message,
        peer,
        'ShellAuthHostCredentialsRequest',
      );
      this.resolve(peer.id, message.executionContextId);
      if (message.purpose === 'raw' && !this.rawAllowed())
        throw hostFault('raw_token_disabled');
      let credentials;
      const key = JSON.stringify(guard.identity);
      for (let attempt = 0; attempt < 2; attempt++) {
        credentials = await this.broker.enterprise();
        if (!valid('ShellAuthHostCredentials', credentials))
          throw hostFault('credentials_unavailable');
        if (credentials.credentialVersion >= (this.versions.get(key) ?? 0))
          break;
        credentials = undefined;
      }
      if (!credentials) throw hostFault('credentials_unavailable');
      this.assert(peer, guard, entry);
      this.resolve(peer.id, message.executionContextId);
      if (message.purpose === 'raw' && !this.rawAllowed())
        throw hostFault('raw_token_disabled');
      if (!valid('ShellAuthHostCredentials', credentials))
        throw hostFault('credentials_unavailable');
      const identity = guard.identity,
        binding = credentials.binding,
        source = identity.uacSessionBinding;
      if (
        !source ||
        ['tenantId', 'memberId', 'deviceId', 'providerId'].some(
          (key) => binding[key] !== identity[key],
        ) ||
        ['sourceSessionId', 'authGeneration', 'providerId'].some(
          (key) => binding[key] !== source[key],
        ) ||
        Date.parse(binding.expiresAt) <= this.now() ||
        (binding.credentialVersion !== undefined &&
          binding.credentialVersion !== credentials.credentialVersion)
      )
        throw hostFault('stale_identity');
      this.versions.set(key, credentials.credentialVersion);
      result = this.result(message, {
        executionContextId: message.executionContextId,
        purpose: message.purpose,
        credentials,
      });
    } catch (e) {
      result = this.result(message, {
        executionContextId: message?.executionContextId,
        purpose: ['raw', 'outbound'].includes(message?.purpose)
          ? message.purpose
          : 'raw',
        error: e.hostError ?? hostFault('credentials_unavailable').hostError,
      });
    }
    return requireValid('ShellAuthHostCredentialsResult', result);
  }
  close() {
    clearInterval(this.timer);
    this.changed();
    this.peers.clear();
    this.broker.off('changed', this.changed);
  }
}
