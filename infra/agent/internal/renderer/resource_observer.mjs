import { readFileSync } from 'node:fs';
import { timingSafeEqual } from 'node:crypto';

export const name = 'auriga-node-resource-observer';
export const inject = ['webServer', 'skills', 'sessionController', 'agentPresets'];
export async function apply(ctx, config) {
  const policy = JSON.parse(readFileSync(config.policyFile, 'utf8'));
  if (policy.schemaVersion !== 1 || typeof policy.newSessionRequired !== 'boolean') throw new Error('Invalid node session policy');
  const permitted = new Set();
  if (policy.newSessionRequired) {
    const controller = ctx.sessionController;
    if (typeof controller.create !== 'function' || typeof ctx.on !== 'function') throw new Error('Private session gate unavailable');
    const original = controller.create;
    const create = async function (...args) { const result = await original.apply(controller, args); if (typeof result?.sessionId === 'string') permitted.add(result.sessionId); return result; };
    controller.create = create;
    ctx.effect(() => () => { if (controller.create === create) controller.create = original; }, 'auriga-node-private-session-gate');
    for (const method of ['prompt', 'fork', 'updateQueue']) {
      const native = controller[method]; if (typeof native !== 'function') throw new Error('Private session admission API unavailable');
      const gated = async function (request, ...args) { if (!permitted.has(request?.sessionId)) throw new Error('Private-only new session required'); const result = await native.call(controller, request, ...args); if (method === 'fork' && typeof result?.sessionId === 'string') permitted.add(result.sessionId); return result; };
      controller[method] = gated;
      ctx.effect(() => () => { if (controller[method] === gated) controller[method] = native; }, 'auriga-node-private-' + method);
    }
    ctx.on('agent/pre-step', ({ agent }, next) => { if (!permitted.has(agent?.session?.id)) throw new Error('共享技能已停用，请新建会话；原历史仍可查看。 Shared skills disabled; create a new session.'); return next(); });
    ctx.on('llm/stream', (options, next) => { if (!permitted.has(options.sessionId)) throw new Error('Private-only new session required'); return next(); });
    ctx.on('tools/execute', (exec, next) => { if (!permitted.has(exec.agent?.session?.id)) throw new Error('Private-only new session required'); return next(); });
  }
  if (typeof ctx.agentPresets?.acquireScope !== 'function' || typeof ctx.skills?.list !== 'function' || typeof ctx.skills?.get !== 'function') throw new Error('Native skill catalog unavailable');
  ctx.effect(() => ctx.webServer.register({ kind: 'prefix', path: '/auriga-node-resource-catalog', async handler(req, res) {
    const expected = Buffer.from('Bearer ' + readFileSync(config.tokenFile, 'utf8').trim());
    const supplied = Buffer.from(typeof req.headers.authorization === 'string' ? req.headers.authorization : '');
    if (req.method !== 'GET' || req.url !== '/auriga-node-resource-catalog' || expected.length !== supplied.length || !timingSafeEqual(expected, supplied)) { res.writeHead(403); res.end(); return; }
    try {
      const skills = [];
      // Public standing preset scopes need no Agent or Session activation.
      for (const preset of ['cordis', 'standard', 'ptc']) {
        const lease = await ctx.agentPresets.acquireScope(preset);
        try {
          if (!lease?.key) throw new Error('Native preset scope unavailable');
          const options = { cwd: process.cwd(), scope: lease.key };
          for (const row of await ctx.skills.list(options)) { const loaded = await ctx.skills.get(row.name, options); skills.push({ name: row.name, provider: row.provider, path: row.path, content: loaded?.content, preset }); }
        } finally { await lease?.[Symbol.asyncDispose]?.(); }
      }
      res.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'no-store' }); res.end(JSON.stringify({ privateOnly: policy.newSessionRequired, skills }));
    } catch { res.writeHead(503, { 'cache-control': 'no-store' }); res.end(); }
  }}), 'auriga-node-resource-observer');
}
