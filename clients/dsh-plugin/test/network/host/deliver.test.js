import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Crash,
  PEER_NODE,
  boundSessionWithRelation,
  createWorld,
  multicast,
  peer,
  unicast,
} from './fixtures.js';
import { createHostAdapter } from '../../../src/network/host/adapter.js';
import { CODES } from '../../../src/network/host/errors.js';

const deferred = () => {
  let resolve;
  const promise = new Promise((r) => (resolve = r));
  return { promise, resolve };
};

/** Approved conversation first contact delivered to sys (local 0). */
async function acceptFirstContact(world, contactId = 'contact-1') {
  const item = unicast({
    recipientId: 'sys-r1',
    local: '0',
    kind: 'first_contact',
    inReplyTo: null,
    messageId: contactId,
    contactScope: 'conversation',
    threadId: 'TC',
    text: '请求建立对话',
  });
  const { receipts } = await world.platform.pump(world.adapter, [item]);
  return receipts[0];
}

test('existing binding with non-default cwd resumes the cold session; never creates', async () => {
  const world = await createWorld();
  const local = await boundSessionWithRelation(world, {
    cwd: 'E:/elsewhere/proj',
  });
  assert.equal(local, '1');
  const { receipts } = await world.platform.pump(world.adapter, [
    unicast({ recipientId: 'r1', local }),
  ]);
  assert.equal(receipts[0].status, 'host_accepted');
  assert.equal(receipts[0].acceptedBy, 'session');
  assert.equal(receipts[0].acceptanceMode, 'prompt');
  assert.equal(world.host.counts.create, 0);
  assert.equal(world.host.counts.resume, 1);
  const text = world.host.history('host-a1')[0].text;
  assert.match(text, /不可信外部数据/);
  assert.match(text, /aur1:2\.3f\.5/);
  assert.equal('hostSessionId' in receipts[0], false);
});

test('system inbox local=0: approved first contact is system-accepted, no model', async () => {
  const world = await createWorld();
  const receipt = await acceptFirstContact(world);
  assert.equal(receipt.status, 'host_accepted');
  assert.equal(receipt.acceptedBy, 'system');
  assert.equal(receipt.acceptanceMode, 'inbox_only');
  assert.deepEqual(receipt.targetAddress, {
    version: 1,
    realm: '2',
    type: 'system',
    node: '64',
    local: '0',
  });
  assert.equal(
    world.ledger.read().contacts['contact-1'].state,
    'awaiting_session',
  );
  assert.equal(world.host.counts.prompt + world.host.counts.create, 0);
  // control to sys is refused with the uniform receipt.
  const ctl = await world.platform.pump(world.adapter, [
    unicast({
      recipientId: 'sys-ctl',
      local: '0',
      kind: 'control',
      inReplyTo: null,
    }),
  ]);
  assert.equal(ctl.receipts[0].status, 'rejected');
  assert.equal(ctl.receipts[0].code, CODES.targetUnavailable);
});

test('contact session: durable ID + explicit cwd, create once, bind, session-stage receipt', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  const out = await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/team/new',
  });
  assert.equal(out.state, 'session_ready');
  assert.equal(out.local, '1');
  const c = world.ledger.read().contacts['contact-1'];
  assert.equal(world.host.counts.create, 1);
  assert.equal(world.host.sessions.get(c.hostSessionId).cwd, 'F:/team/new');
  assert.equal(world.ledger.read().bindings['1'].state, 'bound');
  // Repeating is idempotent.
  await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/team/new',
  });
  assert.equal(world.host.counts.create, 1);
  // Without second-layer authorization the session stage is inbox_only.
  const r = await world.adapter.deliverContactToSession({
    contactId: 'contact-1',
    epoch: world.platform.state.epoch,
    bootId: 'boot-1',
    attemptId: world.platform.attemptBegin('sys-r1'),
  });
  assert.equal(r.status, 'host_accepted');
  assert.equal(r.acceptedBy, 'session');
  assert.equal(r.acceptanceMode, 'inbox_only');
  assert.equal(r.assignedAddress.local, '1');
  assert.equal(r.targetAddress.local, '0');
  assert.equal(world.host.counts.prompt, 0);
});

