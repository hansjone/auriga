import { randomBytes, createHash } from 'node:crypto';

const uuid = (v) => typeof v === 'string' && /^[0-9a-f-]{36}$/i.test(v);
const fail = (code = 'common.validation_failed') =>
  Object.assign(new Error(code), { code, status: 400 });

/** One cookie jar, PKCE verifier and transaction per initiating OS connection.
 * No platform cookie, device code or token is ever included in view(). */
export class InlineLogin {
  constructor(platform, signal) {
    this.platform = platform;
    this.signal = signal;
    this.request = platform.loginSession(signal);
    this.verifier = randomBytes(32).toString('base64url');
    this.nativeConnect = {
      clientId: platform.config.clientId,
      codeChallenge: createHash('sha256')
        .update(this.verifier)
        .digest('base64url'),
      codeChallengeMethod: 'S256',
      deviceName: 'Auriga Desktop',
    };
    this.providers = [];
  }
  async start() {
    const data = await this.request('/api/iam/v1/login/providers');
    if (!Array.isArray(data.items)) throw fail();
    this.providers = data.items
      .filter(
        (p) =>
          uuid(p.id) &&
          ['uac', 'team-account', 'emergency'].includes(p.connectorType),
      )
      .map((p) => ({
        id: p.id,
        connectorType: p.connectorType,
        displayName: p.displayName,
      }));
    const preferred =
      this.providers.find((p) => p.connectorType === 'uac') ??
      this.providers[0];
    if (!preferred) throw fail('iam.provider_unavailable');
    await this.select(preferred.id);
  }
  async select(id) {
    const provider = this.providers.find((p) => p.id === id);
    if (!provider) throw fail();
    await this.cancel();
    this.providerId = id;
    const uac = provider.connectorType === 'uac';
    const tx = await this.request(
      uac
        ? '/api/iam/v1/login/uac/challenges'
        : '/api/iam/v1/login/transactions',
      {
        method: 'POST',
        body: {
          providerId: id,
          purpose: 'login',
          nativeConnect: this.nativeConnect,
        },
      },
    );
    if (
      !uuid(uac ? tx.transactionId : tx.id) ||
      !tx.nativeDevice?.deviceCode ||
      !Number.isFinite(Date.parse(tx.nativeDevice.expiresAt))
    )
      throw fail();
    this.transactionId = uac ? tx.transactionId : tx.id;
    this.grant = tx.nativeDevice;
    if (uac) {
      if (
        !uuid(tx.id) ||
        typeof tx.qrContent !== 'string' ||
        tx.qrContent.length > 2048
      )
        throw fail();
      this.challengeId = tx.id;
      this.step = {
        type: 'qr',
        content: tx.qrContent,
        status: 'waiting',
        expiresAt: tx.expiresAt,
      };
    } else this.update(tx);
  }
  update(tx) {
    if (
      tx.id !== this.transactionId ||
      !tx.step ||
      !['form', 'complete', 'fail'].includes(tx.step.type)
    )
      throw fail();
    this.step = tx.step;
    this.completed = tx.status === 'completed' && tx.step.type === 'complete';
    if (tx.status === 'expired') throw fail('expired_token');
    if (['failed', 'cancelled'].includes(tx.status))
      throw fail('access_denied');
  }
  async advance(input) {
    if (!this.grant || Date.parse(this.grant.expiresAt) <= Date.now())
      throw fail('expired_token');
    if (input.operation === 'select') return this.select(input.providerId);
    if (this.challengeId) {
      if (input.operation !== 'poll') throw fail();
      const path = '/api/iam/v1/login/uac/challenges/' + this.challengeId;
      const state = await this.request(path);
      if (
        [
          'expired',
          'cancelled',
          'failed',
          'rejected',
          'denied',
          'unavailable',
        ].includes(state.state)
      )
        throw fail(
          state.state === 'expired' ? 'expired_token' : 'access_denied',
        );
      if (['waiting', 'scanned'].includes(state.state)) {
        this.step.status = state.state;
        return;
      }
      if (state.state !== 'verified') throw fail();
      await this.request(path + '/complete', { method: 'POST', body: {} });
      this.challengeId = undefined;
      this.update(
        await this.request(
          '/api/iam/v1/login/transactions/' + this.transactionId,
        ),
      );
    } else if (!this.completed) {
      if (
        input.operation !== 'submit' ||
        this.step.type !== 'form' ||
        input.formId !== this.step.formId
      )
        throw fail();
      this.update(
        await this.request(
          '/api/iam/v1/login/transactions/' + this.transactionId + '/advance',
          {
            method: 'POST',
            body: { type: 'form', formId: input.formId, values: input.values },
          },
        ),
      );
    }
  }
  async tokens() {
    if (!this.completed) return undefined;
    return this.platform.nativePoll(
      this.grant.deviceCode,
      this.verifier,
      this.signal,
    );
  }
  view() {
    return {
      providers: this.providers,
      providerId: this.providerId,
      step: this.step,
    };
  }
  async cancel() {
    const id = this.challengeId ?? this.transactionId;
    const path = this.challengeId
      ? '/api/iam/v1/login/uac/challenges/'
      : '/api/iam/v1/login/transactions/';
    this.challengeId = this.transactionId = this.grant = this.step = undefined;
    this.completed = false;
    if (id) await this.request(path + id, { method: 'DELETE' }).catch(() => {});
  }
}
