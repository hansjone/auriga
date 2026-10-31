import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  installLocalAdmission,
  installHttpResponseTracking,
} from '../src/local/host.js';

for (const state of ['unconfigured', 'signed-out', 'signed-in'])
  test(`native desktop shell remains bootable while business admission is ${state}`, async () => {
    const request = (extra = {}) => ({
      method: 'GET',
      url: '/',
      headers: { host: 'localhost:3000', cookie: 'native=valid', ...extra },
      rawHeaders: ['Host', 'localhost:3000', ...Object.entries(extra).flat()],
    });
    const response = () => ({
      writeHead(status) {
        this.status = status;
      },
      end() {
        this.writableEnded = true;
      },
    });
    let hook;
    const native = (req) => req.headers.cookie === 'native=valid';
    const connection = {
      admit: (req) =>
        native(req) ? { peer: 'native-owner' } : { rejection: 401 },
      authorizeIndex(req, res) {
        if (native(req)) return true;
        res.writeHead(401);
        res.end();
        return false;
      },
      isTrusted: (req) => req.headers.host === 'localhost:3000',
    };
    const runtime = {
      isConfigured: () => state !== 'unconfigured',
      resolve: () => (state === 'signed-in' ? { key: 'local-session' } : null),
      bindSocket() {},
      installHttpTracking: () =>
        installHttpResponseTracking(
          {
            on(name, handler) {
              assert.equal(name, 'connection/request');
              hook = handler;
              return () => {};
            },
          },
          () => state === 'signed-in',
        ),
    };
    const admission = installLocalAdmission(connection, runtime, {
      protocol: 'http:',
    });
    try {
      assert.equal(connection.authorizeIndex(request(), response()), true);
      const rejected = response();
      assert.equal(
        connection.authorizeIndex(request({ cookie: '' }), rejected),
        false,
      );
      assert.equal(rejected.status, 401);
      const business = connection.admit(request());
      assert.deepEqual(
        business,
        state === 'signed-out' ? { rejection: 401 } : { peer: 'native-owner' },
      );
      for (const path of [
        '/api/settings/describe',
        '/api/llm/listConfigurableProviders',
        '/api/credentials/describe',
        '/api/account/getState',
      ]) {
        const metadata = {
          ...request({ 'content-type': 'application/json' }),
          method: 'POST',
          url: path,
        };
        assert.deepEqual(connection.admit(metadata), { peer: 'native-owner' });
        if (state !== 'unconfigured') {
          assert.deepEqual(
            connection.admit({
              ...metadata,
              headers: { ...metadata.headers, origin: 'https://evil.example' },
            }),
            { rejection: 403 },
          );
          assert.deepEqual(
            connection.admit({
              ...metadata,
              headers: { ...metadata.headers, cookie: '' },
            }),
            { rejection: 401 },
          );
          for (const suffix of ['/extra', '?method=session/create'])
            assert.deepEqual(
              connection.admit({ ...metadata, url: path + suffix }),
              { rejection: 403 },
            );
        }
      }
      if (state === 'signed-out')
        for (const path of [
          '/api/settings/update',
          '/api/credentials/set',
          '/api/account/startSignIn',
          '/api/session/create',
        ])
          assert.deepEqual(
            connection.admit({
              ...request({
                'content-type': 'application/json',
                origin: 'http://localhost:3000',
              }),
              method: 'POST',
              url: path,
            }),
            { rejection: 401 },
          );
      if (state !== 'unconfigured') {
        for (const extra of [
          { origin: 'https://evil.example' },
          { 'sec-fetch-site': 'cross-site' },
        ]) {
          const res = response();
          assert.equal(connection.authorizeIndex(request(extra), res), false);
          assert.equal(res.status, 403);
        }
      }
      for (const path of [
        '/',
        '/auriga/local-login',
        '/auriga/local-login/assets/adapter.js',
        '/auriga/local-login/status',
        '/auriga/local-login/login',
      ]) {
        const req = { ...request(), url: path };
        const res = response();
        let calls = 0;
        await hook(req, res, () => calls++);
        assert.equal(calls, 1, path);
        assert.equal(res.status, undefined, path);
      }
    } finally {
      admission.dispose();
    }
  });
