/** Local UI projection only; never expose this port as an MCP/model tool.
 * The host must bind context to its authenticated user, not browser input.
 * @typedef {import('../network/consent/types.js').Layer} Layer
 * @typedef {{available: boolean, revision: string, conversation: 'allow'|'ask_each'|'deny_all', execution: 'allow'|'ask_each'|'deny_all'}} Policy
 */

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
          ...row,
          // Preview is a separate authenticated, digest-bound user-only port.
          // Its result is never handed to a session, prompt or model API.
          preview: String(await preview(row.request)).slice(0, 1000),
        })),
      );
      return { ...settings, pending: requests, organization: policy };
    },
    /** @param {Parameters<typeof consent.decide>[1]} input */
    decide(input) {
      return consent.decide(context, input);
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
