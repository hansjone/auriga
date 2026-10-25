<script setup lang="ts">
import { computed, reactive } from 'vue';
import { ElButton } from 'element-plus';
import {
  filterUsage,
  useModelUsageData,
  useModelUsageI18n,
  type UsageFilters,
} from './modelUsageState';
import UsageSummary from './UsageSummary.vue';
import UsageRecords from './UsageRecords.vue';
import './modelUsage.css';
const { t } = useModelUsageI18n();
const {
  usage,
  models,
  providers,
  providerByModel,
  loading,
  error,
  updatedAt,
  reload,
} = useModelUsageData();
const filters = reactive<UsageFilters>({
  from: '',
  to: '',
  accountId: '',
  modelId: '',
  providerId: '',
});
const invalidDates = computed(
  () => !!filters.from && !!filters.to && filters.from > filters.to,
);
const rows = computed(() =>
  invalidDates.value
    ? []
    : filterUsage(usage.value, filters, providerByModel.value),
);
const modelIds = computed(() =>
  [
    ...new Set([
      ...models.value.map((model) => model.id),
      ...usage.value.map((row) => row.modelId),
    ]),
  ].sort(),
);
</script>

<template>
  <section class="model-usage" :aria-busy="loading">
    <header>
      <h1>{{ t('usage') }}</h1>
      <ElButton :disabled="loading" @click="reload">{{
        t('refresh')
      }}</ElButton>
    </header>
    <p>{{ t('scope') }}</p>
    <p>{{ t('providerNote') }}</p>
    <form class="usage-filters" @submit.prevent>
      <label
        >{{ t('from')
        }}<input v-model="filters.from" type="date" data-test="from"
      /></label>
      <label
        >{{ t('to') }}<input v-model="filters.to" type="date" data-test="to"
      /></label>
      <label
        >{{ t('member') }}<input v-model="filters.accountId" data-test="member"
      /></label>
      <label
        >{{ t('provider')
        }}<select v-model="filters.providerId" data-test="provider">
          <option value="">{{ t('all') }}</option>
          <option
            v-for="provider in providers"
            :key="provider.id"
            :value="provider.id"
          >
            {{ provider.name }}
          </option>
        </select></label
      >
      <label
        >{{ t('model')
        }}<select v-model="filters.modelId" data-test="model">
          <option value="">{{ t('all') }}</option>
          <option v-for="id in modelIds" :key="id" :value="id">{{ id }}</option>
        </select></label
      >
    </form>
    <p v-if="invalidDates" role="alert">{{ t('invalidDates') }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-else-if="error" role="alert">{{ t(error) }}</p>
    <template v-else-if="updatedAt && !invalidDates">
      <p>{{ t('updated') }}: {{ updatedAt }}</p>
      <UsageSummary :rows="rows" /><UsageRecords :rows="rows" />
    </template>
  </section>
</template>
