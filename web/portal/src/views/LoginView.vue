<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { createUacLoginView } from '../../../shared/src/native-login/uac';
import type { IAMComponents } from '@auriga/shared';
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
import DesktopDownload from '../components/DesktopDownload.vue';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();

const providers = ref<LoginProvider[]>([]);
const tx = ref<LoginTransaction | null>(null);
const error = ref('');
const loading = ref(true);
const uacRoot = ref<globalThis.HTMLElement>();
const uacActive = ref(false);
let uacView: { dispose(): void } | undefined;
onUnmounted(() => uacView?.dispose());

onMounted(async () => {
  try {
    const res = await iamApi.listLoginProviders();
    providers.value = res.items;
    const preferred = res.items.find((p) => p.connectorType === 'uac');
    if (preferred && document.getElementById('auriga-uac-navigation'))
      await selectProvider(preferred);
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  } finally {
    loading.value = false;
  }
});

async function selectProvider(provider: LoginProvider) {
  if (provider.connectorType === 'uac' && uacRoot.value) {
    uacView?.dispose();
    uacActive.value = true;
    loading.value = false;
    let navigation:
      IAMComponents['schemas']['UacNavigationContext'] | undefined;
    const context = document.getElementById('auriga-uac-navigation');
    if (context) {
      try {
        navigation = JSON.parse(context.textContent ?? '');
      } catch {
        /* QR fallback */
      }
      context.remove();
    }
    uacView = createUacLoginView(uacRoot.value, {
      api: iamApi,
      providerId: provider.id,
      navigation,
      locale: document.documentElement.lang,
      invitationCode:
        typeof route.query.invitation === 'string'
          ? route.query.invitation
          : undefined,
      onComplete: (result) => {
        if (result.state === 'authenticated')
          globalThis.location.assign('/instance-login.html');
        else if (result.state === 'additional_step_required')
          void iamApi
            .getLoginTransaction(result.transactionId)
            .then((value) => {
              tx.value = value;
              uacActive.value = false;
              uacView?.dispose();
            })
            .catch(() => {
              error.value = t('errors.unknown');
            });
      },
    });
    return;
  }
  uacView?.dispose();
  uacActive.value = false;
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
        </p>
      </div>
    </header>
    <p v-if="error" class="auriga-login-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="auriga-login-status" role="status">
      {{ t('login.loading') }}
    </p>
    <div v-show="uacActive" ref="uacRoot" />
    <ProviderSelect
      v-if="!loading && !tx && !uacActive"
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
    <DesktopDownload />
  </section>
</template>
