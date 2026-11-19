// @ts-nocheck
import { fail, SHARED_RISK } from './protocol.js';
import { executionSession, openSharedTask } from './task-context.js';
import { memberFilePath, memberWorkspace } from './member-files.js';
import { installSharedMessageTool } from './message-tool.js';
import { installSharedSkillGate } from './skill-policy.js';

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
  'auriga_send_message',
  'skill',
]);

/** Explicit composition entry for R's restricted profile; never patch host internals.
 * assertProfile must attest actual unloaded controllers and fixed platform skill roots.
 * invoke is the self-owned carrier; native owner remains a disclosed bypass.
 */
export function installSharedHost(
  ctx,
  { runtime, enterprise, ports, messaging, taskEnvironment },
) {
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
  async function memberSessionIds(peer) {
    await runtime.refresh(peer);
    if (
      typeof ctx.sessions.list !== 'function' ||
      typeof ctx.sessionPersistence?.list !== 'function'
    )
      fail('common.unavailable');
    const stored = await ctx.sessionPersistence.list();
    if (!Array.isArray(stored)) fail('common.unavailable');
    const candidates = new Set([
      ...ctx.sessions.list().map((session) => session.id),
      ...stored.map((snapshot) => snapshot?.header?.id),
    ]);
    const result = [];
    for (const id of candidates) {
      // K session IDs are UUIDs. Owner/native legacy IDs never become a
      // guessed W binding and are not attached/resumed merely to enumerate.
      if (
        typeof id !== 'string' ||
        !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(
          id,
        )
      )
        continue;
      try {
        await runtime.authorize(peer, 'session/page', id);
        result.push(id);
      } catch (error) {
        if (
          !['shared.binding_mismatch', 'common.not_found'].includes(error.code)
        )
          throw error;
      }
    }
    await runtime.refresh(peer);
    return result;
  }
  try {
    disposers.push(
      ctx.tools.guard((call) =>
        finiteTools.has(call.name) &&
        (call.name !== 'skill' || ports.assertProfile(SHARED_PROFILE) === true)
          ? undefined
          : 'shared-mode-disabled',
      ),
    );
    disposers.push(
      installSharedSkillGate(ctx, {
        runtime,
        assertProfile: () => ports.assertProfile(SHARED_PROFILE),
      }),
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
    for (const name of [
      'auriga_enterprise_request',
      'auriga_shared_resources',
    ]) {
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
            const borrowed = taskEnvironment?.borrow(execution);
            if (taskEnvironment && (!borrowed || !borrowed.company))
              fail('common.forbidden');
            const task =
              borrowed ??
              (await openSharedTask({
                ctx,
                runtime,
                ports,
                execution,
              }));
            try {
              return await enterprise.outbound({
                remoteAddress: '127.0.0.1',
                signal: execution.signal,
                body: { ...args, dshSessionId: id, taskCapability: task.token },
              });
            } finally {
              if (!borrowed) await task.close();
            }
          },
        }),
      );
    }
    disposers.push(
      installSharedMessageTool(ctx, {
        settings: () => ({ messageTrust: ports.messageTrustConfig?.() }),
      }),
    );
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
    // Internal native-browser composition only. Its fixed finite gateway
    // rejects interaction results and owner waterfall handlers; answer enables
    // the public rc.3 event stream without manufacturing a new principal.
    admitBrowser: (token, request) =>
      runtime.admit(token, request, (principal) =>
        ports.createPeer({ principal, interactions: 'answer' }),
      ),
    create: (peer, input) => runtime.create(peer, ports.sessions, input),
    authorize: (peer, action, sessionId) =>
      runtime.authorize(peer, action, sessionId),
    async refresh(peer) {
      return memberSessionIds(peer);
    },
    async messagingSettings(peer, input) {
      if (!messaging) fail('common.unavailable');
      if (
        !input ||
        typeof input !== 'object' ||
        Array.isArray(input) ||
        Object.keys(input).some(
          (key) => !['sessionId', 'enabled'].includes(key),
        ) ||
        typeof input.sessionId !== 'string' ||
        (Object.hasOwn(input, 'enabled') && typeof input.enabled !== 'boolean')
      )
        fail('common.validation_failed');
      const binding = await runtime.authorize(
        peer,
        'session/page',
        input.sessionId,
      );
      const result = Object.hasOwn(input, 'enabled')
        ? await messaging.setReceive(binding, input.enabled)
        : await messaging.status(binding);
      await runtime.authorize(peer, 'session/page', input.sessionId);
      return result;
    },
    async messagingSessions(peer) {
      return (await memberSessionIds(peer)).map((sessionId, index) => ({
        sessionId,
        label: `会话 ${index + 1}`,
      }));
    },
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
      taskEnvironment?.revoke(event);
      messaging?.revoke(event);
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
