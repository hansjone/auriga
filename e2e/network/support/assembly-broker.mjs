import assert from 'node:assert/strict';
import { once } from 'node:events';
import { waitUntil } from './assembly-process.mjs';

const MAX_PAYLOAD = 65536;
const violation = "-ERR 'Maximum Payload Violation'";

// A reset alone is not proof. Correlate the public WSS upgrade socket's port,
// both INFOs, the exact owned Broker PID/ID and its account-limit error record.
export function payloadCloseEvidence(peer, log, brokerPid) {
  const { initialInfo: initial, info, localPort, closed, lines } = peer;
  if (
    !Number.isSafeInteger(brokerPid) ||
    brokerPid <= 0 ||
    initial?.version !== '2.15.1' ||
    info?.version !== initial.version ||
    !/^N[A-Z2-7]{55}$/.test(initial?.server_id ?? '') ||
    info.server_id !== initial.server_id ||
    !Number.isSafeInteger(initial.client_id) ||
    initial.client_id <= 0 ||
    info.client_id !== initial.client_id ||
    info.connect_info !== true ||
    info.remote_account !== 'CLIENTS' ||
    info.max_payload !== MAX_PAYLOAD ||
    !Number.isSafeInteger(localPort) ||
    localPort <= 0 ||
    localPort > 65535 ||
    !closed ||
    ![1006, 1009].includes(closed.code) ||
    (closed.code === 1009 &&
      closed.reason !== 'Maximum Message Payload Exceeded') ||
    lines.some((line) => line.startsWith('-ERR') && line !== violation)
  )
    return null;
  const records = log.split(/\r?\n/);
  const prefix = `[${brokerPid}] `;
  if (
    !records.some(
      (line) =>
        line.startsWith(prefix) &&
        line.includes('[INF]') &&
        new RegExp('\\bID:\\s+' + initial.server_id + '\\s*$').test(line),
    )
  )
    return null;
  const pattern =
    /^\[(\d+)\] .*? \[ERR\] 127\.0\.0\.1:(\d+) - wid:(\d+) - (?:"v1:test" - )?"(CLIENTS)(?:\/[^"\r\n]*)?" - maximum payload exceeded: (\d+) vs (\d+)\s*$/;
  const record = records
    .map((line) => pattern.exec(line))
    .find(
      (match) =>
        match &&
        Number(match[1]) === brokerPid &&
        Number(match[2]) === localPort &&
        Number(match[3]) === info.client_id &&
        Number(match[5]) === MAX_PAYLOAD + 1 &&
        Number(match[6]) === MAX_PAYLOAD,
    );
  if (!record) return null;
  return {
    brokerPid,
    serverId: info.server_id,
    clientId: info.client_id,
    localPort,
    account: record[4],
    actualBytes: Number(record[5]),
    limitBytes: Number(record[6]),
    errorFrameObserved: lines.includes(violation),
    record: record[0].replace(
      /"CLIENTS(?:\/[^"\r\n]*)?"/,
      '"CLIENTS/[redacted-user]"',
    ),
  };
}

export function probeState(session) {
  return {
    initialInfo: session.initialInfo,
    info: session.info,
    localPort: session.localPort,
    closed: session.closed,
    lines: [...session.lines],
  };
}

