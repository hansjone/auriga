// @ts-nocheck
import { fail, SHARED_RISK } from './protocol.js';
import { executionSession, openSharedTask } from './task-context.js';
import { memberFilePath, memberWorkspace } from './member-files.js';

export const SHARED_PROFILE = Object.freeze({
  shell: false,
  terminal: false,
  jobs: false,
  subagent: false,
  stdioMcp: false,
  pluginInstall: false,
  privateSkills: false,
  personalKeys: false,
  includeDefaultRoots: false,
  toolPresentation: 'native',
});
const finiteTools = new Set([
  'auriga_enterprise_request',
  'auriga_shared_resources',
]);

/** Explicit composition entry for R's restricted profile; never patch host internals.
 * assertProfile must attest actual unloaded controllers and fixed platform skill roots.
 * invoke is the self-owned carrier; native owner remains a disclosed bypass.
 */
export function installSharedHost(ctx, { runtime, enterprise, ports }) {
  if (
    !ctx.remoteAccess?.registerPolicy ||
    !ctx.tools?.guard ||
    !ctx.tools?.register ||
    !ports.createPeer ||
    !ports.assertProfile
  )
    fail('common.unavailable');
  if (ports.assertProfile(SHARED_PROFILE) !== true) fail('common.unavailable');
  const disposers = [];
  try {
    disposers.push(
      ctx.tools.guard((call) =>
        finiteTools.has(call.name) ? undefined : 'shared-mode-disabled',
      ),
    );
    disposers.push(
      ctx.remoteAccess.registerPolicy('auriga-shared-member', {
        bindings: (p) => runtime.bindings(p),
      }),
    );
    disposers.push(
      ctx.webServer.register({
        kind: 'prefix',
        path: '/dsh-team',
        handler: enterprise.handler,
      }),
    );
    for (const name of finiteTools) {
      disposers.push(
        ctx.tools.register({
          name,
          description:
            name === 'auriga_shared_resources'
              ? '查看平台统一资源及共享风险'
              : '调用平台企业应用',
          parameters: {
            type: 'object',
            properties:
              name === 'auriga_shared_resources'
                ? {}
                : {
                    url: { type: 'string' },
                    method: { type: 'string' },
                    headers: { type: 'object' },
                    body: {},
                    timeoutMs: { type: 'integer' },
                  },
            additionalProperties: false,
          },
          output: {
            schema: { type: 'object', additionalProperties: true },
            render: (_args, value) => [
              { type: 'text', text: JSON.stringify(value) },
            ],
          },
          async execute(args, execution) {
            const id = executionSession(ctx, execution);
            if (name === 'auriga_shared_resources')
              return runtime.resources(id);
            // Capability issuer derives task identity from the live host execution.
            const task = await openSharedTask({
              ctx,
              runtime,
              ports,
              execution,
            });
            try {
              return await enterprise.outbound({
                remoteAddress: '127.0.0.1',
                body: { ...args, dshSessionId: id, taskCapability: task.token },
              });
            } finally {
              await task.close();
            }
          },
        }),
      );
    }
  } catch (e) {
    for (const d of disposers.reverse()) d?.();
    throw e;
  }
  return {
    risk: SHARED_RISK,
    admit: (token, request) =>
      runtime.admit(token, request, (p) =>
        ports.createPeer({ principal: p, interactions: 'none' }),
      ),
    create: (peer, input) => runtime.create(peer, ports.sessions, input),
    async invoke(peer, action, sessionId, args) {
      const b = await runtime.authorize(peer, action, sessionId);
      if (!args || typeof args !== 'object' || Array.isArray(args))
        fail('common.validation_failed');
      for (const key of [
        'cwd',
        'ownerId',
        'tenantId',
        'memberId',
        'placementId',
        'physicalInstanceId',
        'epoch',
        'authGeneration',
      ]) {
        if (Object.hasOwn(args, key)) fail('common.validation_failed');
      }
      for (const key of ['sessionId', 'dshSessionId', 'workspaceFileScopeId']) {
        if (Object.hasOwn(args, key) && args[key] !== b.sessionId)
          fail('shared.binding_mismatch');
      }
      let checkedArgs = args;
      if (
        [
          'workspaceFiles/read',
          'workspaceFiles/readBytes',
          'workspaceFiles/stat',
        ].includes(action)
      ) {
        checkedArgs = {
          ...args,
          path: await memberFilePath(memberWorkspace(b), args.path),
        };
        await runtime.authorize(peer, action, sessionId);
      }
      const result = await ports.invoke(action, b, checkedArgs);
      await runtime.authorize(peer, action, sessionId);
      return result;
    },
    revoke(event) {
      runtime.revoke(event);
      ctx.remoteAccess.invalidate();
      ports.disconnect(event);
    },
    close() {
      runtime.close();
      for (const d of disposers.reverse()) d?.();
    },
  };
}
