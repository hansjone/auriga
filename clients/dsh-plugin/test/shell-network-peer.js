/* global structuredClone */
import { setImmediate } from 'node:timers';
import { randomUUID } from 'node:crypto';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fixture } from '../../shell-auth/test/fixtures.mjs';
import { BusinessProxy } from '../../shell-auth/business.mjs';
import * as core from '../../shell-auth/network-core.mjs';
import { createConnectRuntime } from '../src/local/runtime.js';
import { operations, validate } from './broker-fixture.js';
import {
  publicationFixture,
  signedV2Keyset as signedKeyset,
  signedV2Announcement as signedAnnouncement,
  testRoots,
} from './network/trust/keyset-fixtures.js';

/** Actual C broker, host handles, business dispatch, network and D runtime.
 * IAM and OS peer evidence are doubled. No private pipe or browser is started. */
export async function shellNetworkPeer(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'auriga-shell-network-'));
  const source = fixture(options.platform);
  const config = {
    platformOrigin: 'https://portal.example.com',
    issuer: 'https://portal.example.com',
    clientId: 'auriga-dsh',
    providerId: source.identity.providerId,
    network: {
      realm: '2',
      roots: testRoots,
      wssUrl: 'wss://network.example.com/',
    },
    ...options.config,
  };
  config.network = structuredClone(config.network);
  source.platform.config = config;
  source.platform.audience = async () => ({
    ...source.tokens,
    refresh_token: randomUUID(),
  });
  const begin = source.platform.begin;
  source.platform.begin = async (...args) => ({
    ...(await begin(...args)),
    verification_uri: config.platformOrigin + '/device',
  });
  await options.configure?.(source);
  await source.broker.initialize();
  const requests = [];
  const publication = publicationFixture().payload;
  publication.realm = config.network.realm;
  const fetcher = async (url, init) => {
    const path = new URL(url).pathname;
    requests.push(path);
    if (options.fetcher) return options.fetcher(url, init, source);
    if (path === '/api/iam/v1/me/device-profile')
      return Response.json({
        account: {
          id: source.claims.sub,
          tenantId: source.identity.tenantId,
          displayName: 'Member',
        },
      });
    if (path.endsWith('/bootstrap/controls'))
      return Response.json(
        signedKeyset(publication, source.identity.tenantId).control,
      );
    if (path.endsWith('/keys/announcement'))
      return Response.json(
        signedAnnouncement(publication, {}, source.identity.tenantId),
      );
    if (path.endsWith('/network/keys'))
      return Response.json(signedKeyset(publication, source.identity.tenantId));
    if (path === '/api/iam/v1/network/register')
      return (
        options.register?.(init, source) ??
        Response.json({ code: 'common.forbidden' }, { status: 403 })
      );
    if (path === '/api/iam/v1/network/tickets') {
      options.ticketDenied?.();
      return Response.json({ code: 'common.forbidden' }, { status: 403 });
    }
    if (path === '/ai/mcp')
      return (
        options.teamRequest?.(init) ??
        Response.json({ code: 'common.unavailable' }, { status: 503 })
      );
    throw new Error('unexpected fixed platform path ' + path);
  };
  t.mock.method(globalThis, 'fetch', fetcher);
  await mkdir(join(directory, 'uploads'));
  await mkdir(join(directory, 'shell-network'));
  const proxy = new BusinessProxy(
    source.broker,
    fetcher,
    join(directory, 'uploads'),
    {
      networkDirectory: join(directory, 'shell-network'),
      networkCore: core,
    },
  );
  const peer = proxy.host.connect({
    verified: true,
    role: 'plugin-backend',
    sid: randomUUID(),
    connectionId: randomUUID(),
    processStart: randomUUID(),
  });
  const sessions = new Map(),
    registered = new Set(),
    subscriptions = [];
  const ctx = {
    ...(!options.noTeamCapabilities
      ? {
          tools: {
            register(tool) {
              registered.add(tool.name);
              return () => registered.delete(tool.name);
            },
          },
        }
      : {}),
    sessions: { get: (id) => sessions.get(id), flush() {} },
    sessionController: {
      create({ sessionId }) {
        sessions.set(sessionId, { header: { id: sessionId } });
        return { sessionId };
      },
      prompt() {},
    },
    ...options.ctx,
  };
  const runtime = await createConnectRuntime({
    directory,
    config,
    ctx,
    expectedIdentity: options.expectedIdentity,
    connection: {
      operations,
      validate,
      ipc: {
        async call(message) {
          if (message.path === '/host/contexts')
            return { body: await proxy.host.command(message.body, peer) };
          if (message.path === '/host/credentials')
            return { body: await proxy.host.credentials(message.body, peer) };
          if (message.call) return proxy.call(message, peer.id);
          return source.broker.command(message, peer.id);
        },
        subscribe(message, callback) {
          subscriptions.push(
            source.broker.subscribe(message, peer.id, callback),
          );
        },
        subscribeStream(message, callback) {
          subscriptions.push(
            proxy.streams.subscribe(message, peer.id, callback),
          );
        },
        streamCommand(message) {
          return proxy.streams.command(message, peer.id);
        },
        close() {
          for (const stop of subscriptions.splice(0)) stop();
          proxy.host.disconnect(peer);
        },
      },
    },
  });
  t.after(async () => {
    await runtime.close();
    proxy.close();
    await Promise.allSettled([source.broker.close()]);
    await rm(directory, { recursive: true, force: true });
  });
  const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
  async function complete() {
    await runtime.command(request, 'open_login');
    await Promise.allSettled([...source.broker.inflight]);
    await new Promise((resolve) => setImmediate(resolve));
  }
  if (options.loggedIn) await complete();
  return {
    source,
    runtime,
    request,
    requests,
    directory,
    registered,
    complete,
    ctx,
    proxy,
    peer,
  };
}
