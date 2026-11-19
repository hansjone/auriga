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
  const token = await ports.issueExecution(binding, execution);
  let enterprise;
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    const result = await Promise.allSettled([
      enterprise?.close(),
      ports.finishTask(binding, execution, token),
    ]);
    const error = result.find((entry) => entry.status === 'rejected');
    if (error) throw error.reason;
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
        execution: { agent: execution.agent, signal: execution.signal },
      });
      await runtime.capability(token, sessionId, taskId, 'task.execute');
      return Object.freeze({ ...enterprise, close });
    }
    if (state !== 'SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT')
      fail('common.unavailable');
    if (
      execution.signal?.aborted ||
      executionSession(ctx, execution) !== sessionId
    )
      fail();
    return Object.freeze({
      binding,
      token,
      env: taskEnvironment(ports, sessionId, token, {
        DSH_AURIGA_ID: '',
        DSH_AURIGA_TOKEN: '',
      }),
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
  const token = await ports.issueTask(binding, execution, scopes);
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    await ports.finishTask(binding, execution, token);
  };
  try {
    const taskId = await ports.enterprise.taskId(token);
    await runtime.capability(token, sessionId, taskId, 'outbound');
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
      await runtime.capability(token, sessionId, taskId, 'credentials');
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
    const env = taskEnvironment(ports, sessionId, token, credentials);
    return Object.freeze({ binding, token, env, close });
  } catch (error) {
    await close();
    throw error;
  }
}
