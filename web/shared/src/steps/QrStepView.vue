<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import type { QrStep, StepInput } from '../generated/steps';
import { pickLocalized } from '../locale';

const props = defineProps<{ step: QrStep }>();
const emit = defineEmits<{ submit: [input: StepInput] }>();
const { t, locale } = useI18n();
const expired = ref(false);
let timer: ReturnType<typeof globalThis.setInterval> | undefined;
let poll: ReturnType<typeof globalThis.setInterval> | undefined;

const status = computed(() => props.step.status ?? 'waiting');

onMounted(() => {
  timer = globalThis.setInterval(() => {
    expired.value = Date.now() >= Date.parse(props.step.expiresAt);
  }, 1000);
  const interval = props.step.pollIntervalMs ?? 2000;
  poll = globalThis.setInterval(() => {
    if (!expired.value) emit('submit', { type: 'poll' });
  }, interval);
});

onUnmounted(() => {
  if (timer) globalThis.clearInterval(timer);
  if (poll) globalThis.clearInterval(poll);
});
</script>

<template>
  <div class="qr-step" data-testid="qr-step">
    <div class="qr-tile" :data-state="expired ? 'expired' : status">
      <div class="qr-fake" :title="step.content" aria-hidden="true">
        <span v-for="n in 36" :key="n" class="cell" />
      </div>
      <p class="match">{{ step.matchCode }}</p>
    </div>
    <p class="caption">
      {{
        expired
          ? t('steps.qrExpired')
          : status === 'scanned'
            ? t('steps.qrScanned')
            : pickLocalized(step.hint, locale) || t('steps.qrWaiting')
      }}
    </p>
    <p class="match-label">{{ t('steps.matchCode') }} · {{ step.matchCode }}</p>
  </div>
</template>

<style scoped>
.qr-step {
  text-align: center;
}
.qr-tile {
  width: 220px;
  margin: 0 auto;
  padding: 14px;
  border-radius: 22px;
  background: #b9c5d2;
  color: #0a1520;
}
.qr-fake {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 4px;
  aspect-ratio: 1;
}
.cell {
  background: #0a1520;
  border-radius: 2px;
  opacity: 0.85;
}
.cell:nth-child(3n) {
  opacity: 0.25;
}
.match {
  margin: 10px 0 0;
  font-size: 2.5rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  font-variant-numeric: tabular-nums;
}
.caption,
.match-label {
  color: var(--auriga-muted);
  font-size: 0.875rem;
}
.qr-tile[data-state='expired'] .qr-fake,
.qr-tile[data-state='scanned'] .qr-fake {
  filter: blur(4px);
  opacity: 0.5;
}
</style>
