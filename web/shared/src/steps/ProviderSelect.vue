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
      {{
        pickLocalized(p.buttonText, locale) ||
        pickLocalized(p.displayName, locale)
      }}
    </button>
  </div>
</template>

<style scoped>
.provider-select {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.title {
  margin: 0 0 0.5rem;
  font-size: 1.25rem;
  font-weight: 600;
  text-align: center;
}
.provider {
  height: 2.75rem;
  border-radius: 14px;
  border: 1px solid var(--auriga-border);
  background: rgba(120, 160, 192, 0.1);
  color: var(--auriga-text);
  font: inherit;
  cursor: pointer;
}
.provider:hover {
  background: rgba(120, 160, 192, 0.18);
}
</style>
