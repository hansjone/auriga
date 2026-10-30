import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';
import { platformConfig } from '../../src/local/platform.js';

const base = {
  platformOrigin: 'https://portal.example.com',
  issuer: 'https://portal.example.com',
  clientId: 'auriga-dsh',
  providerId: 'team',
};
const pair = generateKeyPairSync('ed25519');
const spki = pair.publicKey
  .export({ format: 'der', type: 'spki' })
  .toString('base64url');
const installation = () => ({
  realm: '1',
  wssUrl: 'wss://network.example.com:9443/custom/mailbox',
  roots: [{ kid: 'root-1', public_key_spki: spki }],
});
const parse = (network) => platformConfig({ ...base, network }).network;

test('generated config types require complete installation fields and preserve readonly provider pins', () => {
  const filename = fileURLToPath(
    new URL('installation-types.ts', import.meta.url),
  ).replaceAll('\\', '/');
  const source = `
    import type { ConnectConfig, ConnectNetwork } from '../../src/generated/connect-config.js';
    import type { NetworkInstallation } from '../../src/network/runtime-types.js';
    const base = { platformOrigin: 'https://portal.example.com', issuer: 'https://portal.example.com', clientId: 'dsh', providerId: 'team' };
    const legacy: ConnectConfig = base;
    const pins: ConnectNetwork = { realm: '1', wssUrl: 'wss://network.example.com/ws', roots: [{ kid: 'root', public_key_spki: 'spki' }] };
    const complete: ConnectConfig = { ...base, network: pins };
    // @ts-expect-error network is complete when supplied
    const missing: ConnectConfig = { ...base, network: { realm: '1' } };
    // @ts-expect-error realm is a string, never a JSON number
    const numeric: ConnectNetwork = { ...pins, realm: 1 };
    // @ts-expect-error root URLs are not installation authority
    const unknown: ConnectNetwork = { ...pins, rootUrl: 'https://example.com/keys' };
    // @ts-expect-error private keys are not installation pins
    const privateRoot: ConnectNetwork = { ...pins, roots: [{ kid: 'root', private_key: 'secret' }] };
    declare const installation: NetworkInstallation;
    // @ts-expect-error provider cannot mutate URL
    installation.wssUrl = 'wss://other.example.com/ws';
    // @ts-expect-error provider cannot append roots
    installation.roots.push(pins.roots[0]);
    // @ts-expect-error provider cannot mutate a root
    installation.roots[0].public_key_spki = 'changed';
  `;
  const options = {
    noEmit: true,
    strict: true,
    skipLibCheck: true,
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.NodeNext,
    moduleResolution: ts.ModuleResolutionKind.NodeNext,
  };
  const host = ts.createCompilerHost(options);
  const original = host.getSourceFile.bind(host);
  host.getSourceFile = (name, version, ...rest) =>
    name.replaceAll('\\', '/') === filename
      ? ts.createSourceFile(name, source, version, true)
      : original(name, version, ...rest);
  const diagnostics = ts.getPreEmitDiagnostics(
    ts.createProgram([filename], options, host),
  );
  assert.deepEqual(
    diagnostics.map((d) =>
      ts.flattenDiagnosticMessageText(d.messageText, '\n'),
    ),
    [],
  );
});

test('optional installation preserves absence and freezes a detached complete copy', () => {
  assert.equal(Object.hasOwn(platformConfig(base), 'network'), false);
  const input = installation();
  const parsed = parse(input);
  assert.deepEqual(parsed, input);
  for (const value of [parsed, parsed.roots, parsed.roots[0]])
    assert.ok(Object.isFrozen(value));
  input.realm = '2';
  input.wssUrl = 'wss://other.example.com/ws';
  input.roots[0].public_key_spki = 'changed';
  input.roots.push({ kid: 'injected', public_key_spki: spki });
  assert.deepEqual(parsed, installation());
  assert.throws(() => {
    parsed.roots[0].kid = 'changed';
  }, TypeError);
});