export async function openBrokerProbe({
  url,
  createSocket,
  auth,
  sessions,
  rejected = false,
}) {
  const socket = createSocket(url),
    lines = [];
  const session = {
    socket,
    lines,
    closed: null,
    localPort: undefined,
    initialInfo: undefined,
    info: undefined,
    send: (value) => socket.send(Buffer.from(value)),
  };
  sessions.push(session);
  let tail = '';
  socket.on('upgrade', (response) => {
    session.localPort = response.socket.localPort;
  });
  socket.on('close', (code, reason) => {
    session.closed = { code, reason: reason.toString() };
  });
  socket.on('message', (data) => {
    tail += data.toString();
    for (;;) {
      const end = tail.indexOf('\r\n');
      if (end < 0) break;
      const line = tail.slice(0, end);
      tail = tail.slice(end + 2);
      if (line === 'PING') socket.send(Buffer.from('PONG\r\n'));
      else {
        lines.push(line);
        if (line.startsWith('INFO ')) {
          const info = JSON.parse(line.slice(5));
          session.initialInfo ??= info;
          if (info.connect_info === true) session.info = info;
        }
      }
    }
  });
  socket.on('error', () => {});
  await once(socket, 'open');
  await waitUntil(() => session.initialInfo, 'Broker INFO missing');
  session.send(
    'CONNECT ' +
      JSON.stringify({
        ...auth,
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
      lines.some((line) =>
        rejected ? /Authorization Violation/i.test(line) : line === 'PONG',
      ) &&
      (rejected || session.info),
    rejected
      ? 'spent ticket was not rejected by real Redeem'
      : 'raw authenticated Broker account INFO/PONG missing',
  );
  if (!rejected) {
    assert.equal(session.info.remote_account, 'CLIENTS');
    assert.equal(session.info.max_payload, MAX_PAYLOAD);
    assert.equal(session.info.client_id, session.initialInfo.client_id);
    assert.equal(session.info.server_id, session.initialInfo.server_id);
  }
  return session;
}

export async function probePayload(session, subject, evidence) {
  const pongs = session.lines.filter((line) => line === 'PONG').length;
  session.send(
    `PUB ${subject} ${MAX_PAYLOAD}\r\n${'x'.repeat(MAX_PAYLOAD)}\r\nPING\r\n`,
  );
  await waitUntil(() => {
    assert.equal(
      session.closed,
      null,
      '65536-byte boundary publish must stay open',
    );
    assert.equal(
      session.lines.some((line) => line.startsWith('-ERR')),
      false,
      '65536-byte boundary publish must be accepted',
    );
    return session.lines.filter((line) => line === 'PONG').length === pongs + 1;
  }, 'Broker boundary payload PONG missing');
  // Preserve the entire original 65537-byte body; do not substitute a header-only probe.
  session.send(
    `PUB ${subject} ${MAX_PAYLOAD + 1}\r\n${'x'.repeat(MAX_PAYLOAD + 1)}\r\n`,
  );
  let proof;
  await waitUntil(async () => {
    if (!session.closed) return false;
    proof = await evidence(probeState(session));
    return proof;
  }, 'Broker payload account limit not enforced');
  return {
    boundaryBytes: MAX_PAYLOAD,
    attemptedBytes: MAX_PAYLOAD + 1,
    ...proof,
  };
}

// Bypass SDK size/subscription guards to probe the pinned Broker itself.
// Authentication still uses real device tickets/callout. Never retain CONNECT.
export async function brokerGuards(
  prepared,
  createSocket,
  { payloadEvidence, onEvidence } = {},
) {
  const sessions = [];
  const open = (ticket, rejected = false) =>
    openBrokerProbe({
      url: prepared.url,
      createSocket,
      auth: { auth_token: ticket.auth.token },
      sessions,
      rejected,
    });
  const ticket = () => prepared.getTicket(new AbortController().signal);
  let payload,
    complete = false;
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
    payload = await probePayload(
      large,
      largeTicket.subjects.upSubject,
      payloadEvidence,
    );
    assert.equal(
      allowed.lines.filter((v) => /Permissions Violation/i.test(v)).length,
      2,
    );
    complete = true;
    return {
      replay: replay.lines,
      acl: allowed.lines,
      subscriptions: bounded.lines,
      payload: large.lines,
      payloadEvidence: payload,
      brokerVersion: '2.15.1',
    };
  } finally {
    for (const { socket } of sessions)
      if (socket.readyState !== 3) {
        const closed = once(socket, 'close');
        socket.terminate();
        await closed;
      }
    await onEvidence?.({
      complete,
      payload,
      sessions: sessions.map(probeState),
    });
  }
}
