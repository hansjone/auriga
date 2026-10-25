import { test } from 'node:test';

test(
  'N1: two plugin sessions through gateway + collab PG complete RECEIPT_COMMITTED',
  {
    skip: '#138 gateway→IAM, #139/#140 signing/verifying and collab↔IAM pending; gateway Mailbox has no collab RPC implementation and FETCH lacks plugin delivery fields. No memory mailbox substitute.',
  },
);

test(
  'N1: client restart restores persistent resume_cursor and READY reconciliation',
  {
    skip: 'NetworkSession resumeCursor is memory-only; host ledger has no protocol cursor. Needs durable protocol checkpoint and authoritative PG READY reconciliation; file-ledger replay is covered separately.',
  },
);

test(
  'N1: lost receipt commit response reconciles with authoritative PG after restart',
  {
    skip: 'Requires real collab receipt transaction + gateway RPC wiring, IAM authorization and #139/#140 signatures; seam tests observe outgoing receipts only.',
  },
);

test(
  'N1: two real DSH profiles, non-default cwd, cold restore and concurrent contact mapping',
  {
    skip: 'Requires pinned real DSH build, isolated profiles, host adapter wiring and same-machine credential/ledger/IPC isolation gate; fake hosts cannot establish compatibility.',
  },
);
