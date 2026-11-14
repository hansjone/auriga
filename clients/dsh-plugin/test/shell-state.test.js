import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createShellState } from '../src/local/shell-state.js';

async function fixture(t) {
  let time = Date.now(),
    mono = 0,
    callback,
    disconnect,
    sub,
    send;
  let revision = 1,
    epoch = randomUUID(),
    state = 'authenticated';
  const ids = {
    tenantId: randomUUID(),
    memberId: randomUUID(),
    deviceId: randomUUID(),
    providerId: randomUUID(),
  };
  const commands = [];
  const row = () => ({
    protocolVersion: 1,
    shellEpoch: epoch,
    revision,
    state,
    expiresAt: new Date(time + 30000).toISOString(),
    ...(state === 'authenticated' ? { identity: ids } : {}),
  });
  const transport = {
    async send(command) {
      commands.push(command);
      if (send) return send(command);
      if (command.action === 'subscribe') {
        sub = command.requestId;
        return {
          protocolVersion: 1,
          requestId: sub,
          type: 'snapshot',
          snapshot: row(),
        };
      }
      return {
        protocolVersion: 1,
        requestId: command.requestId,
        type: 'snapshot',
        snapshot: row(),
      };
    },
    subscribe(fn, off) {
      callback = fn;
      disconnect = off;
      return () => {};
    },
  };
  const shell = createShellState({
    transport,
    now: () => time,
    monotonic: () => mono,
  });
  t.after(() => shell.close());
  await shell.connect();
  return {
    shell,
    commands,
    ids,
    event(change = {}) {
      callback({
        protocolVersion: 1,
        requestId: sub,
        type: 'changed',
        snapshot: { ...row(), ...change },
      });
    },
    disconnect() {
      disconnect();
    },
    expired() {
      time += 30001;
      mono += 30001;
    },
    rollback() {
      time -= 60000;
      mono += 30001;
    },
    next(s) {
      revision++;
      state = s;
    },
    restart() {
      epoch = randomUUID();
      revision = 0;
    },
    override(fn) {
      send = fn;
    },
  };
}
test('discovers once, subscribes with exact epoch/revision, projects no credentials', async (t) => {
  const f = await fixture(t);
  assert.equal(
    f.commands[0].shellEpoch,
    '00000000-0000-0000-0000-000000000000',
  );
  assert.equal(f.commands[1].shellEpoch, f.shell.current().shellEpoch);
  assert.equal(f.commands[1].expectedRevision, 1);
  f.event({ token: 'must-not-project' });
  assert.equal(f.shell.current().token, undefined);
});
test('push authentication becomes visible and invalidates old requests on logout', async (t) => {
  const f = await fixture(t),
    check = f.shell.fence();
  f.next('signed_out');
  f.event();
  assert.equal(f.shell.current().state, 'signed_out');
  assert.throws(check, { code: 'common.unauthenticated' });
});
test('same epoch lower revision cannot roll back current identity', async (t) => {
  const f = await fixture(t);
  f.next('revoked');
  f.event();
  f.event({ revision: 1, state: 'authenticated', identity: f.ids });
  assert.equal(f.shell.current().state, 'revoked');
});
test('same revision identity replacement fails closed', async (t) => {
  const f = await fixture(t);
  f.event({ identity: { ...f.ids, memberId: randomUUID() } });
  assert.equal(f.shell.current(), undefined);
});
test('snapshot expires independently of a backward wall clock', async (t) => {
  const f = await fixture(t),
    check = f.shell.fence();
  f.rollback();
  assert.equal(f.shell.current(), undefined);
  assert.throws(check, { code: 'common.unauthenticated' });
});
test('disconnect immediately invalidates platform state', async (t) => {
  const f = await fixture(t),
    check = f.shell.fence();
  f.disconnect();
  assert.equal(f.shell.current(), undefined);
  assert.throws(check);
});
test('broker restart requires new discovery, old requests stay fenced', async (t) => {
  const f = await fixture(t),
    check = f.shell.fence(),
    old = f.shell.current().shellEpoch;
  f.restart();
  f.event();
  assert.equal(f.shell.current().shellEpoch, old);
  await f.shell.connect();
  assert.notEqual(f.shell.current().shellEpoch, old);
  assert.throws(check);
});
test('logout clears local authority before acknowledgment and rejects old snapshots', async (t) => {
  const f = await fixture(t);
  let finish;
  f.override(
    (command) =>
      new Promise((r) => {
        finish = () =>
          r({ protocolVersion: 1, requestId: command.requestId, type: 'ack' });
      }),
  );
  const pending = f.shell.command('logout');
  assert.equal(f.shell.current(), undefined);
  f.event();
  assert.equal(f.shell.current(), undefined);
  finish();
  await pending;
});
test('unknown commands, expiry and invalid snapshot producers cannot gain authority', async (t) => {
  const f = await fixture(t);
  await assert.rejects(f.shell.command('arbitrary_url'), {
    code: 'common.validation_failed',
  });
  f.event({ expiresAt: new Date(Date.now() + 600000).toISOString() });
  assert.equal(f.shell.current(), undefined);
});
test('missing broker leaves owner usable with no platform authority', async (t) => {
  const shell = createShellState({ transport: undefined });
  t.after(() => shell.close());
  await shell.connect();
  assert.equal(shell.current(), undefined);
  assert.throws(() => shell.fence(), { code: 'common.unauthenticated' });
});

for (const action of ['cancel_login', 'logout']) {
  test(`${action} reply cannot replace a newer revocation push`, async (t) => {
    const f = await fixture(t);
    f.override(async (command) => {
      f.next('signed_out');
      f.event();
      const snapshot = f.shell.current();
      f.next('revoked');
      f.event();
      return { requestId: command.requestId, type: 'snapshot', snapshot };
    });
    await assert.rejects(f.shell.command(action), { code: 'common.conflict' });
    assert.equal(f.shell.current().state, 'revoked');
  });

  test(`${action} reply cannot restore authority after transport disconnect`, async (t) => {
    const f = await fixture(t);
    f.override(async (command) => {
      f.next('signed_out');
      f.event();
      const snapshot = f.shell.current();
      f.disconnect();
      return { requestId: command.requestId, type: 'snapshot', snapshot };
    });
    await assert.rejects(f.shell.command(action), { code: 'common.conflict' });
    assert.equal(f.shell.current(), undefined);
  });
}
