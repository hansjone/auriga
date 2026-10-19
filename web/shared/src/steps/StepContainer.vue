<script setup lang="ts">
import type { ClientStep, StepInput } from '../generated/steps';
import FormStepView from './FormStepView.vue';
import QrStepView from './QrStepView.vue';
import RedirectStepView from './RedirectStepView.vue';
import WebAuthnStepView from './WebAuthnStepView.vue';
import DeviceStepView from './DeviceStepView.vue';
import FailStepView from './FailStepView.vue';
import CompleteStepView from './CompleteStepView.vue';

defineProps<{ step: ClientStep }>();
const emit = defineEmits<{
  submit: [input: StepInput];
  retry: [];
  complete: [redirectTo: string];
}>();
</script>

<template>
  <div
    class="step-container"
    data-testid="step-container"
    :data-step-type="step.type"
  >
    <FormStepView
      v-if="step.type === 'form'"
      :step="step"
      @submit="emit('submit', $event)"
    />
    <QrStepView
      v-else-if="step.type === 'qr'"
      :step="step"
      @submit="emit('submit', $event)"
    />
    <RedirectStepView v-else-if="step.type === 'redirect'" :step="step" />
    <WebAuthnStepView
      v-else-if="step.type === 'webauthn'"
      :step="step"
      @submit="emit('submit', $event)"
    />
    <DeviceStepView
      v-else-if="step.type === 'device'"
      :step="step"
      @submit="emit('submit', $event)"
    />
    <FailStepView
      v-else-if="step.type === 'fail'"
      :step="step"
      @retry="emit('retry')"
    />
    <CompleteStepView
      v-else-if="step.type === 'complete'"
      :step="step"
      @complete="emit('complete', $event)"
    />
  </div>
</template>
