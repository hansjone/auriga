import { randomUUID, createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import { performance } from 'node:perf_hooks';
import { setTimeout as delay } from 'node:timers/promises';
import { requireValid } from './schema.mjs';

const ZERO = '00000000-0000-0000-0000-000000000000';
const conflict = () =>
  Object.assign(new Error('common.conflict'), {
    status: 409,
    code: 'common.conflict',
  });
const unavailable = () =>
  Object.assign(new Error('common.unavailable'), {
    code: 'common.unavailable',
  });
const reasonFor = (error) =>
  ({
    'iam.uac_revalidation_rejected': 'revalidation_rejected',
    'iam.uac_revalidation_unreachable': 'revalidation_unreachable',
    'iam.provider_disabled': 'source_disabled',
    'iam.member_disabled': 'member_disabled',
    'iam.admin_disabled': 'admin_disabled',
  })[error.code] ?? 'revalidation_rejected';

export class Broker extends EventEmitter {
  constructor({
    platform,
    vault,
    openBrowser,
    log = (event) => void event,
    now = Date.now,
    mono = () => performance.now(),
    wait = delay,
  }) {
    super();
    this.platform = platform;
    this.vault = vault;
    this.openBrowser = openBrowser;
    this.log = log;
    this.now = now;
    this.mono = mono;
    this.wait = wait;
    this.epoch = randomUUID();
    this.revision = 0;
    this.state = 'signed_out';
    this.generation = 0;
    this.cache = new Map();
    this.inflight = new Set();
    this.record = { manualLoginRequired: false, revocations: [] };
    this.persistence = Promise.resolve();
    this.health = Promise.resolve();
  }
  save() {
    const value = structuredClone(this.record);
    this.persistence = this.persistence
      .catch(() => {})
      .then(() => this.vault.save(value));
    return this.persistence;
  }
  async initialize() {
    this.record = (await this.vault.load()) ?? this.record;
    if (!Array.isArray(this.record.revocations))
      throw new Error('invalid vault');
    if (this.record.tokens && !this.record.manualLoginRequired) {
      this.state = 'unreachable';
      this.reason = 'shell_restarted';
      await this.check();
    }
    await this.drainRevocations();
    return this;
  }
  transition(state, reason, projection) {
    this.state = state;
    this.reason = reason;
    this.projection = projection;
    this.revision++;
    const snapshot = this.snapshot();
    this.log({
      stage: 'shell_identity',
      phase: 'end',
      result: 'success',
      state,
      reason,
      correlationId: randomUUID(),
      revision: this.revision,
    });
    this.emit('changed', snapshot);
  }
  snapshot() {
    if (
      this.state === 'authenticated' &&
      (this.now() >= this.tokenUntil || this.mono() >= this.tokenMonoUntil)
    )
      this.transition('reauth_required', 'expired');
    let state = this.state,
      reason = this.reason;
    if (
      state === 'authenticated' &&
      (this.now() >= this.tokenUntil || this.mono() >= this.tokenMonoUntil)
    ) {
      state = 'reauth_required';
      reason = 'expired';
    }
    const expiry = Math.max(
      this.now(),
      Math.min(
        this.now() + 30000,
        state === 'authenticated' ? this.tokenUntil : Infinity,
      ),
    );
    return requireValid('ShellAuthSnapshot', {
      protocolVersion: 1,
      shellEpoch: this.epoch,
      revision: this.revision,
      state,
      expiresAt: new Date(expiry).toISOString(),
      ...(reason ? { reason } : {}),
      ...(state === 'authenticated' && this.projection ? this.projection : {}),
    });
  }
  guard(epoch = this.epoch, revision = this.revision) {
    if (this.closed || this.snapshot().state !== 'authenticated')
      throw Object.assign(new Error('common.unauthenticated'), {
        code: 'common.unauthenticated',
      });
    if (epoch !== this.epoch || revision !== this.revision) throw conflict();
    return {
      epoch,
      revision,
      generation: this.generation,
      claims: this.claims,
      identity: this.projection.identity,
    };
  }
  assert(context) {
    this.guard(context.epoch, context.revision);
    if (context.generation !== this.generation) throw conflict();
  }
  async command(command, owner, page = false) {
    requireValid(page ? 'ShellAuthPageCommand' : 'ShellAuthCommand', command);
    if (this.closed) throw unavailable();
    for (const [key, value] of this.cache)
      if (value.until <= this.mono()) this.cache.delete(key);
    const hash = createHash('sha256')
      .update(JSON.stringify(command))
      .digest('hex');
    const key = command.requestId,
      previous = this.cache.get(key);
    if (previous) {
      if (previous.hash !== hash || previous.owner !== owner) throw conflict();
      return previous.result;
    }
    const discovery =
      command.action === 'snapshot' &&
      command.shellEpoch === ZERO &&
      command.expectedRevision === 0;
    if (
      !discovery &&
      (command.shellEpoch !== this.epoch ||
        command.expectedRevision !== this.revision)
    )
      throw conflict();
    if (this.cache.size >= 512) throw unavailable();
    const result = this.execute(command, owner);
    this.cache.set(key, { hash, owner, until: this.mono() + 30000, result });
    return result;
  }
  async execute(command, owner) {
    if (command.action === 'open_login') {
      if (this.state === 'authenticated' || this.attempt) throw conflict();
      this.begin(owner);
    } else if (
      command.action === 'cancel_login' ||
      command.action === 'skip_login'
    ) {
      if (
        command.action === 'skip_login' &&
        !['signed_out', 'authenticating', 'unreachable'].includes(this.state)
      )
        throw conflict();
      if (this.attempt && this.attempt.owner !== owner) throw conflict();
      this.cancel();
    } else if (command.action === 'logout') await this.logout();
    return {
      protocolVersion: 1,
      requestId: command.requestId,
      type: 'snapshot',
      snapshot: this.snapshot(),
    };
  }
  pageCommand(command, owner) {
    return this.command(command, owner, true);
  }
  subscribe(command, owner, send) {
    requireValid('ShellAuthCommand', command);
    if (
      command.action !== 'subscribe' ||
      command.shellEpoch !== this.epoch ||
      command.expectedRevision !== this.revision
    )
      throw conflict();
    const publish = (snapshot) =>
      send({
        protocolVersion: 1,
        requestId: command.requestId,
        type: 'changed',
        snapshot,
      });
    this.on('changed', publish);
    publish(this.snapshot()); // listener installed before initial projection
    const timer = setInterval(() => publish(this.snapshot()), 10000);
    timer.unref();
    return () => {
      clearInterval(timer);
      this.off('changed', publish);
    };
  }
  track(job) {
    this.inflight.add(job);
    job.finally(() => this.inflight.delete(job)).catch(() => {});
    return job;
  }
  begin(owner) {
    const attempt = {
      owner,
      generation: ++this.generation,
      controller: new AbortController(),
      correlationId: randomUUID(),
    };
    this.attempt = attempt;
    this.transition('authenticating');
    this.track(this.runLogin(attempt));
  }
  current(attempt) {
    return (
      !this.closed &&
      this.attempt === attempt &&
      attempt.generation === this.generation &&
      !attempt.controller.signal.aborted
    );
  }
  cancel() {
    const attempt = this.attempt;
    this.attempt = undefined;
    this.generation++;
    attempt?.controller.abort();
    // RFC8628 has no pre-approval revocation endpoint: stop polling. IAM expires
    // the unused grant; a response already in flight is revoked below.
    this.transition('signed_out');
  }
  async queueRevoke(tokens) {
    if (!tokens) return;
    for (const token of [tokens.refresh_token, tokens.access_token])
      if (
        typeof token === 'string' &&
        token &&
        !this.record.revocations.includes(token)
      )
        this.record.revocations.push(token);
    await this.save();
    await this.drainRevocations();
  }
  async drainRevocations() {
    if (this.draining) return this.draining;
    this.draining = (async () => {
      for (const token of [...this.record.revocations].slice(0, 4)) {
        try {
          await this.platform.revoke(token);
          this.record.revocations = this.record.revocations.filter(
            (t) => t !== token,
          );
          await this.save();
        } catch {
          this.log({
            stage: 'session_revoke',
            phase: 'end',
            result: 'failure',
            correlationId: randomUUID(),
          });
          break;
        }
      }
    })();
    try {
      await this.draining;
    } finally {
      this.draining = undefined;
    }
  }
  async runLogin(attempt) {
    const signal = attempt.controller.signal;
    this.log({
      stage: 'device_authorization',
      phase: 'start',
      result: 'waiting',
      correlationId: attempt.correlationId,
    });
    let tokens;
    try {
      const grant = await this.platform.begin(signal);
      if (!this.current(attempt)) return;
      if (
        typeof grant.device_code !== 'string' ||
        grant.device_code.length > 16384 ||
        !grant.device_code ||
        typeof grant.user_code !== 'string' ||
        grant.user_code.length > 128 ||
        !Number.isSafeInteger(grant.expires_in) ||
        grant.expires_in < 1 ||
        grant.expires_in > 900
      )
        throw new Error('invalid device grant');
      const url = new URL(grant.verification_uri);
      if (
        url.protocol !== 'https:' ||
        url.origin !== this.platform.config.platformOrigin ||
        url.pathname !== '/device' ||
        url.username ||
        url.password ||
        url.search ||
        url.hash
      )
        throw new Error('untrusted verification page');
      const until = this.mono() + grant.expires_in * 1000;
      let interval = Math.max(
        5,
        Number.isSafeInteger(grant.interval) ? grant.interval : 5,
      );
      if (interval > 60) throw new Error('invalid interval');
      // K2 will add the public attempt projection; never send device_code to UI.
      if (this.record.manualLoginRequired)
        url.searchParams.set('mode', 'manual');
      await this.openBrowser(url.href);
      while (this.current(attempt) && this.mono() < until) {
        await this.wait(interval * 1000, undefined, { signal });
        if (!this.current(attempt) || this.mono() >= until) break;
        try {
          tokens = await this.platform.poll(grant.device_code, signal);
          break;
        } catch (error) {
          if (error.code === 'authorization_pending') continue;
          if (error.code === 'slow_down') {
            interval = Math.min(60, interval + 5);
            continue;
          }
          throw error;
        }
      }
      if (!tokens) {
        if (this.current(attempt)) {
          this.attempt = undefined;
          this.transition('reauth_required', 'expired');
        }
        return;
      }
      if (!this.current(attempt)) {
        await this.queueRevoke(tokens);
        return;
      }
      const verified = await this.validateTokens(tokens);
      if (!this.current(attempt)) {
        await this.queueRevoke(tokens);
        return;
      }
      const old = this.record.tokens;
      this.record.tokens = tokens;
      this.record.manualLoginRequired = false;
      await this.save();
      if (!this.current(attempt)) {
        await this.queueRevoke(tokens);
        return;
      }
      this.attempt = undefined;
      this.acceptVerified(verified);
      this.transition('authenticated', undefined, verified.projection);
      if (old) await this.queueRevoke(old);
      this.log({
        stage: 'device_authorization',
        phase: 'end',
        result: 'success',
        correlationId: attempt.correlationId,
      });
    } catch (error) {
      if (this.record.tokens === tokens) delete this.record.tokens;
      if (tokens) await this.queueRevoke(tokens).catch(() => {});
      if (this.current(attempt)) {
        this.attempt = undefined;
        this.transition(
          error.code === 'access_denied' ? 'signed_out' : 'unreachable',
          error.code === 'access_denied' ? undefined : 'offline',
        );
      }
      this.log({
        stage: 'device_authorization',
        phase: 'end',
        result: signal.aborted ? 'cancelled' : 'failure',
        correlationId: attempt.correlationId,
      });
    }
  }
  async validateTokens(tokens, expected) {
    if (
      tokens.token_type?.toLowerCase() !== 'bearer' ||
      tokens.scope !== 'auriga.connect' ||
      typeof tokens.refresh_token !== 'string' ||
      !tokens.refresh_token ||
      tokens.refresh_token.length > 16384
    )
      throw new Error('invalid token response');
    const claims = await this.platform.verifyDevice(
      tokens.access_token,
      expected,
    );
    const projection = await this.platform.profile(tokens.access_token, claims);
    if (
      expected &&
      this.projection &&
      JSON.stringify(projection.identity) !==
        JSON.stringify(this.projection.identity)
    )
      throw Object.assign(new Error('source binding changed'), {
        status: 403,
        code: 'iam.provider_disabled',
      });
    const tokenUntil = claims.exp * 1000;
    return {
      claims,
      tokenUntil,
      tokenMonoUntil: this.mono() + Math.max(0, tokenUntil - this.now()),
      projection: {
        identity: projection.identity,
        ...(projection.uacLoginInfo
          ? { uacLoginInfo: projection.uacLoginInfo }
          : {}),
      },
    };
  }
  acceptVerified(verified) {
    this.claims = verified.claims;
    this.tokenUntil = verified.tokenUntil;
    this.tokenMonoUntil = verified.tokenMonoUntil;
  }
  check() {
    if (this.checking) return this.checking;
    this.health = this.health.catch(() => {}).then(() => this.checkOnce());
    this.checking = this.health.finally(() => {
      this.checking = undefined;
    });
    return this.checking;
  }
  async checkOnce() {
    if (!this.record.tokens || this.closed || this.attempt) return;
    const generation = this.generation,
      old = this.record.tokens;
    let next;
    try {
      const refresh =
        !this.claims ||
        this.tokenUntil - this.now() < 60000 ||
        this.state === 'unreachable';
      next = refresh ? await this.platform.refresh(old.refresh_token) : old;
      if (generation !== this.generation || this.closed) {
        if (next !== old) await this.queueRevoke(next);
        return;
      }
      const verified = await this.validateTokens(next, this.claims);
      if (generation !== this.generation || this.closed) {
        if (next !== old) await this.queueRevoke(next);
        return;
      }
      this.record.tokens = next;
      await this.save();
      if (generation !== this.generation || this.closed) {
        if (next !== old) await this.queueRevoke(next);
        return;
      }
      this.acceptVerified(verified);
      if (
        this.state !== 'authenticated' ||
        JSON.stringify(verified.projection) !== JSON.stringify(this.projection)
      )
        this.transition('authenticated', undefined, verified.projection);
    } catch (error) {
      if (generation !== this.generation || this.closed) return;
      if ([400, 401, 403].includes(error.status)) {
        this.generation++;
        delete this.record.tokens;
        this.record.manualLoginRequired = true;
        this.transition('revoked', reasonFor(error));
        await this.save();
        await this.queueRevoke(next ?? old);
      } else this.transition('unreachable', 'offline');
    }
    await this.drainRevocations();
  }
  async logout() {
    const tokens = this.record.tokens;
    this.generation++;
    this.attempt?.controller.abort();
    this.attempt = undefined;
    delete this.record.tokens;
    this.record.manualLoginRequired = true;
    this.transition('signed_out', 'user_logout'); // local stop before network
    await this.save();
    this.track(this.queueRevoke(tokens));
  }
  async enterprise() {
    const context = this.guard();
    const result = await this.platform.enterprise(
      this.record.tokens.access_token,
    );
    this.assert(context);
    requireValid('ShellAuthAgentCredentials', {
      empNo: result.empNo,
      token: result.token,
      updatedAt: result.updatedAt,
    });
    return result;
  }
  async tokenFor(audience, context) {
    this.assert(context);
    if (audience === 'auriga') return this.record.tokens.access_token;
    const acquire = this.health
      .catch(() => {})
      .then(async () => {
        this.assert(context);
        const result = await this.platform.audience(
          this.record.tokens.refresh_token,
          this.claims,
          audience,
        );
        if (context.generation !== this.generation || this.closed) {
          await this.queueRevoke(result);
          throw conflict();
        }
        if (typeof result.refresh_token !== 'string' || !result.refresh_token)
          throw unavailable();
        this.record.tokens = {
          ...this.record.tokens,
          refresh_token: result.refresh_token,
        };
        await this.save();
        this.assert(context);
        return result.access_token;
      });
    this.health = acquire.then(
      () => {},
      () => {},
    );
    return acquire;
  }
  async close() {
    this.closed = true;
    this.generation++;
    this.attempt?.controller.abort();
    this.attempt = undefined;
    await Promise.allSettled([...this.inflight, this.health]);
    await this.persistence;
    this.removeAllListeners();
  }
}