test('installation rejects missing/unknown fields, noncanonical uint64 and root bounds or duplicate kids', () => {
  for (const invalid of [undefined, null, [], {}, 'network'])
    assert.throws(() => parse(invalid), /invalid network installation/);
  for (const field of ['realm', 'wssUrl', 'roots']) {
    const value = installation();
    delete value[field];
    assert.throws(() => parse(value), /invalid network installation/);
  }
  for (const key of [
    'rootUrl',
    'TOFU',
    'skipVerify',
    'initialize',
    'created',
    'privateKey',
    'fingerprint',
  ])
    assert.throws(
      () => parse({ ...installation(), [key]: true }),
      /invalid network installation/,
    );
  for (const realm of [
    '0',
    '01',
    '-1',
    '+1',
    '1.0',
    ' 1',
    '1e2',
    '18446744073709551616',
    '9'.repeat(100),
    1,
  ])
    assert.throws(
      () => parse({ ...installation(), realm }),
      /invalid network installation/,
    );
  assert.equal(
    parse({ ...installation(), realm: '18446744073709551615' }).realm,
    '18446744073709551615',
  );
  for (const roots of [
    [],
    Array(1),
    Array.from({ length: 17 }, (_, i) => ({
      kid: `root-${i}`,
      public_key_spki: spki,
    })),
    [installation().roots[0], installation().roots[0]],
    [
      installation().roots[0],
      {
        kid: 'root-1',
        public_key_spki: generateKeyPairSync('ed25519')
          .publicKey.export({ format: 'der', type: 'spki' })
          .toString('base64url'),
      },
    ],
    [{ kid: '', public_key_spki: spki }],
    [{ kid: 'a/b', public_key_spki: spki }],
    [{ kid: 'a'.repeat(129), public_key_spki: spki }],
    [{ ...installation().roots[0], private_key: 'secret' }],
  ])
    assert.throws(
      () => parse({ ...installation(), roots }),
      /invalid network installation/,
    );
  assert.equal(
    parse({
      ...installation(),
      roots: Array.from({ length: 16 }, (_, i) => ({
        kid: `root-${i}`,
        public_key_spki: spki,
      })),
    }).roots.length,
    16,
  );
});

test('installation reuses canonical Ed25519 public SPKI validation, refusing DER/PEM/private keys', () => {
  const ec = generateKeyPairSync('ec', { namedCurve: 'P-256' });
  const other = generateKeyPairSync('x25519');
  for (const key of [
    spki + '=',
    'A'.repeat(59),
    spki.slice(0, -1) + 'B',
    pair.publicKey.export({ format: 'pem', type: 'spki' }),
    pair.privateKey
      .export({ format: 'der', type: 'pkcs8' })
      .toString('base64url'),
    pair.privateKey.export({ format: 'pem', type: 'pkcs8' }),
    ec.publicKey.export({ format: 'der', type: 'spki' }).toString('base64url'),
    other.publicKey
      .export({ format: 'der', type: 'spki' })
      .toString('base64url'),
  ])
    assert.throws(
      () =>
        parse({
          ...installation(),
          roots: [{ kid: 'root', public_key_spki: key }],
        }),
      /invalid network installation/,
    );
});

test('installation rejects trailing line breaks rather than normalizing identity or URL text', () => {
  for (const patch of [
    { realm: '1\n' },
    { wssUrl: installation().wssUrl + '\n' },
    { roots: [{ kid: 'root\n', public_key_spki: spki }] },
  ])
    assert.throws(
      () => parse({ ...installation(), ...patch }),
      /invalid network installation/,
    );
});

test('fixed WSS accepts independent path/host/port and HTTPS IP deployments without weakening loopback rules', () => {
  for (const wssUrl of [
    'wss://portal.example.com/net/v1/ws',
    'wss://network.example.com:9443/custom/ws',
    'wss://192.0.2.1:9443/ws',
    'wss://[2001:db8::1]:9443/custom/ws',
  ])
    assert.equal(parse({ ...installation(), wssUrl }).wssUrl, wssUrl);
  for (const wssUrl of [
    'ws://localhost/ws',
    'https://portal.example.com/net/v1/ws',
    'wss://alice@network.example.com/ws',
    'wss://network.example.com/ws?ticket=x',
    'wss://network.example.com/ws#fragment',
    'wss://network.example.com/ws?',
    'wss://network.example.com/ws#',
    'not a url',
  ])
    assert.throws(
      () => parse({ ...installation(), wssUrl }),
      /invalid network installation/,
    );
  for (const origin of ['https://192.0.2.1:8443', 'https://[2001:db8::1]:8443'])
    assert.equal(
      platformConfig({
        ...base,
        platformOrigin: origin,
        issuer: origin,
        network: installation(),
      }).platformOrigin,
      origin,
    );
  for (const host of ['localhost', '127.0.0.1', '[::1]']) {
    const origin = `http://${host}:8080`;
    assert.equal(
      platformConfig({
        ...base,
        platformOrigin: origin,
        issuer: origin,
        allowLocalHttp: true,
        network: installation(),
      }).platformOrigin,
      origin,
    );
    assert.throws(() =>
      platformConfig({
        ...base,
        platformOrigin: origin,
        issuer: origin,
        network: installation(),
      }),
    );
  }
  for (const patch of [
    {
      platformOrigin: 'http://192.0.2.1',
      issuer: 'http://192.0.2.1',
      allowLocalHttp: true,
    },
    { allowLocalHttp: true },
    { issuer: 'https://other.example.com' },
    { issuer: base.issuer + '?x' },
  ])
    assert.throws(() =>
      platformConfig({ ...base, ...patch, network: installation() }),
    );
});
