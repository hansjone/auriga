<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import {
  BrandMark,
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
      typeof route.query.returnTo === 'string'
        ? route.query.returnTo
        : '/overview';
    tx.value = await iamApi.createLoginTransaction({
      providerId: provider.id,
      purpose: 'login',
      returnTo,
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
  void router.replace(redirectTo || '/overview');
}

function backToProviders() {
  error.value = '';
  tx.value = null;
}

const step = (): ClientStep | null =>
  (tx.value?.step as ClientStep | undefined) ?? null;
</script>

<template>
  <section class="auriga-login">
    <header class="auriga-login-head">
      <BrandMark :size="40" />
      <div>
        <p class="auriga-login-name">
          {{ t('brand') }}
          <small class="auriga-login-sub">{{ t('admin') }}</small>
        </p>
      </div>
    </header>
    <p v-if="error" class="auriga-login-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="auriga-login-status" role="status">
      {{ t('login.loading') }}
    </p>
    <ProviderSelect
      v-else-if="!tx"
      :providers="providers"
      @select="selectProvider"
    />
    <template v-else-if="step()">
      <StepContainer
        :step="step()!"
        @submit="onSubmit"
        @retry="onRetry"
        @complete="onComplete"
      />
      <button
        v-if="providers.length > 1 && step()!.type !== 'complete'"
        type="button"
        class="auriga-login-back"
        @click="backToProviders"
      >
        {{ t('login.back') }}
      </button>
    </template>
  </section>
</template>
