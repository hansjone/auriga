import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
const require = createRequire(process.env.P1K_DSH_CLI);
const { scopeOf } = await import(
  pathToFileURL(require.resolve('@deepseek-ai/dsh-scope')).href
);
export const name = 'p1k-real-skill-probe';
export const inject = ['skills', 'agentPresets', 'sessionController'];
export function apply(ctx) {
  const run = async () => {
    const results = [];
    for (const preset of ['cordis', 'standard', 'ptc']) {
      const metadata = await ctx.agentPresets.resolve(preset);
      assert(!metadata.broken, JSON.stringify(metadata));
      const session = await ctx.sessionController.create({
        cwd: process.env.P1K_WORKSPACE,
        agentPreset: preset,
      });
      const resolved = await ctx.sessionController.resolveAgent(
        session.sessionId,
      );
      assert(resolved.agent, JSON.stringify(resolved.error));
      const scope = scopeOf(resolved.agent.ctx);
      assert(scope, 'real preset scope missing');
      const options = { cwd: process.env.P1K_WORKSPACE, scope };
      const catalog = await ctx.skills.list(options);
      const skills = [];
      for (const skill of catalog) {
        const loaded = await ctx.skills.get(skill.name, options);
        skills.push({
          name: skill.name,
          provider: skill.provider,
          path: skill.path,
          content: loaded?.content,
        });
      }
      results.push({ preset, sessionId: session.sessionId, skills });
    }
    return {
      ok: true,
      version: '0.2.1-rc.3',
      realCli: true,
      realAgents: true,
      results,
    };
  };
  // Avoid waiting for loader settlement from inside the probe's own activation.
  setImmediate(
    () =>
      void run().then(
        (value) =>
          writeFile(process.env.P1K_RESULT, JSON.stringify(value, null, 2)),
        (error) =>
          writeFile(
            process.env.P1K_RESULT,
            JSON.stringify(
              { ok: false, error: String(error), stack: error.stack },
              null,
              2,
            ),
          ),
      ),
  );
}
