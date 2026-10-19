<script setup lang="ts">
import { onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import type { RedirectStep } from '../generated/steps';

const props = defineProps<{ step: RedirectStep }>();
const { t } = useI18n();

onMounted(() => {
  if ((props.step.method ?? 'GET') === 'GET') {
    globalThis.location.assign(props.step.url);
    return;
  }
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = props.step.url;
  for (const [name, value] of Object.entries(props.step.params ?? {})) {
    const input = document.createElement('input');
    input.type = 'hidden';
    input.name = name;
    input.value = value;
    form.appendChild(input);
  }
  document.body.appendChild(form);
  form.submit();
});
</script>

<template>
  <div class="redirect-step" data-testid="redirect-step">
    <p>{{ t('steps.redirect') }}</p>
    <a class="link" :href="step.url">{{ t('steps.redirectManual') }}</a>
  </div>
</template>

<style scoped>
.redirect-step {
  text-align: center;
  color: var(--auriga-muted);
}
.link {
  color: var(--auriga-accent);
}
</style>
