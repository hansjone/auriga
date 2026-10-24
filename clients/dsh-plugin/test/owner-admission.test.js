import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fork } from 'node:child_process';
import { mkdtemp, rm, writeFile, mkdir, readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createOwnerAdmission } from '../src/identity/owner-admission.js';
import { createStandalone } from '../src/modes/standalone.js';
import { testEmergency, otpFor } from './helpers.js';

const key = 'auriga-dsh/standalone-owner';
const owner = { providerId: 'emergency', subject: 'alice' };
const state = (lastCounter = -1) => ({
  version: 1,
  owner: { ...owner },
  lastCounter,
});
const record = (lastCounter = -1) => ({
  kind: 'grant',
  payload: state(lastCounter),
});
const input = (emergencyCounter = 10) => ({
  providerId: owner.providerId,
  profile: { subject: owner.subject, mfaPerformed: true },
  emergencyCounter,
});
const internalError = { message: 'owner admission unavailable' };

function credentialFixture(initial) {
  let current = initial;
  let tail = Promise.resolve();
  let writes = 0;
  const credentials = {
    modifyRecord(keyArg, mutate) {
      assert.equal(keyArg, key);
      const operation = tail.then(async () => {
        const next = await mutate(current);
        if (next !== undefined) {
          assert.notEqual(next, current);
          current = next;
          writes++;
        }
        return current;
      });
      tail = operation.catch(() => {});
      return operation;
    },
  };
  return {
    credentials,
    current: () => current,
    writes: () => writes,
    replace: (value) => {
      current = value;
    },
  };
}

test('owner record requires explicit initialization and read-only opens never rewrite it', async () => {
  const fixture = credentialFixture(undefined);
  await assert.rejects(
    createOwnerAdmission({ credentials: fixture.credentials, owner }),
    internalError,
  );
  assert.equal(fixture.writes(), 0);
  const admission = await createOwnerAdmission({
    credentials: fixture.credentials,
    owner,
    initialize: true,
  });
  assert.deepEqual(fixture.current(), record());
  await createOwnerAdmission({
    credentials: fixture.credentials,
    owner,
    initialize: true,
  });
  const read = await admission.readState();
  read.owner.subject = 'changed';
  read.lastCounter = 500;
  assert.deepEqual(await admission.readState(), state());
  assert.equal(fixture.writes(), 1);
});

test('initialization snapshots caller owner and never changes frozen provider records or input', async () => {
  const existing = Object.freeze({
    kind: 'grant',
    payload: Object.freeze({ ...state(), owner: Object.freeze({ ...owner }) }),
  });
  const fixture = credentialFixture(existing);
  const mutableOwner = { ...owner };
  const admission = await createOwnerAdmission({
    credentials: fixture.credentials,
    owner: mutableOwner,
  });
  mutableOwner.subject = 'mallory';
  const value = Object.freeze({
    ...input(),
    profile: Object.freeze(input().profile),
  });
  assert.equal(await admission.commitAdmission(value), true);
  assert.deepEqual(existing, record());
  assert.deepEqual(fixture.current(), record(10));
});

for (const invalidOwner of [
  undefined,
  null,
  {},
  { providerId: '', subject: 'alice' },
  { providerId: 'emergency', subject: ' ' },
  { ...owner, extra: true },
]) {
  test(`invalid fixed owner is rejected (${JSON.stringify(invalidOwner)})`, async () => {
    const fixture = credentialFixture(record());
    await assert.rejects(
      createOwnerAdmission({
        credentials: fixture.credentials,
        owner: invalidOwner,
      }),
      internalError,
    );
    assert.equal(fixture.writes(), 0);
  });
}

const corruptRecords = [
  { kind: 'api-key', key: 'test-only' },
  { kind: 'grant', payload: { ...state(), version: 2 } },
  {
    kind: 'grant',
    payload: { ...state(), owner: { ...owner, subject: 'mallory' } },
  },
  { kind: 'grant', payload: { ...state(), owner: { ...owner, extra: true } } },
  { kind: 'grant', payload: { ...state(), extra: true } },
  { ...record(), extra: true },
  { kind: 'grant', payload: { ...state(), lastCounter: -2 } },
  { kind: 'grant', payload: { ...state(), lastCounter: 0.5 } },
  {
    kind: 'grant',
    payload: { ...state(), lastCounter: Number.MAX_SAFE_INTEGER + 1 },
  },
  { kind: 'grant', payload: null },
];
for (const [index, corrupt] of corruptRecords.entries()) {
  test(`existing corrupt or conflicting record is never overwritten (${index})`, async () => {
    const fixture = credentialFixture(corrupt);
    await assert.rejects(
      createOwnerAdmission({
        credentials: fixture.credentials,
        owner,
        initialize: true,
      }),
      internalError,
    );
    assert.equal(fixture.current(), corrupt);
    assert.equal(fixture.writes(), 0);
  });
}

