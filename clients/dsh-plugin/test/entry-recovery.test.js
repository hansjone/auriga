import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setImmediate } from 'node:timers/promises';

const { EventTarget, Event } = globalThis;

const id = '019a0000-0000-7000-8000-000000000001';
const base = `https://portal.example.com/i/${id}/`;
const lost = () =>
  new Response(
    JSON.stringify({
      code: 'edge.unauthorized',
      status: 401,
      detail: 'instance cookie required',
    }),
    {
      status: 401,
      headers: {
        'content-type': 'application/problem+json',
        'x-auriga-instance-entry': 'required',
      },
    },
  );
const settle = () => setImmediate();

async function fixture(t, http = async () => lost(), options = {}) {
  const listeners = { state: new Set(), generation: new Set() };
  let state = options.disconnected ? 'connecting' : 'connected',
    generation = options.disconnected ? undefined : { id: 1 },
    module;
  const observable = (name, value) => ({
    getSnapshot: value,
    subscribe(fn) {
      listeners[name].add(fn);
      return () => listeners[name].delete(fn);
    },
  });
  const connection = {
    state: observable('state', () => state),
    generation: observable('generation', () => generation),
  };
  const events = new EventTarget(),
    docEvents = new EventTarget();
  const storage = options.storage ?? new Map();
  const navigations = [],
    calls = [],
    timers = new Map();
  let timerId = 0;
  let now = 0;
  const logs = [],
    consoleEntries = [];
  const schedule = (fn, ms) => {
    const id = ++timerId;
    timers.set(id, { fn, ms, at: now + ms });
    return id;
  };
  const cancelTimer = (id) => timers.delete(id);
  const win = {
    performance: { now: () => now },
    location: {
      href: options.url ?? base,
      origin: new URL(options.url ?? base).origin,
      pathname: new URL(options.url ?? base).pathname,
      replace: (url) => navigations.push(url),
    },
    sessionStorage: {
      getItem: (key) => storage.get(key) ?? null,
      removeItem: (key) => storage.delete(key),
      setItem: (key, value) => storage.set(key, value),
    },
    addEventListener: (...args) => events.addEventListener(...args),
    removeEventListener: (...args) => events.removeEventListener(...args),
  };
  win.top = options.frame ? {} : win;
  const doc = {
    baseURI: options.baseURI ?? win.location.href,
    hidden: false,
    createElement: () => ({ remove() {} }),
    head: { append() {} },
    addEventListener: (...args) => docEvents.addEventListener(...args),
    removeEventListener: (...args) => docEvents.removeEventListener(...args),
  };
  win.__ModuleLoader__ = {
    load: (value) => {
      module = value;
    },
  };
  vm.runInNewContext(
    await readFile(new URL('../src/client.js', import.meta.url), 'utf8'),
    {
      window: win,
      document: doc,
      URL,
      AbortController,
      console: {
        ...console,
        info: (...args) => {
          consoleEntries.push(args);
          const [tag, fields] = args;
          logs.push(
            args.length === 1 && tag.startsWith('auriga.entry_recovery ')
              ? {
                  tag: 'auriga.entry_recovery',
                  ...JSON.parse(tag.slice('auriga.entry_recovery '.length)),
                }
              : { tag, ...fields },
          );
        },
      },
      setTimeout: schedule,
      clearTimeout: cancelTimer,
      fetch: async (url, init) => {
        calls.push({ url: String(url), init, at: now });
        return http(url, init);
      },
    },
  );
  const plugin = module.factory((name) =>
    name === 'react' ? { createElement() {} } : {},
  );
  const scopeCleanups = [];
  const ctx = {
    connection: options.noCapability ? {} : (options.connection ?? connection),
    locale: {
      register() {},
      bind: () => (s) => s,
      getLocale: () => ({ active: 'en' }),
    },
    theme: { getTheme: () => ({}) },
    slots: { inject() {} },
    on: () => () => {},
    effect(fn) {
      scopeCleanups.push(fn());
    },
    inject(_deps, fn) {
      fn(ctx);
      return () =>
        scopeCleanups
          .splice(0)
          .reverse()
          .forEach((cleanup) => cleanup?.());
    },
  };
  const dispose = plugin.apply(ctx);
  t.after(dispose);
  function emit(name, next) {
    const old = name === 'state' ? state : generation;
    if (Object.is(old, next)) return; // actual rc.3 deduplicates equal states
    if (name === 'state') state = next;
    else generation = next;
    for (const fn of [...listeners[name]]) fn();
  }
  async function advance(ms) {
    const until = now + ms;
    for (;;) {
      const due = [...timers]
        .sort((a, b) => a[1].at - b[1].at)
        .find(([, v]) => v.at <= until);
      if (!due) break;
      now = due[1].at;
      timers.delete(due[0]);
      due[1].fn();
      await settle();
    }
    now = until;
    await settle();
  }
  return {
    calls,
    logs,
    consoleEntries,
    navigations,
    storage,
    timers,
    schedule,
    cancelTimer,
    dispose,
    listeners,
    win,
    doc,
    advance,
    now: () => now,
    jump: (ms) => {
      now += ms;
    },
    lose() {
      emit('generation', undefined);
      emit('state', 'connecting');
    },
    connect() {
      emit('generation', { id: 2 });
      emit('state', 'connected');
    },
    state: (s) => emit('state', s),
    event: (s) => events.dispatchEvent(new Event(s)),
    hidden(value) {
      doc.hidden = value;
      docEvents.dispatchEvent(new Event('visibilitychange'));
    },
    async retry() {
      const pair = [...timers].find(([, v]) => v.ms === 2000);
      assert.ok(pair, 'bounded read-only retry is scheduled');
      await advance(pair[1].at - now);
    },
  };
}

