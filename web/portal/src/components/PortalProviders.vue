<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { pickLocalized, type LoginProvider } from '@auriga/shared';
const props = defineProps<{
  providers: LoginProvider[];
  selectedId?: string;
  disabled?: boolean;
}>();
const emit = defineEmits<{ select: [provider: LoginProvider] }>();
const { locale } = useI18n();
const methods = computed(() =>
  props.providers.slice().sort((a, b) => {
    const rank = (p: LoginProvider) =>
      p.connectorType === 'uac' ? 0 : p.connectorType === 'emergency' ? 2 : 1;
    return rank(a) - rank(b) || a.order - b.order;
  }),
);
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
      :aria-label="pickLocalized(p.displayName, locale)"
      :title="pickLocalized(p.displayName, locale)"
      :aria-pressed="p.id === selectedId"
      :disabled="disabled"
      :class="{ emergency: p.connectorType === 'emergency' }"
      @click="emit('select', p)"
    >
      <svg
        viewBox="0 0 24 24"
        width="20"
        height="20"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path :d="icon(p)" /></svg
      ><span>{{ label(p) }}</span>
    </button>
  </div>
</template>
<style scoped>
.login-methods {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  width: 100%;
  margin-top: 24px;
  padding: 4px;
  box-sizing: border-box;
  border: 1px solid var(--auriga-border);
  border-radius: 16px;
  background: #07142166;
}
button {
  display: flex;
  flex: 1 1 56px;
  min-width: 0;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 8px 4px;
  border: 0;
  border-radius: 12px;
  background: transparent;
  color: var(--auriga-muted);
  font: inherit;
  font-size: 11px;
  line-height: 16px;
  cursor: pointer;
}
span {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
button[aria-pressed='true'] {
  color: var(--auriga-text);
  background: #8fb9d61a;
}
button.emergency:not([aria-pressed='true']) {
  opacity: 0.7;
}
button:hover {
  color: var(--auriga-text);
}
button:disabled {
  cursor: default;
  opacity: 0.5;
}
button:focus-visible {
  outline: 2px solid var(--auriga-accent-line);
  outline-offset: 2px;
}
</style>
