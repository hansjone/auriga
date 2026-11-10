<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { relayApi } from '../../admin/relayApi';
import { useRelayI18n } from './relayMessages';
import { useRelayUi } from './relayUi';
import './modelUsage.css';
const { t } = useRelayI18n();
const ui = useRelayUi(t);
const counts = ref<{
  providers: [number, number];
  pools: [number, number];
  keys: [number, number];
  members: number;
}>();
const { loading, error } = ui;
async function load() {
  counts.value = undefined;
  const data = await ui.read('ai.providers.read', () =>
    Promise.all([
      relayApi.providers(ui.signal),
      relayApi.pools(ui.signal),
      relayApi.keys(ui.signal),
    ]),
  );
  if (data)
    counts.value = {
      providers: [
        data[0].items.filter((p) => p.configuration.enabled).length,
        data[0].items.length,
      ],
      pools: [
        data[1].items.filter((p) => p.enabled).length,
        data[1].items.length,
      ],
      keys: [
        data[2].items.filter((k) => k.source !== 'member' && k.active).length,
        data[2].items.filter((k) => k.source !== 'member').length,
      ],
      members: data[2].items.filter((k) => k.source === 'member').length,
    };
}
onMounted(load);
</script>
<template>
  <section class="model-usage">
    <header>
      <h1>{{ t('relay') }}</h1>
      <button :disabled="loading" @click="load">{{ t('refresh') }}</button>
    </header>
    <p v-if="error" class="relay-error" role="alert">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <div v-if="counts" class="relay-cards">
      <RouterLink to="/models/providers" class="relay-card">
        <span>{{ t('providers') }}</span>
        <strong>{{ counts.providers[0] }}</strong>
        <small>{{ t('ofTotal', { total: counts.providers[1] }) }}</small>
      </RouterLink>
      <RouterLink to="/models/providers" class="relay-card">
        <span>{{ t('pools') }}</span>
        <strong>{{ counts.pools[0] }}</strong>
        <small>{{ t('ofTotal', { total: counts.pools[1] }) }}</small>
      </RouterLink>
      <RouterLink to="/models/keys" class="relay-card">
        <span>{{ t('externalKeys') }}</span>
        <strong>{{ counts.keys[0] }}</strong>
        <small
          >{{ t('ofTotal', { total: counts.keys[1] }) }} ·
          {{ t('memberKeyCount', { count: counts.members }) }}</small
        >
      </RouterLink>
    </div>
    <div class="relay-endpoint">
      <h2>{{ t('endpoint') }}</h2>
      <code>https://&lt;portal&gt;/ai/v1</code>
      <p>{{ t('endpointHint') }}</p>
    </div>
    <p class="relay-footnote">{{ t('scope') }}</p>
  </section>
</template>
