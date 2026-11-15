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
    let env;
    if (ports.helperBase) {
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
      env = Object.freeze({
        DSH_SESSION_ID: sessionId,
        DSH_TEAM_TASK_CAPABILITY: token,
        DSH_TEAM_BASE: base.href,
        DSH_WEB_URL: base.origin,
        DSH_TEAM_HELPERS: fileURLToPath(
          new URL('./skill-helpers/python/', import.meta.url),
        ),
      });
    }
    return Object.freeze({ binding, token, env, close });
  } catch (error) {
    await close();
    throw error;
  }
}
