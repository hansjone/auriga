import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtemp, writeFile, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { randomUUID, randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { createVault } from '../src/local/vault.js';

const { createTeam } = await import(
  process.env.AURIGA_TEST_TEAM_ENTRY ??
    new URL('../src/local/team.js', import.meta.url)
);

test(
  'actual rc.3 ordinary sessions: bound delivery keeps cwd/identity across repeated input and cold restoration',
  { skip: !process.env.AURIGA_DESKTOP_DSH_ROOT, timeout: 60000 },
  async (t) => {
    const req = createRequire(
      pathToFileURL(join(process.env.AURIGA_DESKTOP_DSH_ROOT, 'package.json')),
    );
    const load = (name) =>
      import(pathToFileURL(req.resolve('@deepseek-ai/' + name)).href);
    const { boot, initProfile, readProfilePatches } =
      await load('dsh-app-boot');
    assert.equal(
      JSON.parse(
        await readFile(
          req.resolve('@deepseek-ai/dsh-api-session-controller/package.json'),
          'utf8',
        ),
      ).version,
      '0.2.1-rc.3',
    );
    const home = await mkdtemp(join(tmpdir(), 'auriga-session-native-')),
      dir = join(home, 'profile');
    console.log('ISOLATED_SESSION_PROFILE', home);
    initProfile(dir, []);
    await writeFile(join(dir, 'cordis.yml'), '[]\n');
    const profile = {
      name: 'session-native',
      dir,
      home,
      cwd: home,
      installAnchor: join(home, 'package.json'),
      patchPath: join(dir, 'cordis.patch.yml'),
      overlays: [],
      startedBundles: [],
    };
    await writeFile(
      profile.installAnchor,
      '{"name":"isolated-session","dependencies":{}}',
    );
    async function host() {
      const ctx = await boot(
        profile.name,
        join(dir, 'cordis.yml'),
        readProfilePatches(profile.name, profile),
        (root) => {
          root.provide('profileContext', profile);
          root.provide('appReady', {
            onReady(fn) {
              fn();
              return () => {};
            },
          });
        },
      );
      try {
        const modules = [
          ['dsh-system-prompt'],
          ['dsh-session-projection'],
          ['dsh-session'],
          ['dsh-llm'],
          ['dsh-tools'],
          ['dsh-agent'],
          [
            'dsh-agent-default-model',
            { provider: 'unconfigured-native-test', model: 'no-network-model' },
          ],
          [
            'dsh-session-persistence-jsonl',
            { root: join(home, 'sessions'), compression: 'none' },
          ],
          ['dsh-session-query-sqlite', { path: ':memory:', openAt: 'never' }],
          ['dsh-storage'],
          ['dsh-storage-json', { root: join(home, 'storage') }],
          ['dsh-storage-domain', { backend: 'json' }],
          ['dsh-workspace'],
          ['dsh-typert-registry'],
          ['dsh-api-remote-access'],
          ['dsh-attachment-local', { dshHome: home }],
          ['dsh-fs-local', { cwd: home }],
          ['dsh-commands'],
          ['dsh-host-webserver', { host: '127.0.0.1', port: 0 }],
          [
            'dsh-credentials-local',
            { path: join(home, 'credentials.yaml'), watch: false },
          ],
          ['dsh-client-connection'],
          ['dsh-client-file-upload'],
          ['dsh-agent-loop', { agents: [] }],
          ['dsh-api-session-controller', {}],
        ];
        for (const [name, config] of modules) {
          const mod = await load(name);
          await ctx.plugin(mod.default ?? mod, config);
        }
        const deadline = Date.now() + 5000;
        while (!ctx.get('sessionController')) {
          assert.ok(
            Date.now() < deadline,
            'real native controller did not activate',
          );
          await delay(10);
        }
        assert.deepEqual(
          ctx.get('llm').listProviders(),
          [],
          'test must not load a paid/model provider',
        );
        return ctx;
      } catch (error) {
        await ctx.fiber.dispose();
        throw error;
      }
    }
    let ctx, vault, team;
    const endpoint = randomUUID(),
      threadId = randomUUID(),
      bindingId = randomUUID(),
      acknowledgements = [],
      binds = [],
      boundIds = new Map(),
      allowedThreads = new Set([threadId]);
    let failBind = false,
      interruptDelivery;
    // Explicit private transport boundary; no IAM/remote Team success is claimed.
    const platform = {
      async request(path, { body }) {
        if (path === '/ai/team/node/bind') {
          assert.equal(body.endpointId, endpoint);
          assert.ok(allowedThreads.has(body.threadId));
          assert.equal(
            ctx.get('sessions').get(body.hostSessionId)?.id,
            body.hostSessionId,
          );
          binds.push(body);
          if (failBind) {
            failBind = false;
            throw new Error('test transport unavailable before binding');
          }
          const actualBinding =
            boundIds.get(body.hostSessionId) ?? randomUUID();
          boundIds.set(body.hostSessionId, actualBinding);
          return {
            binding: {
              id: actualBinding,
              endpointId: endpoint,
              hostSessionId: body.hostSessionId,
            },
          };
        }
        assert.equal(path, '/ai/team/node/ack');
        assert.equal(body.endpointId, endpoint);
        assert.equal(body.bindingId, boundIds.get(body.hostSessionId));
        assert.equal(body.hostRequestId, body.deliveryId);
        acknowledgements.push(body);
        return { delivery: { id: body.deliveryId, status: 'host_accepted' } };
      },
    };
    function delivery(text, targetThread = threadId) {
      return {
        id: randomUUID(),
        targetEndpointId: endpoint,
        leaseToken: randomBytes(24).toString('base64url'),
        leaseExpiresAt: new Date(Date.now() + 60000).toISOString(),
        message: { id: randomUUID(), threadId: targetThread, text },
      };
    }
    function connect() {
      return createTeam({
        platform,
        vault,
        getAccess: async () => 'transport-boundary-only',
        assertActive() {
          if (
            interruptDelivery &&
            vault.snapshot().deliveries[interruptDelivery]?.accepted === false
          ) {
            interruptDelivery = undefined;
            throw new Error('test interruption before prompt');
          }
        },
        ctx,
        label: 'native-session-test',
      });
    }
    async function settled(id) {
      const deadline = Date.now() + 3000;
      while (ctx.get('agents').get(id)?.status === 'running') {
        assert.ok(
          Date.now() < deadline,
          'unconfigured native turn did not settle',
        );
        await delay(5);
      }
    }
    function occurrences(id, requestId) {
      const agent = ctx.get('agents').get(id);
      return (
        agent.session
          .snapshotEvents()
          .filter(
            (e) =>
              e.type === 'user/message' && e.data.source.rpcId === requestId,
          ).length +
        agent.inbox.nextTurn.filter((m) => m.source.rpcId === requestId)
          .length +
        agent.inbox.nextStep.filter((m) => m.source.rpcId === requestId).length
      );
    }
    try {
      ctx = await host();
      const controller = ctx.get('sessionController'),
        sessions = ctx.get('sessions');
      const created = await controller.create({
          cwd: join(home, 'non-default-project'),
        }),
        id = created.sessionId;
      boundIds.set(id, bindingId);
      assert.ok(id.startsWith('session-'));
      assert.equal(
        sessions.get(id).header.cwd,
        join(home, 'non-default-project'),
      );
      assert.equal(await sessions.flush(sessions.get(id)), true);
      await assert.rejects(
        controller.create({ sessionId: id }),
        (error) => error.code === 'session/conflict',
      );
      vault = await createVault(join(dir, 'auriga-connect'));
      await vault.update((state) => {
        state.endpoint = endpoint;
        state.threads[threadId] = { sessionId: id, bindingId };
      });
      team = connect();
      const first = delivery('first native queue input'),
        second = delivery('second native queue input');
      await t.test('bound non-default cwd and second delivery', async () => {
        await team.receive(first);
        await team.receive(second);
        await settled(id);
        assert.equal(occurrences(id, first.id), 1);
        assert.equal(occurrences(id, second.id), 1);
        assert.equal(acknowledgements.length, 2);
        assert.equal(binds.length, 0);
        for (const ack of acknowledgements) assert.equal(ack.hostSessionId, id);
        assert.equal(
          sessions.get(id).header.cwd,
          join(home, 'non-default-project'),
        );
        assert.equal(sessions.list().length, 1);
      });
      team.close();
      team = undefined;
      vault.close();
      vault = undefined;
      await ctx.fiber.dispose();
      ctx = undefined;
      ctx = await host();
      vault = await createVault(join(dir, 'auriga-connect'));
      team = connect();
      await t.test(
        'cold native prompt restores original session and persisted request dedup',
        async () => {
          assert.equal(ctx.get('sessions').get(id), undefined);
          assert.equal(ctx.get('agents').get(id), undefined);
          const third = delivery('cold native queue input');
          await team.receive(third);
          await settled(id);
          assert.equal(
            ctx.get('sessions').get(id).header.cwd,
            join(home, 'non-default-project'),
          );
          assert.equal(acknowledgements.at(-1).hostSessionId, id);
          await team.receive({
            ...first,
            leaseToken: randomBytes(24).toString('base64url'),
          });
          // Simulate a crash after native flush, before the vault accepted marker.
          await vault.update((state) => {
            state.deliveries[first.id].accepted = false;
          });
          await team.receive({
            ...first,
            leaseToken: randomBytes(24).toString('base64url'),
          });
          assert.equal(occurrences(id, first.id), 1);
          assert.equal(occurrences(id, third.id), 1);
          assert.equal(acknowledgements.at(-1).hostSessionId, id);
          assert.equal(ctx.get('sessions').list().length, 1);
          assert.equal(binds.length, 0);
        },
      );
      await t.test(
        'unbound creation intent survives restart before binding, then second input reuses native identity',
        async () => {
          const incoming = randomUUID();
          allowedThreads.add(incoming);
          const pending = delivery(
            'new receiver after transport failure',
            incoming,
          );
          failBind = true;
          const before = acknowledgements.length;
          await assert.rejects(
            team.receive(pending),
            /transport unavailable before binding/,
          );
          const intent = vault.snapshot().threads[incoming];
          assert.equal(intent.bindingId, null);
          assert.equal(
            ctx.get('sessions').get(intent.sessionId)?.id,
            intent.sessionId,
          );
          assert.equal(acknowledgements.length, before);
          assert.equal(vault.snapshot().deliveries[pending.id], undefined);
          team.close();
          team = undefined;
          vault.close();
          vault = undefined;
          await ctx.fiber.dispose();
          ctx = undefined;
          ctx = await host();
          vault = await createVault(join(dir, 'auriga-connect'));
          team = connect();
          assert.equal(ctx.get('sessions').get(intent.sessionId), undefined);
          await team.receive(pending);
          await settled(intent.sessionId);
          await team.receive(delivery('second new receiver input', incoming));
          await settled(intent.sessionId);
          assert.equal(
            vault.snapshot().threads[incoming].sessionId,
            intent.sessionId,
          );
          assert.equal(
            vault.snapshot().threads[incoming].bindingId,
            boundIds.get(intent.sessionId),
          );
          assert.deepEqual(
            acknowledgements.slice(before).map((row) => row.hostSessionId),
            [intent.sessionId, intent.sessionId],
          );
          assert.equal(occurrences(intent.sessionId, pending.id), 1);
        },
      );
      await t.test(
        'durable binding with interruption before prompt cold-resumes the flushed native session',
        async () => {
          const incoming = randomUUID();
          allowedThreads.add(incoming);
          const pending = delivery(
              'binding committed but prompt not yet invoked',
              incoming,
            ),
            before = acknowledgements.length;
          interruptDelivery = pending.id;
          await assert.rejects(
            team.receive(pending),
            /test interruption before prompt/,
          );
          const bound = vault.snapshot().threads[incoming];
          assert.ok(bound.bindingId);
          assert.equal(vault.snapshot().deliveries[pending.id].accepted, false);
          assert.equal(occurrences(bound.sessionId, pending.id), 0);
          assert.equal(acknowledgements.length, before);
          team.close();
          team = undefined;
          vault.close();
          vault = undefined;
          await ctx.fiber.dispose();
          ctx = undefined;
          ctx = await host();
          vault = await createVault(join(dir, 'auriga-connect'));
          team = connect();
          assert.equal(ctx.get('sessions').get(bound.sessionId), undefined);
          const bindCount = binds.length;
          await team.receive(pending);
          await settled(bound.sessionId);
          assert.equal(binds.length, bindCount);
          assert.equal(occurrences(bound.sessionId, pending.id), 1);
          assert.equal(acknowledgements.at(-1).hostSessionId, bound.sessionId);
        },
      );
      await t.test(
        'ordinary routing still rejects real subagent-origin ownership without ACK',
        async () => {
          const childId = 'child-' + randomUUID(),
            childThread = randomUUID();
          const child = await ctx.get('agents').create({
            sessionId: childId,
            meta: { origin: 'subagent', cwd: home },
            agentOptions: {
              provider: 'unconfigured-native-test',
              model: 'no-network-model',
            },
          });
          assert.equal(child.agent.session.header.origin, 'subagent');
          await vault.update((state) => {
            state.threads[childThread] = {
              sessionId: childId,
              bindingId: randomUUID(),
            };
          });
          const before = acknowledgements.length;
          await assert.rejects(
            team.receive(
              delivery('child must use native subagent delivery', childThread),
            ),
            (error) => error.code === 'session/agent-busy',
          );
          assert.equal(acknowledgements.length, before);
        },
      );
      await t.test(
        'native create can return a different ID for a started session; pending mapping cannot adopt it',
        async () => {
          const ordinary = await ctx.get('sessionController').create({});
          await ctx.get('sessionController').prompt(
            {
              sessionId: ordinary.sessionId,
              requestId: randomUUID(),
              content: [
                { type: 'text', text: 'native started-session semantics' },
              ],
              mode: 'queue',
            },
            new AbortController().signal,
          );
          await settled(ordinary.sessionId);
          const renamed = await ctx
            .get('sessionController')
            .create({ sessionId: ordinary.sessionId });
          assert.notEqual(renamed.sessionId, ordinary.sessionId);
          const pendingThread = randomUUID();
          await vault.update((state) => {
            state.threads[pendingThread] = {
              sessionId: ordinary.sessionId,
              bindingId: null,
            };
          });
          const before = acknowledgements.length,
            bindCount = binds.length;
          await assert.rejects(
            team.receive(
              delivery('must not adopt replacement identity', pendingThread),
            ),
            /DSH session identity mismatch/,
          );
          assert.equal(
            vault.snapshot().threads[pendingThread].sessionId,
            ordinary.sessionId,
          );
          assert.equal(vault.snapshot().threads[pendingThread].bindingId, null);
          assert.equal(acknowledgements.length, before);
          assert.equal(binds.length, bindCount);
        },
      );
      console.log(
        'RC3_SESSION_RECOVERY_PASS: real controller/AgentLoop/JSONL/query, no configured model provider or paid request; transport boundary only',
      );
    } finally {
      team?.close();
      vault?.close();
      await ctx?.fiber.dispose();
      console.log('SESSION_PROFILE_EVIDENCE_RETAINED', home);
    }
  },
);