test('exact entry gate loss re-enters the same instance once through document navigation', async (t) => {
  const f = await fixture(t);
  f.lose();
  await settle();
  assert.deepEqual(f.navigations, [base]);
  assert.equal(f.calls.length, 1);
  assert.equal(
    f.calls[0].url,
    base + '_auriga/health',
    'probe must not count as a native UI interaction',
  );
  assert.equal(f.calls[0].init.method, 'GET');
  assert.equal(f.calls[0].init.credentials, 'same-origin');
  assert.equal(f.calls[0].init.redirect, 'error');
  f.event('focus');
  f.event('online');
  await settle();
  assert.equal(f.navigations.length, 1);
});

test('network/temporary failures allow only two paced availability reads after the three short probes', async (t) => {
  let count = 0;
  const f = await fixture(t, async () => {
    if (++count === 1) throw new TypeError('network');
    return new Response('', { status: 503 });
  });
  f.lose();
  await settle();
  await f.retry();
  await f.retry();
  assert.equal(f.calls.length, 3);
  assert.deepEqual(f.navigations, []);
  f.state('connecting');
  await settle();
  f.event('focus');
  f.event('online');
  await settle();
  assert.equal(f.calls.length, 3);
  await f.advance(9999);
  assert.equal(f.calls.length, 3);
  await f.advance(1);
  assert.equal(f.calls.length, 4);
  f.event('focus');
  f.event('online');
  await settle();
  assert.equal(f.calls.length, 4);
  await f.advance(10000);
  assert.equal(f.calls.length, 5);
  assert.deepEqual(
    f.calls.map((call) => call.at),
    [0, 2000, 4000, 14000, 24000],
  );
  assert.equal(f.timers.size, 0);
  f.event('focus');
  f.event('online');
  await f.advance(20000);
  assert.equal(f.calls.length, 5);
});

test('availability eligibility excludes other HTTP failures and stops at the first definitive reply', async (t) => {
  for (const status of [200, 401, 403, 500, 504]) {
    const f = await fixture(t, async () => new Response('', { status }));
    f.lose();
    await settle();
    await f.advance(30000);
    assert.equal(f.calls.length, status === 504 ? 3 : 1);
    assert.deepEqual(f.navigations, []);
  }
  const f = await fixture(
    t,
    async () => new Response('', { status: f.calls.length <= 3 ? 503 : 403 }),
  );
  f.lose();
  await settle();
  await f.advance(30000);
  assert.equal(f.calls.length, 4);
  assert.deepEqual(f.navigations, []);
});

test('a locally cancelled short probe cannot qualify two completed failures for availability or add replacement reads', async (t) => {
  const f = await fixture(t, async () =>
    f.calls.length === 1
      ? new Promise(() => {})
      : f.calls.length <= 3
        ? new Response('', { status: 503 })
        : lost(),
  );
  f.lose();
  await settle();
  await f.advance(500);
  f.hidden(true);
  f.hidden(false);
  await settle();
  await f.advance(4000);
  assert.deepEqual(
    f.calls.map((call) => call.at),
    [0, 2500, 4500],
  );
  assert.equal(f.calls[0].init.signal.aborted, true);
  await f.advance(25500);
  f.event('focus');
  f.event('online');
  await settle();
  assert.equal(
    f.calls.length,
    3,
    'three starts with only two completed failures must remain the complete short budget',
  );
  assert.ok(!f.logs.some((row) => row.event === 'availability_wait'));
  assert.deepEqual(f.navigations, []);
});

