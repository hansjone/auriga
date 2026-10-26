// Run only with an explicit installed rc.2 anchor and an isolated probe home.
// This does not load a user profile or start a DSH process.
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const anchor = process.env.AURIGA_RELAY_DSH_ANCHOR;
const home = process.env.AURIGA_RELAY_PROBE_HOME;
assert(
  anchor && home,
  'explicit installed SDK anchor and isolated home required',
);
const req = createRequire(anchor);
const profileDir = join(home, 'profiles', 'isolated');
mkdirSync(profileDir, { recursive: true });
writeFileSync(
  join(profileDir, 'package.json'),
  JSON.stringify({
    name: 'auriga-relay-device-probe',
    version: '0.0.0',
    private: true,
    type: 'module',
    dsh: { profile: { bundles: [] } },
  }),
);
const { createRuntimeResolution, loadProfileDirectory, PluginPackages } =
  await import(pathToFileURL(req.resolve('@deepseek-ai/dsh-app-boot')));
const { Context } = await import(
  pathToFileURL(req.resolve('@deepseek-ai/cordis'))
);
const hostApi = await import(
  pathToFileURL(req.resolve('@deepseek-ai/dsh-llm'))
);
const profile = loadProfileDirectory('relay-probe', profileDir, anchor, {
  userLayer: false,
});
const resolution = await createRuntimeResolution({
  installAnchor: anchor,
  profile,
  home,
});
const selected = resolution.entries.find(
  (e) => e.name === '@deepseek-ai/dsh-llm',
);
assert.equal(selected?.version, '0.2.0-rc.2');
const { registerAurigaAdapter } = await import(
  pathToFileURL(
    resolve(import.meta.dirname, '../../clients/dsh-plugin/src/ai/index.js'),
  )
);
const ctx = new Context();
let packagesFiber, llmFiber, registration;
try {
  packagesFiber = await ctx.plugin(PluginPackages, { resolution });
  llmFiber = await ctx.plugin(hostApi.LlmRuntime);
  const revoked = new AbortController();
  const token = process.env.AURIGA_RELAY_PROBE_TOKEN;
  const origin = process.env.AURIGA_RELAY_PROBE_URL;
  assert(token && origin);
  const lease = {
    token,
    expiresAt: Date.now() + 30000,
    signal: revoked.signal,
  };
  for (const mode of ['json', 'sse']) {
    registration = registerAurigaAdapter(ctx.llm, {
      hostApi,
      apiBase: origin,
      fetch,
      getAccessToken: async () => lease,
      responseMode: mode,
      modelLimits: {
        synthetic: { maxOutputTokens: 512, contextWindow: 4096, tools: true },
      },
      // Only the synthetic test provider defines this exact complete-input bound.
      countInputTokens: () => 32,
    });
    const models = await ctx.llm.listModels('auriga');
    assert(models.some((m) => m.id === 'synthetic'));
    const chunks = [];
    for await (const c of ctx.llm.stream({
      provider: 'auriga',
      model: 'synthetic',
      maxTokens: 16,
      messages: [
        hostApi.createUserMessage({
          source: { kind: 'user' },
          content: [{ type: 'text', text: 'synthetic-only' }],
        }),
      ],
    }))
      chunks.push(c);
    const finish = chunks.findLast((c) => c.type === 'finish');
    assert(finish, 'installed Runtime did not finish');
    assert.notEqual(finish.reason?.kind, 'error', JSON.stringify(finish));
    assert.notEqual(finish.reason?.kind, 'aborted');
    assert(chunks.some((c) => JSON.stringify(c).includes('ok')));
    console.log(
      JSON.stringify({
        mode,
        version: selected.version,
        chunks: chunks.length,
        realRuntime: true,
        realHTTP: true,
        transport: 'synthetic-fake',
      }),
    );
    registration.dispose();
    registration = undefined;
  }
} finally {
  registration?.dispose();
  await llmFiber?.dispose();
  await packagesFiber?.dispose();
}
