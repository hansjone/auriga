<script setup lang="ts">
import { computed, ref } from 'vue';
import { ElButton } from 'element-plus';
import { useModelUsageData, useModelUsageI18n } from './modelUsageState';
import UsageSummary from './UsageSummary.vue';
import UsageRecords from './UsageRecords.vue';
import ModelUsageError from './ModelUsageError.vue';
import './modelUsage.css';
const { t } = useModelUsageI18n();
const { usage, models, providers, loading, error, updatedAt, reload } =
  useModelUsageData();
const days = ref(1);
const recent = computed(() => {
  const end = Date.parse(updatedAt.value);
  return usage.value.filter((row) => {
    const time = Date.parse(row.createdAt);
    return time >= end - days.value * 86400000 && time <= end;
  });
});
const latest = computed(() =>
  [...recent.value]
    .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
    .slice(0, 20),
);
</script>

<template>
  <section class="model-usage" :aria-busy="loading">
    <header>
      <h1>{{ t('relay') }}</h1>
      <ElButton :disabled="loading" @click="reload">{{
        t('refresh')
      }}</ElButton>
    </header>
    <h2>{{ t('endpoint') }}</h2>
    <code>/ai/v1</code>
    <p>{{ t('endpointNote') }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <ModelUsageError
      v-else-if="error"
      :error="error"
      return-to="/models/relay"
    />
    <template v-else-if="updatedAt">
      <p>{{ t('updated') }}: {{ updatedAt }}</p>
      <dl class="usage-summary">
        <div data-test="providers">
          <dt>{{ t('providers') }}</dt>
          <dd>{{ providers.filter((provider) => provider.enabled).length }}</dd>
        </div>
        <div data-test="models">
          <dt>{{ t('models') }}</dt>
          <dd>{{ models.filter((model) => model.enabled).length }}</dd>
        </div>
      </dl>
      <h2>{{ t('recent') }}</h2>
      <label
        >{{ t('window') }}
        <select v-model="days">
          <option :value="1">{{ t('day') }}</option>
          <option :value="7">{{ t('week') }}</option>
        </select></label
      >
      <UsageSummary :rows="recent" />
      <h2>{{ t('recentRecords') }}</h2>
      <UsageRecords :rows="latest" />
    </template>
    <p>{{ t('scope') }}</p>
  </section>
</template>
