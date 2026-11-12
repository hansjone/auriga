// Test-only plugin loaded by the real isolated DSH CLI. No fake host services.
import { writeFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { BASE_TOOL_NAMES } from '../../src/network/base-tools.js';
export const name = 'auriga-base-tools-real-host-probe';
export const inject = [
  'tools',
  'sessionController',
  'sessionPersistence',
  'agents',
  'sessions',
  'workspaceRegistry',
  'profileContext',
];
export async function apply(ctx) {
  const output = process.env.AURIGA_BASE_PROBE_RESULT;
  if (!output) throw new Error('probe output required');
  // Defer until the loader has mounted the plugin and the profile settings read settles.
  const run = async () => {
    const deadline = Date.now() + 20000;
    while (
      !BASE_TOOL_NAMES.every((name) =>
        ctx.tools.schemas().some((tool) => tool.name === name),
      )
    ) {
      if (Date.now() > deadline)
        throw new Error('base tool schemas unavailable');
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
    const created = await ctx.sessionController.create({
      cwd: process.env.AURIGA_BASE_PROBE_WORKSPACE,
      agentPreset: 'standard',
    });
    const resolved = await ctx.sessionController.resolveAgent(
      created.sessionId,
    );
    if (resolved.error || !resolved.agent)
      throw new Error('native agent unavailable');
    const agent = resolved.agent;
    const tools = ctx.tools
      .schemas(agent)
      .filter((tool) => tool.name.startsWith('auriga_net_'))
      .map((tool) => tool.name);
    const execute = async (name) => {
      const result = await ctx.tools.execute({
        agent,
        name,
        arguments: {},
        callId: randomUUID(),
        signal: new AbortController().signal,
      });
      if (result.isError) throw new Error(JSON.stringify(result));
      return JSON.parse(
        result.content.find((block) => block.type === 'text').text,
      );
    };
    const initial = await execute('auriga_net_status');
    const diagnose = await execute('auriga_net_diagnose');
    const after = await execute('auriga_net_status');
    const identity = await execute('auriga_net_identity');
    return {
      dsh: '0.2.1-rc.3',
      realCli: true,
      realToolRuntime: true,
      realSessionController: true,
      realAgent: true,
      modelVisibleTools: tools,
      initial,
      diagnose,
      after,
      identity,
    };
  };
  // Yield startup while probing; no steady-state timer is added.
  void run().then(
    (value) =>
      writeFile(output, JSON.stringify({ ok: true, ...value }, null, 2)),
    (error) =>
      writeFile(
        output,
        JSON.stringify(
          { ok: false, error: String(error), stack: error.stack },
          null,
          2,
        ),
      ),
  );
}