test('HTTP 504 in availability ends the cycle instead of reaching the second late probe', async (t) => {
  const f = await fixture(t, async () =>
    f.calls.length <= 3
      ? new Response('', { status: 503 })
      : f.calls.length === 4
        ? new Response('', { status: 504 })
        : lost(),
  );
  f.lose();
  await settle();
  await f.advance(30000);
  f.event('focus');
  f.event('online');
  await settle();
  assert.deepEqual(
    f.calls.map((call) => call.at),
    [0, 2000, 4000, 14000],
  );
  assert.deepEqual(f.navigations, []);
  assert.equal(f.timers.size, 0);
});

test('an explicitly aborted fetch is not a completed network-unavailable result', async (t) => {
  const f = await fixture(t, async () => {
    throw Object.assign(new Error('cancelled fixture'), { name: 'AbortError' });
  });
  f.lose();
  await settle();
  await f.advance(30000);
  assert.equal(f.calls.length, 1);
  assert.ok(!f.logs.some((row) => row.event === 'availability_wait'));
  assert.deepEqual(f.navigations, []);
});

test('availability keeps the original absolute thirty-second deadline across hiding and late replies', async (t) => {
  let finish;
  const f = await fixture(t, async () =>
    f.calls.length <= 3
      ? new Response('', { status: 503 })
      : new Promise((resolve) => {
          finish = resolve;
        }),
  );
  f.lose();
  await settle();
  await f.advance(14000);
  assert.equal(f.calls.length, 4);
  f.jump(16000);
  finish(lost());
  await settle();
  assert.deepEqual(f.navigations, []);
  const g = await fixture(t, async () => new Response('', { status: 502 }));
  g.lose();
  await settle();
  await g.advance(5000);
  g.hidden(true);
  await g.advance(26000);
  g.hidden(false);
  g.event('focus');
  g.event('online');
  await settle();
  assert.equal(g.calls.length, 3);
  assert.deepEqual(g.navigations, []);
});

test('stale cycle and retry callbacks cannot replace the original deadline after hiding', async (t) => {
  const f = await fixture(t, async () => new Response('', { status: 503 }));
  f.lose();
  await settle();
  const oldExpiry = [...f.timers.values()].find((v) => v.ms === 30000).fn;
  await f.advance(4000);
  const oldRetry = [...f.timers.values()].find((v) => v.ms === 10000).fn;
  assert.equal(f.calls.length, 3);
  await f.advance(1000);
  f.hidden(true);
  await f.advance(1000);
  f.hidden(false);
  const active = [...f.timers.keys()];
  oldExpiry();
  oldRetry();
  assert.deepEqual([...f.timers.keys()], active);
  await f.advance(8000);
  assert.deepEqual(
    f.calls.map((call) => call.at),
    [0, 2000, 4000, 14000],
  );
});

test('entry recovery diagnostics expose only safe events and never response bodies, URLs or thrown messages', async (t) => {
  let calls = 0;
  const canary = 'do-not-log-fixture-private-cookie';
  const f = await fixture(t, async () => {
    if (++calls === 1) throw new Error(canary);
    return new Response(
      JSON.stringify({
        code: 'edge.unauthorized',
        status: 401,
        detail: 'instance cookie required',
        extra: canary,
      }),
      {
        status: 401,
        headers: {
          'content-type': 'application/problem+json',
          'x-auriga-instance-entry': 'required',
        },
      },
    );
  });
  f.lose();
  await settle();
  await f.advance(2000);
  for (const event of [
    'installed',
    'trigger',
    'probe_started',
    'probe_completed',
    'navigation',
  ])
    assert.ok(f.logs.some((row) => row.event === event));
  assert.ok(f.logs.every((row) => row.tag === 'auriga.entry_recovery'));
  assert.ok(
    f.consoleEntries.every(
      (args) =>
        args.length === 1 &&
        typeof args[0] === 'string' &&
        args[0].startsWith('auriga.entry_recovery {'),
    ),
    'a text-only browser collector must retain the safe fields, without an Object placeholder',
  );
  assert.ok(!JSON.stringify(f.logs).includes(canary));
  assert.ok(!JSON.stringify(f.logs).includes('portal.example.com'));
});

