<script setup lang="ts">
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import ResourcesView from '../ResourcesView.vue';
import ResourceGovernanceView from './ResourceGovernanceView.vue';
import ResourceCatalogView from './ResourceCatalogView.vue';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
const props = defineProps<{
  phase: 'directory' | 'review' | 'published' | 'distribution';
}>();
const route = useRoute(),
  router = useRouter();
const { t } = useI18n({ useScope: 'local', messages: catalogMessages });
const kind = computed(() =>
  route.query.kind === 'skill' ? 'skill' : 'plugin',
);
</script>
<template>
  <div class="resource-center">
    <nav class="market-tabs" :aria-label="t('kind')">
      <button
        v-for="k in ['plugin', 'skill']"
        :key="k"
        :aria-pressed="kind === k"
        @click="router.replace({ query: { ...route.query, kind: k } })"
      >
        {{ t(k) }}
      </button>
      <span>{{ t('tool') }}</span>
    </nav>
    <ResourceCatalogView
      v-if="kind === 'skill'"
      :key="props.phase"
      :phase="props.phase"
    />
    <ResourcesView v-else-if="props.phase === 'directory'" />
    <ResourceGovernanceView v-else :phase="props.phase" />
  </div>
</template>
<style scoped>
.resource-center > nav {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 20px;
}
.resource-center > nav button {
  padding: 8px 20px;
  border: 1px solid var(--auriga-border);
  border-radius: 12px;
  background: var(--auriga-surface);
  color: var(--auriga-text);
  cursor: pointer;
}
.resource-center > nav button[aria-pressed='true'] {
  border-color: var(--auriga-accent);
  background: var(--admin-blue-soft);
}
.resource-center > nav span {
  margin-left: 12px;
  color: var(--auriga-muted);
}
</style>
