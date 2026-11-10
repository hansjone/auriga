<script setup lang="ts">
import type { Usage } from '../../admin/relayApi';
import {
  formatMicros,
  unknownUsage,
  useModelUsageI18n,
} from './modelUsageState';
import { formatDateTime, shortId } from './relayFormat';
defineProps<{
  rows: Usage[];
  memberName?: (accountId: string) => string;
}>();
const { t } = useModelUsageI18n();
const tokens = (row: Usage, value: number | null) =>
  unknownUsage(row) || value === null || !Number.isSafeInteger(value)
    ? t('unknown')
    : value;
</script>

<template>
  <p v-if="!rows.length">{{ t('empty') }}</p>
  <div v-else class="usage-table">
    <table>
      <thead>
        <tr>
          <th
            v-for="label in [
              'time',
              'request',
              'member',
              'model',
              'outcome',
              'quality',
              'input',
              'output',
              'amount',
              'frozen',
              'reservation',
            ]"
            :key="label"
            scope="col"
          >
            {{ t(label) }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.requestId">
          <td :title="row.createdAt">{{ formatDateTime(row.createdAt) }}</td>
          <td class="relay-mono" :title="row.requestId">
            {{ shortId(row.requestId) }}
          </td>
          <td :title="row.accountId">
            {{ memberName ? memberName(row.accountId) : row.accountId }}
          </td>
          <td>{{ row.modelId }}</td>
          <td>
            {{ t(row.outcome === 'failed' ? 'failedState' : row.outcome) }}
          </td>
          <td>{{ t(row.usageQuality) }}</td>
          <td>{{ tokens(row, row.inputTokens) }}</td>
          <td>{{ tokens(row, row.outputTokens) }}</td>
          <td>
            {{
              unknownUsage(row)
                ? t('unknown')
                : (formatMicros(row.costMicros) ?? t('unknown'))
            }}
            {{ row.currency }}
          </td>
          <td>
            {{ formatMicros(row.frozenMicros) ?? t('unknown') }}
            {{ row.currency }}
          </td>
          <td>{{ row.frozenTokens }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
