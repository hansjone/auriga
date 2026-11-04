import { useI18n } from 'vue-i18n';
import type { Usage } from '../../admin/relayApi';
import { messages } from './modelUsageMessages';
export const useModelUsageI18n = () => useI18n({ useScope: 'local', messages });
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
