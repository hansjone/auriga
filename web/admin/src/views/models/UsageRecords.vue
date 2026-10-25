<script setup lang="ts">
import type { Usage } from '../../admin/modelUsageApi';
import {
  formatMicros,
  unknownUsage,
  useModelUsageI18n,
} from './modelUsageState';
defineProps<{ rows: Usage[] }>();
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
          <td>{{ new Date(row.createdAt).toISOString() }}</td>
          <td>{{ row.requestId }}</td>
          <td>{{ row.accountId }}</td>
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
