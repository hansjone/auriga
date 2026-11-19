// @ts-nocheck
import { posix } from 'node:path';
import { canonical, fail } from './protocol.js';
import { executionSession } from './task-context.js';

// Agent configurePool mounts the verified active view here read-only. Host
// paths from PoolLayout are not paths inside the DSH container.
export const PLATFORM_SKILL_ROOT = '/run/auriga/skills-active/current';
export const isPlatformSkillRoots = (roots) =>
  Array.isArray(roots) &&
  roots.length === 1 &&
  roots[0] === PLATFORM_SKILL_ROOT;

function platformDefinition(skill, name) {
  const path = skill?.path,
    base = skill?.resourceBase;
  if (
    skill?.name !== name ||
    skill.provider !== 'auriga-shared' ||
    skill.source !== 'custom' ||
    typeof skill.content !== 'string' ||
    typeof path !== 'string' ||
    !path.startsWith(PLATFORM_SKILL_ROOT + '/') ||
    /[\\\0]/.test(path) ||
    path.split('/').some((part) => part === '.' || part === '..') ||
    posix.normalize(path) !== path ||
    base?.kind !== 'directory' ||
    base.path !== posix.dirname(path)
  )
    fail('common.forbidden');
}

/** Keep the ORIGINAL rc.3 skill tool/registry/provider. The public execute
 * waterfall proves that its chosen definition and returned body belong to
 * the fixed platform view; a same-name runtime/private provider cannot win. */
export function installSharedSkillGate(ctx, { runtime, assertProfile }) {
  if (typeof ctx.on !== 'function') return () => {};
  return ctx.on('tools/execute', async (exec, next) => {
    if (exec.name !== 'skill') return next();
    const check = () => {
      exec.signal?.throwIfAborted();
      if (assertProfile() !== true) fail('common.unavailable');
      if (executionSession(ctx, exec) !== sessionId)
        fail('shared.binding_mismatch');
    };
    const sessionId = executionSession(ctx, exec);
    check();
    if (
      !exec.arguments ||
      Object.keys(exec.arguments).join(',') !== 'name' ||
      typeof exec.arguments.name !== 'string' ||
      !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(exec.arguments.name) ||
      typeof ctx.skills?.get !== 'function' ||
      typeof ctx.workingDirectory?.ensure !== 'function'
    )
      fail('common.validation_failed');
    const before = await runtime.session(sessionId);
    check();
    const cwd = await ctx.workingDirectory.ensure(exec.agent, exec.signal);
    check();
    const definition = await ctx.skills.get(exec.arguments.name, {
      cwd,
      scope: exec.agent,
      signal: exec.signal,
    });
    check();
    platformDefinition(definition, exec.arguments.name);
    const result = await next();
    check();
    const after = await runtime.session(sessionId);
    check();
    if (canonical(before) !== canonical(after)) fail('shared.binding_mismatch');
    if (result.isError) return result;
    if (
      result.value?.name !== definition.name ||
      result.value.provider !== definition.provider ||
      canonical(result.value.resourceBase) !==
        canonical(definition.resourceBase) ||
      result.value.content !== definition.content
    )
      fail('common.forbidden');
    return result;
  });
}