test('contact session with execution authorized prompts once with requestId = sys recipient', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/team/new',
    executeMode: 'prompt',
  });
  const args = () => ({
    contactId: 'contact-1',
    epoch: world.platform.state.epoch,
    bootId: 'boot-1',
    attemptId: world.platform.attemptBegin('sys-r1'),
  });
  const r = await world.adapter.deliverContactToSession(args());
  assert.equal(r.status, 'host_accepted');
  assert.equal(r.acceptanceMode, 'prompt');
  const again = await world.adapter.deliverContactToSession(args());
  assert.equal(again.status, 'host_accepted');
  assert.equal(world.host.counts.prompt, 1);
  const sid = world.ledger.read().contacts['contact-1'].hostSessionId;
  assert.deepEqual(
    world.host.history(sid).map((x) => x.requestId),
    ['sys-r1'],
  );
});

test('create response lost → query same identity, no second create', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  world.host.faults.loseCreateResponse = true;
  const out = await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
  });
  assert.equal(out.state, 'session_ready');
  assert.equal(world.host.counts.create, 1);
});

test('crash after creation_started but before create → zero creations, outcome unknown', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  world.crashAt('before_create');
  await assert.rejects(
    world.adapter.openContactSession({ contactId: 'contact-1', cwd: 'F:/x' }),
    Crash,
  );
  const adapter = await world.restart();
  const out = await adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
  });
  assert.equal(out.state, 'unknown');
  assert.equal(world.host.counts.create, 0);
  assert.equal(world.ledger.read().bindings['1'].state, 'unknown');
});

test('crash right after create → restart binds the created session, still one create', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  world.crashAt('after_create');
  await assert.rejects(
    world.adapter.openContactSession({ contactId: 'contact-1', cwd: 'F:/x' }),
    Crash,
  );
  const adapter = await world.restart();
  const out = await adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
  });
  assert.equal(out.state, 'session_ready');
  assert.equal(world.host.counts.create, 1);
});

test('CONTACT_ASSIGN response lost → no creation permit is re-obtained', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  world.platform.drop.assign = 1;
  const first = await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
  });
  assert.equal(first.state, 'assign_pending');
  const second = await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
  });
  assert.equal(second.state, 'unknown');
  assert.equal(world.host.counts.create, 0);
});

test('host create error (e.g. session/conflict) → unknown, never a random second session', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  world.host.faults.createFails = true;
  const out = await world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
  });
  assert.equal(out.state, 'unknown');
  assert.equal(world.host.sessions.size, 0);
});

test('mapping race: message for a session being created waits on the binding lock', async () => {
  const world = await createWorld();
  await acceptFirstContact(world);
  // The local the contact will get (1) already gave its address in thread T1.
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-1',
    local: '1',
    threadId: 'T1',
    peer: peer('5'),
  });
  await world.adapter.activateOutboundRequest('req-1');
  const entered = deferred();
  const release = deferred();
  world.host.faults.createGate = {
    entered: entered.resolve,
    release: release.promise,
  };
  const opening = world.adapter.openContactSession({
    contactId: 'contact-1',
    cwd: 'F:/x',
    executeMode: 'prompt',
  });
  await entered.promise;
  const delivering = world.platform.pump(world.adapter, [
    unicast({ recipientId: 'r-race', local: '1' }),
  ]);
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(world.record('r-race'), undefined);
  release.resolve();
  const [opened, { receipts }] = await Promise.all([opening, delivering]);
  assert.equal(opened.state, 'session_ready');
  assert.equal(receipts[0].status, 'host_accepted');
  assert.equal(world.host.sessions.size, 1);
  const sid = world.ledger.read().contacts['contact-1'].hostSessionId;
  assert.deepEqual(
    world.host.history(sid).map((x) => x.requestId),
    ['r-race'],
  );
});

