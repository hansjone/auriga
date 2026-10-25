import assert from 'node:assert/strict';
import test from 'node:test';
import { setImmediate } from 'node:timers';
import { createHostPortOperations as createPort } from '../../../../src/network/host/native/host-port.js';
import { createWorld, peer, unicast } from '../fixtures.js';
import { fixture } from './fixtures.js';

test('core admission and receipts use the native seam; failed flush never accepts or re-prompts', async () => {
  const f = fixture();
  const world = await createWorld({ host: await createPort(f) });
  const local = await world.adapter.bindExistingSession({
    opId: 'bind',
    hostSessionId: 'session-a',
    cwd: '/work/project',
    executeMode: 'prompt',
  });
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-1',
    local,
    threadId: 'T1',
    peer: peer('5'),
  });
  await world.adapter.activateOutboundRequest('req-1');
  const item = unicast({ recipientId: 'r1', local });
  f.state.flush = false;
  const first = await world.platform.pump(world.adapter, [item]);
  assert.equal(first.receipts[0].status, 'retry');
  assert.equal(world.platform.state.committed.has('r1'), false);
  f.state.flush = true;
  const recovered = await world.platform.pump(world.adapter, [item]);
  assert.equal(recovered.receipts[0].status, 'host_accepted');
  assert.equal(recovered.receipts[0].hostRequestId, 'r1');
  assert.equal(world.platform.state.calls.admit, 1);
  await world.platform.pump(world.adapter, [item]);
  assert.equal(f.calls.filter(([kind]) => kind === 'prompt').length, 1);
  assert.equal(
    f.calls.some(([kind]) => kind === 'create'),
    false,
  );
});

test('first message before open-contact response waits for the fixed native binding', async () => {
  const f = fixture();
  const world = await createWorld({ host: await createPort(f) });
  const first = unicast({
    recipientId: 'sys-r',
    local: '0',
    kind: 'first_contact',
    inReplyTo: null,
    messageId: 'contact-a',
    contactScope: 'conversation',
    threadId: 'TC',
  });
  await world.platform.pump(world.adapter, [first]);
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-1',
    local: '1',
    threadId: 'T1',
    peer: peer('5'),
  });
  await world.adapter.activateOutboundRequest('req-1');
  let entered, release;
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const create = f.ctx.sessionController.create;
  f.ctx.sessionController.create = async (request) => {
    const result = await create(request);
    entered();
    await pending;
    return result;
  };
  const opening = world.adapter.openContactSession({
    contactId: 'contact-a',
    cwd: '/contact/project',
    preset: 'coding',
    receiveMode: 'thread_messages',
    executeMode: 'prompt',
  });
  await started;
  const contact = world.ledger.read().contacts['contact-a'];
  const item = unicast({ recipientId: 'early-r', local: contact.local });
  const delivery = world.platform.pump(world.adapter, [item]);
  try {
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(world.record('early-r'), undefined);
    release();
    const opened = await opening;
    assert.equal(opened.state, 'session_ready');
    const result = await delivery;
    assert.equal(result.receipts[0].status, 'host_accepted');
    assert.equal(
      f.calls.find(([kind]) => kind === 'prompt')[1].sessionId,
      contact.hostSessionId,
    );
    assert.equal(
      (await world.host.getSession(contact.hostSessionId)).cwd,
      '/contact/project',
    );
    assert.equal(f.calls.filter(([kind]) => kind === 'create').length, 1);
  } finally {
    release();
    await Promise.allSettled([opening, delivery]);
  }
});