test('atomic commit accepts one concurrent counter and never rolls back for older counters', async () => {
  const fixture = credentialFixture(record());
  const first = await createOwnerAdmission({
    credentials: fixture.credentials,
    owner,
  });
  const second = await createOwnerAdmission({
    credentials: fixture.credentials,
    owner,
  });
  const results = await Promise.all([
    first.commitAdmission(input()),
    second.commitAdmission(input()),
  ]);
  assert.deepEqual(results.sort(), [false, true]);
  assert.equal(await second.commitAdmission(input(9)), false);
  assert.equal(await second.commitAdmission(input(11)), true);
  assert.deepEqual(await first.readState(), state(11));
  assert.equal(fixture.writes(), 2);
});

test('wrong owner, missing MFA and invalid counters veto without writes', async () => {
  const fixture = credentialFixture(record());
  const admission = await createOwnerAdmission({
    credentials: fixture.credentials,
    owner,
  });
  const invalid = [
    { ...input(), providerId: 'other' },
    { ...input(), profile: { subject: 'mallory', mfaPerformed: true } },
    { ...input(), profile: { subject: 'alice' } },
    { ...input(), profile: { subject: 'alice', mfaPerformed: 'true' } },
    ...[-1, 0.1, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1].map(input),
    { ...input(), emergencyCounter: undefined },
  ];
  for (const value of invalid)
    assert.equal(await admission.commitAdmission(value), false);
  assert.equal(fixture.writes(), 0);
});

test('deletion or corruption after open fails closed and never recreates the record', async () => {
  for (const replacement of [undefined, ...corruptRecords]) {
    const fixture = credentialFixture(record());
    const admission = await createOwnerAdmission({
      credentials: fixture.credentials,
      owner,
      initialize: true,
    });
    fixture.replace(replacement);
    await assert.rejects(admission.readState(), internalError);
    await assert.rejects(admission.commitAdmission(input()), internalError);
    assert.equal(fixture.current(), replacement);
    assert.equal(fixture.writes(), 0);
  }
});

test('storage errors including post-write errors are sanitized without cause or success', async () => {
  const fixture = credentialFixture(record());
  const admission = await createOwnerAdmission({
    credentials: fixture.credentials,
    owner,
  });
  const modify = fixture.credentials.modifyRecord;
  fixture.credentials.modifyRecord = async (...args) => {
    await modify(...args);
    throw new Error('secret payload and private path');
  };
  await assert.rejects(admission.commitAdmission(input()), (error) => {
    assert.equal(error.message, internalError.message);
    assert.equal(error.cause, undefined);
    assert.equal(error.code, undefined);
    return true;
  });
  assert.deepEqual(fixture.current(), record(10));
  await assert.rejects(admission.readState(), internalError);
  await assert.rejects(
    createOwnerAdmission({
      credentials: {
        modifyRecord: async () => {
          throw new Error('private');
        },
      },
      owner,
    }),
    internalError,
  );
});

const dshPackage = process.env.AURIGA_TEST_DSH_PACKAGE;

async function openRealProvider(home) {
  const { Context, Service } = await import(
    pathToFileURL(
      join(dshPackage, 'node_modules/@deepseek-ai/cordis/lib/index.js'),
    ).href
  );
  const { LocalCredentialProvider } = await import(
    pathToFileURL(
      join(
        dshPackage,
        'node_modules/@deepseek-ai/dsh-credentials-local/lib/index.js',
      ),
    ).href
  );
  const provider = new LocalCredentialProvider(new Context(), {
    dshHome: home,
    watch: false,
  });
  const lifecycle = provider[Service.init]();
  const cleanup = (await lifecycle.next()).value;
  await lifecycle.next();
  return { provider, cleanup };
}

function probe(home, operation, counter) {
  const child = fork(
    fileURLToPath(new URL('./private-owner-provider.mjs', import.meta.url)),
    [dshPackage, home, operation, String(counter)],
    {
      cwd: home,
      silent: true,
      env: { ...process.env, DSH_HOME: home },
    },
  );
  const ready = Promise.withResolvers();
  const done = Promise.withResolvers();
  let result;
  child.on('message', (message) => {
    if (message.ready) ready.resolve();
    else result = message;
  });
  child.on('error', () => {
    ready.reject(new Error('provider probe failed'));
    done.reject(new Error('provider probe failed'));
  });
  child.on('exit', (code) => {
    if (code === 0 && result && !result.failed) done.resolve(result);
    else {
      ready.reject(new Error('provider probe failed'));
      done.reject(new Error('provider probe failed'));
    }
  });
  return {
    ready: ready.promise,
    done: done.promise,
    go: () => child.send('go'),
    kill: () => child.kill(),
  };
}

