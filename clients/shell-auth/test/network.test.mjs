import { test } from 'node:test';
/* global Headers */
import assert from 'node:assert/strict';
import { randomUUID, randomBytes, createHash, sign } from 'node:crypto';
import { mkdtempSync, rmSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fixture, login } from './fixtures.mjs';
import { BusinessProxy } from '../business.mjs';
import { trustedConfig } from '../platform.mjs';
import * as core from '../network-core.mjs';
import { createWsSocketFactory } from '../../dsh-plugin/src/network/transport/ws-socket.js';
import { signingInput } from '../../dsh-plugin/src/network/trust/verifier.js';
import {
  natsBinary,
  skipNoServer,
} from '../../dsh-plugin/test/network/transport/support/env.js';
import { startNatsServer } from '../../dsh-plugin/test/network/transport/support/nats-server.js';
import {
  publicationFixture,
  signedKeyset,
  signedAnnouncement,
  signedReadyResult,
  testRoots,
} from '../../dsh-plugin/test/network/trust/keyset-fixtures.js';

const until = (offset) =>
  new Date(Math.floor((Date.now() + offset) / 1000) * 1000)
    .toISOString()
    .replace('.000Z', 'Z');
const envelope = (broker) => ({
  protocolVersion: 1,
  requestId: randomUUID(),
  shellEpoch: broker.epoch,
  expectedRevision: broker.revision,
  expiresAt: until(25000),
});
const waitFor = async (condition) => {
  const deadline = Date.now() + 5000;
  while (!condition()) {
    if (Date.now() >= deadline) throw new Error('network test deadline');
    await delay(5);
  }
};

