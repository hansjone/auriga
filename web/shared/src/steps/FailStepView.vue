<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type { FailStep } from '../generated/steps';
import { pickLocalized } from '../locale';
import { mapProblemMessage } from '../api/problem';

defineProps<{ step: FailStep }>();
const emit = defineEmits<{ retry: [] }>();
const { t, locale } = useI18n();
</script>

<template>
  <div class="fail-step" data-testid="fail-step">
    <h2>{{ t('steps.fail') }}</h2>
    <p class="error" role="alert">
      {{
        step.message
          ? pickLocalized(step.message, locale)
          : mapProblemMessage(
              { title: step.code, status: 400, code: step.code },
              t,
            )
      }}
    </p>
    <button
      v-if="step.retryable"
      type="button"
      class="primary"
      @click="emit('retry')"
    >
      {{ t('login.retry') }}
    </button>
  </div>
</template>

<style scoped>
.fail-step {
  text-align: center;
}
.error {
  color: #d27a7a;
}
.primary {
  height: 2.75rem;
  padding: 0 1.25rem;
  border: 0;
  border-radius: 14px;
  background: var(--auriga-accent);
  color: #dbe4ee;
  font: inherit;
  cursor: pointer;
}
</style>
