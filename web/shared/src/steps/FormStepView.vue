<script setup lang="ts">
import { reactive, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import type { FormStep, StepInput } from '../generated/steps';
import { pickLocalized } from '../locale';
import { mapProblemMessage } from '../api/problem';

const props = defineProps<{ step: FormStep }>();
const emit = defineEmits<{ submit: [input: StepInput] }>();
const { t, locale } = useI18n();

const values = reactive<Record<string, string | boolean>>({});

watch(
  () => props.step,
  (step) => {
    for (const key of Object.keys(values)) delete values[key];
    for (const field of step.fields) {
      if (field.kind === 'checkbox') {
        values[field.name] = field.value === true;
      } else if (field.kind !== 'info') {
        values[field.name] = typeof field.value === 'string' ? field.value : '';
      }
    }
  },
  { immediate: true },
);

function label(text: FormStep['title']) {
  return pickLocalized(text, locale.value);
}

function onSubmit(e: { preventDefault: () => void }) {
  e.preventDefault();
  const payload: Record<string, string | boolean> = {};
  for (const field of props.step.fields) {
    if (field.kind === 'info') continue;
    if (field.kind === 'hidden') {
      payload[field.name] =
        typeof field.value === 'boolean' || typeof field.value === 'string'
          ? field.value
          : '';
      continue;
    }
    payload[field.name] = values[field.name] ?? '';
  }
  emit('submit', {
    type: 'form',
    formId: props.step.formId,
    values: payload,
  });
}
</script>

<template>
  <form class="form-step" data-testid="form-step" @submit="onSubmit">
    <h2 v-if="step.title" class="title">{{ label(step.title) }}</h2>
    <p v-if="step.description" class="desc">{{ label(step.description) }}</p>
    <p v-if="step.error" class="error" role="alert">
      {{
        step.error.message
          ? label(step.error.message)
          : mapProblemMessage(
              { title: step.error.code, status: 400, code: step.error.code },
              t,
            )
      }}
    </p>
    <template v-for="field in step.fields" :key="field.name">
      <p v-if="field.kind === 'info'" class="info">
        {{ label(field.label) || label(field.help) }}
      </p>
      <input
        v-else-if="field.kind === 'hidden'"
        type="hidden"
        :name="field.name"
        :value="String(field.value ?? '')"
      />
      <label v-else-if="field.kind === 'checkbox'" class="check">
        <input
          v-model="values[field.name]"
          type="checkbox"
          :name="field.name"
        />
        <span>{{ label(field.label) }}</span>
      </label>
      <label v-else-if="field.kind === 'select'" class="field">
        <span v-if="field.label">{{ label(field.label) }}</span>
        <select
          v-model="values[field.name]"
          :name="field.name"
          :required="field.required"
        >
          <option
            v-for="opt in field.options || []"
            :key="opt.value"
            :value="opt.value"
          >
            {{ label(opt.label) }}
          </option>
        </select>
      </label>
      <label v-else class="field">
        <span v-if="field.label">{{ label(field.label) }}</span>
        <input
          v-model="values[field.name]"
          :type="
            field.kind === 'password'
              ? 'password'
              : field.kind === 'email'
                ? 'email'
                : field.kind === 'tel'
                  ? 'tel'
                  : 'text'
          "
          :name="field.name"
          :placeholder="label(field.placeholder)"
          :required="field.required"
          :autocomplete="field.autocomplete"
          :pattern="field.pattern"
          :minlength="field.minLength"
          :maxlength="field.maxLength"
          :inputmode="field.kind === 'otp' ? 'numeric' : undefined"
          :class="{ otp: field.kind === 'otp' }"
        />
        <small v-if="field.help" class="help">{{ label(field.help) }}</small>
        <small v-if="step.fieldErrors?.[field.name]" class="error" role="alert">
          {{
            step.fieldErrors[field.name]?.message
              ? label(step.fieldErrors[field.name]!.message)
              : step.fieldErrors[field.name]?.code
          }}
        </small>
      </label>
    </template>
    <button type="submit" class="primary">
      {{ label(step.submitLabel) || t('steps.submit') }}
    </button>
  </form>
</template>

<style scoped>
.form-step {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.title {
  margin: 0;
  font-size: 1.125rem;
  font-weight: 600;
  text-align: center;
  letter-spacing: -0.01em;
}
.desc,
.info,
.help {
  margin: 0;
  color: var(--auriga-muted);
  font-size: 0.8125rem;
  line-height: 1.6;
}
.desc {
  margin-top: -0.5rem;
  text-align: center;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  font-size: 0.8125rem;
  color: var(--auriga-muted);
}
.field input,
.field select {
  box-sizing: border-box;
  width: 100%;
  height: 2.75rem;
  padding: 0 0.875rem;
  border-radius: 12px;
  border: 1px solid var(--auriga-border);
  background: var(--auriga-field-bg, rgba(4, 12, 20, 0.5));
  color: var(--auriga-text);
  font: inherit;
  font-size: 0.9375rem;
  outline: none;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease;
}
.field input::placeholder {
  color: var(--auriga-muted);
  opacity: 0.7;
}
.field input:hover,
.field select:hover {
  border-color: var(--auriga-border-strong, rgba(150, 182, 208, 0.32));
}
.field input:focus,
.field select:focus {
  border-color: var(--auriga-accent-line, #5b93bd);
}
.field input.otp {
  letter-spacing: 0.4em;
  text-align: center;
  font-size: 1.125rem;
  font-variant-numeric: tabular-nums;
}
.check {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.875rem;
}
.error {
  color: var(--auriga-danger, #e08c8c);
  font-size: 0.8125rem;
  margin: 0;
}
p.error {
  padding: 0.625rem 0.75rem;
  border-radius: 10px;
  background: var(--auriga-danger-soft, rgba(224, 140, 140, 0.1));
  text-align: center;
}
.primary {
  margin-top: 0.25rem;
  height: 2.75rem;
  border: 0;
  border-radius: 12px;
  background: var(--auriga-accent);
  color: var(--auriga-on-accent, #f1f6fa);
  font: inherit;
  font-size: 0.9375rem;
  font-weight: 600;
  cursor: pointer;
  transition: filter 0.15s ease;
}
.primary:hover {
  filter: brightness(1.08);
}
.primary:active {
  filter: brightness(0.95);
}
.primary:focus-visible {
  outline: 2px solid var(--auriga-accent-line, #5b93bd);
  outline-offset: 2px;
}
</style>
