// @ts-nocheck
import { fileURLToPath } from 'node:url';
import { fail } from './protocol.js';

export function executionSession(ctx, execution) {
  const agent = execution?.agent;
  if (
    !agent ||
    ctx.sessions.get(agent.session?.id) !== agent.session ||
    ctx.agents.get(agent.id) !== agent
  )
    fail();
  return agent.session.id;
}

function taskEnvironment(ports, sessionId, token, credentials) {
  if (!ports.helperBase) return undefined;
  const base = new URL(ports.helperBase);
  if (
    !['http:', 'https:'].includes(base.protocol) ||
    !['127.0.0.1', 'localhost', '[::1]'].includes(base.hostname) ||
    base.username ||
    base.password ||
    base.search ||
    base.hash ||
    base.pathname !== '/dsh-team'
  )
    fail('common.validation_failed');
  return Object.freeze({
    ...credentials,
    DSH_SESSION_ID: sessionId,
    DSH_TEAM_TASK_CAPABILITY: token,
    DSH_TEAM_BASE: base.href,
    DSH_WEB_URL: base.origin,
    DSH_TEAM_HELPERS: fileURLToPath(
      new URL('./skill-helpers/python/', import.meta.url),
    ),
  });
}

async function acquireOrdinary(ports, binding, execution) {
  if (typeof ports.acquireExecution === 'function')
    return ports.acquireExecution(binding, execution);
  // Pure port compositions still own one ordinary handle explicitly. The
  // production ports always use the native turn authority above.
  let token = await ports.issueExecution(binding, execution);
  let closed = false,
    closing;
  return {
    binding,
    signal: execution.signal,
    get ordinaryToken() {
      return closed ? '' : token;
    },
    check() {
      if (closed || execution.signal?.aborted) fail();
    },
    async refresh() {
      if (closed) fail();
      const next = await ports.enterprise.refreshCapability(
        token,
        binding.sessionId,
        'task.execute',
      );
      if (closed) fail();
      token = next;
      return token;
    },
    close() {
      if (!closing) {
        closed = true;
        closing = Promise.resolve().then(() =>
          ports.finishTask(binding, execution, token),
        );
      }
      return closing;
    },
  };
}

/** A valid ordinary task is independent of optional company credentials. */
export async function openSharedExecution({
  ctx,
  runtime,
  ports,
  execution,
  raw = true,
}) {
  if (
    typeof ports.issueExecution !== 'function' ||
    typeof ports.finishTask !== 'function'
  )
    fail('common.unavailable');
  const sessionId = executionSession(ctx, execution);
  const binding = await runtime.session(sessionId);
  const ordinary = await acquireOrdinary(ports, binding, execution);
  let token = ordinary.ordinaryToken;
  let enterprise;
  let closed = false,
    closing;
  let closeRequested = false,
    retained = 0,
    settleWaiting;
  let env;
  const check = () => {
    ordinary.check?.();
    if (
      closed ||
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== sessionId
    )
      fail();
  };
  const close = () => {
    if (!closing) {
      closeRequested = true;
      closing = (async () => {
        if (retained)
          await new Promise((resolve) => {
            settleWaiting = resolve;
          });
        closed = true;
        env = undefined;
        try {
          await enterprise?.close();
        } finally {
          await ordinary.close();
        }
      })();
    }
    return closing;
  };
  try {
    const taskId = await ports.enterprise.taskId(token);
    const authorized = await runtime.capability(
      token,
      sessionId,
      taskId,
      'task.execute',
    );
    const state = authorized.state.enterpriseCredentialState;
    if (state === 'SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE') {
      // Node remembers one immutable audience/scope set for each task ID.
      // Optional company access therefore uses a separate trusted execution.
      enterprise = await openSharedTask({
        ctx,
        runtime,
        ports,
        raw,
        ordinary,
        execution: {
          agent: execution.agent,
          signal: globalThis.AbortSignal.any(
            [execution.signal, ordinary.signal].filter(Boolean),
          ),
        },
      });
      await runtime.capability(token, sessionId, taskId, 'task.execute');
    }
    if (!enterprise && state !== 'SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT')
      fail('common.unavailable');
    if (
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== sessionId
    )
      fail();
    env = taskEnvironment(ports, sessionId, token, {
      DSH_AURIGA_ID: '',
      DSH_AURIGA_TOKEN: '',
    });
    return Object.freeze({
      binding,
      company: !!enterprise,
      signal: ordinary.signal,
      get ordinaryToken() {
        return closed ? '' : ordinary.ordinaryToken;
      },
      get token() {
        return closed || !ordinary.ordinaryToken
          ? ''
          : (enterprise?.token ?? token);
      },
      get env() {
        return closed || !ordinary.ordinaryToken
          ? undefined
          : (enterprise?.env ?? env);
      },
      retain() {
        check();
        if (closeRequested || typeof ordinary.retain !== 'function') fail();
        const settle = ordinary.retain();
        retained++;
        let ended = false;
        return async () => {
          if (ended) return;
          ended = true;
          await settle();
          retained--;
          if (retained === 0) settleWaiting?.();
        };
      },
      async refresh() {
        check();
        if (typeof ports.enterprise.refreshCapability !== 'function')
          fail('common.unavailable');
        const refreshed = await ordinary.refresh();
        check();
        token = refreshed;
        const current = await runtime.capability(
          token,
          sessionId,
          taskId,
          'task.execute',
        );
        check();
        if (current.state.enterpriseCredentialState !== state)
          fail('shared.revoked');
        if (enterprise) await enterprise.refresh();
        else
          env = taskEnvironment(ports, sessionId, token, {
            DSH_AURIGA_ID: '',
            DSH_AURIGA_TOKEN: '',
          });
        check();
        await runtime.capability(token, sessionId, taskId, 'task.execute');
        check();
        return enterprise?.env ?? env;
      },
      close,
    });
  } catch (error) {
    await close();
    throw error;
  }
}

