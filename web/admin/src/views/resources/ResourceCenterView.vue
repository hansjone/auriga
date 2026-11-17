<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import ResourceCatalogView from './ResourceCatalogView.vue';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
const props = defineProps<{
  kind: 'plugin' | 'skill';
  phase: 'directory' | 'review' | 'published' | 'distribution';
}>();
const { t } = useI18n({ useScope: 'local', messages: catalogMessages });
const phases = ['directory', 'review', 'published', 'distribution'] as const;
const base = () =>
  props.kind === 'skill' ? '/resources/skills' : '/resources/plugins';
</script>
<template>
  <div class="resource-center">
    <nav class="market-tabs" :aria-label="t(props.kind)">
      <RouterLink
        v-for="p in phases"
        :key="p"
        :to="p === 'directory' ? base() : base() + '/' + p"
        :aria-current="props.phase === p ? 'page' : undefined"
      >
        {{ t(p) }}
      </RouterLink>
    </nav>
    <ResourceCatalogView
      :key="props.phase + props.kind"
      :phase="props.phase"
      :kind="props.kind"
    />
  </div>
</template>
<style scoped>
.resource-center > nav {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 20px;
}
.resource-center > nav a {
  padding: 8px 20px;
  border: 1px solid var(--auriga-border);
  border-radius: 12px;
  background: var(--auriga-surface);
  color: var(--auriga-text);
  text-decoration: none;
}
.resource-center > nav a[aria-current='page'] {
  border-color: var(--auriga-accent);
  background: var(--admin-blue-soft);
}
</style>
