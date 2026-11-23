<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';

const props = withDefaults(
  defineProps<{
    name: string;
    modelValue?: string;
    length?: number;
    required?: boolean;
    autocomplete?: string;
    disabled?: boolean;
  }>(),
  {
    modelValue: '',
    length: 6,
    required: false,
    autocomplete: 'one-time-code',
    disabled: false,
  },
);
const emit = defineEmits<{ 'update:modelValue': [value: string] }>();

const size = computed(() => Math.max(4, Math.min(props.length || 6, 8)));
const cells = ref<string[]>([]);
const refs = ref<(globalThis.HTMLInputElement | null)[]>([]);

function fill(raw: string) {
  const digits = raw.replace(/\D/g, '').slice(0, size.value).split('');
  cells.value = Array.from({ length: size.value }, (_, i) => digits[i] ?? '');
}

watch(
  () => props.modelValue,
  (value) => {
    const next = (value || '').replace(/\D/g, '').slice(0, size.value);
    if (next === cells.value.join('')) return;
    fill(next);
  },
  { immediate: true },
);

watch(size, () => fill(props.modelValue || ''));

function publish() {
  emit('update:modelValue', cells.value.join(''));
}

function focusAt(index: number) {
  void nextTick(() => refs.value[index]?.focus());
}

function onInput(index: number, event: globalThis.Event) {
  const target = event.target as globalThis.HTMLInputElement;
  const raw = target.value.replace(/\D/g, '');
  if (!raw) {
    cells.value[index] = '';
    publish();
    return;
  }
  if (raw.length > 1) {
    const start = cells.value.slice(0, index);
    const incoming = raw.split('');
    const rest = cells.value.slice(index + incoming.length);
    cells.value = [...start, ...incoming, ...rest].slice(0, size.value);
    while (cells.value.length < size.value) cells.value.push('');
    publish();
    focusAt(Math.min(index + incoming.length, size.value - 1));
    return;
  }
  cells.value[index] = raw;
  publish();
  if (index < size.value - 1) focusAt(index + 1);
}

function onKeydown(index: number, event: globalThis.KeyboardEvent) {
  if (event.key === 'Backspace') {
    if (cells.value[index]) {
      cells.value[index] = '';
      publish();
      return;
    }
    if (index > 0) {
      event.preventDefault();
      cells.value[index - 1] = '';
      publish();
      focusAt(index - 1);
    }
    return;
  }
  if (event.key === 'ArrowLeft' && index > 0) {
    event.preventDefault();
    focusAt(index - 1);
  } else if (event.key === 'ArrowRight' && index < size.value - 1) {
    event.preventDefault();
    focusAt(index + 1);
  }
}

function onPaste(index: number, event: globalThis.ClipboardEvent) {
  const text = event.clipboardData?.getData('text') ?? '';
  if (!/\d/.test(text)) return;
  event.preventDefault();
  const digits = text.replace(/\D/g, '').slice(0, size.value - index).split('');
  for (let i = 0; i < digits.length; i++) cells.value[index + i] = digits[i]!;
  publish();
  focusAt(Math.min(index + digits.length, size.value - 1));
}

function onBackingInvalid(event: globalThis.Event) {
  event.preventDefault();
  const empty = cells.value.findIndex((d) => !d);
  focusAt(empty < 0 ? 0 : empty);
}

function setRef(el: unknown, index: number) {
  refs.value[index] =
    el instanceof globalThis.HTMLInputElement ? el : null;
}
</script>

<template>
  <div
    class="otp-boxes"
    data-testid="otp-boxes"
    role="group"
    :style="{ gridTemplateColumns: `repeat(${size}, minmax(0, 1fr))` }"
  >
    <!-- Named backing for submit/tests; kept out of tab order. -->
    <input
      class="otp-value"
      :name="name"
      :value="modelValue"
      :required="required"
      tabindex="-1"
      aria-hidden="true"
      inputmode="numeric"
      pattern="[0-9]*"
      :minlength="size"
      :maxlength="size"
      :disabled="disabled"
      @input="
        fill(($event.target as globalThis.HTMLInputElement).value);
        publish();
      "
      @invalid="onBackingInvalid"
    />
    <input
      v-for="(_, index) in size"
      :key="index"
      :ref="(el) => setRef(el, index)"
      class="otp-cell"
      type="text"
      inputmode="numeric"
      maxlength="1"
      :autocomplete="index === 0 ? autocomplete || 'one-time-code' : 'off'"
      :aria-label="`${index + 1} / ${size}`"
      :disabled="disabled"
      :value="cells[index] || ''"
      @input="onInput(index, $event)"
      @keydown="onKeydown(index, $event)"
      @paste="onPaste(index, $event)"
      @focus="($event.target as globalThis.HTMLInputElement).select()"
    />
  </div>
</template>

<style scoped>
.otp-boxes {
  position: relative;
  display: grid;
  gap: 8px;
  width: 100%;
}
.otp-value {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
  border: 0;
  pointer-events: none;
}
.otp-cell {
  box-sizing: border-box;
  width: 100%;
  aspect-ratio: 1 / 1;
  max-height: 3rem;
  padding: 0;
  border-radius: 12px;
  border: 1px solid var(--auriga-border);
  background: var(--auriga-field-bg, rgba(4, 12, 20, 0.5));
  color: var(--auriga-text);
  font: inherit;
  font-size: 1.25rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  text-align: center;
  outline: none;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease;
}
.otp-cell:hover {
  border-color: var(--auriga-border-strong, rgba(150, 182, 208, 0.32));
}
.otp-cell:focus-visible {
  border-color: var(--auriga-accent-line, #5b93bd);
}
.otp-cell:disabled {
  opacity: 0.55;
  cursor: default;
}
</style>
