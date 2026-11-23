<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { HintTrigger } from '@auriga/shared';
import ResourceCatalogView from './ResourceCatalogView.vue';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
const props = defineProps<{
  kind: 'plugin' | 'skill';
  phase: 'directory' | 'review' | 'published' | 'distribution';
}>();
const { t, locale } = useI18n({ useScope: 'local', messages: catalogMessages });
const phases = ['directory', 'review', 'published', 'distribution'] as const;
const base = () =>
  props.kind === 'skill' ? '/resources/skills' : '/resources/plugins';
</script>
<template>
  <div class="resource-center">
    <header class="resource-toolbar-head">
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
      <HintTrigger :label="locale === 'en' ? 'Workflow notes' : '流程说明'">
        <p>{{ t(props.kind === 'plugin' ? 'pluginIntro' : 'skillIntro') }}</p>
        <p>{{ t(props.phase + 'Intro') }} {{ t('workflowIntro') }}</p>
      </HintTrigger>
    </header>
    <ResourceCatalogView
      :key="props.phase + props.kind"
      :phase="props.phase"
      :kind="props.kind"
    />
  </div>
</template>
<style scoped>
.resource-toolbar-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 20px;
  border-bottom: 1px solid var(--auriga-border);
}
.resource-center > .resource-toolbar-head > nav {
  display: flex;
  align-items: center;
  gap: 20px;
  overflow-x: auto;
  min-width: 0;
  flex: 1;
  margin-bottom: -1px;
}
.resource-center > .resource-toolbar-head > nav a {
  padding: 0 2px 11px;
  border-bottom: 2px solid transparent;
  color: var(--auriga-muted);
  text-decoration: none;
  font-size: 13.5px;
  white-space: nowrap;
}
.resource-center > .resource-toolbar-head > nav a[aria-current='page'] {
  border-bottom-color: var(--auriga-accent);
  color: var(--auriga-text);
  font-weight: 600;
}
.resource-center > .resource-toolbar-head > nav a:hover {
  color: var(--auriga-text);
}
.resource-toolbar-head :deep(.auriga-hint) {
  margin-bottom: 6px;
}
.resource-center > .resource-toolbar-head > nav a:focus-visible {
  outline: 2px solid var(--auriga-accent);
  outline-offset: 3px;
}
</style>
