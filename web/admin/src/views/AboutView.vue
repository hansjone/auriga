<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import '../admin/management.css';

const { locale } = useI18n();
const labels = computed(() =>
  locale.value === 'en'
    ? {
        title: 'About',
        version: 'Version',
        upstreamVersion: 'Upstream version',
        commit: 'Source commit',
        branch: 'Site branch',
        dirty: 'Uncommitted changes',
        siteChanges: 'Contains site changes',
        snapshotSha256: 'Source SHA-256',
        builtAt: 'Build time',
        yes: 'Yes',
        no: 'No',
        absent: 'This release has no recorded build provenance.',
        loading: 'Loading…',
      }
    : {
        title: '关于',
        version: '版本',
        upstreamVersion: '上游版本',
        commit: '源码提交',
        branch: '现场分支',
        dirty: '工作区未提交改动',
        siteChanges: '含现场改动',
        snapshotSha256: '源码 SHA-256',
        builtAt: '构建时间',
        yes: '是',
        no: '否',
        absent: '当前发行尚未记录版本来源',
        loading: '加载中…',
      },
);
const fields = [
  'version',
  'upstreamVersion',
  'commit',
  'branch',
  'dirty',
  'siteChanges',
  'snapshotSha256',
  'builtAt',
] as const;
const source = ref<Record<string, string | boolean> | null>(null);
const loading = ref(true);
const controller = new globalThis.AbortController();
onBeforeUnmount(() => controller.abort());
onMounted(async () => {
  try {
    const response = await globalThis.fetch('/provenance.json', {
      cache: 'no-store',
      signal: controller.signal,
    });
    if (!response.ok) return;
    const data: unknown = await response.json();
    if (!data || typeof data !== 'object') return;
    const record = data as Record<string, unknown>;
    if (
      !fields.every(
        (key) =>
          typeof record[key] ===
          (key === 'dirty' || key === 'siteChanges' ? 'boolean' : 'string'),
      )
    )
      return;
    source.value = record as Record<string, string | boolean>;
  } catch {
    source.value = null;
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <section class="management">
    <div class="head">
      <h1>{{ labels.title }}</h1>
    </div>
    <p v-if="loading" role="status">{{ labels.loading }}</p>
    <div v-else-if="source" class="details">
      <dl>
        <template v-for="key in fields" :key="key">
          <dt>{{ labels[key] }}</dt>
          <dd>
            {{
              key === 'dirty' || key === 'siteChanges'
                ? source[key]
                  ? labels.yes
                  : labels.no
                : source[key] || '—'
            }}
          </dd>
        </template>
      </dl>
    </div>
    <p v-else role="status" class="muted">{{ labels.absent }}</p>
  </section>
</template>
