<script setup lang="ts">
/* global File, Event, HTMLInputElement, TextEncoder, btoa */
import { onMounted, ref, shallowRef } from 'vue';
import { useAdminI18n } from '../admin/messages';
import { adminApi } from '../admin/api';
import { apiClient } from '../api';
import { requestStepUp } from '../admin/stepUp';
import type { components } from '../../../shared/src/generated/resource';
type Ledger = components['schemas']['DesktopReleaseLedger'];
const { t } = useAdminI18n();
const ledger = shallowRef<Ledger>({});
const allowed = ref(false),
  busy = ref(false),
  error = ref('');
const manifest = shallowRef<File>(),
  archive = shallowRef<File>();
const base = '/api/resource/v1/admin/desktop/releases';
function selected(event: Event, kind: 'manifest' | 'archive') {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (kind === 'manifest') manifest.value = file;
  else archive.value = file;
}
async function load() {
  ledger.value = await apiClient.get<Ledger>(base);
}
async function run(action: () => Promise<unknown>) {
  busy.value = true;
  error.value = '';
  try {
    await action();
    await load();
  } catch {
    error.value = t('desktopFailed');
  } finally {
    busy.value = false;
  }
}
function withdraw(sequence: number) {
  return run(() => apiClient.delete(`${base}/${sequence}`));
}
async function publish() {
  if (!manifest.value || !archive.value || manifest.value.size > 32768) return;
  const text = await manifest.value.text();
  const bytes = new TextEncoder().encode(text);
  const header = btoa(Array.from(bytes, (b) => String.fromCharCode(b)).join(''))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '');
  await run(() =>
    apiClient.request(base, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/octet-stream',
        'X-Desktop-Manifest': header,
      },
      body: archive.value,
    }),
  );
}
onMounted(async () => {
  try {
    allowed.value = (await adminApi.membership()).permissions.includes(
      'resource.versions.publish',
    );
    if (allowed.value) await run(load);
  } catch {
    error.value = t('desktopFailed');
  }
});
</script>

<template>
  <section class="management-page">
    <h1>{{ t('desktopTitle') }}</h1>
    <p v-if="!allowed">{{ t('forbidden') }}</p>
    <template v-else>
      <p v-if="error" role="alert">{{ error }}</p>
      <button :disabled="busy" @click="run(requestStepUp)">
        {{ t('desktopVerify') }}
      </button>
      <form @submit.prevent="publish">
        <label
          >{{ t('desktopManifest') }}
          <input
            type="file"
            accept=".json"
            :disabled="busy"
            @change="selected($event, 'manifest')"
        /></label>
        <label
          >{{ t('desktopArchive') }}
          <input
            type="file"
            accept=".zip"
            :disabled="busy"
            @change="selected($event, 'archive')"
        /></label>
        <button :disabled="busy || !manifest || !archive">
          {{ t('desktopPublish') }}
        </button>
      </form>
      <p v-if="!ledger.items?.length">{{ t('desktopEmpty') }}</p>
      <article v-for="item in ledger.items" :key="item.manifest.sequence">
        <h2>{{ item.manifest.version }}</h2>
        <p>
          {{ (item.manifest.sizeBytes / 1048576).toFixed(1) }} MiB ·
          {{ item.manifest.publishedAt }}
        </p>
        <p>{{ item.manifest.notes }}</p>
        <code>{{ item.manifest.sha256 }}</code>
        <p v-if="item.withdrawn">{{ t('desktopWithdrawn') }}</p>
        <button
          v-else
          :disabled="busy"
          @click="withdraw(item.manifest.sequence)"
        >
          {{ t('desktopWithdraw') }}
        </button>
      </article>
    </template>
  </section>
</template>

<style scoped>
form,
article {
  display: grid;
  gap: 1rem;
  margin-block: 1.5rem;
}
code {
  overflow-wrap: anywhere;
}
</style>
