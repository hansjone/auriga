<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import {
  isApiError,
  mapProblemMessage,
  type QrChallenge,
} from '@auriga/shared';
import { iamApi } from '../api';

const { t } = useI18n();
const route = useRoute();
const challenge = ref<QrChallenge | null>(null);
const matchCode = ref('');
const error = ref('');
const done = ref<'confirmed' | 'rejected' | ''>('');
const loading = ref(true);

onMounted(async () => {
  try {
    challenge.value = await iamApi.getQrChallenge(
      String(route.params.challengeId || ''),
    );
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  } finally {
    loading.value = false;
  }
});

async function confirm() {
  error.value = '';
  try {
    await iamApi.confirmQrChallenge(
      String(route.params.challengeId || ''),
      matchCode.value,
    );
    done.value = 'confirmed';
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  }
}

async function reject() {
  error.value = '';
  try {
    await iamApi.rejectQrChallenge(String(route.params.challengeId || ''));
    done.value = 'rejected';
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  }
}
</script>

<template>
  <section class="qr-card auriga-glass">
    <h1>{{ t('qrConfirm.title') }}</h1>
    <p v-if="loading" class="muted">{{ t('login.loading') }}</p>
    <template v-else-if="challenge && !done">
      <p class="muted">
        {{ t('qrConfirm.initiator', { ip: challenge.initiator.ip }) }}
      </p>
      <p class="hint">{{ t('qrConfirm.hint') }}</p>
      <label class="field">
        <span>{{ t('qrConfirm.matchCode') }}</span>
        <input
          v-model="matchCode"
          maxlength="2"
          pattern="[0-9]{2}"
          inputmode="numeric"
          autocomplete="one-time-code"
        />
      </label>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <div class="actions">
        <button type="button" class="primary" @click="confirm">
          {{ t('qrConfirm.confirm') }}
        </button>
        <button type="button" class="ghost" @click="reject">
          {{ t('qrConfirm.reject') }}
        </button>
      </div>
    </template>
    <p v-else-if="done === 'confirmed'" class="ok">{{ t('qrConfirm.done') }}</p>
    <p v-else-if="done === 'rejected'">{{ t('qrConfirm.rejected') }}</p>
    <p v-else-if="error" class="error">{{ error }}</p>
  </section>
</template>

<style scoped>
.qr-card {
  width: min(380px, 100%);
  text-align: center;
}
.muted,
.hint {
  color: var(--auriga-muted);
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  text-align: left;
  margin: 1rem 0;
}
.field input {
  height: 2.75rem;
  text-align: center;
  letter-spacing: 0.3em;
  font-size: 1.25rem;
  border-radius: 13px;
  border: 1px solid var(--auriga-border);
  background: rgba(8, 18, 28, 0.45);
  color: var(--auriga-text);
}
.actions {
  display: flex;
  gap: 0.75rem;
  justify-content: center;
}
.primary,
.ghost {
  height: 2.75rem;
  padding: 0 1.1rem;
  border-radius: 14px;
  font: inherit;
  cursor: pointer;
}
.primary {
  border: 0;
  background: var(--auriga-accent);
  color: #dbe4ee;
}
.ghost {
  border: 1px solid var(--auriga-border);
  background: transparent;
  color: var(--auriga-text);
}
.error {
  color: #d27a7a;
}
.ok {
  color: #5aa58a;
}
</style>
