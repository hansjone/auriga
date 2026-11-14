<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import {
  createUacLoginView,
  type UacPageEvent,
} from '../../../shared/src/native-login/uac';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import {
  BrandMark,
  StepContainer,
  type ClientStep,
  type LoginProvider,
  type LoginTransaction,
  type StepInput,
  isApiError,
} from '@auriga/shared';
import { iamApi as portalIamApi } from '../api';
import type { IamApi } from '@auriga/shared';
import {
  navigationCorrelationId,
  navigationProviderId,
  takeNavigationContext,
} from '../../../shared/src/api/correlation';
import PortalProviders from '../components/PortalProviders.vue';
import { pickLocalized } from '@auriga/shared';
import { loginReturnTo } from '../login-return';

const { t, locale } = useI18n();
const props = defineProps<{ deviceContinuation?: boolean; authApi?: IamApi }>();
const iamApi = props.authApi ?? portalIamApi;
const emit = defineEmits<{ authEvent: [event: UacPageEvent]; signedIn: [] }>();
const route = useRoute(),
  router = useRouter();
const providers = ref<LoginProvider[]>([]),
  tx = ref<LoginTransaction | null>(null);
const selectedProvider = ref<LoginProvider | null>(null);
const heading = computed(() => {
  const kind = selectedProvider.value?.connectorType;
  if (kind === 'uac')
    return locale.value === 'en' ? 'Scan to sign in' : '扫码登录';
  if (kind === 'team-account')
    return locale.value === 'en' ? 'Team account' : '团队账号';
  if (kind === 'emergency')
    return locale.value === 'en' ? 'Emergency account' : '应急账号';
  return selectedProvider.value
    ? pickLocalized(selectedProvider.value.displayName, locale.value)
    : locale.value === 'en'
      ? 'Sign in to your workspace'
      : '登录团队工作台';
});
const error = ref(''),
  loading = ref(true),
  busy = ref(false);
const uacRoot = ref<globalThis.HTMLElement>(),
  uacActive = ref(false);
let uacView: ReturnType<typeof createUacLoginView> | undefined;
watch(locale, (value) => uacView?.setLocale(value));
let alive = true,
  generation = 0;
let uacContinuation = false;
const current = (version: number) => alive && generation === version;
function describe(error: unknown) {
  const code = isApiError(error) ? error.problem.code : '';
  const known = [
    'iam.invalid_credentials',
    'common.forbidden',
    'common.unauthenticated',
    'common.validation_failed',
    'common.rate_limited',
    'common.unavailable',
  ];
  const key = known.includes(code) ? 'errors.' + code : 'errors.unknown';
  const message = t(key);
  // Never render upstream problem detail/title, which may contain secrets.
  return message === key ? t('errors.unknown') : message;
}
async function cancelTx(value: LoginTransaction | null) {
  if (value?.purpose === 'login' && value.status === 'pending')
    await iamApi.cancelLoginTransaction(value.id);
}
function leaveDocument() {
  if (!alive) return;
  alive = false;
  generation++;
  void uacView?.dispose();
  void cancelTx(tx.value).catch(() => {});
}
function restoreDocument(event: globalThis.PageTransitionEvent) {
  if (event.persisted && !props.deviceContinuation)
    globalThis.location.reload();
}
onUnmounted(() => {
  leaveDocument();
  globalThis.removeEventListener('pagehide', leaveDocument);
  globalThis.removeEventListener('pageshow', restoreDocument);
});
onMounted(async () => {
  globalThis.addEventListener('pagehide', leaveDocument);
  globalThis.addEventListener('pageshow', restoreDocument);
  const version = generation;
  try {
    const res = await iamApi.listLoginProviders();
    if (!current(version)) return;
    providers.value = res.items;
    const preferred = res.items.find(
      (p) =>
        p.connectorType === 'uac' && p.id === navigationProviderId(document),
    );
    const initial =
      (preferred && document.getElementById('auriga-uac-navigation')
        ? preferred
        : undefined) ??
      res.items.find((p) => p.connectorType === 'uac') ??
      res.items.find((p) => p.connectorType !== 'emergency') ??
      res.items[0];
    if (initial) await selectProvider(initial);
  } catch (e) {
    if (current(version)) error.value = describe(e);
  } finally {
    if (alive) loading.value = false;
  }
});
function validate(value: LoginTransaction) {
  if (value.purpose !== 'login') throw new Error('Invalid login purpose');
  return value;
}
async function selectProvider(provider: LoginProvider, restart = false) {
  if (!alive || busy.value) return;
  if (
    !restart &&
    selectedProvider.value?.id === provider.id &&
    (uacActive.value || tx.value)
  )
    return;
  selectedProvider.value = provider;
  const version = ++generation;
  busy.value = true;
  loading.value = true;
  error.value = '';
  const previous = tx.value;
  tx.value = null;
  const oldView = uacView;
  uacView = undefined;
  uacActive.value = false;
  try {
    await oldView?.dispose();
    await cancelTx(previous);
    if (!current(version)) return;
    if (provider.connectorType === 'uac' && uacRoot.value) {
      uacContinuation = true;
      uacActive.value = true;
      const navigation = takeNavigationContext(document, provider.id);
      uacView = createUacLoginView(uacRoot.value, {
        api: iamApi,
        providerId: provider.id,
        targetName: provider.displayName,
        navigation,
        initialCorrelationId: navigationCorrelationId(document),
        locale: locale.value,
        onEvent: (event) => emit('authEvent', event),
        invitationCode:
          typeof route.query.invitation === 'string'
            ? route.query.invitation
            : undefined,
        onComplete: (result) => {
          if (!current(version)) return;
          if (result.state === 'authenticated') finishLogin();
          else if (result.state === 'additional_step_required') {
            busy.value = true;
            void iamApi
              .getLoginTransaction(result.transactionId)
              .then((value) => {
                if (!current(version)) return;
                tx.value = validate(value);
                uacActive.value = false;
                void uacView?.dispose();
              })
              .catch((e) => {
                if (current(version)) error.value = describe(e);
              })
              .finally(() => {
                if (current(version)) busy.value = false;
              });
          }
        },
      });
      return;
    }
    uacActive.value = false;
    uacContinuation = false;
    const value = await iamApi.createLoginTransaction({
      providerId: provider.id,
      purpose: 'login',
      returnTo: props.deviceContinuation
        ? route.fullPath
        : typeof route.query.returnTo === 'string'
          ? route.query.returnTo
          : '/',
      invitationCode:
        typeof route.query.invitation === 'string'
          ? route.query.invitation
          : undefined,
    });
    if (!current(version)) {
      await cancelTx(value);
      return;
    }
    tx.value = validate(value);
  } catch (e) {
    if (current(version)) error.value = describe(e);
  } finally {
    if (current(version)) {
      busy.value = false;
      loading.value = false;
    }
  }
}
async function onSubmit(input: StepInput) {
  if (!tx.value || busy.value || !alive) return;
  const version = generation,
    value = tx.value;
  busy.value = true;
  error.value = '';
  try {
    const result = await iamApi.advanceLoginTransaction(value.id, input);
    if (current(version)) tx.value = validate(result);
  } catch (e) {
    if (current(version)) error.value = describe(e);
  } finally {
    if (current(version)) busy.value = false;
  }
}
async function onRetry() {
  if (!tx.value) return;
  const provider = providers.value.find((p) => p.id === tx.value?.provider.id);
  if (provider) await selectProvider(provider, true);
}
function onComplete(redirectTo: string) {
  if (alive && props.deviceContinuation) {
    emit('signedIn');
    return;
  }
  if (alive && uacContinuation) {
    finishLogin();
    return;
  }
  if (
    alive &&
    redirectTo.startsWith('/') &&
    !redirectTo.startsWith('//') &&
    !redirectTo.includes('\\')
  )
    void router.replace(redirectTo || '/');
}
function finishLogin() {
  if (props.deviceContinuation) emit('signedIn');
  else
    globalThis.location.assign(
      loginReturnTo(route.query.returnTo) ?? '/instance-login.html',
    );
}
async function backToProviders() {
  if (!alive || busy.value) return;
  const version = ++generation,
    previous = tx.value,
    view = uacView;
  tx.value = null;
  uacContinuation = false;
  uacView = undefined;
  error.value = '';
  busy.value = true;
  try {
    await view?.dispose();
    await cancelTx(previous);
  } catch (e) {
    if (current(version)) error.value = describe(e);
  } finally {
    if (current(version)) {
      uacActive.value = false;
      busy.value = false;
    }
  }
}
const step = (): ClientStep | null =>
  (tx.value?.step as ClientStep | undefined) ?? null;
