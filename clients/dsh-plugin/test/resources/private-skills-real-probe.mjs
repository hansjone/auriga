import { writeFile } from 'node:fs/promises';
import { setImmediate } from 'node:timers';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
const require = createRequire(process.env.P1C_DSH_CLI);
const { scopeOf } = await import(
  pathToFileURL(require.resolve('@deepseek-ai/dsh-scope')).href
);
export const name = 'auriga-private-real-probe';
export const inject = ['skills', 'agentPresets', 'sessionController'];
export function apply(ctx) {
  const run = async () => {
    const results = [];
    for (const preset of ['cordis', 'standard', 'ptc']) {
      const metadata = await ctx.agentPresets.resolve(preset);
      if (metadata.broken) throw new Error('broken preset: ' + preset);
      const session = await ctx.sessionController.create({
        cwd: process.env.P1C_WORKSPACE,
        agentPreset: preset,
      });
      const resolved = await ctx.sessionController.resolveAgent(
        session.sessionId,
      );
      if (!resolved.agent) throw new Error('agent unavailable: ' + preset);
      const options = {
        cwd: process.env.P1C_WORKSPACE,
        scope: scopeOf(resolved.agent.ctx),
      };
      const catalog = await ctx.skills.list(options);
      const found = catalog.find((s) => s.name === 'p1c-collision');
      const loaded = await ctx.skills.get('p1c-collision', options);
      results.push({
        preset,
        names: catalog.map((s) => s.name),
        found: found && {
          name: found.name,
          provider: found.provider,
          path: found.path,
        },
        content: loaded?.content,
      });
    }
    return { ok: true, results };
  };
  setImmediate(
    () =>
      void run().then(
        (value) =>
          writeFile(process.env.P1C_RESULT, JSON.stringify(value, null, 2)),
        (error) =>
          writeFile(
            process.env.P1C_RESULT,
            JSON.stringify({
              ok: false,
              error: String(error),
              stack: error.stack,
            }),
          ),
      ),
  );
}
