<script setup lang="ts">
import { onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import type { CompleteStep } from '../generated/steps';

const props = defineProps<{ step: CompleteStep }>();
const emit = defineEmits<{ complete: [redirectTo: string] }>();
const { t } = useI18n();

onMounted(() => {
  emit('complete', props.step.redirectTo);
});
</script>

<template>
  <div class="complete-step" data-testid="complete-step">
    <p>{{ t('steps.complete') }}</p>
  </div>
</template>

<style scoped>
.complete-step {
  text-align: center;
  color: var(--auriga-muted);
}
</style>
