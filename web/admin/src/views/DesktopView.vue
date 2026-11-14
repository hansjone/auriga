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
  error = ref(''),
  notice = ref('');
const manifest = shallowRef<File>(),
  archive = shallowRef<File>();
const policy = shallowRef<{
  channel?: string;
  minimumVersion?: string;
  rolloutPercentage?: number;
  rollbackFrom?: string | null;
}>();
const base = '/api/resource/v1/admin/desktop/releases';
async function selected(event: Event, kind: 'manifest' | 'archive') {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (kind === 'manifest') {
    manifest.value = file;
    policy.value = undefined;
    if (file && file.size <= 32768) {
      try {
        policy.value = JSON.parse(await file.text());
      } catch {
        error.value = t('desktopFailed');
      }
    }
  } else archive.value = file;
}
async function load() {
  ledger.value = await apiClient.get<Ledger>(base);
}
async function run(action: () => Promise<unknown>, completedMessage = '') {
  busy.value = true;
  error.value = '';
  notice.value = '';
  let completed = false;
  try {
    await action();
    if (completedMessage) {
      completed = true;
      notice.value = completedMessage;
    }
    await load();
  } catch {
    if (completed)
      notice.value = `${completedMessage}，台账刷新失败，请验证身份并刷新。`;
    else error.value = t('desktopFailed');
  } finally {
    busy.value = false;
  }
}
function withdraw(sequence: number) {
  return run(() => apiClient.delete(`${base}/${sequence}`), '发行已下架');
}
async function publish() {
  if (!manifest.value || !archive.value || manifest.value.size > 32768) return;
  const text = await manifest.value.text();
  const bytes = new TextEncoder().encode(text);
  const header = btoa(Array.from(bytes, (b) => String.fromCharCode(b)).join(''))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '');
  await run(
    () =>
      apiClient.request(base, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/octet-stream',
          'X-Desktop-Manifest': header,
        },
        body: archive.value,
      }),
    '发行已发布',
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
      <p v-if="notice" role="status">{{ notice }}</p>
      <button :disabled="busy" @click="run(requestStepUp)">
        {{ t('desktopVerify') }}
      </button>
      <p>
        发布策略包含在离线签名清单中。调整最低可用版本、渠道或灰度时，使用更高发行序号重新签名发布；服务端不持有私钥。
      </p>
      <p>
        低于最低可用版本的客户端必须更新，不受灰度比例限制。显式回滚需在清单中指定
        rollbackFrom 来源版本。
      </p>
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
            accept=".exe"
            :disabled="busy"
            @change="selected($event, 'archive')"
        /></label>
        <p v-if="policy" role="status">
          渠道：{{ policy.channel === 'beta' ? '测试' : '正式' }} ·
          最低可用版本：{{ policy.minimumVersion }} · 灰度：{{
            policy.rolloutPercentage ?? 100
          }}%
          <span v-if="policy.rollbackFrom">
            · 显式回滚来源：{{ policy.rollbackFrom }}</span
          >
        </p>
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
        <p>
          渠道：{{ item.manifest.channel === 'beta' ? '测试' : '正式' }} ·
          最低可用版本：{{ item.manifest.minimumVersion }} · 灰度：{{
            item.manifest.rolloutPercentage ?? 100
          }}%
        </p>
        <p v-if="item.manifest.rollbackFrom">
          显式回滚来源：{{ item.manifest.rollbackFrom }}
        </p>
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
