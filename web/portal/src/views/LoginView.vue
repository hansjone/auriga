<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import {
  ProviderSelect,
  StepContainer,
  type ClientStep,
  type LoginProvider,
  type LoginTransaction,
  type StepInput,
  isApiError,
  mapProblemMessage,
} from '@auriga/shared';
import { iamApi } from '../api';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();

const providers = ref<LoginProvider[]>([]);
const tx = ref<LoginTransaction | null>(null);
const error = ref('');
const loading = ref(true);

onMounted(async () => {
  try {
    const res = await iamApi.listLoginProviders();
    providers.value = res.items;
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  } finally {
    loading.value = false;
  }
});

async function selectProvider(provider: LoginProvider) {
  error.value = '';
  loading.value = true;
  try {
    const returnTo =
      typeof route.query.returnTo === 'string' ? route.query.returnTo : '/';
    const invitationCode =
      typeof route.query.invitation === 'string'
        ? route.query.invitation
        : undefined;
    tx.value = await iamApi.createLoginTransaction({
      providerId: provider.id,
      purpose: 'login',
      returnTo,
      invitationCode,
    });
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  } finally {
    loading.value = false;
  }
}

async function onSubmit(input: StepInput) {
  if (!tx.value) return;
  error.value = '';
  try {
    tx.value = await iamApi.advanceLoginTransaction(tx.value.id, input);
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  }
}

async function onRetry() {
  if (!tx.value) return;
  tx.value = await iamApi.advanceLoginTransaction(tx.value.id, {
    type: 'form',
    formId: 'retry',
    values: {},
  });
}

function onComplete(redirectTo: string) {
  void router.replace(redirectTo || '/');
}

const step = (): ClientStep | null =>
  (tx.value?.step as ClientStep | undefined) ?? null;
</script>

<template>
  <section class="login-card auriga-glass">
    <p class="brand">{{ t('brand') }}</p>
    <p v-if="loading" class="muted">{{ t('login.loading') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <ProviderSelect
      v-if="!tx && !loading"
      :providers="providers"
      @select="selectProvider"
    />
    <StepContainer
      v-else-if="step()"
      :step="step()!"
      @submit="onSubmit"
      @retry="onRetry"
      @complete="onComplete"
    />
  </section>
</template>

<style scoped>
.login-card {
  width: min(380px, 100%);
  padding: 1.75rem;
}
.brand {
  letter-spacing: 0.2em;
  text-transform: uppercase;
  font-size: 0.75rem;
  margin: 0 0 1.25rem;
  text-align: center;
}
.muted {
  color: var(--auriga-muted);
  text-align: center;
}
.error {
  color: #d27a7a;
  text-align: center;
}
</style>
