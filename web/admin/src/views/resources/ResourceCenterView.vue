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
    <header class="resource-intro">
      <p>{{ t(props.kind === 'plugin' ? 'pluginIntro' : 'skillIntro') }}</p>
      <p>{{ t(props.phase + 'Intro') }} {{ t('workflowIntro') }}</p>
    </header>
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
  gap: 24px;
  margin-bottom: 20px;
  border-bottom: 1px solid var(--auriga-border);
}
.resource-center > nav a {
  padding: 12px 4px;
  border-bottom: 2px solid transparent;
  color: var(--auriga-muted);
  text-decoration: none;
}
.resource-center > nav a[aria-current='page'] {
  border-bottom-color: var(--auriga-accent-line);
  color: var(--auriga-text);
}
.resource-intro {
  margin-bottom: 16px;
  max-width: 80ch;
}
.resource-intro p {
  line-height: 1.7;
  margin: 0 0 8px;
  color: var(--auriga-muted);
}
.resource-center > nav a:focus-visible {
  outline: 2px solid var(--auriga-accent-line);
  outline-offset: 3px;
}
</style>