/** Returned variables belong to one trusted task. The host never applies them globally. */
export async function openSharedTask({
  ctx,
  runtime,
  ports,
  execution,
  raw = false,
  ordinary: suppliedOrdinary,
}) {
  if (typeof ports.finishTask !== 'function') fail('common.unavailable');
  const sessionId = executionSession(ctx, execution);
  const binding = await runtime.session(sessionId);
  const settings = ports.config();
  if (
    settings.allowRawAgentToken !== undefined &&
    typeof settings.allowRawAgentToken !== 'boolean'
  )
    fail('common.unavailable');
  const scopes =
    raw && settings.allowRawAgentToken !== false
      ? ['outbound', 'credentials']
      : ['outbound'];
  const ordinary =
    suppliedOrdinary ?? (await acquireOrdinary(ports, binding, execution));
  let token;
  let env;
  let closed = false,
    closing;
  const check = () => {
    ordinary.check?.();
    if (
      closed ||
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== sessionId
    )
      fail();
  };
  const close = () => {
    if (!closing) {
      closed = true;
      env = undefined;
      closing = (async () => {
        try {
          await ports.finishTask(binding, execution, token);
        } finally {
          if (!suppliedOrdinary) await ordinary.close();
        }
      })();
    }
    return closing;
  };
  const load = async (taskId) => {
    check();
    await runtime.capability(token, sessionId, taskId, 'outbound');
    check();
    const credentials = { DSH_AURIGA_ID: '', DSH_AURIGA_TOKEN: '' };
    if (scopes.includes('credentials')) {
      const authorized = await runtime.capability(
        token,
        sessionId,
        taskId,
        'credentials',
      );
      const material = await ports.enterprise.credentials(
        binding,
        authorized.claims,
      );
      check();
      await runtime.capability(token, sessionId, taskId, 'credentials');
      check();
      if (
        material?.credentialVersion !== authorized.claims.credentialVersion ||
        typeof material.empNo !== 'string' ||
        !material.empNo ||
        typeof material.token !== 'string' ||
        !material.token
      )
        fail();
      credentials.DSH_AURIGA_ID = material.empNo;
      credentials['DSH_AURIGA_TOKEN'] = material.token;
    }
    env = taskEnvironment(ports, sessionId, token, credentials);
    return env;
  };
  try {
    token = await ports.issueTask(binding, execution, scopes, ordinary);
    const taskId = await ports.enterprise.taskId(token);
    await load(taskId);
    return Object.freeze({
      binding,
      get token() {
        return closed ? '' : token;
      },
      get env() {
        return closed ? undefined : env;
      },
      async refresh() {
        check();
        if (typeof ports.enterprise.refreshCapability !== 'function')
          fail('common.unavailable');
        const refreshed = await ports.enterprise.refreshCapability(
          token,
          sessionId,
          'outbound',
        );
        check();
        token = refreshed;
        return load(taskId);
      },
      close,
    });
  } catch (error) {
    await close();
    throw error;
  }
}