test('reply racing the outbound commit: reserved relation → retry, then accepted', async () => {
  const world = await createWorld();
  world.host.addSession('host-a1', { cwd: 'D:/w' });
  const local = await world.adapter.bindExistingSession({
    opId: 'b1',
    hostSessionId: 'host-a1',
    cwd: 'D:/w',
  });
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-1',
    local,
    threadId: 'T1',
    peer: peer('5'),
  });
  const item = unicast({ recipientId: 'r1', local });
  const early = await world.platform.pump(world.adapter, [item]);
  assert.equal(early.receipts[0].status, 'retry');
  assert.equal(early.receipts[0].code, CODES.relationPending);
  assert.equal(world.record('r1'), undefined);
  await world.adapter.activateOutboundRequest('req-1');
  const late = await world.platform.pump(world.adapter, [item]);
  assert.equal(late.receipts[0].status, 'host_accepted');
});

test('first-contact intro reply fixes the exact assigned address; other locals refused', async () => {
  const world = await createWorld();
  world.host.addSession('host-a1', { cwd: 'D:/w' });
  const local = await world.adapter.bindExistingSession({
    opId: 'b1',
    hostSessionId: 'host-a1',
    cwd: 'D:/w',
  });
  await world.adapter.reserveOutboundRequest({
    requestId: 'contact-9',
    local,
    threadId: 'TC',
    peerNode: PEER_NODE,
    introContactId: 'contact-9',
  });
  await world.adapter.activateOutboundRequest('contact-9');
  const intro = unicast({
    recipientId: 'r-intro',
    local,
    from: peer('9'),
    threadId: 'TC',
    inReplyTo: 'contact-9',
  });
  const other = unicast({
    recipientId: 'r-other',
    local,
    from: peer('10'),
    threadId: 'TC',
    inReplyTo: 'contact-9',
  });
  const { receipts } = await world.platform.pump(world.adapter, [intro]);
  assert.equal(receipts[0].status, 'host_accepted');
  assert.equal(world.ledger.read().relations['rel:contact-9'].peer, '2.63.9');
  const second = await world.platform.pump(world.adapter, [other]);
  assert.equal(second.receipts[0].status, 'rejected');
});

test('local gate: unrelated/guessed/archived/blocked targets get one uniform receipt shape', async () => {
  const world = await createWorld();
  const local = await boundSessionWithRelation(world);
  const cases = [
    unicast({ recipientId: 'no-rel', local, from: peer('7') }),
    unicast({ recipientId: 'unknown-local', local: '99' }),
    unicast({ recipientId: 'wrong-req', local, inReplyTo: 'req-404' }),
    unicast({
      recipientId: 'msg-in-replies-only',
      local,
      kind: 'message',
      inReplyTo: null,
    }),
    unicast({ recipientId: 'ctl', local, kind: 'control', inReplyTo: null }),
    unicast({
      recipientId: 'fc-local',
      local,
      kind: 'first_contact',
      inReplyTo: null,
    }),
    unicast({ recipientId: 'other-thread', local, threadId: 'T2' }),
  ];
  const { receipts } = await world.platform.pump(world.adapter, cases);
  for (const r of receipts) {
    assert.equal(r.status, 'rejected', r.recipientId);
    assert.equal(r.code, CODES.targetUnavailable);
    assert.deepEqual(Object.keys(r).sort(), Object.keys(receipts[0]).sort());
  }
  assert.equal(world.host.counts.prompt, 0);
  // Detailed reasons stay local.
  const reasons = new Set(world.ledger.read().audit.map((a) => a.reason));
  assert.ok(reasons.size >= 4);
  // Archive: refused without touching the host session.
  await world.adapter.updateBinding(local, { lifecycle: 'archived' });
  const resumes = world.host.counts.resume;
  const archived = await world.platform.pump(world.adapter, [
    unicast({ recipientId: 'arch', local }),
  ]);
  assert.equal(archived.receipts[0].status, 'rejected');
  assert.equal(archived.receipts[0].code, CODES.targetUnavailable);
  assert.equal(world.host.counts.resume, resumes);
  assert.equal(world.ledger.read().bodies['msg-arch'], undefined);
});

