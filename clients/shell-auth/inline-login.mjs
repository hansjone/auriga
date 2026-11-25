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
    this.providers = [];
  }
  async start() {
    const data = await this.request('/api/iam/v1/login/providers');
    if (!Array.isArray(data.items)) throw fail();
    this.providers = data.items
      .filter(
        (p) => uuid(p.id) && ['uac', 'team-account'].includes(p.connectorType),
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
    this.prepareNative();
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
  prepareNative() {
    this.verifier = randomBytes(32).toString('base64url');
    this.nativeConnect = {
      clientId: this.platform.config.nativeClientId,
      codeChallenge: createHash('sha256')
        .update(this.verifier)
        .digest('base64url'),
      codeChallengeMethod: 'S256',
      deviceName: 'Auriga Desktop',
    };
  }
  async startProof(credential, now = Date.now()) {
    if (!this.proofBody) {
      const data = await this.request('/api/iam/v1/login/providers');
      const providers = data.items?.filter(
        (p) => uuid(p.id) && p.connectorType === 'uac',
      );
      // Only a unique provider advertised by the trusted portal can be selected silently.
      if (providers?.length !== 1)
        throw fail('iam.uac_proof_provider_required');
      const provider = providers[0];
      this.providerId = provider.id;
      this.providers = [
        {
          id: provider.id,
          connectorType: 'uac',
          displayName: provider.displayName,
        },
      ];
      this.prepareNative();
      this.proofBody = {
        providerId: provider.id,
        purpose: 'login',
        nativeConnect: this.nativeConnect,
        uacProof: {
          empNo: credential.empNo,
          token: credential.token,
          nonce: randomBytes(32).toString('base64url'),
          issuedAt: new Date(now).toISOString(),
        },
      };
    }
    if (!this.grant) {
      const tx = await this.request('/api/iam/v1/login/uac/challenges', {
        method: 'POST',
        body: this.proofBody,
      });
      if (
        !uuid(tx.id) ||
        !uuid(tx.transactionId) ||
        !tx.nativeDevice?.deviceCode ||
        !Number.isFinite(Date.parse(tx.nativeDevice.expiresAt))
      )
        throw fail();
      this.challengeId = tx.id;
      this.transactionId = tx.transactionId;
      this.grant = tx.nativeDevice;
      if (tx.state !== 'verified') throw fail('iam.uac_proof_rejected');
    }
    if (this.challengeId) {
      await this.request(
        '/api/iam/v1/login/uac/challenges/' + this.challengeId + '/complete',
        { method: 'POST', body: {} },
      );
      this.update(
        await this.request(
          '/api/iam/v1/login/transactions/' + this.transactionId,
        ),
      );
      this.challengeId = undefined;
    }
  }
  update(tx) {
    if (
      tx.id !== this.transactionId ||
      !tx.step ||
      !['form', 'complete', 'fail'].includes(tx.step.type)
    )
      throw fail();
    if (tx.status === 'expired') throw fail('expired_token');
    if (['failed', 'cancelled'].includes(tx.status)) {
      const code =
        typeof tx.step.code === 'string' &&
        /^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$/.test(tx.step.code)
          ? tx.step.code
          : 'access_denied';
      throw Object.assign(fail(code), { correlationId: tx.correlationId });
    }
    this.step = tx.step;
    this.completed = tx.status === 'completed' && tx.step.type === 'complete';
  }
  async advance(input) {
    if (!this.grant || Date.parse(this.grant.expiresAt) <= Date.now())
      throw fail('expired_token');
    if (input.operation === 'select') {
      if (this.proofBody) throw fail();
      return this.select(input.providerId);
    }
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
      try {
        this.update(
          await this.request(
            '/api/iam/v1/login/transactions/' + this.transactionId + '/advance',
            {
              method: 'POST',
              body: {
                type: 'form',
                formId: input.formId,
                values: input.values,
              },
            },
          ),
        );
      } catch (error) {
        if (this.proofBody || error.code !== 'iam.invalid_credentials')
          throw error;
        // IAM terminates failed transactions and invalidates their device grants.
        // Restart on the selected provider before accepting another credential.
        await this.select(this.providerId);
        if (this.step.type !== 'form') throw fail();
        this.step.error = { code: error.code };
      }
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
    if (!['form', 'qr'].includes(this.step?.type)) return undefined;
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
    this.proofBody = undefined;
    if (id) await this.request(path + id, { method: 'DELETE' }).catch(() => {});
  }
}
