<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import type { DeviceStep, StepInput } from '../generated/steps';

const props = defineProps<{ step: DeviceStep }>();
const emit = defineEmits<{ submit: [input: StepInput] }>();
const { t } = useI18n();
let poll: ReturnType<typeof globalThis.setInterval> | undefined;

onMounted(() => {
  const seconds = props.step.intervalSeconds ?? 5;
  poll = globalThis.setInterval(
    () => emit('submit', { type: 'poll' }),
    seconds * 1000,
  );
});

onUnmounted(() => {
  if (poll) globalThis.clearInterval(poll);
});
</script>

<template>
  <div class="device-step" data-testid="device-step">
    <p class="code">{{ step.userCode }}</p>
    <p>
      {{
        t('steps.device', {
          uri: step.verificationUri,
          code: step.userCode,
        })
      }}
    </p>
    <a :href="step.verificationUriComplete || step.verificationUri">
      {{ step.verificationUri }}
    </a>
  </div>
</template>

<style scoped>
.device-step {
  text-align: center;
  color: var(--auriga-muted);
}
.code {
  font-size: 2rem;
  font-weight: 600;
  letter-spacing: 0.2em;
  color: var(--auriga-text);
  font-variant-numeric: tabular-nums;
}
</style>
