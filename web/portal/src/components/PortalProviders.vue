<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { ProviderSelect, type LoginProvider } from '@auriga/shared';
const props = defineProps<{ providers: LoginProvider[] }>();
const emit = defineEmits<{ select: [provider: LoginProvider] }>();
const { locale } = useI18n();
const team = computed(() =>
  props.providers
    .filter((p) => p.connectorType !== 'emergency')
    .sort(
      (a, b) =>
        Number(b.connectorType === 'uac') - Number(a.connectorType === 'uac'),
    ),
);
const emergency = computed(() =>
  props.providers.filter((p) => p.connectorType === 'emergency'),
);
</script>
<template>
  <div>
    <h1>
      {{ locale === 'en' ? 'Sign in to your workspace' : '登录团队工作台' }}
    </h1>
    <ProviderSelect
      v-if="team.length"
      :providers="team"
      @select="emit('select', $event)"
    />
    <details v-if="team.length && emergency.length">
      <summary>
        {{ locale === 'en' ? 'Emergency account' : '应急账号登录' }}
      </summary>
      <ProviderSelect :providers="emergency" @select="emit('select', $event)" />
    </details>
    <ProviderSelect
      v-else-if="!team.length"
      :providers="emergency"
      @select="emit('select', $event)"
    />
  </div>
</template>
<style scoped>
h1 {
  font-size: 21px;
  font-weight: 500;
  text-align: center;
  margin: 0 0 26px;
}
details {
  margin-top: 26px;
}
summary {
  cursor: pointer;
  color: var(--auriga-muted);
  text-align: center;
  font-size: 13px;
  margin-bottom: 14px;
}
summary:focus-visible {
  outline: 2px solid var(--auriga-accent-line);
  outline-offset: 4px;
}
:deep(.title) {
  display: none;
}
</style>
