<script setup lang="ts">
import { computed, onMounted, onUnmounted, shallowRef, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { BRAND_NAME, isApiError } from '@auriga/shared';
import { apiClient } from '../api';
import type { components } from '../../../shared/src/generated/resource';
const { t, locale } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      title: BRAND_NAME,
      download: '下载 Windows 版',
      empty: '暂无可用版本',
      failed: '无法读取版本',
      retry: '重试',
      details: '版本与校验信息',
      copy: '复制校验值',
      copied: '已复制',
      copyFailed: '复制失败，请手动复制',
      loading: '正在读取版本…',
    },
    en: {
      title: BRAND_NAME,
      download: 'Download for Windows',
      empty: 'No release available',
      failed: 'Could not load release',
      retry: 'Retry',
      details: 'Release details',
      copy: 'Copy checksum',
      copied: 'Copied',
      copyFailed: 'Copy failed; select and copy manually',
      loading: 'Loading release…',
    },
  },
});
const release = shallowRef<components['schemas']['DesktopManifest']>();
const error = ref<'' | 'empty' | 'failed'>('');
const copyStatus = ref<'' | 'copied' | 'copyFailed'>('');
const loading = ref(false);
const details = ref<globalThis.HTMLDetailsElement>();
const publishedAt = computed(() => {
  if (!release.value) return '';
  const date = new Date(release.value.publishedAt);
  return Number.isNaN(date.getTime())
    ? release.value.publishedAt
    : new Intl.DateTimeFormat(locale.value, {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(date);
});
function closeDetails(event: globalThis.Event) {
  const node = details.value;
  if (!node?.open) return;
  if (event instanceof globalThis.KeyboardEvent) {
    if (event.key !== 'Escape') return;
    node.open = false;
    node.querySelector('summary')?.focus();
  } else if (
    event.target instanceof globalThis.Node &&
    !node.contains(event.target)
  ) {
    node.open = false;
  }
}
onMounted(() => {
  document.addEventListener('keydown', closeDetails);
  document.addEventListener('click', closeDetails);
});
onUnmounted(() => {
  document.removeEventListener('keydown', closeDetails);
  document.removeEventListener('click', closeDetails);
});
async function copyChecksum() {
  if (!release.value) return;
  try {
    await globalThis.navigator.clipboard.writeText(release.value.sha256);
    copyStatus.value = 'copied';
  } catch {
    copyStatus.value = 'copyFailed';
  }
}
async function load() {
  error.value = '';
  loading.value = true;
  try {
    release.value = await apiClient.get('/api/resource/v1/desktop/latest', {
      credentials: 'omit',
    });
  } catch (e) {
    error.value = isApiError(e) && e.status === 404 ? 'empty' : 'failed';
  } finally {
    loading.value = false;
  }
}
onMounted(load);
</script>

<template>
  <section class="desktop-download" :aria-label="t('title')">
    <div v-if="release" class="download-controls">
      <a
        class="download-button"
        :href="`/api/resource/v1/desktop/packages/${release.sequence}`"
        ><svg
          width="18"
          height="18"
          viewBox="0 0 20 20"
          fill="none"
          aria-hidden="true"
        >
          <path
            d="M10 2v11m-4-4 4 4 4-4M3 14v4h14v-4"
            stroke="currentColor"
            stroke-width="1.5"
          /></svg
        >{{ t('download') }}</a
      >
      <details ref="details" class="release-details">
        <summary :aria-label="t('details')">
          <svg
            class="chevron"
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="m4 6 4 4 4-4"
              stroke="currentColor"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </summary>
        <div class="release-panel">
          <strong>Windows · {{ release.version }}</strong>
          <p>{{ (release.sizeBytes / 1048576).toFixed(1) }} MiB</p>
          <time :datetime="release.publishedAt">{{ publishedAt }}</time>
          <p class="checksum">
            SHA-256<code>{{ release.sha256 }}</code>
          </p>
          <button type="button" @click="copyChecksum">{{ t('copy') }}</button>
          <p v-if="copyStatus" role="status">{{ t(copyStatus) }}</p>
        </div>
      </details>
    </div>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <div v-if="error" class="download-error">
      <p role="status">{{ t(error) }}</p>
      <button type="button" @click="load">{{ t('retry') }}</button>
    </div>
  </section>
</template>
<style scoped>
.desktop-download {
  position: relative;
  text-align: center;
  font-size: 14px;
}
.download-controls {
  display: inline-flex;
  position: relative;
  text-align: left;
  max-width: 100%;
}
.download-button {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 24px;
  background: #bad8ec;
  color: #0b2235;
  font-weight: 600;
  text-decoration: none;
  border-radius: 9px 0 0 9px;
}
.release-details summary {
  display: flex;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  list-style: none;
  cursor: pointer;
  padding: 14px 17px;
  font-size: 20px;
  background: #bad8ec;
  color: #0b2235;
  border-left: 1px solid #8eafc6;
  border-radius: 0 9px 9px 0;
  height: 100%;
}
.release-details[open] .chevron {
  transform: rotate(180deg);
}
summary::-webkit-details-marker {
  display: none;
}
.release-panel {
  position: absolute;
  z-index: 5;
  top: calc(100% + 12px);
  left: 50%;
  transform: translateX(-50%);
  width: min(360px, calc(100vw - 64px));
  padding: 22px;
  box-sizing: border-box;
  background: #10283b;
  color: #e8f0f7;
  border: 1px solid #365065;
  border-radius: 10px;
  box-shadow: 0 16px 32px #0005;
}
.release-panel p {
  margin: 12px 0;
}
time {
  color: #a9bfd0;
  font-size: 12px;
  overflow-wrap: anywhere;
}
code {
  display: block;
  overflow-wrap: anywhere;
  white-space: normal;
  margin-top: 8px;
  color: #a9bfd0;
  line-height: 1.6;
}
button {
  border: 1px solid #51738e;
  color: #e8f0f7;
  background: #193b55;
  border-radius: 6px;
  padding: 8px 14px;
  cursor: pointer;
  font: inherit;
}
a:focus-visible,
summary:focus-visible,
button:focus-visible {
  outline: 2px solid #8fb9d6;
  outline-offset: 4px;
}
.download-error {
  color: #a9bfd0;
}
</style>