</script>

<template>
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
      <div v-if="!loading && !tx && !uacActive" class="login-idle">
        <button
          v-if="selectedProvider"
          class="restart"
          type="button"
          :disabled="busy"
          @click="selectProvider(selectedProvider)"
        >
          {{ locale === 'en' ? 'Continue sign-in' : '继续登录' }}
        </button>
        <p v-else role="status">
          {{
            locale === 'en'
              ? 'No sign-in methods available. Contact your administrator.'
              : '暂无可用登录方式，请联系管理员。'
          }}
        </p>
      </div>
    </div>
    <PortalProviders
      :providers="providers"
      :selected-id="selectedProvider?.id"
      :disabled="busy || loading"
      @select="selectProvider"
    />
    <button
      v-if="uacActive"
      type="button"
      class="auriga-login-back"
      :disabled="busy"
      @click="backToProviders"
    >
      {{ locale === 'en' ? 'Cancel' : '取消' }}
    </button>
    <a v-if="!deviceContinuation && !uacActive" class="home-link" href="/">{{
      locale === 'en' ? 'Back to home' : '返回首页'
    }}</a>
  </section>
</template>
<style scoped>
.login-panel {
  width: min(384px, 100%);
  min-height: 552px;
  padding: 32px;
  border-radius: 24px;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.qr-heading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-bottom: 24px;
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
  flex: 1;
}
.auriga-qr-login :deep([data-testid='uac-target']),
.auriga-qr-login :deep(.uac-cancel) {
  display: none;
}
.auriga-login-back,
.home-link {
  display: block;
  margin: 16px 0 0;
  padding: 8px;
  line-height: 16px;
  font-size: 12px;
  color: var(--auriga-muted);
  text-decoration: none;
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
  padding: 48px 0;
}
.restart {
  font: inherit;
  color: var(--auriga-text);
  background: transparent;
  border: 1px solid var(--auriga-border);
  border-radius: 8px;
  padding: 12px;
  cursor: pointer;
}
button:focus-visible,
a:focus-visible {
  outline: 2px solid var(--auriga-accent-line);
  outline-offset: 2px;
}
@media (max-width: 390px) {
  .login-panel {
    padding-inline: 24px;
  }
}
</style>