test(
  'actual rc.2 provider: private file, cross-process replay exclusion, restart, conflict and storage failures',
  { skip: !dshPackage, timeout: 30_000 },
  async (t) => {
    const home = await mkdtemp(
      join(process.cwd(), '.owner-admission-fixture-'),
    );
    const fixtureRoot = resolve(process.cwd());
    t.after(async () => {
      assert.ok(
        home.startsWith(join(fixtureRoot, '.owner-admission-fixture-')),
      );
      await rm(home, { recursive: true, force: true });
    });
    const packageJson = JSON.parse(
      await readFile(
        join(
          dshPackage,
          'node_modules/@deepseek-ai/dsh-credentials-local/package.json',
        ),
        'utf8',
      ),
    );
    assert.equal(packageJson.version, '0.2.0-rc.2');
    const first = await openRealProvider(home);
    t.after(first.cleanup);
    await assert.rejects(
      createOwnerAdmission({ credentials: first.provider, owner }),
      internalError,
    );
    const admission = await createOwnerAdmission({
      credentials: first.provider,
      owner,
      initialize: true,
    });
    const second = await openRealProvider(home);
    t.after(second.cleanup);
    const other = await createOwnerAdmission({
      credentials: second.provider,
      owner,
    });
    assert.deepEqual(
      (
        await Promise.all([
          admission.commitAdmission(input(1)),
          other.commitAdmission(input(1)),
        ])
      ).sort(),
      [false, true],
    );

    const children = [probe(home, 'commit', 2), probe(home, 'commit', 2)];
    t.after(() => children.forEach((child) => child.kill()));
    await Promise.all(children.map((child) => child.ready));
    children.forEach((child) => child.go());
    const results = await Promise.all(children.map((child) => child.done));
    assert.deepEqual(results.map((result) => result.accepted).sort(), [
      false,
      true,
    ]);
    const restarted = probe(home, 'commit', 2);
    t.after(restarted.kill);
    await restarted.ready;
    restarted.go();
    assert.deepEqual(await restarted.done, { accepted: false, lastCounter: 2 });
    assert.equal((await admission.readState()).lastCounter, 2);
    assert.equal(await admission.commitAdmission(input(1)), false);
    await assert.rejects(
      createOwnerAdmission({
        credentials: second.provider,
        owner: { ...owner, subject: 'mallory' },
        initialize: true,
      }),
      internalError,
    );

    let nowMs = Date.parse('2026-06-01T00:00:00.000Z');
    const account = testEmergency();
    const runtime = await createStandalone({
      now: () => new Date(nowMs),
      commitAdmission: admission.commitAdmission,
      config: {
        secure: false,
        owner: { providers: ['emergency'], subject: 'alice' },
        connectors: [{ type: 'emergency', id: 'emergency', config: account }],
      },
    });
    const login = async () => {
      const begin = await runtime.beginLogin('emergency');
      return runtime.advanceLogin(begin.txId, {
        type: 'form',
        formId: 'emergency',
        values: {
          username: account.username,
          password: account.password,
          otp: otpFor(account.totpSecret, new Date(nowMs)),
        },
      });
    };
    const success = await login();
    assert.equal(success.step.type, 'complete');
    assert.ok(success.setCookie);
    assert.equal(
      (await admission.readState()).lastCounter,
      Math.floor(nowMs / 30_000),
    );

    await second.provider.deleteRecord(key);
    nowMs += 30_000;
    const failedLogin = await login();
    assert.equal(failedLogin.step.code, 'iam.connector_unavailable');
    assert.equal(failedLogin.setCookie, undefined);
    assert.equal(failedLogin.csrf, undefined);
    assert.equal(failedLogin.session, undefined);
    await assert.rejects(admission.commitAdmission(input(3)), internalError);
    await assert.rejects(admission.readState(), internalError);
    await writeFile(join(home, '.credentials.yaml'), 'invalid: [');
    await assert.rejects(admission.commitAdmission(input(3)), internalError);
    const badProvider = await openRealProvider(join(home, 'empty'));
    t.after(badProvider.cleanup);
    // A directory in the document position forces a genuine provider I/O error.
    await mkdir(join(home, 'empty/.credentials.yaml'), { recursive: true });
    await assert.rejects(
      createOwnerAdmission({
        credentials: badProvider.provider,
        owner,
        initialize: true,
      }),
      internalError,
    );
  },
);
