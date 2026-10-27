import assert from 'node:assert/strict';
import { once } from 'node:events';
import { waitUntil } from './assembly-process.mjs';

// Bypass SDK size/subscription guards to probe the pinned Broker itself.
// Authentication still uses real device tickets/callout. Never retain CONNECT.
export async function brokerGuards(prepared, createSocket) {
  const sessions = [];
  async function open(ticket, rejected = false) {
    const socket = createSocket(prepared.url),
      lines = [];
    let tail = '';
    socket.on('message', (data) => {
      tail += data.toString();
      for (;;) {
        const end = tail.indexOf('\r\n');
        if (end < 0) break;
        const line = tail.slice(0, end);
        tail = tail.slice(end + 2);
        if (line === 'PING') socket.send(Buffer.from('PONG\r\n'));
        else lines.push(line);
      }
    });
    socket.on('error', () => {});
    const session = {
      socket,
      lines,
      send: (value) => socket.send(Buffer.from(value)),
    };
    sessions.push(session);
    await once(socket, 'open');
    await waitUntil(
      () => lines.some((v) => v.startsWith('INFO ')),
      'Broker INFO missing',
    );
    session.send(
      'CONNECT ' +
        JSON.stringify({
          auth_token: ticket.auth.token,
          protocol: 1,
          lang: 'test',
          version: '1',
          verbose: false,
          pedantic: false,
          tls_required: true,
        }) +
        '\r\nPING\r\n',
    );
    await waitUntil(
      () =>
        lines.some((v) =>
          rejected ? /Authorization Violation/i.test(v) : v === 'PONG',
        ),
      rejected
        ? 'spent ticket was not rejected by real Redeem'
        : 'raw authenticated Broker connection failed',
    );
    return session;
  }
  const ticket = () => prepared.getTicket(new AbortController().signal);
  try {
    const oneUse = await ticket();
    const allowed = await open(oneUse),
      replay = await open(oneUse, true);
    allowed.send(
      'SUB auriga.net.down.aaaaaaaaaaaaaaaaaaaaaaaaaa 1\r\nPUB auriga.net.up.a.aaaaaaaaaaaaaaaaaaaaaaaaaa 1\r\nx\r\nPING\r\n',
    );
    await waitUntil(
      () =>
        allowed.lines.filter((v) => /Permissions Violation/i.test(v)).length >=
        2,
      'Broker foreign-ingress ACL was not enforced',
    );
    const boundedTicket = await ticket(),
      bounded = await open(boundedTicket);
    for (let i = 1; i <= 9; i++)
      bounded.send(
        `SUB ${boundedTicket.subjects.inboxPrefix}.probe${i} ${i}\r\n`,
      );
    await waitUntil(
      () =>
        bounded.lines.some((v) => /Maximum Subscriptions Exceeded/i.test(v)),
      'Broker subscription account limit not enforced',
    );
    const largeTicket = await ticket(),
      large = await open(largeTicket);
    large.send(
      `PUB ${largeTicket.subjects.upSubject} 65537\r\n${'x'.repeat(65537)}\r\n`,
    );
    await waitUntil(
      () => large.lines.some((v) => /Maximum Payload Violation/i.test(v)),
      'Broker payload account limit not enforced',
    );
    assert.equal(
      allowed.lines.filter((v) => /Permissions Violation/i.test(v)).length,
      2,
    );
    return {
      replay: replay.lines,
      acl: allowed.lines,
      subscriptions: bounded.lines,
      payload: large.lines,
      brokerVersion: '2.15.1',
    };
  } finally {
    for (const { socket } of sessions)
      if (socket.readyState !== 3) {
        const closed = once(socket, 'close');
        socket.terminate();
        await closed;
      }
  }
}
