/* global AbortController, Buffer */
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createWsSocketFactory } from '../../../clients/dsh-plugin/src/network/transport/ws-socket.js';
import { NatsLink } from '../../../clients/dsh-plugin/src/network/transport/nats-link.js';
import { bootstrapReady } from '../../../clients/dsh-plugin/src/network/session/bootstrap-ready.js';
import { openTrustStore } from '../../../clients/dsh-plugin/src/network/trust/store.js';
import { assemblyDeviceProvider, prepareDevice } from './assembly-device.mjs';
import { createInterface } from 'node:readline';
import { setTimeout as delay } from 'node:timers/promises';
import { createWireObserver, invalidTicketVector } from './edge-wss-wire.mjs';
const spec = JSON.parse(
  await readFile(process.env.AURIGA_EDGE_WSS_CLIENT, 'utf8'),
);
const { config, platformOrigin } = spec;
const roots = [
  {
    kid: config.rootKid,
    public_key_spki: (await readFile(config.rootPublicFile)).toString(
      'base64url',
    ),
  },
];
const controller = new AbortController();
const signal = controller.signal;
const device = JSON.parse(await readFile(config.device, 'utf8'));
const provider = await prepareDevice(
  assemblyDeviceProvider({
    origin: platformOrigin,
    network: {
      realm: config.realm,
      wssUrl: platformOrigin.replace('https:', 'wss:') + '/net/v1/ws',
      roots,
    },
    async getDevice() {
      return {
        accessToken: device.access_token,
        claims: JSON.parse(
          Buffer.from(device.access_token.split('.')[1], 'base64url'),
        ),
      };
    },
  }),
  signal,
);
const registration = await provider.register(signal);
const tickets = new Map();
const links = new Set();
const emit = (v) => process.stdout.write(JSON.stringify(v) + '\n');
const input = createInterface({ input: process.stdin });
emit({
  stage: 'client-ready',
  pid: process.pid,
  parentPid: process.ppid,
  registration,
});
try {
  for await (const line of input) {
    if (line === 'stop') break;
    const command = JSON.parse(line);
    const result = { stage: 'reply', id: command.id };
    try {
      if (command.op === 'ticket') {
        const ticket = await provider.getTicket(signal);
        tickets.set(command.id, ticket);
        result.handle = command.id;
        result.ingress = ticket.subjects.downSubject.split('.').at(-1);
      } else if (command.op === 'connect') {
        const saved = tickets.get(command.handle);
        assert.ok(saved);
        const auth = command.invalid
          ? {
              token: JSON.stringify({
                ...JSON.parse(saved.auth.token),
                ticket: invalidTicketVector.ticket,
              }),
            }
          : saved.auth;
        result.expectedIngress = command.invalid
          ? invalidTicketVector.ingress
          : saved.subjects.downSubject.split('.').at(-1);
        const wire = createWireObserver();
        const socketClosures = [];
        const observations = {
          upgraded: false,
          authorized: false,
          natsAuthError: false,
          httpStatus: null,
          problem: null,
          tlsVerified: false,
        };
        const listeners = new Set();
        const link = new NatsLink({
          url: command.url,
          auth,
          ...saved.subjects,
          epoch: 1,
          connectTimeoutMs: 5000,
          createSocket(url) {
            // The same production factory used by connection-runtime, with
            // installation-owned Origin; the wrapper observes events only.
            const socket = createWsSocketFactory({
              origin: command.origin ?? provider.origin,
            })(url);
            socket.on('upgrade', (response) => {
              observations.upgraded = true;
              observations.tlsVerified = response.socket.authorized === true;
            });
            socket.on('message', (data) => {
              wire.message(data);
            });
            socketClosures.push(
              new Promise((resolve) =>
                socket.once('close', (code, reason) => {
                  wire.close(code, reason);
                  resolve();
                }),
              ),
            );
            socket.on('unexpected-response', (_request, response) => {
              observations.httpStatus = response.statusCode;
              let body = '';
              response.on('data', (part) => {
                if (body.length < 8192) body += part;
              });
              response.on('end', () => {
                try {
                  observations.problem = JSON.parse(body).code;
                } catch {
                  /* malformed response fails assertion */
                }
                socket.terminate();
              });
            });
            return socket;
          },
          onMessage(bytes) {
            for (const listener of listeners) listener(bytes, 1);
          },
        });
        links.add(link);
        try {
          await link.open();
          observations.authorized = true;
          if (command.ready) {
            let value;
            const trust = await openTrustStore({
              roots,
              realm: registration.realm,
              requireAnnouncement: true,
              cache: {
                async load() {
                  return value;
                },
                async commit(next) {
                  value = next;
                },
              },
              refresh: async () => provider.refresh(trust, signal),
            });
            await provider.refresh(trust, signal);
            const transport = {
              send: (b) => link.send(b),
              epoch: 1,
              get state() {
                return link.state;
              },
              onMessage(fn) {
                listeners.add(fn);
                return () => listeners.delete(fn);
              },
            };
            const output = await bootstrapReady({
              registration,
              transport,
              trust,
              signal,
              timeoutMs: 10000,
            });
            result.readyResult = output.readyResult;
          }
        } catch (error) {
          result.errorCode = error.code ?? 'unclassified';
        } finally {
          await link.close();
          await Promise.race([Promise.all(socketClosures), delay(1000)]);
          links.delete(link);
        }
        observations.wire = wire.evidence();
        observations.natsAuthError =
          observations.wire.authError === 'authorization-violation';
        result.observations = observations;
      } else throw new Error('unknown command');
    } catch {
      result.failure = 'client operation failed';
    }
    emit(result);
  }
} finally {
  controller.abort();
  for (const link of links) await link.close();
  input.close();
  emit({ stage: 'client-closed' });
}
