<script setup lang="ts">
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { navigation } from '../admin/navigation';
import { useAdminI18n } from '../admin/messages';
import { catalogMessages } from '../admin/resourceCatalogMessages';
const { t, mergeLocaleMessage } = useAdminI18n();
for (const locale of ['zh-CN', 'en'] as const) {
  const { toolIntro, workflowIntro } = catalogMessages[locale];
  mergeLocaleMessage(locale, { toolIntro, workflowIntro });
}
const route = useRoute();
const title = computed(
  () =>
    navigation
      .flatMap((item) => [[item.path, item.key], ...item.children])
      .find(([path]) => path === route.path)?.[1] ?? 'navNotOpen',
);
const explanation = computed(() =>
  route.path.includes('headquarters')
    ? 'navHeadquartersDetail'
    : route.path.startsWith('/models')
      ? 'navModelsDetail'
      : route.path === '/overview'
        ? 'navOverviewDetail'
        : 'navNotOpenDetail',
);
</script>
<template>
  <section class="resource-page">
    <header class="resource-head">
      <h1>{{ t(title) }}</h1>
    </header>
    <template v-if="$route.path === '/resources/tools'">
      <p>{{ t('toolIntro') }}</p>
      <p>{{ t('workflowIntro') }}</p>
    </template>
    <template v-else
      ><p class="resource-notice">{{ t('navNotOpen') }}</p>
      <p>{{ t(explanation) }}</p></template
    >
    <p
      v-if="
        $route.path.startsWith('/resources') &&
        $route.path !== '/resources/tools'
      "
    >
      {{ t('resourceBoundary') }}
    </p>
    <RouterLink v-if="$route.path === '/overview'" to="/runtime/headquarters"
      >{{ t('navHeadquarters') }} →</RouterLink
    ><RouterLink
      v-else-if="$route.path.startsWith('/resources')"
      to="/resources"
      >{{ t('resourceCatalog') }} →</RouterLink
    >
  </section>
</template>
