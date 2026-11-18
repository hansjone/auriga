import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { randomUUID } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createSharedNodeTransport } from '../../src/shared/node-transport.js';

async function server(handler) {
  const path =
    process.platform === 'win32'
      ? '\\\\.\\pipe\\auriga-shared-' + randomUUID()
      : join(tmpdir(), 'auriga-shared-' + randomUUID() + '.sock');
  const http = createServer(handler);
  await new Promise((resolve, reject) => {
    http.once('error', reject);
    http.listen(path, resolve);
  });
  const transport = createSharedNodeTransport(path);
  return {
    transport,
    async close() {
      transport.close();
      http.closeAllConnections();
      await new Promise((resolve) => http.close(resolve));
    },
  };
}

test('node carrier uses only canonical operations and never forwards bearer or cookie headers', async () => {
  const fixture = await server(async (request, response) => {
    assert.equal(request.url, '/_auriga/shared-rpc/lookup');
    assert.equal(request.method, 'POST');
    assert.equal(request.headers.authorization, undefined);
    assert.equal(request.headers.cookie, undefined);
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), {
      sessionId: 'synthetic',
    });
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end('{"binding":{"sessionId":"synthetic"}}');
  });
  try {
    assert.deepEqual(
      await fixture.transport.call('lookup', { sessionId: 'synthetic' }),
      { binding: { sessionId: 'synthetic' } },
    );
    await assert.rejects(fixture.transport.call('../lookup', {}));
  } finally {
    await fixture.close();
  }
});

test('stream carrier decodes split UTF-8 and rejects a truncated final frame', async () => {
  let truncated = false;
  const fixture = await server((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/x-ndjson' });
    const body = Buffer.from('{"text":"你好"}\n');
    response.write(body.subarray(0, 10));
    response.end(
      truncated ? body.subarray(10, body.length - 1) : body.subarray(10),
    );
  });
  try {
    const output = [];
    for await (const chunk of fixture.transport.stream('model', {}))
      output.push(chunk);
    assert.deepEqual(output, [{ text: '你好' }]);
    truncated = true;
    await assert.rejects(async () => {
      for await (const chunk of fixture.transport.stream('model', {}))
        assert.ok(chunk);
    });
  } finally {
    await fixture.close();
  }
});

test('closing carrier cancels an in-flight read and future work is denied', async () => {
  let received;
  const pending = new Promise((resolve) => {
    received = resolve;
  });
  const fixture = await server((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.flushHeaders();
    received();
  });
  try {
    const read = fixture.transport.call('resolve', {});
    await pending;
    fixture.transport.close();
    await assert.rejects(read);
    await assert.rejects(fixture.transport.call('resolve', {}));
  } finally {
    await fixture.close();
  }
});

test('node failures expose a fixed code and reject redirects', async () => {
  let redirected = false;
  const fixture = await server((_request, response) => {
    response.writeHead(redirected ? 302 : 403, {
      'content-type': 'application/json',
      location: 'https://unused.invalid/',
    });
    response.end(
      '{"code":"shared.revoked","message":"synthetic-private-detail"}',
    );
  });
  try {
    await assert.rejects(
      fixture.transport.call('resolve', {}),
      (error) =>
        error.code === 'shared.revoked' && !error.message.includes('detail'),
    );
    redirected = true;
    await assert.rejects(fixture.transport.call('resolve', {}));
  } finally {
    await fixture.close();
  }
});
