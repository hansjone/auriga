<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type { LoginProvider } from '../api/iam';
import { pickLocalized } from '../locale';

defineProps<{ providers: LoginProvider[] }>();
const emit = defineEmits<{ select: [provider: LoginProvider] }>();
const { t, locale } = useI18n();
</script>

<template>
  <div class="provider-select" data-testid="provider-select">
    <h2 class="title">{{ t('login.chooseProvider') }}</h2>
    <button
      v-for="p in providers"
      :key="p.id"
      type="button"
      class="provider"
      :data-provider-id="p.id"
      @click="emit('select', p)"
    >
      <span>{{
        pickLocalized(p.buttonText, locale) ||
        pickLocalized(p.displayName, locale)
      }}</span>
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
        <path
          d="M6 3.5 10.5 8 6 12.5"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.provider-select {
  display: flex;
  flex-direction: column;
  gap: 0.625rem;
}
.title {
  margin: 0 0 0.375rem;
  font-size: 1.125rem;
  font-weight: 600;
  text-align: center;
  letter-spacing: -0.01em;
}
.provider {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  height: 2.875rem;
  padding: 0 1rem;
  border-radius: 12px;
  border: 1px solid var(--auriga-border);
  background: var(--auriga-field-bg, rgba(4, 12, 20, 0.5));
  color: var(--auriga-text);
  font: inherit;
  font-size: 0.9375rem;
  text-align: left;
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease;
}
.provider svg {
  color: var(--auriga-muted);
  flex-shrink: 0;
  transition: transform 0.15s ease;
}
.provider:hover {
  border-color: var(--auriga-border-strong, rgba(150, 182, 208, 0.32));
  background: var(--auriga-field-hover, rgba(120, 160, 192, 0.1));
}
.provider:hover svg {
  transform: translateX(2px);
  color: var(--auriga-text);
}
.provider:focus-visible {
  outline: 2px solid var(--auriga-accent-line, #5b93bd);
  outline-offset: 2px;
}
</style>
