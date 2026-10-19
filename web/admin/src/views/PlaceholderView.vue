<script setup lang="ts">
import { useI18n } from 'vue-i18n';
defineProps<{ titleKey: string }>();
const { t } = useI18n();
</script>
<template>
  <section class="auriga-glass">
    <h1>{{ t(titleKey) }}</h1>
    <p class="muted">{{ t('placeholder') }}</p>
  </section>
</template>
<style scoped>
.muted {
  color: var(--auriga-muted);
}
</style>
