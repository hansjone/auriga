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
import UsageRecords from './UsageRecords.vue';
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
    missingInputTokens: 1,
    missingOutputTokens: 1,
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

it.each([
  [RelayView, '/models/relay', 'zh-CN'],
  [UsageView, '/models/usage', 'zh-CN'],
  [RelayView, '/models/relay', 'en'],
  [UsageView, '/models/usage', 'en'],
] as const)(
  'distinguishes step-up recovery from permission denial on %s (%s, %s)',
  async (view, path, locale) => {
    let code = 'iam.step_up_required';
    const fetch = vi.fn(async () =>
      Response.json(
        { status: 403, code, detail: 'private detail' },
        { status: 403 },
      ),
    );
    vi.stubGlobal('fetch', fetch);
    const wrapper = mount(view, {
      global: { plugins: [createAurigaI18n(locale)] },
    });
    await flushPromises();
    const alert = wrapper.get('[role="alert"]');
    expect(alert.text()).toContain(
      locale === 'en'
        ? 'Recent verification has expired'
        : '近期身份验证已过期',
    );
    expect(alert.text()).not.toContain('private detail');
    expect(wrapper.get('a[data-test="verify-login"]').attributes('href')).toBe(
      `/login?returnTo=${encodeURIComponent(path)}`,
    );
    expect(fetch.mock.calls).toHaveLength(3);
    code = 'common.forbidden';
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain(
      locale === 'en' ? 'Permission' : '没有读取',
    );
    expect(wrapper.find('a[data-test="verify-login"]').exists()).toBe(false);
    wrapper.unmount();
  },
);

it.each([
  {
    name: 'input only',
    rows: [record({ outputTokens: null })],
    input: 20,
    output: 0,
    missingInput: 0,
    missingOutput: 1,
    shownInput: '20',
    shownOutput: '未知',
  },
  {
    name: 'output only',
    rows: [record({ inputTokens: null })],
    input: 0,
    output: 10,
    missingInput: 1,
    missingOutput: 0,
    shownInput: '未知',
    shownOutput: '10',
  },
  {
    name: 'mixed with normal',
    rows: [
      record({ outputTokens: null }),
      record({ inputTokens: null }),
      record({ inputTokens: 5 }),
    ],
    input: 25,
    output: 20,
    missingInput: 1,
    missingOutput: 1,
    shownInput: '25',
    shownOutput: '20',
  },
  {
    name: 'unknown excluded despite numeric fields',
    rows: [
      record({ outcome: 'unknown', inputTokens: 900, outputTokens: 900 }),
      record({ usageQuality: 'unknown' }),
      record({ outputTokens: null }),
    ],
    input: 20,
    output: 0,
    missingInput: 2,
    missingOutput: 3,
    shownInput: '20',
    shownOutput: '未知',
  },
  {
    name: 'unsafe input',
    rows: [record({ inputTokens: Number.MAX_SAFE_INTEGER + 1 })],
    input: 0,
    output: 10,
    missingInput: 1,
    missingOutput: 0,
    shownInput: '未知',
    shownOutput: '10',
  },
  {
    name: 'input sum overflow',
    rows: [
      record({ inputTokens: Number.MAX_SAFE_INTEGER }),
      record({ inputTokens: 1, outputTokens: null }),
    ],
    input: Number.MAX_SAFE_INTEGER + 1,
    output: 10,
    missingInput: 0,
    missingOutput: 1,
    shownInput: '未知',
    shownOutput: '10',
  },
])(
  'counts each known token side independently: $name',
  ({
    rows,
    input,
    output,
    missingInput,
    missingOutput,
    shownInput,
    shownOutput,
  }) => {
    const summary = summarizeUsage(rows);
    expect(summary).toMatchObject({
      inputTokens: input,
      outputTokens: output,
      missingInputTokens: missingInput,
      missingOutputTokens: missingOutput,
    });
    const wrapper = mount(UsageSummary, {
      props: { rows },
      global: { plugins: [createAurigaI18n()] },
    });
    const inputTotal = wrapper.get('[data-test="input-total"]');
    const outputTotal = wrapper.get('[data-test="output-total"]');
    expect(inputTotal.text()).toContain(shownInput);
    expect(outputTotal.text()).toContain(shownOutput);
    expect(inputTotal.text().includes('不含未知部分')).toBe(missingInput > 0);
    expect(outputTotal.text().includes('不含未知部分')).toBe(missingOutput > 0);
    const details = mount(UsageRecords, {
      props: {
        rows: rows.map((row, index) => ({
          ...row,
          requestId: `request-${index}`,
        })),
      },
      global: { plugins: [createAurigaI18n()] },
    });
    for (const [index, row] of rows.entries()) {
      const cells = details.findAll('tbody tr')[index]!.findAll('td');
      const unknown =
        row.outcome === 'unknown' || row.usageQuality === 'unknown';
      expect(cells[6]!.text()).toBe(
        unknown ||
          row.inputTokens === null ||
          !Number.isSafeInteger(row.inputTokens)
          ? '未知'
          : String(row.inputTokens),
      );
      expect(cells[7]!.text()).toBe(
        unknown ||
          row.outputTokens === null ||
          !Number.isSafeInteger(row.outputTokens)
          ? '未知'
          : String(row.outputTokens),
      );
    }
    details.unmount();
    wrapper.unmount();
  },
);
