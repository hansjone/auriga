import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import { apply } from '../../src/plugin.js';
import { createSettingsStore } from '../../src/settings/store.js';

for (const scenario of ['missing', 'false', 'true', 'saved'])
  test(`formal apply respects HTTP permission provenance: ${scenario}`, async () => {
    const dir = await mkdtemp(join(tmpdir(), 'auriga-http-permission-'));
    const routes = [],
      cleanups = [],
      pending = [];
    const origin = 'http://10.1.2.3:8080'; // gitleaks:allow
    const config = {
      connect: {
        platformOrigin: origin,
        issuer: origin,
        clientId: 'auriga-dsh',
        providerId: 'team',
        ...(scenario === 'missing'
          ? {}
          : { allowLocalHttp: scenario === 'true' }),
      },
    };
    const ctx = {
      profileContext: { dir },
      connection: {
        requestRejection() {},
        admit() {
          return { peer: { principal: { kind: 'owner' } } };
        },
        authorizeIndex() {
          return true;
        },
        isTrusted() {
          return true;
        },
      },
      webServer: {
        protocol: 'http:',
        register(route) {
          routes.push(route);
          return () => {};
        },
      },
      tools: {
        register() {
          return () => {};
        },
      },
      sessions: { get() {}, flush() {} },
      sessionController: { create() {}, prompt() {} },
      on() {
        return () => {};
      },
      effect(fn) {
        const dispose = fn();
        if (dispose) cleanups.push(dispose);
      },
      inject(keys, callback) {
        if (!keys.includes('llm') && !keys.includes('aurigaNetwork'))
          pending.push(callback(ctx));
      },
    };
    try {
      if (scenario === 'saved') {
        const store = await createSettingsStore(join(dir, 'auriga-settings'));
        await store.save({ platformOrigin: origin, deployment: 'local' });
      }
      await apply(ctx, config);
      await Promise.all(pending);
      const req = Readable.from([]);
      Object.assign(req, {
        method: 'GET',
        url: '/auriga/settings/state',
        headers: { host: 'localhost:3000' },
        rawHeaders: ['Host', 'localhost:3000'],
      });
      const res = {
        writeHead(status) {
          this.status = status;
        },
        end(body) {
          this.body = JSON.parse(body);
        },
      };
      await routes
        .find((route) => route.path === '/auriga/settings')
        .handler(req, res);
      assert.equal(res.status, 200);
      assert.equal(res.body.platformOrigin, origin);
      assert.equal(
        res.body.activeOrigin,
        ['true', 'saved'].includes(scenario) ? origin : '',
      );
    } finally {
      await Promise.all(cleanups.reverse().map((dispose) => dispose()));
      await rm(dir, { recursive: true, force: true });
    }
  });
