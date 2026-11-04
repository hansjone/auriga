<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { relayApi } from '../../admin/relayApi';
import { useRelayI18n } from './relayMessages';
import { useRelayUi } from './relayUi';
import './modelUsage.css';
const { t } = useRelayI18n();
const ui = useRelayUi(t);
const counts = ref<{ providers: number; pools: number; keys: number }>();
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
      providers: data[0].items.filter((p) => p.configuration.enabled).length,
      pools: data[1].items.filter((p) => p.enabled).length,
      keys: data[2].items.filter((k) => k.active).length,
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
    <h2>{{ t('endpoint') }}</h2>
    <code>/ai/v1</code>
    <nav class="usage-filters">
      <RouterLink to="/models/providers">{{ t('providers') }}</RouterLink
      ><RouterLink to="/models/keys">{{ t('keys') }}</RouterLink
      ><RouterLink to="/models/usage">{{ t('usage') }}</RouterLink>
    </nav>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <dl v-if="counts" class="usage-summary">
      <div>
        <dt>{{ t('providers') }}</dt>
        <dd>{{ counts.providers }}</dd>
      </div>
      <div>
        <dt>{{ t('pools') }}</dt>
        <dd>{{ counts.pools }}</dd>
      </div>
      <div>
        <dt>{{ t('keys') }}</dt>
        <dd>{{ counts.keys }}</dd>
      </div>
    </dl>
    <p>{{ t('scope') }}</p>
  </section>
</template>