test(
  'Desktop bridge uses real NATS/WSS, root trust, signed raw frames, current source observations and owner fences',
  { skip: skipNoServer, timeout: 30000 },
  async (t) => {
    const server = await startNatsServer({ binary: natsBinary });
    t.after(() => server.close());
    const directory = mkdtempSync(join(tmpdir(), 'auriga-network-test-'));
    t.after(() => rmSync(directory, { recursive: true, force: true }));
    const f = fixture();
    await login(f);
    const publication = publicationFixture(),
      ticket = randomBytes(32).toString('base64url');
    // The production provider verifies these ticket-derived subjects itself.
    const alphabet = 'abcdefghijklmnopqrstuvwxyz234567';
    let bits = 0,
      accumulator = 0,
      ingress = '';
    for (const byte of createHash('sha256')
      .update(ticket, 'ascii')
      .digest()
      .subarray(0, 16)) {
      accumulator = (accumulator << 8) | byte;
      bits += 8;
      while (bits >= 5) {
        bits -= 5;
        ingress += alphabet[(accumulator >>> bits) & 31];
      }
    }
    if (bits) ingress += alphabet[(accumulator << (5 - bits)) & 31];
    const shard =
      alphabet[createHash('sha256').update(ingress).digest()[0] & 31];
    const upSubject = `auriga.net.up.${shard}.${ingress}`,
      downSubject = `auriga.net.down.${ingress}`,
      replyPrefix = `auriga.net.reply.${ingress}`;
    const identity = {
      tenant: f.claims.tid,
      device: f.claims.did,
      realm: '2',
      node: '64',
      systemAddress: 'aur1:2.40.0',
    };
    f.platform.config = trustedConfig({
      platformOrigin: 'https://example.com',
      network: { realm: '2', wssUrl: server.url, roots: testRoots },
    });
    const keyWire = JSON.stringify(signedKeyset(publication.payload)),
      announcementWire = JSON.stringify(
        signedAnnouncement(publication.payload),
      );
    let registered = 0,
      submitted = 0;
    const controlFetch = async (url, options) => {
      assert.equal(new URL(url).origin, 'https://example.com');
      assert.equal(options.redirect, 'error');
      assert.equal(options.credentials, 'omit');
      const headers = new Headers(options.headers);
      assert.ok(
        [f.tokens.access_token].some(
          (value) => headers.get('authorization') === 'Bearer ' + value,
        ),
      );
      const path = new URL(url).pathname;
      if (path.endsWith('/keys/announcement'))
        return new Response(announcementWire);
      if (path.endsWith('/keys')) return new Response(keyWire);
      if (path.endsWith('/register')) {
        registered++;
        return Response.json({ ...identity, created: true });
      }
      if (path.endsWith('/tickets'))
        return Response.json({
          identity,
          ticket,
          serverAudience: 'test',
          expiresAt: until(25000),
          connectionExpiresAt: until(300000),
          ingress,
          upSubject,
          downSubject,
          replyPrefix,
          protocolVersion: 1,
        });
      if (path.endsWith('/submit')) {
        submitted++;
        return Response.json({
          status: 'platform_stored',
          from: {
            realm: '2',
            node: '64',
            local: '1',
            type: 'session',
            version: 1,
          },
          to: {
            realm: '2',
            node: '65',
            local: '1',
            type: 'session',
            version: 1,
          },
          message_id: randomUUID(),
          thread_id: randomUUID(),
          message_class: 'unicast',
          expires_at: until(60000),
        });
      }
      throw new Error('unexpected control route');
    };
    // Only control HTTP is substituted. WSS TLS and NATS below use real sockets;
    // the generated CA is passed locally, never installed in the OS trust store.
    const originalFetch = globalThis.fetch;
    globalThis.fetch = controlFetch;
    t.after(() => {
      globalThis.fetch = originalFetch;
    });
    const createSocket = createWsSocketFactory({ ca: server.rootPem });
    const networkCore = {
      ...core,
      NatsLink: class extends core.NatsLink {
        constructor(options) {
          super({ ...options, createSocket });
        }
      },
    };
    f.platform.audience = async () => f.tokens;
    const proxy = new BusinessProxy(f.broker, controlFetch, undefined, {
      networkDirectory: directory,
      networkCore,
    });
    t.after(async () => {
      proxy.close();
      await f.broker.close();
    });
    const peer = proxy.host.connect({
      verified: true,
      connectionId: randomUUID(),
      sid: randomUUID(),
      processStart: String(Date.now()),
      role: 'plugin-backend',
    });
    const register = async (hostSessionId) =>
      (
        await proxy.host.command(
          {
            ...envelope(f.broker),
            action: 'register',
            context: {
              kind: 'session',
              hostSessionId,
              expiresAt: until(300000),
            },
          },
          peer,
        )
      ).context.executionContextId;
    const executionContextId = await register('first-session');
    const call = (operation, input) =>
      proxy.call(
        { ...envelope(f.broker), call: { operation, input } },
        peer.id,
      );
    const events = [],
      outbound = [];
    const serverLink = new core.NatsLink({
      url: server.url,
      upSubject: downSubject,
      downSubject: upSubject,
      inboxPrefix: replyPrefix + '.server',
      auth: { token: randomUUID() },
      createSocket,
      onMessage: (bytes) =>
        outbound.push(JSON.parse(Buffer.from(bytes).toString('utf8'))),
    });
    t.after(() => serverLink.close());
    await serverLink.open();
    let failure;
    const originalExecute = proxy.network.execute.bind(proxy.network);
    proxy.network.execute = async (...args) => {
      try {
        return await originalExecute(...args);
      } catch (error) {
        failure = error;
        throw error;
      }
    };
    const openMessage = {
      ...envelope(f.broker),
      call: { operation: 'network_open', input: { executionContextId } },
    };
    const opened = await Promise.all([
      proxy.call(openMessage, peer.id),
      call('network_open', { executionContextId }),
    ]);
    for (const result of opened) {
      assert.ok(result.output, failure?.stack ?? JSON.stringify(result.error));
      assert.deepEqual(
        { ...result.output.body.registration, created: false },
        { ...identity, created: false },
      );
      assert.deepEqual(result.output.body.binding, {
        executionContextId,
        shellEpoch: f.broker.epoch,
        revision: f.broker.revision,
      });
    }
    assert.equal(
      opened.filter((result) => result.output.body.registration.created).length,
      1,
    );
    assert.equal(
      (await proxy.call(openMessage, peer.id)).output.body.registration.created,
      false,
    );
    assert.equal(
      (await call('network_open', { executionContextId })).output.body
        .registration.created,
      false,
    );
    assert.equal(
      registered,
      1,
      'concurrent opens share one authenticated link',
    );
    assert.ok(readdirSync(directory).some((file) => file.endsWith('.json')));
    const streamId = opened[0].output.body.streamId;
    const subscription = {
      streamId,
      shellEpoch: f.broker.epoch,
      expectedRevision: f.broker.revision,
    };
    proxy.streams.subscribe(subscription, peer.id, (event) =>
      events.push(event),
    );
    const credited = proxy.streams.command(
      { ...subscription, action: 'credit', creditBytes: 1048576 },
      peer.id,
    );
    const send = (frame) =>
      call('network_send', {
        streamId,
        frameWire: Buffer.from(JSON.stringify(frame)).toString('base64'),
      });
    const ready = {
      app_version: '1.0',
      type: 'ready',
      request_id: randomUUID(),
      channel_nonce: randomUUID(),
      boot_id: randomUUID(),
      features: [],
      adapter_capabilities: [],
      resume_cursor: '0',
      local_high_water: '0',
      registration_revision: '0',
      receipt_checkpoint: {
        checkpoint_seq: '0',
        checkpoint_revision: '0',
        summary_digest:
          'sha256:' + createHash('sha256').update('').digest('hex'),
      },
      keyset_version: '5',
      pending_registration: null,
    };
    assert.ok((await send(ready)).output);
    await waitFor(() => outbound.length === 1);
    const rawReady = Buffer.from(
      JSON.stringify(signedReadyResult(publication, ready)),
    );
    await serverLink.send(rawReady);
    await credited;
    assert.deepEqual(Buffer.from(events[0].data, 'base64'), rawReady);
    assert.equal((await send(ready)).error.code, 'common.forbidden');
    const sourceBinding = randomUUID();
    const registration = {
      registration_id: randomUUID(),
      allocation_op_id: randomUUID(),
      allocation_fingerprint:
        'sha256:' + createHash('sha256').update(randomBytes(16)).digest('hex'),
      registration_revision: '1',
      local: '1',
      address_type: 'session',
    };
    const witness = {
      witness_id: randomUUID(),
      binding_id: randomUUID(),
      binding_revision: '1',
      observed_at: until(-1000),
      valid_until: until(25000),
    };
    const source = {
      source_binding: sourceBinding,
      registration,
      scope: 'durable_unicast_send',
      revision: '1',
      state: 'active',
      not_before: until(-1000),
      expires_at: until(60000),
      predecessor_binding: '',
      native_binding: {
        binding_id: witness.binding_id,
        binding_revision: witness.binding_revision,
      },
    };
    const seal = (value, request) => {
      value.server_auth = {
        ...signedReadyResult(publication, ready).server_auth,
        request_id: request.request_id,
      };
      value.server_auth.signature = sign(
        null,
        signingInput(value),
        publication.pair.privateKey,
      ).toString('base64url');
      return Buffer.from(JSON.stringify(value));
    };
    const requestWire = Buffer.from(
      JSON.stringify({
        kind: 'message',
        intent: 'information',
        to: { realm: '2', node: '65', local: '1', type: 'session', version: 1 },
        durability: 'durable',
        body: { subtype: 'text', text: 'test' },
        source: { source_binding: sourceBinding },
        idempotency: { producer_epoch: '1', key: randomUUID() },
      }),
    ).toString('base64');
    assert.equal(
      (await call('network_submit', { executionContextId, requestWire })).error
        .code,
      'common.forbidden',
    );
    const get = {
      app_version: '1.0',
      type: 'source_get',
      request_id: randomUUID(),
      epoch: '1',
      boot_id: ready.boot_id,
      source_binding: sourceBinding,
      registration,
      native_witness: witness,
    };
    assert.ok((await send(get)).output, failure?.stack);
    await serverLink.send(
      seal(
        {
          app_version: '1.0',
          type: 'source_get_result',
          request_id: get.request_id,
          source,
          policy_revision: '1',
          observed_at: until(-500),
          valid_until: until(20000),
          witness_id: witness.witness_id,
        },
        get,
      ),
    );
    await waitFor(() => events.filter((e) => e.type === 'data').length === 2);
    const sent = await call('network_submit', {
      executionContextId,
      requestWire,
    });
    assert.ok(sent.output, JSON.stringify(sent.error));
    assert.equal(submitted, 1);
    const other = await register('other-session');
    assert.equal(
      (await call('network_submit', { executionContextId: other, requestWire }))
        .error.code,
      'common.forbidden',
    );
    assert.ok(
      (
        await send({
          app_version: '1.0',
          type: 'source_revoke',
          request_id: randomUUID(),
          epoch: '1',
          boot_id: ready.boot_id,
          source_binding: sourceBinding,
          operation_id: randomUUID(),
        })
      ).output,
    );
    assert.equal(
      (await call('network_submit', { executionContextId, requestWire })).error
        .code,
      'common.forbidden',
    );
    const lateGet = { ...get, request_id: randomUUID() };
    assert.ok((await send(lateGet)).output);
    await serverLink.send(
      seal(
        {
          app_version: '1.0',
          type: 'source_get_result',
          request_id: lateGet.request_id,
          source,
          policy_revision: '1',
          observed_at: until(-500),
          valid_until: until(20000),
          witness_id: witness.witness_id,
        },
        lateGet,
      ),
    );
    await waitFor(
      () => events.filter((event) => event.type === 'data').length === 3,
    );
    assert.equal(
      (await call('network_submit', { executionContextId, requestWire })).error
        .code,
      'common.forbidden',
    );
    const head = seal(
      {
        app_version: '1.0',
        type: 'head',
        epoch: '1',
        head_seq: '0',
        keyset_announcement: JSON.parse(announcementWire),
      },
      { request_id: null },
    );
    await serverLink.send(head);
    await waitFor(
      () => events.filter((event) => event.type === 'data').length === 4,
    );
    assert.deepEqual(
      Buffer.from(
        events.filter((event) => event.type === 'data').at(-1).data,
        'base64',
      ),
      head,
    );
    // A changed authenticated frame must never reach D, and tears down the link.
    const tampered = JSON.parse(rawReady.toString());
    tampered.head_seq = '9';
    await serverLink.send(Buffer.from(JSON.stringify(tampered)));
    await waitFor(() => events.some((event) => event.type === 'error'));
    assert.equal(events.filter((event) => event.type === 'data').length, 4);
    assert.equal(proxy.network.links.size, 0);
  },
);
