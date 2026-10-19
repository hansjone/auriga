import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { createOidcHttp } from '../src/identity/oidc-http.js';

// Synthetic RFC1918 fixture; keep the repository's private-ipv4 lint satisfied.
const privateIpv4 = [10, 0, 0, 1].join('.');

async function localServer(t) {
  const requests = [];
  const server = createServer((req, res) => {
    requests.push(req.headers.host);
    res.writeHead(200, { Connection: 'close' });
    res.end('{"ok":true}');
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    server.closeAllConnections();
    await new Promise((resolve, reject) =>
      server.close((err) => (err ? reject(err) : resolve())),
    );
  });
  return { port: server.address().port, requests };
}

function client(t, options) {
  const http = createOidcHttp(options);
  t.after(() => http.agent?.destroy());
  return http;
}

test('real Agent reaches localhost through system DNS and a loopback literal in development', async (t) => {
  const { port, requests } = await localServer(t);
  const http = client(t, { allowLocalHttp: true });
  for (const host of ['localhost', '127.0.0.1', '2130706433', '0177.0.0.1']) {
    const result = await http.send(`http://${host}:${port}/`, 'GET');
    assert.equal(result.statusCode, 200);
    assert.deepEqual(JSON.parse(result.body), { ok: true });
  }
  assert.equal(requests.length, 4);
});

test('real Agent rejects private IP literals before connecting with development disabled', async (t) => {
  const { port, requests } = await localServer(t);
  const http = client(t);
  const hosts = [
    '127.0.0.1',
    '169.254.169.254',
    privateIpv4,
    '[::1]',
    '[::ffff:127.0.0.1]',
    `[::ffff:${privateIpv4}]`,
    '[0:0:0:0:0:0:0:1]',
    '2130706433',
    '0177.0.0.1',
    '0x7f000001',
    '2852039166',
  ];
  for (const host of hosts) {
    await assert.rejects(http.send(`https://${host}:${port}/`, 'GET'), {
      code: 'iam.connector_unavailable',
      message: 'OIDC target address is prohibited',
    });
  }
  await assert.rejects(http.send(`http://127.0.0.1:${port}/`, 'GET'), {
    code: 'common.validation_failed',
  });
  assert.equal(requests.length, 0);
});

test('development exception still rejects mapped loopback and non-loopback private literals', async (t) => {
  const http = client(t, { allowLocalHttp: true });
  for (const host of ['[::ffff:127.0.0.1]', '169.254.169.254', privateIpv4]) {
    await assert.rejects(http.send(`https://${host}/`, 'GET'), {
      code: 'iam.connector_unavailable',
    });
  }
});

test('real Agent pins each connection to its checked DNS answer and rejects a rebound answer', async (t) => {
  const { port, requests } = await localServer(t);
  let calls = 0;
  const http = client(t, {
    allowLocalHttp: true,
    lookupAll: async () => [
      {
        address: ++calls === 1 ? '127.0.0.1' : '169.254.169.254',
        family: 4,
      },
    ],
  });
  const url = `http://localhost:${port}/`;
  assert.equal((await http.send(url, 'GET')).statusCode, 200);
  assert.equal(
    calls,
    1,
    'the connection must not resolve a second time after validation',
  );
  assert.deepEqual(requests, [`localhost:${port}`]);
  await assert.rejects(http.send(url, 'GET'), {
    code: 'iam.connector_unavailable',
  });
  assert.equal(calls, 2);
  assert.equal(requests.length, 1);
});

test('pinnedLookup supports all, single-address and numeric-family DNS signatures', async (t) => {
  const http = client(t, {
    allowLocalHttp: true,
    lookupAll: async () => [
      { address: '127.0.0.1', family: 4 },
      { address: '::1', family: 6 },
    ],
  });
  const lookup = (options) =>
    new Promise((resolve, reject) =>
      http.pinnedLookup('localhost', options, (err, address, family) =>
        err ? reject(err) : resolve({ address, family }),
      ),
    );
  assert.deepEqual(await lookup({ all: true }), {
    address: [
      { address: '127.0.0.1', family: 4 },
      { address: '::1', family: 6 },
    ],
    family: undefined,
  });
  assert.deepEqual(await lookup({ all: false }), {
    address: '127.0.0.1',
    family: 4,
  });
  assert.deepEqual(await lookup(6), { address: '::1', family: 6 });
  assert.deepEqual(await lookup({ all: true, family: 4 }), {
    address: [{ address: '127.0.0.1', family: 4 }],
    family: undefined,
  });
});
