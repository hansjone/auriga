<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { messages } from './messages';
const { t } = useI18n({ useScope: 'local', messages });
</script>
<template>
  <nav class="account-nav" :aria-label="t('title')">
    <RouterLink to="/">{{ t('home') }}</RouterLink
    ><RouterLink to="/account/security">{{ t('title') }}</RouterLink>
  </nav>
  <RouterView />
</template>
<style scoped>
.account-nav {
  display: flex;
  justify-content: flex-end;
  gap: 1.5rem;
  padding: 0.75rem 1.5rem;
  border-bottom: 1px solid var(--auriga-border);
}
</style>