test('transient unavailable then precise entry 401 recovers within the bounded cycle', async (t) => {
  let count = 0;
  const f = await fixture(t, async () =>
    ++count === 1 ? new Response('', { status: 503 }) : lost(),
  );
  f.lose();
  await settle();
  await f.retry();
  assert.deepEqual(f.navigations, [base]);
});

test('same loss deadline survives a hidden document beyond the absolute thirty-second window', async (t) => {
  let reads = 0;
  const f = await fixture(t, async () =>
    ++reads === 1 ? new Response('', { status: 503 }) : lost(),
  );
  f.lose();
  await settle();
  await f.advance(1000);
  f.hidden(true);
  await f.advance(30000);
  f.hidden(false);
  f.event('focus');
  f.event('online');
  await settle();
  assert.equal(
    f.calls.length,
    1,
    'visibility and wake events cannot grant a later recovery window',
  );
  assert.deepEqual(f.navigations, []);
  f.connect();
  f.lose();
  await settle();
  assert.equal(
    f.calls.length,
    2,
    'a genuinely new connected generation permits a future independent window',
  );
  assert.deepEqual(f.navigations, [base]);
});

test('focus and online respect the pending two-second retry instead of consuming all reads immediately', async (t) => {
  const times = [];
  const f = await fixture(t, async () => {
    times.push(f.now());
    return f.now() < 2000 ? new Response('', { status: 503 }) : lost();
  });
  f.lose();
  await settle();
  f.event('focus');
  await settle();
  f.event('online');
  await settle();
  assert.deepEqual(
    times,
    [0],
    'wake events must not bypass the pending backoff',
  );
  await f.advance(1999);
  assert.deepEqual(times, [0]);
  await f.advance(1);
  assert.deepEqual(times, [0, 2000]);
  assert.deepEqual(f.navigations, [base]);
});

test('hiding an in-flight read cannot make a quick visibility return consume the next probe early', async (t) => {
  const times = [];
  const f = await fixture(t, async () => {
    times.push(f.now());
    return times.length === 1 ? new Promise(() => {}) : lost();
  });
  f.lose();
  await settle();
  await f.advance(1000);
  f.hidden(true);
  await f.advance(500);
  f.hidden(false);
  await settle();
  assert.deepEqual(times, [0]);
  await f.advance(1499);
  assert.deepEqual(times, [0]);
  await f.advance(1);
  assert.deepEqual(times, [0, 3000]);
  assert.deepEqual(f.navigations, [base]);
});

test('loss first observed while hidden still expires without granting a late visible window', async (t) => {
  const f = await fixture(t);
  f.hidden(true);
  f.lose();
  await f.advance(31000);
  f.hidden(false);
  await settle();
  assert.equal(f.calls.length, 0);
  assert.deepEqual(f.navigations, []);
});

