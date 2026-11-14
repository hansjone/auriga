import { test } from 'node:test';
/* global fetch */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID, webcrypto } from 'node:crypto';
import { runInNewContext } from 'node:vm';
import { fixture, command, OWNER } from './fixtures.mjs';
import { createLoopback } from '../loopback.mjs';
import { valid } from '../schema.mjs';

test('control page displays only its own device code, polls across revisions and clears code after skip', async (t) => {
  let release;
  const f = fixture({
    poll: () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  });
  const loop = await createLoopback({ broker: f.broker, validate: valid });
  t.after(async () => {
    await loop.close();
    await f.broker.close();
  });
  const opened = loop.issue();
  class Button {
    disabled = true;
    onclick;
  }
  const nodes = Object.fromEntries(
    ['login', 'cancel', 'skip'].map((id) => [id, new Button()]),
  );
  nodes.status = { textContent: '' };
  nodes.attempt = { textContent: '' };
  let poll, replaced;
  await runInNewContext(
    readFileSync(new URL('../public/page.js', import.meta.url), 'utf8'),
    {
      location: new URL(opened),
      history: {
        replaceState: (_state, _title, path) => {
          replaced = path;
        },
      },
      document: { getElementById: (id) => nodes[id] },
      HTMLButtonElement: Button,
      crypto: webcrypto,
      btoa: (value) => Buffer.from(value, 'binary').toString('base64'),
      setInterval: (callback) => {
        poll = callback;
      },
      fetch: (path, options) =>
        fetch(new URL(path, loop.origin), {
          ...options,
          headers: {
            ...options.headers,
            Origin: loop.origin,
            'Sec-Fetch-Site': 'same-origin',
            'Sec-Fetch-Mode': 'cors',
            'Sec-Fetch-Dest': 'empty',
          },
        }),
    },
  );
  assert.equal(replaced, '/');
  assert.equal(nodes.login.disabled, false);
  await nodes.login.onclick();
  await poll();
  const attempt = f.broker.attempt.display;
  assert.ok(nodes.attempt.textContent.includes(attempt.userCode));
  assert.equal(f.broker.snapshot(OWNER).deviceAttempt, undefined);
  const other = await f.broker.pageCommand(
    {
      ...command(f.broker, 'snapshot'),
      shellEpoch: '00000000-0000-0000-0000-000000000000',
      expectedRevision: 0,
      requestId: randomUUID(),
    },
    OWNER,
  );
  assert.equal(other.snapshot.deviceAttempt, undefined);
  assert.ok(!f.opened[0].includes(attempt.userCode));
  await nodes.skip.onclick();
  release(f.tokens);
  await Promise.all([...f.broker.inflight]);
  await poll();
  assert.equal(nodes.attempt.textContent, '');
  assert.equal(f.broker.state, 'signed_out');
  assert.equal(f.broker.record.tokens, undefined);
});
