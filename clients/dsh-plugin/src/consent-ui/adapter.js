/** Local UI projection only; never expose this port as an MCP/model tool.
 * The host must bind context to its authenticated user, not browser input.
 * @typedef {import('../network/consent/types.js').Layer} Layer
 * @typedef {{available: boolean, revision: string, conversation: 'allow'|'ask_each'|'deny_all', execution: 'allow'|'ask_each'|'deny_all'}} Policy
 */

/** Explicit browser projection: ownership proofs and decision reasons stay local.
 * @param {import('../network/consent/types.js').VerifiedSource} source */
function sourceView(source) {
  return {
    tenant: source.tenant,
    realm: source.realm,
    node: source.node,
    userId: source.userId,
  };
}

/**
 * @param {{
 * consent: Awaited<ReturnType<typeof import('../network/consent/index.js').openConsent>>,
 * context: unknown,
 * preview: (request: import('../network/consent/types.js').Request) => Promise<string>,
 * organizationPolicy: () => Promise<Policy>
 * }} ports Trusted local ports, supplied by the runtime integration.
 */
export function createConsentUIAdapter({
  consent,
  context,
  preview,
  organizationPolicy,
}) {
  return {
    async snapshot() {
      const pending = await consent.listPending(context);
      const settings = await consent.getSettings(context);
      const policy = await organizationPolicy();
      if (
        !policy ||
        policy.available !== true ||
        !policy.revision ||
        ![policy.conversation, policy.execution].every((mode) =>
          ['allow', 'ask_each', 'deny_all'].includes(mode),
        )
      )
        throw new Error('Organization policy unavailable');
      const requests = await Promise.all(
        pending.map(async (row) => ({
          request: {
            layer: row.request.layer,
            requestId: row.request.requestId,
          },
          source: sourceView(row.source),
          // Preview is a separate authenticated, digest-bound user-only port.
          // Its result is never handed to a session, prompt or model API.
          preview: String(await preview(row.request)).slice(0, 1000),
        })),
      );
      return {
        defaults: {
          conversation: settings.defaults.conversation,
          execution: settings.defaults.execution,
        },
        policyRevision: settings.policyRevision,
        rules: settings.rules.map((rule) => ({
          id: rule.id,
          layer: rule.layer,
          scope: rule.scope,
          source: sourceView(rule.source),
          choice: rule.choice,
        })),
        pending: requests,
        organization: {
          available: true,
          revision: policy.revision,
          conversation: policy.conversation,
          execution: policy.execution,
        },
      };
    },
    /** @param {Parameters<typeof consent.decide>[1]} input */
    async decide(input) {
      try {
        // A successful local choice is not platform/host/model acceptance.
        // Discard the private RecordEntry returned by consent.
        await consent.decide(context, input);
      } catch (error) {
        // Only a fixed local category crosses the browser bridge, never the
        // service exception text, request metadata or local blocking reason.
        throw Object.assign(new Error('Consent decision unavailable'), {
          reason:
            error instanceof Error && /^cooldown active:/.test(error.message)
              ? 'cooldown'
              : 'unavailable',
        });
      }
    },
    /** @param {Layer} layer @param {import('../network/consent/types.js').DefaultMode} mode */
    setDefault(layer, mode) {
      return consent.setDefault(context, layer, mode);
    },
    /** @param {{layer: Layer, ruleId: string}} input */
    revoke(input) {
      return consent.revoke(context, input);
    },
  };
}
