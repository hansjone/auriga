import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { refuse } from './trust.js';
import { createHash } from 'node:crypto';
/** Explicit new-session observation uses public DSH services and scopeOf export.
 * A missing capability cannot be turned into a successful installation report.
 * @param {any} ctx */
export function catalogHost(ctx) {
  return {
    /** @param {string} name @param {string} preset */
    async observe(name, preset = 'standard') {
      if (
        !['cordis', 'standard', 'ptc'].includes(preset) ||
        !ctx?.sessionController?.create ||
        !ctx?.sessionController?.resolveAgent ||
        !ctx?.skills?.list ||
        !ctx?.skills?.get ||
        !process.argv[1]
      )
        refuse('resource.apply_failed');
      const require = createRequire(resolve(process.argv[1]));
      const { scopeOf } = await import(
        pathToFileURL(require.resolve('@deepseek-ai/dsh-scope')).href
      );
      const session = await ctx.sessionController.create({
        cwd: process.cwd(),
        agentPreset: preset,
      });
      const resolved = await ctx.sessionController.resolveAgent(
        session.sessionId,
      );
      if (!resolved.agent || resolved.error) refuse('resource.apply_failed');
      const scope = scopeOf(resolved.agent.ctx);
      if (!scope) refuse('resource.apply_failed');
      const options = { cwd: process.cwd(), scope },
        rows = await ctx.skills.list(options),
        found = rows.find((/** @type {any} */ row) => row.name === name);
      const loaded = await ctx.skills.get(name, options);
      if (!found || !loaded)
        return { state: 'pending_restart', sessionId: session.sessionId };
      return {
        state: 'observed',
        provider: found.provider,
        path: found.path,
        sessionId: session.sessionId,
        contentDigest:
          'sha256:' +
          createHash('sha256')
            .update(String(loaded.content).trim())
            .digest('hex'),
      };
    },
  };
}
