<script setup lang="ts">
import { useI18n } from 'vue-i18n';
defineProps<{ title: string }>();
const { t } = useI18n();
</script>
<template>
  <section class="auriga-glass">
    <p class="brand">AURIGA</p>
    <h1>{{ title }}</h1>
    <p>{{ t('welcome') }}</p>
    <p class="muted">{{ t('placeholder') }}</p>
    <slot />
  </section>
</template>
<style scoped>
.brand {
  letter-spacing: 0.2em;
  font-size: 0.75rem;
}
h1 {
  font-size: 2rem;
  font-weight: 500;
}
.muted {
  color: var(--auriga-muted);
}
</style>
