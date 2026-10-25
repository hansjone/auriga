import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import {
  hasNativePromptRequest,
  isNativeSubagentOwned,
} from '../../../../src/network/host/native/upstream.js';
import { createHostPortOperations } from '../../../../src/network/host/native/host-port.js';
import { fixture } from './fixtures.js';

test('host baseline follows the real-dsh lockfile instead of a desktop release', async () => {
  const lock = JSON.parse(
    await readFile(
      new URL(
        '../../../../../../deploy/compose/real-dsh/package-lock.json',
        import.meta.url,
      ),
      'utf8',
    ),
  );
  assert.equal(
    '0.2.0-rc.2',
    lock.packages['node_modules/@deepseek-ai/dsh'].version,
  );
});

test('public Inbox contract de-duplicates pending user rpcId and rejects malformed lists', () => {
  const agent = { inbox: { nextTurn: [], nextStep: [] } };
  for (const queue of ['nextTurn', 'nextStep']) {
    agent.inbox[queue].push({ source: { kind: 'user', rpcId: 'r' } });
    assert.equal(hasNativePromptRequest(agent, 'r'), true);
    agent.inbox[queue].length = 0;
  }
  agent.inbox.nextTurn.push({ source: { kind: 'system', rpcId: 'r' } });
  assert.equal(hasNativePromptRequest(agent, 'r'), false);
  delete agent.inbox.nextStep;
  assert.throws(() => hasNativePromptRequest(agent, 'r'), {
    code: 'network.host_incompatible',
  });
});

test('parentSession alone is a fork; native live ownership determines child routing', () => {
  const f = fixture();
  const agent = f.attach('session-a');
  agent.session.header.parentSession = 'parent';
  f.seed('parent');
  const parent = f.attach('parent');
  f.ctx.agents.isOwnedBy = (id, owner) => id === agent.id && owner === parent;
  assert.equal(isNativeSubagentOwned(f.ctx, agent.session.header, agent), true);
  f.ctx.agents.isOwnedBy = () => false;
  assert.equal(
    isNativeSubagentOwned(f.ctx, agent.session.header, agent),
    false,
  );
});

test('new null-preset resolves the actual default; existing null-preset does not change its preset', async () => {
  const f = fixture();
  f.ctx.agentPresets.resolve = async (id) => ({ id: id ?? 'standard' });
  const host = await createHostPortOperations(f);
  await host.createSession({
    id: 'new-default',
    cwd: '/work/project',
    preset: null,
  });
  assert.equal(
    f.calls.find(([kind]) => kind === 'create')[1].agentPreset,
    'standard',
  );
  await host.createSession({
    id: 'new-default',
    cwd: '/work/project',
    preset: null,
  });
  assert.equal(f.calls.filter(([kind]) => kind === 'create').length, 1);
});

test('preset adoption checks the active projection rather than the stale creation header', async () => {
  const f = fixture();
  f.seed('preset-session', { agentPreset: 'old' });
  f.attach('preset-session');
  f.presets.set('preset-session', 'current');
  const host = await createHostPortOperations(f);
  await host.createSession({
    id: 'preset-session',
    cwd: '/work/project',
    preset: 'current',
  });
  await assert.rejects(
    host.createSession({
      id: 'preset-session',
      cwd: '/work/project',
      preset: 'old',
    }),
    { code: 'host.session_not_ready' },
  );
});
