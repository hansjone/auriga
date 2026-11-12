<script setup lang="ts">
import { onMounted, shallowRef, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';
import { apiClient } from '../api';
import type { components } from '../../../shared/src/generated/resource';
const { t } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      title: 'Auriga Desktop',
      download: '下载 Windows 版',
      empty: '暂无可用版本',
      failed: '无法读取版本',
      retry: '重试',
    },
    en: {
      title: 'Auriga Desktop',
      download: 'Download for Windows',
      empty: 'No release available',
      failed: 'Could not load release',
      retry: 'Retry',
    },
  },
});
const release = shallowRef<components['schemas']['DesktopManifest']>();
const error = ref('');
async function load() {
  error.value = '';
  try {
    release.value = await apiClient.get('/api/resource/v1/desktop/latest');
  } catch (e) {
    error.value = isApiError(e) && e.status === 404 ? t('empty') : t('failed');
  }
}
onMounted(load);
</script>

<template>
  <section>
    <h2>{{ t('title') }}</h2>
    <template v-if="release">
      <p>
        {{ release.version }} ·
        {{ (release.sizeBytes / 1048576).toFixed(1) }} MiB ·
        {{ release.publishedAt }}
      </p>
      <a :href="`/api/resource/v1/desktop/packages/${release.sequence}`">{{
        t('download')
      }}</a>
      <p>
        <code>SHA-256: {{ release.sha256 }}</code>
      </p>
    </template>
    <template v-if="error"
      ><p role="status">{{ error }}</p>
      <button @click="load">{{ t('retry') }}</button></template
    >
  </section>
</template>

<style scoped>
section {
  margin-block-start: 2rem;
}
code {
  overflow-wrap: anywhere;
}
</style>
