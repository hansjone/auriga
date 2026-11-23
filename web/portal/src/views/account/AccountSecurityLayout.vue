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
  gap: 1.25rem;
  padding: 0.7rem 1.5rem;
  border-bottom: 1px solid var(--auriga-border);
  background: rgba(7, 22, 36, 0.45);
  backdrop-filter: blur(12px);
}
.account-nav a {
  color: var(--auriga-muted);
  text-decoration: none;
  font-size: 0.875rem;
}
.account-nav a:hover,
.account-nav a.router-link-active {
  color: var(--auriga-text);
}
</style>
