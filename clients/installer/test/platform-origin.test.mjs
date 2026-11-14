import test from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { readFileSync, writeFileSync } from 'node:fs';
import {
  normalizePlatformBinding,
  updateOrigin,
} from '../runtime/platform-origin.mjs';
import { install, readState } from '../runtime/core.mjs';
import { buildPayload, tempBase } from './helpers.mjs';

test('HTTPS installer input normalizes slash, host case, default port and path', (t) => {
  const base = tempBase(t),
    payload = buildPayload(base);
  for (const [i, input] of [
    'https://PORTAL.example.com/',
    'https://portal.example.com:443',
    'https://portal.example.com/ignored/path',
  ].entries()) {
    assert.deepEqual(normalizePlatformBinding(input), {
      origin: 'https://portal.example.com',
    });
    const root = join(base, 'install-' + i),
      dataHome = join(base, 'home-' + i);
    install({
      root,
      dataHome,
      payload: payload.dir,
      platformOrigin: input,
      smoke: () => {},
    });
    assert.deepEqual(
      JSON.parse(readFileSync(join(root, 'desktop-origin.json'), 'utf8')),
      { origin: 'https://portal.example.com' },
    );
    assert.match(
      readFileSync(join(dataHome, 'profiles/auriga/cordis.patch.yml'), 'utf8'),
      /https:\/\/portal.example.com/,
    );
  }
  assert.throws(
    () => updateOrigin('https://portal.example.com/path'),
    /canonical/,
  );
});

test('HTTP confirms canonical private and loopback IP origins only', () => {
  for (const input of [
    'http://127.0.0.1',
    'http://10.1.2.3', // gitleaks:allow
    'http://172.16.0.1', // gitleaks:allow
    'http://172.31.255.254', // gitleaks:allow
    'http://192.168.2.10', // gitleaks:allow
    'http://[::1]',
    'http://[fc00::1]', // gitleaks:allow
    'http://[fd12::1234]', // gitleaks:allow
  ]) {
    const origin = new URL(input).origin;
    assert.deepEqual(
      normalizePlatformBinding(input + '/', {
        accessMode: 'ip',
        confirmedOrigin: input + ':80/',
      }),
      { origin, accessMode: 'ip', confirmedOrigin: origin },
    );
  }
  const expanded = 'HTTP://[FD12:0:0:0:0:0:0:1]:80/'; // gitleaks:allow
  const compressed = 'http://[fd12::1]'; // gitleaks:allow
  assert.deepEqual(
    normalizePlatformBinding(expanded, {
      accessMode: 'ip',
      confirmedOrigin: compressed,
    }),
    { origin: compressed, accessMode: 'ip', confirmedOrigin: compressed },
  );
});

test('credentials, HTTP paths, aliases and public IPs are rejected before installation', () => {
  for (const input of [
    'https://alice:password@example.com/path', // gitleaks:allow
    'http://alice:password@127.0.0.1', // gitleaks:allow
    'http://127.0.0.1/path',
    'http://127.0.0.1?x=1',
    'http://127.0.0.1#fragment',
    'http://localhost',
    'http://2130706433',
    'http://0177.0.0.1',
    'http://0x7f000001',
    'http://127.1',
    'http://127.0.0.1.',
    'http://[::ffff:127.0.0.1]',
    'http://[2001:db8::1]',
    'http://[fe80::1]',
    'http://192.0.2.10',
    'http://8.8.8.8',
    'http://172.15.0.1', // gitleaks:allow
    'http://172.32.0.1', // gitleaks:allow
    'http://192.169.1.1', // gitleaks:allow
  ])
    assert.throws(() =>
      normalizePlatformBinding(input, {
        accessMode: 'ip',
        confirmedOrigin: input,
      }),
    );
  assert.throws(
    () =>
      normalizePlatformBinding('http://127.0.0.1', {
        accessMode: 'ip',
        confirmedOrigin: 'http://127.0.0.2',
      }),
    /differs/,
  );
});

test('explicit confirmation replaces the binding; missing confirmation fails without mutation', (t) => {
  const base = tempBase(t),
    root = join(base, 'install'),
    dataHome = join(base, 'home'),
    payload = buildPayload(base);
  install({ root, dataHome, payload: payload.dir, smoke: () => {} });
  const file = join(root, 'desktop-origin.json'),
    origin = 'http://127.0.0.1:8080';
  const legacy = { origin, extra: 'must not survive' };
  writeFileSync(file, JSON.stringify(legacy));
  const before = readState(root);
  assert.throws(
    () => install({ root, dataHome, payload: payload.dir, smoke: () => {} }),
    /explicit address confirmation/,
  );
  assert.deepEqual(readState(root), before);
  assert.deepEqual(JSON.parse(readFileSync(file)), legacy);
  install({
    root,
    dataHome,
    payload: payload.dir,
    platformOrigin: origin + '/',
    platformBinding: { accessMode: 'ip', confirmedOrigin: origin },
    smoke: () => {},
  });
  assert.deepEqual(JSON.parse(readFileSync(file)), {
    origin,
    accessMode: 'ip',
    confirmedOrigin: origin,
  });
  // Explicit confirmation regenerates the binding without parsing old formats.
  writeFileSync(file, 'not-json');
  install({
    root,
    dataHome,
    payload: payload.dir,
    platformOrigin: origin,
    platformBinding: { accessMode: 'ip', confirmedOrigin: origin },
    smoke: () => {},
  });
  assert.deepEqual(JSON.parse(readFileSync(file)), {
    origin,
    accessMode: 'ip',
    confirmedOrigin: origin,
  });
});
