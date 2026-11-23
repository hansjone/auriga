<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import {
  BrandMark,
  StepContainer,
  pickLocalized,
  type ClientStep,
  type LoginProvider,
  type LoginTransaction,
  type StepInput,
  isApiError,
  mapProblemMessage,
} from '@auriga/shared';
import { iamApi } from '../api';
import { preferredLoginProvider } from '../../../shared/src/login-preference';
import { createUacLoginView } from '../../../shared/src/native-login/uac';
import LoginMethods from '../../../shared/src/LoginMethods.vue';
import LoginTopBar from '../../../shared/src/LoginTopBar.vue';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();

const providers = ref<LoginProvider[]>([]);
const selected = ref<LoginProvider | null>(null);
const tx = ref<LoginTransaction | null>(null);
const error = ref('');
const loading = ref(true);
const busy = ref(false);
const uacRoot = ref<globalThis.HTMLElement>();
const uacActive = ref(false);
let uacView: ReturnType<typeof createUacLoginView> | undefined;
let generation = 0;
watch(locale, (value) => uacView?.setLocale(value));
onUnmounted(() => {
  generation++;
  void uacView?.dispose();
});

const heading = computed(() => {
  const kind = selected.value?.connectorType;
  const en = locale.value === 'en';
  if (kind === 'uac') return en ? 'Scan to sign in' : '扫码登录';
  if (kind === 'team-account') return en ? 'Team account' : '团队账号';
  if (kind === 'emergency') return en ? 'Emergency account' : '应急账号';
  return selected.value
    ? pickLocalized(selected.value.displayName, locale.value)
    : en
      ? 'Sign in to admin console'
      : '登录管理台';
});

function describe(e: unknown) {
  return isApiError(e) ? mapProblemMessage(e.problem, t) : t('errors.unknown');
}

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
    const initial =
      preferredLoginProvider(res.items, document) ??
      res.items.find((p) => p.connectorType !== 'emergency') ??
      res.items[0];
    if (initial) await selectProvider(initial);
  } catch (e) {
    error.value = describe(e);
  } finally {
    loading.value = false;
  }
});

async function selectProvider(provider: LoginProvider) {
  if (busy.value) return;
  if (selected.value?.id === provider.id && (uacActive.value || tx.value))
    return;
  const version = ++generation;
  selected.value = provider;
  error.value = '';
  tx.value = null;
  busy.value = true;
  try {
    await stopUac();
    if (provider.connectorType === 'uac') {
      // UAC scan uses the dedicated challenge API; the generic login
      // transaction endpoint rejects UAC with validation_failed.
      uacActive.value = true;
      loading.value = false;
      await nextTick();
      if (version !== generation || !uacRoot.value) return;
      uacView = createUacLoginView(uacRoot.value, {
        api: iamApi,
        providerId: provider.id,
        targetName: provider.displayName,
        locale: locale.value,
        onComplete: (result) => {
          if (version !== generation) return;
          if (result.state === 'authenticated') {
            void stopUac();
            void router.replace(returnTo());
          } else if (result.state === 'additional_step_required') {
            void iamApi
              .getLoginTransaction(result.transactionId)
              .then(async (value) => {
                if (version !== generation) return;
                await stopUac();
                tx.value = value;
              })
              .catch((e) => {
                if (version === generation) error.value = describe(e);
              });
          }
        },
      });
      return;
    }
    const value = await iamApi.createLoginTransaction({
      providerId: provider.id,
      purpose: 'login',
      returnTo: returnTo(),
    });
    if (version === generation) tx.value = value;
  } catch (e) {
    if (version === generation) error.value = describe(e);
  } finally {
    if (version === generation) busy.value = false;
  }
}

async function onSubmit(input: StepInput) {
  if (!tx.value || busy.value) return;
  error.value = '';
  busy.value = true;
  try {
    tx.value = await iamApi.advanceLoginTransaction(tx.value.id, input);
  } catch (e) {
    error.value = describe(e);
  } finally {
    busy.value = false;
  }
}

async function onRetry() {
  const provider = selected.value;
  if (!provider) return;
  tx.value = null;
  selected.value = null;
  await selectProvider(provider);
}

function onComplete(redirectTo: string) {
  void router.replace(redirectTo || '/overview');
}

const step = (): ClientStep | null =>
  (tx.value?.step as ClientStep | undefined) ?? null;
</script>

<template>
  <LoginTopBar :show-back="false" />
  <section
    class="auriga-login login-panel"
    :class="{ 'auriga-qr-login': uacActive }"
  >
    <header class="qr-heading">
      <BrandMark :size="24" />
      <h1>{{ heading }}</h1>
    </header>
    <div class="login-content">
      <p v-if="error" class="auriga-login-error" role="alert">{{ error }}</p>
      <p v-if="loading && !uacActive" class="auriga-login-status" role="status">
        {{ t('login.loading') }}
      </p>
      <div v-show="uacActive" ref="uacRoot" />
      <fieldset v-if="step()" class="auriga-login-step" :disabled="busy">
        <StepContainer
          :key="tx!.id"
          :step="step()!"
          @submit="onSubmit"
          @retry="onRetry"
          @complete="onComplete"
        />
      </fieldset>
      <p v-if="!loading && !providers.length" class="login-idle" role="status">
        {{
          locale === 'en'
            ? 'No sign-in methods available. Contact your administrator.'
            : '暂无可用登录方式，请联系管理员。'
        }}
      </p>
    </div>
    <LoginMethods
      :providers="providers"
      :selected-id="selected?.id"
      :disabled="busy || loading"
      @select="selectProvider"
    />
  </section>
</template>

<style scoped>
.login-panel {
  width: min(384px, 100%);
  min-height: 0;
  padding: 28px 28px 20px;
  border-radius: 20px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0;
}
.login-panel.auriga-qr-login {
  min-height: 420px;
}
.qr-heading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-bottom: 16px;
  flex-shrink: 0;
}
.qr-heading h1 {
  font-size: 18px;
  line-height: 24px;
  font-weight: 500;
  margin: 0;
  color: var(--auriga-text);
}
.login-content {
  width: 100%;
}
.auriga-qr-login :deep([data-testid='uac-target']),
.auriga-qr-login :deep(.uac-cancel) {
  display: none;
}
.auriga-login-step {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.login-idle {
  text-align: center;
  color: var(--auriga-muted);
  font-size: 13px;
  padding: 20px 0 8px;
}
.login-panel :deep(.login-methods) {
  margin-top: 16px;
}
@media (max-width: 390px) {
  .login-panel {
    padding-inline: 24px;
  }
}
</style>
