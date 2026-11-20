<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
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
import { loginPreference } from '../../../shared/src/login-preference';
import { createUacLoginView } from '../../../shared/src/native-login/uac';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();

const providers = ref<LoginProvider[]>([]);
const tx = ref<LoginTransaction | null>(null);
const error = ref('');
const loading = ref(true);
const uacRoot = ref<globalThis.HTMLElement>();
const uacActive = ref(false);
let uacView: ReturnType<typeof createUacLoginView> | undefined;
watch(locale, (value) => uacView?.setLocale(value));
onUnmounted(() => void uacView?.dispose());

function returnTo() {
  return typeof route.query.returnTo === 'string'
    ? route.query.returnTo
    : '/overview';
}

async function stopUac() {
  const view = uacView;
  uacView = undefined;
  uacActive.value = false;
  await view?.dispose();
}

onMounted(async () => {
  try {
    const res = await iamApi.listLoginProviders();
    providers.value = res.items;
    if (loginPreference(document) === 'team') {
      const team = res.items.find(
        (provider) => provider.connectorType === 'team-account',
      );
      if (team) await selectProvider(team);
    }
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
  await stopUac();
  if (provider.connectorType === 'uac') {
    // UAC scan uses the dedicated challenge API; the generic login
    // transaction endpoint rejects UAC with validation_failed.
    tx.value = null;
    uacActive.value = true;
    loading.value = false;
    await nextTick();
    if (!uacRoot.value) return;
    uacView = createUacLoginView(uacRoot.value, {
      api: iamApi,
      providerId: provider.id,
      targetName: provider.displayName,
      locale: locale.value,
      onComplete: (result) => {
        if (result.state === 'authenticated') {
          void stopUac();
          void router.replace(returnTo());
        } else if (result.state === 'additional_step_required') {
          void iamApi
            .getLoginTransaction(result.transactionId)
            .then(async (value) => {
              await stopUac();
              tx.value = value;
            })
            .catch((e) => {
              error.value = isApiError(e)
                ? mapProblemMessage(e.problem, t)
                : t('errors.unknown');
            });
        }
      },
    });
    return;
  }
  loading.value = true;
  try {
    tx.value = await iamApi.createLoginTransaction({
      providerId: provider.id,
      purpose: 'login',
      returnTo: returnTo(),
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
  void stopUac();
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
    <template v-else-if="uacActive">
      <div ref="uacRoot" />
      <button
        v-if="providers.length > 1"
        type="button"
        class="auriga-login-back"
        @click="backToProviders"
      >
        {{ t('login.back') }}
      </button>
    </template>
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
