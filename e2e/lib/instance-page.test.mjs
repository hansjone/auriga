import assert from 'node:assert/strict';
import { test } from 'node:test';
import { edgeInstanceOk } from './assert.mjs';
import { waitInstancePage } from './instance-page.mjs';

const pending = { status: 503, body: { code: 'edge.instance_not_running' } };
const good = { status: 200, text: 'fake-dsh ok' };

function clock() {
  let current = 0;
  return {
    deadline: 1500,
    now: () => current,
    pause: async (ms) => {
      current += ms;
    },
  };
}

test('route projection 503 may converge, but success still requires instance HTML', async () => {
  const responses = [pending, pending, good];
  const budgets = [];
  const page = await waitInstancePage(
    async (budget) => {
      budgets.push(budget);
      return responses.shift();
    },
    edgeInstanceOk,
    clock(),
  );
  assert.equal(page, good);
  assert.deepEqual(budgets, [1500, 1000, 500]);
});

test('persistent route-state 503 fails exactly at the deadline', async () => {
  const timing = clock();
  let requests = 0;
  await assert.rejects(
    waitInstancePage(
      async () => {
        requests++;
        return pending;
      },
      edgeInstanceOk,
      timing,
    ),
    /deadline exceeded/,
  );
  assert.equal(requests, 3);
  assert.equal(timing.now(), timing.deadline);
});

test('authentication, unknown 503 and unrelated 200 pages fail without retry', async () => {
  for (const page of [
    { status: 401, body: { code: 'edge.unauthorized' } },
    { status: 403, body: { code: 'edge.not_owner' } },
    { status: 503, body: { code: 'common.unavailable' } },
    { status: 503, body: { code: 'edge.instance_unavailable' } },
    { status: 200, text: '<html>starting</html>' },
    { status: 200, body: { code: 'edge.instance_not_running' } },
  ]) {
    let requests = 0;
    await assert.rejects(
      waitInstancePage(
        async () => {
          requests++;
          return page;
        },
        edgeInstanceOk,
        clock(),
      ),
      /page rejected/,
    );
    assert.equal(requests, 1);
  }
});

test('a successful response that arrives after the deadline is not accepted', async () => {
  const timing = clock();
  await assert.rejects(
    waitInstancePage(
      async () => {
        await timing.pause(1500);
        return good;
      },
      edgeInstanceOk,
      timing,
    ),
    /deadline exceeded/,
  );
});