test('thread_messages mode admits ordinary messages only through a relation allowing them', async () => {
  const world = await createWorld();
  world.host.addSession('host-a1', { cwd: 'D:/w' });
  const local = await world.adapter.bindExistingSession({
    opId: 'b1',
    hostSessionId: 'host-a1',
    cwd: 'D:/w',
    receiveMode: 'thread_messages',
  });
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-1',
    local,
    threadId: 'T1',
    peer: peer('5'),
    kinds: ['reply', 'message'],
  });
  await world.adapter.activateOutboundRequest('req-1');
  const { receipts } = await world.platform.pump(world.adapter, [
    unicast({ recipientId: 'm1', local, kind: 'message', inReplyTo: null }),
  ]);
  assert.equal(receipts[0].status, 'host_accepted');
});

test('revocation between attempts wins before admit', async () => {
  const world = await createWorld();
  const local = await boundSessionWithRelation(world);
  const item = unicast({ recipientId: 'r1', local });
  world.crashAt('after_received');
  await assert.rejects(world.platform.pump(world.adapter, [item]), Crash);
  const adapter = await world.restart();
  await adapter.revokeRelation('rel:req-1');
  const { receipts } = await world.platform.pump(adapter, [item]);
  assert.equal(receipts[0].status, 'rejected');
  assert.equal(world.platform.state.admissions.size, 0);
  assert.equal(world.host.counts.prompt, 0);
});

test('multicast is inbox_only: one body, per-local receipts, zero prompts', async () => {
  const world = await createWorld();
  const a1 = await boundSessionWithRelation(world, {
    hostSessionId: 'host-a1',
  });
  world.host.addSession('host-a2', { cwd: 'D:/a2' });
  const a2 = await world.adapter.bindExistingSession({
    opId: 'b2',
    hostSessionId: 'host-a2',
    cwd: 'D:/a2',
  });
  const group = {
    version: 1,
    realm: '2',
    type: 'group',
    node: '0',
    local: '23',
  };
  await world.adapter.updateBinding(a1, { joinGroup: group });
  const item = multicast({ messageId: 'g1', locals: ['0', a1, a2] });
  const { receipts } = await world.platform.pump(world.adapter, [item]);
  const by = Object.fromEntries(receipts.map((r) => [r.recipientId, r]));
  assert.equal(by['g1:0'].status, 'host_accepted');
  assert.equal(by['g1:0'].acceptedBy, 'system');
  assert.equal(by[`g1:${a1}`].status, 'host_accepted');
  assert.equal(by[`g1:${a1}`].acceptedBy, 'session');
  assert.equal(by[`g1:${a1}`].acceptanceMode, 'inbox_only');
  assert.equal(by[`g1:${a2}`].status, 'rejected');
  assert.equal(Object.keys(world.ledger.read().bodies).length, 1);
  assert.equal(world.host.counts.prompt, 0);
  const ctl = await world.platform.pump(world.adapter, [
    multicast({ messageId: 'g2', locals: [a1], kind: 'control' }),
  ]);
  assert.equal(ctl.receipts[0].status, 'rejected');
});

