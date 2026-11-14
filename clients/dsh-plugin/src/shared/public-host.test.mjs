/* global AbortSignal */
import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { installSharedHost } from './host.js';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { resolve, sep } from 'node:path';

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
test(
  'public ToolRuntime guard prevents body execution after pre-execute allow',
  { skip: !root },
  async () => {
    const hostRequire = createRequire(join(root, 'package.json'));
    const { Context } = await import(
      pathToFileURL(hostRequire.resolve('@deepseek-ai/cordis')).href
    );
    const { ToolRuntime } = await import(
      pathToFileURL(hostRequire.resolve('@deepseek-ai/dsh-tools')).href
    );
    const { default: SystemPrompt } = await import(
      pathToFileURL(hostRequire.resolve('@deepseek-ai/dsh-system-prompt')).href
    );
    const ctx = new Context();
    let installed;
    try {
      new SystemPrompt(ctx, {});
      const tools = new ToolRuntime(ctx, { mode: 'native' });
      ctx.provide('remoteAccess', { registerPolicy: () => () => {} });
      ctx.provide('webServer', { register: () => () => {} });
      let executed = 0;
      const unregister = tools.register({
        name: 'shared_shell_canary',
        description: 'guard canary',
        parameters: {
          type: 'object',
          properties: {},
          additionalProperties: false,
        },
        output: {
          schema: { type: 'string' },
          render: (_args, value) => [{ type: 'text', text: value }],
        },
        execute: async () => {
          executed++;
          return 'unexpected';
        },
      });
      installed = installSharedHost(ctx, {
        runtime: { bindings: () => [], close() {} },
        enterprise: { handler() {} },
        ports: { createPeer() {}, assertProfile: () => true },
      });
      let allowed = 0;
      ctx.on('tools/pre-execute', async () => {
        allowed++;
        return { kind: 'allow' };
      });
      const result = await tools.execute({
        callId: 'shared-guard-canary',
        name: 'shared_shell_canary',
        arguments: {},
        signal: AbortSignal.timeout(5000),
      });
      assert.equal(result.isError, true);
      assert.equal(executed, 0);
      assert.equal(allowed, 1);
      unregister();
    } finally {
      installed?.close();
      await ctx.fiber.dispose();
    }
  },
);

test(
  'rc.3 native cookie retains owner and native file APIs read another member directory',
  { skip: !root },
  async () => {
    const hostRequire = createRequire(join(root, 'package.json'));
    const load = async (name) =>
      import(pathToFileURL(hostRequire.resolve(name)).href);
    const [
      { Context },
      Connection,
      { RemoteAccessService },
      { LocalFileSystem },
      { WorkspaceFiles },
      { SessionStore },
      { default: TypertRegistry },
      { default: Gateway },
      { TYPERT },
    ] = await Promise.all([
      load('@deepseek-ai/cordis'),
      load('@deepseek-ai/dsh-client-connection'),
      load('@deepseek-ai/dsh-api-remote-access'),
      load('@deepseek-ai/dsh-fs-local'),
      load('@deepseek-ai/dsh-api-workspace-files'),
      load('@deepseek-ai/dsh-session'),
      load('@deepseek-ai/dsh-typert-registry'),
      load('@deepseek-ai/dsh-api-gateway'),
      load('@deepseek-ai/dsh-api-workspace-files/typert'),
    ]);
    const directory = await mkdtemp(join(root, '.public-files-'));
    assert.ok(resolve(directory).startsWith(resolve(root) + sep));
    const ctx = new Context();
    let gateway;
    try {
      new RemoteAccessService(ctx);
      const records = new Map();
      ctx.provide('credentials', {
        async modifyRecord(key, update) {
          const next = await update(records.get(key));
          if (next !== undefined) records.set(key, next);
          return records.get(key);
        },
      });
      ctx.provide('webServer', {
        host: '127.0.0.1',
        protocol: 'http:',
        register() {
          return () => {};
        },
      });
      await ctx.plugin(Connection, {});
      const nativeURL = new URL(
        ctx.connection.authenticatedUrl('http://127.0.0.1/'),
      );
      let responseHeaders;
      ctx.connection.authorizeIndex(
        {
          method: 'GET',
          url: nativeURL.pathname + nativeURL.search,
          headers: { host: '127.0.0.1' },
        },
        {
          writeHead(_status, headers) {
            responseHeaders = headers;
          },
          end() {},
        },
      );
      const value =
        responseHeaders['Set-Cookie'] ?? responseHeaders['set-cookie'];
      const cookie = (Array.isArray(value) ? value[0] : value).split(';')[0];
      const a = ctx.connection.admit({
        headers: { host: '127.0.0.1', cookie, 'Auriga-Identity': 'member-A' },
      });
      const b = ctx.connection.admit({
        headers: { host: '127.0.0.1', cookie, 'Auriga-Identity': 'member-B' },
      });
      assert.equal(a.peer, b.peer);
      assert.equal(a.peer.principal.kind, 'owner');
      const aRoot = join(directory, 'A'),
        bRoot = join(directory, 'B');
      await mkdir(aRoot);
      await mkdir(bRoot);
      await writeFile(join(bRoot, 'canary.txt'), 'MEMBER_B_CANARY');
      new TypertRegistry(ctx);
      const sessions = new SessionStore(ctx);
      ctx.provide('sandboxPolicy', { workspaceRoot: aRoot });
      for (const [id, cwd] of [
        ['A', aRoot],
        ['B', bRoot],
      ])
        sessions.enter(sessions.prepare(id, { meta: { cwd } }));
      ctx.typert.register(TYPERT);
      new LocalFileSystem(ctx, { cwd: aRoot, diffBasisMaxBytes: 1048576 });
      new WorkspaceFiles(ctx, {
        maxBytes: 65536,
        maxFileBytes: 65536,
        maxLines: 100,
        maxEntries: 100,
      });
      gateway = new Gateway(ctx, {
        websocketHeartbeatIntervalMs: 2000,
        streamInboxBytes: 262144,
      });
      const signal = AbortSignal.timeout(5000);
      const other = await gateway.invoke({
        namespace: 'workspaceFiles',
        method: 'read',
        args: { workspaceFileScopeId: 'B', path: 'canary.txt', range: {} },
        peer: a.peer,
        signal,
      });
      assert.equal(other.text, 'MEMBER_B_CANARY');
      const own = await gateway.invoke({
        namespace: 'workspaceFiles',
        method: 'read',
        args: {
          workspaceFileScopeId: 'A',
          path: join(bRoot, 'canary.txt'),
          range: {},
        },
        peer: a.peer,
        signal,
      });
      assert.equal(own.text, 'MEMBER_B_CANARY');
    } finally {
      await ctx.fiber.dispose();
      assert.ok(resolve(directory).startsWith(resolve(root) + sep));
      await rm(directory, { recursive: true, force: true });
    }
  },
);
