<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { pickLocalized } from './locale';
import { orderLoginProviders } from './login-preference';
import type { LoginProvider } from './api/iam';
const props = defineProps<{
  providers: LoginProvider[];
  selectedId?: string;
  disabled?: boolean;
}>();
const emit = defineEmits<{ select: [provider: LoginProvider] }>();
const { locale } = useI18n();
const methods = computed(() => orderLoginProviders(props.providers, document));
function label(p: LoginProvider) {
  const en = locale.value === 'en';
  if (p.connectorType === 'uac') return en ? 'Scan' : 'UAC 扫码';
  if (p.connectorType === 'team-account') return en ? 'Team' : '团队账号';
  if (p.connectorType === 'emergency') return en ? 'Emergency' : '应急账号';
  return pickLocalized(p.displayName, locale.value);
}
function icon(p: LoginProvider) {
  if (p.connectorType === 'uac')
    return 'M3 3h6v6H3zM15 3h6v6h-6zM3 15h6v6H3zM15 15h2v2h-2zM19 15h2v6h-6v-2';
  if (p.connectorType === 'team-account')
    return 'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M2 21v-2a7 7 0 0 1 14 0v2M17 4a4 4 0 0 1 0 8M19 15a6 6 0 0 1 3 5';
  if (p.connectorType === 'emergency')
    return 'M8 20a4 4 0 1 0 0-8 4 4 0 0 0 0 8M11 13 21 3M17 7l3 3';
  return 'M9 5H5v14h4M12 8l4 4-4 4M8 12h13';
}
</script>
<template>
  <div
    v-if="methods.length > 1"
    class="login-methods"
    data-testid="login-methods"
    role="group"
    :aria-label="locale === 'en' ? 'Sign-in methods' : '登录方式'"
  >
    <button
      v-for="p in methods"
      :key="p.id"
      type="button"
      :data-provider-id="p.id"
      :aria-label="label(p)"
      :title="label(p)"
      :aria-pressed="p.id === selectedId"
      :disabled="disabled"
      @click="emit('select', p)"
    >
      <svg
        viewBox="0 0 24 24"
        width="22"
        height="22"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path :d="icon(p)" />
      </svg>
    </button>
  </div>
</template>
<style scoped>
.login-methods {
  display: flex;
  justify-content: center;
  gap: 24px;
  width: 100%;
  margin-top: 10px;
  flex-shrink: 0;
}
button {
  position: relative;
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--auriga-muted);
  opacity: 0.7;
  cursor: pointer;
  transition:
    color 0.15s,
    opacity 0.15s;
}
button:hover,
button[aria-pressed='true'] {
  color: var(--auriga-text);
  opacity: 1;
}
button[aria-pressed='true']::after {
  content: '';
  position: absolute;
  bottom: -4px;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: currentColor;
}
button:disabled {
  cursor: default;
  opacity: 0.4;
}
button:focus-visible {
  outline: 2px solid var(--auriga-accent-line);
  outline-offset: 2px;
}
</style>