test('an exact entry reply after the deadline stays inert even if browser timers have not fired', async (t) => {
  let finish;
  const f = await fixture(
    t,
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  f.lose();
  await settle();
  f.jump(30000);
  finish(lost());
  await settle();
  assert.deepEqual(f.navigations, []);
});

test('200, 403 and unmarked or non-entry 401 stop without navigation or retries', async (t) => {
  for (const response of [
    () => new Response('', { status: 200 }),
    () => new Response('', { status: 403 }),
    () =>
      new Response(
        JSON.stringify({
          code: 'edge.unauthorized',
          status: 401,
          detail: 'instance cookie required',
        }),
        { status: 401 },
      ),
    () =>
      new Response(
        JSON.stringify({ code: 'common.unauthenticated', status: 401 }),
        { status: 401, headers: { 'x-auriga-instance-entry': 'required' } },
      ),
  ]) {
    const f = await fixture(t, async () => response());
    f.lose();
    await settle();
    assert.equal(f.calls.length, 1);
    assert.deepEqual(f.navigations, []);
    assert.equal(f.timers.size, 0);
  }
});

test('concurrent events coalesce and fresh generation fences late probe completion', async (t) => {
  let finish;
  const f = await fixture(
    t,
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  f.lose();
  f.event('focus');
  f.event('online');
  await settle();
  assert.equal(f.calls.length, 1);
  f.connect();
  finish(lost());
  await settle();
  assert.equal(f.calls[0].init.signal.aborted, true);
  assert.deepEqual(f.navigations, []);
});

test('unload/dispose aborts late reads and clears every retry and subscriber', async (t) => {
  for (const action of ['dispose', 'pagehide']) {
    let finish;
    const f = await fixture(
      t,
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    f.lose();
    await settle();
    if (action === 'dispose') f.dispose();
    else f.event('pagehide');
    finish(lost());
    await settle();
    assert.equal(f.calls[0].init.signal.aborted, true);
    assert.deepEqual(f.navigations, []);
    assert.equal(f.timers.size, 0);
    assert.equal(f.listeners.state.size + f.listeners.generation.size, 0);
  }
});

test('a pending recovery marker blocks another failed document but successful generation clears it', async (t) => {
  const key = `auriga.entry-recovery:/i/${id}/`;
  const storage = new Map([[key, '1']]);
  const f = await fixture(t, async () => lost(), { storage });
  assert.equal(
    storage.has(key),
    false,
    'authenticated connected document may handle a later outage',
  );
  storage.set(key, '1');
  f.lose();
  await settle();
  assert.equal(f.calls.length, 0);
  assert.deepEqual(f.navigations, []);
});

test('an unresolved previous-document marker stays blocked until a new connected generation', async (t) => {
  const key = `auriga.entry-recovery:/i/${id}/`;
  const storage = new Map([[key, '1']]);
  const f = await fixture(t, async () => lost(), {
    storage,
    disconnected: true,
  });
  await settle();
  assert.equal(f.calls.length, 0);
  f.connect();
  f.lose();
  await settle();
  assert.deepEqual(f.navigations, [base]);
});

test('hidden document cancels a late result and bounded probe timeout remains cancellable', async (t) => {
  let finish;
  const f = await fixture(
    t,
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  f.lose();
  await settle();
  f.hidden(true);
  finish(lost());
  await settle();
  assert.deepEqual(f.navigations, []);
  assert.equal(f.calls[0].init.signal.aborted, true);
  let late;
  const g = await fixture(
    t,
    () =>
      new Promise((resolve) => {
        late = resolve;
      }),
  );
  g.lose();
  await settle();
  await g.advance(5999);
  assert.equal(g.calls[0].init.signal.aborted, false);
  await g.advance(1);
  assert.equal(g.calls[0].init.signal.aborted, true);
  assert.equal(
    g.timers.size,
    0,
    'the six-second read timeout does not restart or qualify the probe budget',
  );
  g.event('focus');
  g.event('online');
  late(lost());
  await g.advance(30000);
  assert.equal(g.calls.length, 1);
  assert.deepEqual(g.navigations, []);
  assert.ok(!g.logs.some((row) => row.event === 'availability_wait'));
  g.dispose();
  assert.equal(g.timers.size, 0);
});

test('slow completed reads and a pending availability read share the original thirty-second cap', async (t) => {
  let late;
  const delays = [5000, 5000, 3999];
  const f = await fixture(t, async () => {
    const delay = delays[f.calls.length - 1];
    if (delay !== undefined) {
      await new Promise((resolve) => f.schedule(resolve, delay));
      return new Response('', { status: 503 });
    }
    return new Promise((resolve) => {
      late = resolve;
    });
  });
  f.lose();
  await settle();
  await f.advance(29999);
  assert.deepEqual(
    f.calls.map((call) => call.at),
    [0, 7000, 14000, 27999],
  );
  assert.equal(f.calls[3].init.signal.aborted, false);
  await f.advance(1);
  assert.equal(f.calls[3].init.signal.aborted, true);
  assert.equal(f.timers.size, 0);
  late(lost());
  f.event('focus');
  f.event('online');
  await f.advance(30000);
  assert.equal(f.calls.length, 4);
  assert.deepEqual(f.navigations, []);
});

test(
  'locked public browser Connection installs and drives recovery without transport patching',
  { skip: !process.env.AURIGA_ENTRY_CONNECTION_BUNDLE },
  async (t) => {
    let nativeModule;
    vm.runInNewContext(
      await readFile(process.env.AURIGA_ENTRY_CONNECTION_BUNDLE, 'utf8'),
      {
        window: {
          __ModuleLoader__: {
            load: (m) => {
              nativeModule = m;
            },
          },
        },
        URL,
        AbortController,
        setTimeout,
        clearTimeout,
        console,
      },
    );
    const native = nativeModule.factory(() => {
      throw new Error('unexpected external module');
    });
    let connection;
    native.installConnection(
      {
        provide: (name, value) => {
          assert.equal(name, 'connection');
          connection = value;
        },
      },
      { transport: { rpc: {} } },
    );
    let attempts = 0;
    const unregister = connection.registerGenerationSource(
      async (signal, ready) => {
        if (++attempts === 1) ready({ home: 'public-fixture' });
        await new Promise((resolve) =>
          signal.addEventListener('abort', resolve, { once: true }),
        );
      },
    );
    const loop = connection.start({});
    t.after(() => {
      loop.stop();
      unregister();
    });
    for (
      let i = 0;
      i < 20 && connection.state.getSnapshot() !== 'connected';
      i++
    )
      await settle();
    assert.equal(connection.state.getSnapshot(), 'connected');
    const f = await fixture(t, async () => lost(), { connection });
    connection.reconnect();
    connection.reconnect();
    await settle();
    assert.deepEqual(f.navigations, [base]);
    assert.equal(
      f.calls.length,
      1,
      'real repeated connecting states are deduplicated',
    );
  },
);

test(
  'locked Connection plus the default edge recreation interval recovers without a fabricated second state event',
  { skip: !process.env.AURIGA_ENTRY_CONNECTION_BUNDLE },
  async (t) => {
    for (const { lossAt, latencies = [] } of [
      { lossAt: 0 },
      { lossAt: 10000 },
      { lossAt: 0, latencies: [74, 3025] },
    ])
      await t.test(`loss ${lossAt}, lag ${latencies}`, async (t) => {
        let f, nativeModule;
        const random = Object.create(Math);
        random.random = () => 0.5;
        vm.runInNewContext(
          await readFile(process.env.AURIGA_ENTRY_CONNECTION_BUNDLE, 'utf8'),
          {
            window: {
              __ModuleLoader__: {
                load: (m) => {
                  nativeModule = m;
                },
              },
            },
            URL,
            AbortController,
            console,
            Math: random,
            setTimeout: (fn, ms) => f.schedule(fn, ms),
            clearTimeout: (id) => f.cancelTimer(id),
          },
        );
        const native = nativeModule.factory(() => {
          throw new Error('unexpected public bundle dependency');
        });
        let connection;
        native.installConnection(
          {
            provide: (_name, value) => {
              connection = value;
            },
          },
          {
            transport: { rpc: {} },
            location: { hostname: 'portal.example.com' },
          },
        );
        f = await fixture(
          t,
          async () => {
            const latency = latencies[f.calls.length - 1] ?? 0;
            if (latency)
              await new Promise((resolve) => f.schedule(resolve, latency));
            return f.now() < 11846 ? new Response('', { status: 503 }) : lost();
          },
          { connection },
        );
        let drop;
        const attempts = [],
          states = [];
        const unregister = connection.registerGenerationSource(
          async (_signal, ready) => {
            attempts.push(f.now());
            if (attempts.length === 1) {
              ready({ home: 'public-fixture' });
              await new Promise((_, reject) => {
                drop = reject;
              });
            } else
              throw new Error(
                f.now() < 11846
                  ? 'fixture edge unavailable'
                  : 'fixture edge entry unauthorized',
              );
          },
        );
        const loop = connection.start({
          onStateChange: (state) => states.push({ at: f.now(), state }),
        });
        t.after(() => {
          loop.stop();
          unregister();
        });
        await settle();
        await settle();
        assert.equal(connection.state.getSnapshot(), 'connected');
        await f.advance(lossAt);
        drop(new Error('fixture old edge stopped'));
        await settle();
        await settle();
        await f.advance(25000 - lossAt);
        t.diagnostic(
          JSON.stringify({
            lossAt,
            readyAt: 11846,
            latencies,
            probes: f.calls.map((call) => call.at),
            completions: f.logs
              .filter((row) => row.event === 'probe_completed')
              .map(({ elapsedMs, reason, status }) => ({
                elapsedMs,
                reason,
                status,
              })),
            attempts,
            states,
            navigations: f.navigations.length,
          }),
        );
        assert.deepEqual(f.navigations, [base]);
        if (latencies.length)
          assert.deepEqual(
            f.calls.map((call) => call.at),
            [0, 2074, 7099, 17099],
          );
        assert.equal(
          states.filter(({ state }) => state === 'connecting').length,
          1,
          'slow responses cannot rely on a fabricated second connecting event',
        );
      });
  },
);

test('no public capability, wrong scope or child frame never guesses a recovery destination', async (t) => {
  for (const options of [
    { noCapability: true },
    { frame: true },
    { baseURI: 'https://evil.example.com/' },
    { url: 'https://portal.example.com/i/not-an-instance/' },
  ]) {
    const f = await fixture(t, async () => lost(), options);
    f.lose();
    await settle();
    assert.equal(f.calls.length, 0);
    assert.deepEqual(f.navigations, []);
  }
});