test('same session is admitted serially across threads; different sessions run in parallel', async () => {
  const world = await createWorld();
  world.host.faults.promptDelay = 15;
  const a1 = await boundSessionWithRelation(world, {
    hostSessionId: 'host-a1',
  });
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-2',
    local: a1,
    threadId: 'T2',
    peer: peer('6'),
  });
  await world.adapter.activateOutboundRequest('req-2');
  await world.platform.pump(world.adapter, [
    unicast({ recipientId: 'x1', local: a1 }),
    unicast({
      recipientId: 'x2',
      local: a1,
      from: peer('6'),
      threadId: 'T2',
      inReplyTo: 'req-2',
    }),
  ]);
  assert.equal(world.host.maxPerSession, 1);
  assert.equal(world.host.counts.prompt, 2);

  const w2 = await createWorld();
  w2.host.faults.promptDelay = 15;
  const b1 = await boundSessionWithRelation(w2, {
    hostSessionId: 'host-b1',
    requestId: 'q1',
  });
  const b2 = await boundSessionWithRelation(w2, {
    hostSessionId: 'host-b2',
    requestId: 'q2',
    threadId: 'T2',
  });
  await w2.platform.pump(w2.adapter, [
    unicast({ recipientId: 'y1', local: b1, inReplyTo: 'q1' }),
    unicast({ recipientId: 'y2', local: b2, inReplyTo: 'q2', threadId: 'T2' }),
  ]);
  assert.equal(w2.host.maxInflight, 2);
});

test('same thread enters the host in page order; duplicates/reorder never double-prompt', async () => {
  const world = await createWorld();
  world.host.faults.promptDelay = 5;
  const local = await boundSessionWithRelation(world);
  const items = [
    unicast({ recipientId: 's1', local, threadSequence: '1' }),
    unicast({ recipientId: 's2', local, threadSequence: '2' }),
  ];
  await world.platform.pump(world.adapter, items);
  assert.deepEqual(
    world.host.history('host-a1').map((x) => x.requestId),
    ['s1', 's2'],
  );
  await world.platform.pump(world.adapter, items, {
    duplicate: true,
    reorder: true,
  });
  assert.equal(world.host.counts.prompt, 2);
  // A dropped page changes nothing; the next fetch delivers.
  const dropped = await world.platform.pump(
    world.adapter,
    [unicast({ recipientId: 's3', local })],
    { dropPage: true },
  );
  assert.equal(dropped.delivered, false);
  assert.equal(world.record('s3'), undefined);
});

test('subagent session after relation checks → quarantined host_incompatible', async () => {
  const world = await createWorld();
  world.host.addSession('host-sub', { cwd: 'D:/s', origin: 'subagent' });
  const local = await world.adapter.bindExistingSession({
    opId: 'bs',
    hostSessionId: 'host-sub',
    cwd: 'D:/s',
  });
  await world.adapter.reserveOutboundRequest({
    requestId: 'req-1',
    local,
    threadId: 'T1',
    peer: peer('5'),
  });
  await world.adapter.activateOutboundRequest('req-1');
  const { receipts } = await world.platform.pump(world.adapter, [
    unicast({ recipientId: 'sub', local }),
  ]);
  assert.equal(receipts[0].status, 'quarantined');
  assert.equal(receipts[0].code, CODES.hostIncompatible);
  // Unrelated sources still see only target_unavailable.
  const probe = await world.platform.pump(world.adapter, [
    unicast({ recipientId: 'sub2', local, from: peer('7') }),
  ]);
  assert.equal(probe.receipts[0].status, 'rejected');
  assert.equal(world.host.counts.prompt, 0);
});

test('bounded pending recipients, unverified pages and digest conflicts fail closed', async () => {
  const world = await createWorld();
  const local = await boundSessionWithRelation(world);
  const small = createHostAdapter({
    ledger: world.ledger,
    host: world.host,
    network: world.platform.network,
    maxPendingRecipients: 0,
  });
  const over = await small.deliver(
    world.platform.page([unicast({ recipientId: 'o1', local })]),
  );
  assert.equal(over[0].status, 'retry');
  assert.equal(over[0].code, CODES.overloaded);

  await assert.rejects(
    world.adapter.deliver({ ...world.platform.page([]), verified: false }),
  );
  const ok = unicast({ recipientId: 'd1', local });
  await world.platform.pump(world.adapter, [ok]);
  const forged = { ...unicast({ recipientId: 'd1', local, text: 'changed' }) };
  const { receipts } = await world.platform.pump(world.adapter, [forged]);
  assert.equal(receipts[0].status, 'quarantined');
  assert.equal(receipts[0].code, CODES.protocolError);
  assert.equal(world.host.counts.prompt, 1);
});
