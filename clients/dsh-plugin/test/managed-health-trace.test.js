import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, request } from 'node:http';
import { once } from 'node:events';
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { registerHealthRoute } from '../src/managed/health.js';
import { validTraceparent } from '../src/observability/index.js';

const parent = '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01';
test(
  'public Cordis exporter delivers actual health JSON through the bounded stderr writer',
  {
    skip: process.env.AURIGA_CORDIS_MODULE
      ? false
      : 'set AURIGA_CORDIS_MODULE to public Cordis 4.0.5',
  },
  () => {
    const health = new URL('../src/managed/health.js', import.meta.url).href;
    const diagnostics = new URL(
      '../src/network/diagnostics.js',
      import.meta.url,
    ).href;
    const result = spawnSync(
      process.execPath,
      [
        '--input-type=module',
        '-e',
        `
    import assert from 'node:assert/strict';
    import {createServer} from 'node:http';
    import {once} from 'node:events';
    import childProcess from 'node:child_process';
    import {syncBuiltinESMExports} from 'node:module';
    let flushed;const written=new Promise(resolve=>flushed=resolve);
    const original=childProcess.spawn;
    childProcess.spawn=(...args)=>{const child=original(...args);child.on('message',msg=>{if(msg===1)flushed()});return child};
    syncBuiltinESMExports();
    const {Context}=await import(${JSON.stringify(process.env.AURIGA_CORDIS_MODULE ? pathToFileURL(process.env.AURIGA_CORDIS_MODULE).href : '')});
    const {installNetworkDiagnosticSink}=await import(${JSON.stringify(diagnostics)});
    const {registerHealthRoute}=await import(${JSON.stringify(health)});
    const ctx=new Context();installNetworkDiagnosticSink(ctx);
    const server=createServer(registerHealthRoute(ctx,{pluginVersion:'fixture',instanceId:'00000000-0000-7000-8000-000000000001',ready:()=>false}));
    server.listen(0,'127.0.0.1');await once(server,'listening');
    const response=await fetch('http://127.0.0.1:'+server.address().port+'/_auriga/health',{headers:{traceparent:${JSON.stringify(parent)},'Auriga-Observation-Generation':'7'}});
    assert.equal(response.status,200);assert.equal((await response.json()).ready,false);
    const guard=setTimeout(()=>{throw Error('health exporter did not deliver')},7000);
    await written;clearTimeout(guard);
    await new Promise(resolve=>server.close(resolve));await ctx.fiber.dispose();
  `,
      ],
      {
        encoding: 'utf8',
        timeout: 10000,
        env: { SystemRoot: process.env.SystemRoot },
      },
    );
    assert.ifError(result.error);
    assert.equal(result.status, 0, result.stderr);
    const event = JSON.parse(result.stderr.trim());
    assert.equal(event.service, 'dsh-plugin');
    assert.equal(event.trace_id, parent.slice(3, 35));
    assert.equal(event.parent_span_id, parent.slice(36, 52));
    assert.notEqual(event.span_id, event.parent_span_id);
    assert.equal(event.generation, 7);
    assert.equal(event.ready, false);
    assert.equal(event.instance_id, '00000000-0000-7000-8000-000000000001');
  },
);

test('real health HTTP continues a child and rejects duplicate/invalid observation without changing readiness', async () => {
  const server = createServer(
    registerHealthRoute({}, { pluginVersion: 'fixture', ready: () => false }),
  );
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  try {
    for (const headers of [
      { traceparent: parent },
      { traceparent: [parent, parent] },
      { traceparent: 'x'.repeat(1000) },
      {},
    ]) {
      const result = await new Promise((resolve, reject) => {
        const req = request(
          {
            host: '127.0.0.1',
            port: server.address().port,
            path: '/_auriga/health',
            headers,
          },
          (res) => {
            let body = '';
            res.on('data', (chunk) => (body += chunk));
            res.on('end', () =>
              resolve({
                body: JSON.parse(body),
                headers: res.headers,
                status: res.statusCode,
              }),
            );
          },
        );
        req.on('error', reject);
        req.end();
      });
      assert.equal(result.status, 200);
      assert.deepEqual(result.body, {
        status: 'ok',
        ready: false,
        protocol: 1,
        pluginVersion: 'fixture',
      });
      const child = result.headers.traceparent;
      assert.ok(validTraceparent(child));
      if (headers.traceparent === parent) {
        assert.equal(child.slice(3, 35), parent.slice(3, 35));
        assert.notEqual(child.slice(36, 52), parent.slice(36, 52));
      } else assert.notEqual(child.slice(3, 35), parent.slice(3, 35));
      assert.equal(result.headers['cache-control'], 'no-store');
    }
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
});
