// Opt-in real Broker 2.15.1/WSS protocol probe. Static credentials belong only
// to this temporary fixture; this does not claim IAM/callout/native acceptance.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { startNatsServer } from '../../clients/dsh-plugin/test/network/transport/support/nats-server.js';
import { ensureNatsServer } from '../../clients/dsh-plugin/test/network/transport/support/nats-binary.js';
import { createCaSocketFactory } from '../../clients/dsh-plugin/test/network/transport/support/client.js';
import {
  openBrokerProbe,
  payloadCloseEvidence,
  probePayload,
  probeState,
} from './support/assembly-broker.mjs';
import { waitUntil } from './support/assembly-process.mjs';
import { currentNativeProcess } from './support/native-process.mjs';

assert.equal(process.argv.slice(2).join(' '), '--run');
await mkdir(new URL('./target/', import.meta.url), { recursive: true });
const artifacts = await mkdtemp(
  new URL('./target/broker-payload-', import.meta.url),
);
const password = randomUUID(),
  sessions = [],
  proofs = [];
const processRecord = await currentNativeProcess();
const server = await startNatsServer({
  binary: await ensureNatsServer(),
  maxPayload: 1048576,
  authConfig: `accounts { CLIENTS { users: [{user: "probe", password: "${password}"}], limits {max_payload: 65536, max_subscriptions: 8} } }`,
});
const brokerPid = Number(/^\[(\d+)\]/.exec(server.log)?.[1]);
const manifest = {
  process: processRecord,
  brokerPid,
  brokerVersion: '2.15.1',
  complete: false,
  pg: false,
  callout: false,
  dsh: false,
};
const open = () =>
  openBrokerProbe({
    url: server.url,
    createSocket: createCaSocketFactory(server.rootPem),
    auth: { user: 'probe', pass: password },
    sessions,
  });
try {
  for (let i = 0; i < 20; i++) {
    const peer = await open();
    const result = await probePayload(peer, 'probe.up', async (state) =>
      payloadCloseEvidence(state, server.log, brokerPid),
    );
    assert.equal(result.boundaryBytes, 65536);
    assert.equal(result.attemptedBytes, 65537);
    // Exercise the dropped-error-frame branch deterministically as well as
    // retaining whichever native socket outcome actually occurred.
    const noFrame = {
      ...probeState(peer),
      lines: peer.lines.filter((line) => !line.startsWith('-ERR')),
    };
    assert.ok(payloadCloseEvidence(noFrame, server.log, brokerPid));
    assert.equal(payloadCloseEvidence(noFrame, '', brokerPid), null);
    proofs.push({ ...probeState(peer), result });
  }
  for (const mode of ['normal-close', 'protocol-error']) {
    const peer = await open();
    if (mode === 'normal-close') peer.socket.close();
    else peer.send('BOGUS\r\n');
    await waitUntil(() => peer.closed, 'counterexample socket did not close');
    assert.equal(
      payloadCloseEvidence(probeState(peer), server.log, brokerPid),
      null,
    );
    proofs.push({ mode, ...probeState(peer), rejectedAsPayloadEvidence: true });
  }
  manifest.complete = true;
} finally {
  for (const { socket } of sessions) {
    if (socket.readyState !== 3) {
      const done = once(socket, 'close');
      socket.terminate();
      await done;
    }
  }
  await server.close();
  // Only the narrow public payload records are retained; fixture credentials,
  // config paths and CONNECT are deliberately absent from this evidence.
  await writeFile(
    join(artifacts, 'proofs.json'),
    JSON.stringify(
      {
        proofs,
        sessions: sessions.map(probeState),
        records: server.log
          .split(/\r?\n/)
          .filter((line) => /\[INF\].*ID:|maximum payload exceeded:/.test(line))
          .map((line) =>
            line.replace(
              /"CLIENTS(?:\/[^"\r\n]*)?"/,
              '"CLIENTS/[redacted-user]"',
            ),
          ),
      },
      null,
      2,
    ),
  );
  await writeFile(
    join(artifacts, 'manifest.json'),
    JSON.stringify(
      {
        ...manifest,
        closed: true,
        socketCount: sessions.length,
        payloadChecks: proofs.filter((proof) => proof.result).length,
        closureCounterexamples: proofs.filter((proof) => proof.mode).length,
      },
      null,
      2,
    ),
  );
}
console.log(
  JSON.stringify({
    artifacts,
    complete: true,
    payloadChecks: 20,
    closureCounterexamples: 2,
    naturallyMissingErrorFrames: proofs.filter(
      (proof) => proof.result && !proof.result.errorFrameObserved,
    ).length,
  }),
);
