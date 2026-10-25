import { afterEach, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createAurigaI18n } from '@auriga/shared';
import {
  filterUsage,
  formatMicros,
  readAllPages,
  summarizeUsage,
} from './modelUsageState';
import UsageView from './UsageView.vue';
import RelayView from './RelayView.vue';
import UsageSummary from './UsageSummary.vue';
import type { Usage } from '../../admin/modelUsageApi';

const record = (overrides: Partial<Usage> = {}): Usage => ({
  requestId: 'request-example',
  accountId: 'member-example',
  modelId: 'model-example',
  outcome: 'completed',
  usageQuality: 'confirmed',
  inputTokens: 20,
  outputTokens: 10,
  costMicros: 1234567,
  frozenMicros: 0,
  frozenTokens: 0,
  currency: 'USD',
  dayPeriodStart: '2026-10-25T00:00:00Z',
  monthPeriodStart: '2026-10-01T00:00:00Z',
  priceVersion: 'v1',
  createdAt: '2026-10-25T01:00:00Z',
  ...overrides,
});
afterEach(() => vi.unstubAllGlobals());
it('filters UTC dates, member, model and current provider mapping', () => {
  const rows = [
    record(),
    record({ accountId: 'other' }),
    record({ modelId: 'deleted' }),
    record({ createdAt: '2026-10-26T00:00:00Z' }),
  ];
  expect(
    filterUsage(
      rows,
      {
        from: '2026-10-25',
        to: '2026-10-25',
        accountId: 'member-example',
        providerId: 'provider-example',
        modelId: 'model-example',
      },
      new Map([['model-example', 'provider-example']]),
    ),
  ).toEqual([rows[0]]);
});
it('keeps unknown and null out of known totals and retains frozen reservations', () => {
  const result = summarizeUsage([
    record(),
    record({
      outcome: 'unknown',
      usageQuality: 'unknown',
      inputTokens: null,
      outputTokens: null,
      costMicros: null,
      frozenMicros: 9000000,
      frozenTokens: 800,
    }),
    record({ outcome: 'failed', costMicros: null }),
  ]);
  expect(result).toMatchObject({
    requests: 3,
    failed: 1,
    unknown: 1,
    inputTokens: 40,
    outputTokens: 20,
    missingTokens: 1,
  });
  expect(result.money).toEqual([
    { currency: 'USD', costMicros: 1234567, frozenMicros: 9000000, missing: 2 },
  ]);
});
it('formats micros exactly including zero and sub-cent amounts', () => {
  expect(formatMicros(1234567)).toBe('1.234567');
  expect(formatMicros(1)).toBe('0.000001');
  expect(formatMicros(0)).toBe('0.000000');
  expect(formatMicros(null)).toBeNull();
  expect(formatMicros(Number.MAX_SAFE_INTEGER + 1)).toBeNull();
});
it('reads all pages and rejects repeating cursors', async () => {
  const page = vi
    .fn()
    .mockResolvedValueOnce({ items: [1], nextCursor: 'next' })
    .mockResolvedValueOnce({ items: [2] });
  expect(await readAllPages(page)).toEqual([1, 2]);
  expect(page.mock.calls[1]?.[0]).toBe('next');
  await expect(
    readAllPages(async () => ({ items: [], nextCursor: 'loop' })),
  ).rejects.toThrow();
});
it('renders unknown, micros, budget reservations and filters without invented query parameters', async () => {
  const fetch = vi.fn(async (url: string) =>
    Response.json(
      url.includes('/usage')
        ? {
            items: [
              record(),
              record({
                requestId: 'unknown-example',
                outcome: 'unknown',
                usageQuality: 'unknown',
                inputTokens: null,
                outputTokens: null,
                costMicros: null,
                frozenMicros: 5000000,
              }),
            ],
          }
        : { items: [] },
    ),
  );
  vi.stubGlobal('fetch', fetch);
  const wrapper = mount(UsageView, {
    global: { plugins: [createAurigaI18n()] },
  });
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(wrapper.text()).toContain('1.234567');
  expect(wrapper.text()).toContain('5.000000');
  expect(wrapper.text()).toContain('未知');
  expect(wrapper.findAll('tbody tr')).toHaveLength(2);
  await wrapper.get('[data-test="member"]').setValue('absent');
  expect(wrapper.findAll('tbody tr')).toHaveLength(0);
  wrapper.unmount();
});
it('renders relay enabled counts and recent totals', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      Response.json(
        url.includes('/usage')
          ? {
              items: [
                record({
                  createdAt: new Date().toISOString(),
                  outcome: 'failed',
                }),
              ],
            }
          : url.includes('/providers')
            ? {
                items: [
                  { id: 'p', enabled: true },
                  { id: 'disabled', enabled: false },
                ],
              }
            : { items: [{ id: 'm', enabled: true }] },
      ),
    ),
  );
  const wrapper = mount(RelayView, {
    global: { plugins: [createAurigaI18n()] },
  });
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(wrapper.get('[data-test="providers"]').text()).toContain('1');
  expect(wrapper.get('[data-test="models"]').text()).toContain('1');
  expect(wrapper.get('[data-test="failed"]').text()).toContain('1');
  expect(wrapper.text()).toContain('/ai/v1');
  wrapper.unmount();
});
it('clears data and shows safe errors on failure, aborts on unmount', async () => {
  let signal: AbortSignal | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url, init) => {
      signal = init.signal;
      return new Response(
        JSON.stringify({
          status: 403,
          code: 'common.forbidden',
          detail: 'private upstream detail',
        }),
        { status: 403 },
      );
    }),
  );
  const wrapper = mount(UsageView, {
    global: { plugins: [createAurigaI18n()] },
  });
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(true);
  expect(wrapper.text()).not.toContain('private upstream detail');
  expect(wrapper.findAll('tbody tr')).toHaveLength(0);
  wrapper.unmount();
  expect(signal?.aborted).toBe(true);
});
it('never displays an all-unknown token or cost total as zero', () => {
  const wrapper = mount(UsageSummary, {
    props: {
      rows: [
        record({
          outcome: 'unknown',
          usageQuality: 'unknown',
          inputTokens: null,
          outputTokens: null,
          costMicros: null,
        }),
      ],
    },
    global: { plugins: [createAurigaI18n()] },
  });
  const amounts = wrapper.findAll('dd').map((item) => item.text());
  expect(amounts[3]).toContain('未知');
  expect(amounts[4]).toContain('未知');
  wrapper.unmount();
});
it('keeps currencies separate and distinguishes confirmed zero from unknown', () => {
  const summary = summarizeUsage([
    record({ currency: 'USD', costMicros: 0 }),
    record({ currency: 'EUR', costMicros: 2000000 }),
  ]);
  expect(
    summary.money.map((row) => [row.currency, row.costMicros, row.missing]),
  ).toEqual([
    ['USD', 0, 0],
    ['EUR', 2000000, 0],
  ]);
});
it('rejects an already cancelled pagination operation without making a request', async () => {
  const controller = new AbortController();
  controller.abort();
  const read = vi.fn();
  await expect(readAllPages(read, controller.signal)).rejects.toThrow();
  expect(read).not.toHaveBeenCalled();
});
it('renders English and rejects inverted date ranges', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockImplementation(async () => Response.json({ items: [] })),
  );
  const wrapper = mount(UsageView, {
    global: { plugins: [createAurigaI18n('en')] },
  });
  await flushPromises();
  expect(wrapper.text()).toContain('Only platform gateway traffic');
  await wrapper.get('[data-test="from"]').setValue('2026-10-26');
  await wrapper.get('[data-test="to"]').setValue('2026-10-25');
  expect(wrapper.get('[role="alert"]').text()).toContain('Start date');
  wrapper.unmount();
});
it('ignores pending responses after unmount', async () => {
  let resolve!: (value: Response) => void;
  const response = new Promise<Response>((done) => {
    resolve = done;
  });
  const signals: AbortSignal[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn((_url, init) => {
      signals.push(init.signal);
      return response;
    }),
  );
  const wrapper = mount(UsageView, {
    global: { plugins: [createAurigaI18n()] },
  });
  wrapper.unmount();
  expect(signals.every((signal) => signal.aborted)).toBe(true);
  resolve(Response.json({ items: [] }));
  await flushPromises();
});
