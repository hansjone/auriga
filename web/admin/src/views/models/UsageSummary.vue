<script setup lang="ts">
import type { Usage } from '../../admin/modelUsageApi';
import { computed } from 'vue';
import {
  formatMicros,
  summarizeUsage,
  useModelUsageI18n,
} from './modelUsageState';
const props = defineProps<{ rows: Usage[] }>();
const { t } = useModelUsageI18n();
const summary = computed(() => summarizeUsage(props.rows));
</script>

<template>
  <dl class="usage-summary">
    <div>
      <dt>{{ t('requests') }}</dt>
      <dd>{{ summary.requests }}</dd>
    </div>
    <div data-test="failed">
      <dt>{{ t('failed') }}</dt>
      <dd>{{ summary.failed }}</dd>
    </div>
    <div>
      <dt>{{ t('unknownCount') }}</dt>
      <dd>{{ summary.unknown }}</dd>
    </div>
    <div>
      <dt>{{ t('tokens') }}</dt>
      <dd>
        <span data-test="input-total"
          >{{
            (summary.requests === 0 ||
              summary.missingInputTokens < summary.requests) &&
            Number.isSafeInteger(summary.inputTokens)
              ? summary.inputTokens
              : t('unknown')
          }}<small v-if="summary.missingInputTokens">
            ({{ t('partial') }})</small
          ></span
        >
        /
        <span data-test="output-total"
          >{{
            (summary.requests === 0 ||
              summary.missingOutputTokens < summary.requests) &&
            Number.isSafeInteger(summary.outputTokens)
              ? summary.outputTokens
              : t('unknown')
          }}<small v-if="summary.missingOutputTokens">
            ({{ t('partial') }})</small
          ></span
        >
      </dd>
    </div>
    <template v-for="money in summary.money" :key="money.currency">
      <div>
        <dt>{{ t('cost') }} · {{ money.currency }}</dt>
        <dd>
          {{
            money.missing ===
            rows.filter((row) => row.currency === money.currency).length
              ? t('unknown')
              : (formatMicros(money.costMicros) ?? t('unknown'))
          }}
          <span v-if="money.missing">({{ t('partial') }})</span>
        </dd>
      </div>
      <div>
        <dt>{{ t('frozen') }} · {{ money.currency }}</dt>
        <dd>{{ formatMicros(money.frozenMicros) ?? t('unknown') }}</dd>
      </div>
    </template>
  </dl>
  <p>{{ t('budgetNote') }}</p>
</template>
