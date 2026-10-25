import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { useI18n } from 'vue-i18n';
import { ApiError } from '@auriga/shared';
import {
  modelUsageApi,
  type Model,
  type Provider,
  type Usage,
} from '../../admin/modelUsageApi';
import { messages } from './modelUsageMessages';

export const useModelUsageI18n = () => useI18n({ useScope: 'local', messages });
export async function readAllPages<T>(
  read: (
    cursor?: string,
    signal?: AbortSignal,
  ) => Promise<{ items: T[]; nextCursor?: string }>,
  signal?: AbortSignal,
) {
  const items: T[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  do {
    signal?.throwIfAborted();
    const page = await read(cursor, signal);
    signal?.throwIfAborted();
    items.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor))
      throw new Error('Repeated pagination cursor');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return items;
}
export type UsageFilters = {
  from: string;
  to: string;
  accountId: string;
  modelId: string;
  providerId: string;
};
export function filterUsage(
  rows: Usage[],
  filters: UsageFilters,
  providers: Map<string, string>,
) {
  const start = filters.from
    ? Date.parse(`${filters.from}T00:00:00Z`)
    : -Infinity;
  const end = filters.to
    ? Date.parse(`${filters.to}T00:00:00Z`) + 86400000
    : Infinity;
  return rows.filter((row) => {
    const time = Date.parse(row.createdAt);
    return (
      time >= start &&
      time < end &&
      (!filters.accountId.trim() ||
        row.accountId === filters.accountId.trim()) &&
      (!filters.modelId || row.modelId === filters.modelId) &&
      (!filters.providerId || providers.get(row.modelId) === filters.providerId)
    );
  });
}
export const unknownUsage = (row: Usage) =>
  row.outcome === 'unknown' || row.usageQuality === 'unknown';
const safe = (value: number | null): value is number =>
  value !== null && Number.isSafeInteger(value) && value >= 0;
export function formatMicros(value: number | null) {
  if (!safe(value)) return null;
  const integer = BigInt(value);
  return `${integer / 1000000n}.${(integer % 1000000n).toString().padStart(6, '0')}`;
}
export function summarizeUsage(rows: Usage[]) {
  let inputTokens = 0,
    outputTokens = 0,
    missingInputTokens = 0,
    missingOutputTokens = 0;
  const money = new Map<
    string,
    {
      currency: string;
      costMicros: number;
      frozenMicros: number;
      missing: number;
    }
  >();
  for (const row of rows) {
    if (!unknownUsage(row) && safe(row.inputTokens)) {
      inputTokens += row.inputTokens;
    } else missingInputTokens++;
    if (!unknownUsage(row) && safe(row.outputTokens)) {
      outputTokens += row.outputTokens;
    } else missingOutputTokens++;
    const amount = money.get(row.currency) ?? {
      currency: row.currency,
      costMicros: 0,
      frozenMicros: 0,
      missing: 0,
    };
    if (!unknownUsage(row) && safe(row.costMicros))
      amount.costMicros += row.costMicros;
    else amount.missing++;
    amount.frozenMicros += row.frozenMicros;
    money.set(row.currency, amount);
  }
  return {
    requests: rows.length,
    failed: rows.filter((row) => row.outcome === 'failed').length,
    unknown: rows.filter(unknownUsage).length,
    inputTokens,
    outputTokens,
    missingInputTokens,
    missingOutputTokens,
    money: [...money.values()],
  };
}
export function useModelUsageData() {
  const usage = shallowRef<Usage[]>([]),
    models = shallowRef<Model[]>([]),
    providers = shallowRef<Provider[]>([]);
  const loading = ref(false),
    error = ref(''),
    updatedAt = ref('');
  let controller: AbortController | undefined;
  let version = 0;
  async function reload() {
    const current = ++version;
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    loading.value = true;
    error.value = '';
    usage.value = [];
    models.value = [];
    providers.value = [];
    updatedAt.value = '';
    try {
      const [u, m, p] = await Promise.all([
        readAllPages(modelUsageApi.usage, signal),
        readAllPages(modelUsageApi.models, signal),
        readAllPages(modelUsageApi.providers, signal),
      ]);
      if (current !== version) return;
      usage.value = u;
      models.value = m;
      providers.value = p;
      updatedAt.value = new Date().toISOString();
    } catch (e) {
      if (current !== version) return;
      controller.abort();
      error.value =
        e instanceof ApiError && e.problem.code === 'iam.step_up_required'
          ? 'stepUpRequired'
          : e instanceof ApiError && e.status === 403
            ? 'forbidden'
            : e instanceof ApiError && e.status === 401
              ? 'unauthenticated'
              : 'loadError';
    } finally {
      if (current === version) loading.value = false;
    }
  }
  onMounted(reload);
  onBeforeUnmount(() => {
    version++;
    controller?.abort();
  });
  const providerByModel = computed(
    () => new Map(models.value.map((model) => [model.id, model.providerId])),
  );
  return {
    usage,
    models,
    providers,
    providerByModel,
    loading,
    error,
    updatedAt,
    reload,
  };
}
