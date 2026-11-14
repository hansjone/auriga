<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue';
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
import { loginReturnTo } from '../login-return';

const { t, locale } = useI18n();
const props = defineProps<{ deviceContinuation?: boolean; authApi?: IamApi }>();
const iamApi = props.authApi ?? portalIamApi;
const emit = defineEmits<{ authEvent: [event: UacPageEvent]; signedIn: [] }>();
const route = useRoute(),
  router = useRouter();
const providers = ref<LoginProvider[]>([]),
  tx = ref<LoginTransaction | null>(null);
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
    if (preferred && document.getElementById('auriga-uac-navigation'))
      await selectProvider(preferred);
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
async function selectProvider(provider: LoginProvider) {
  if (!alive || busy.value) return;
  const version = ++generation;
  busy.value = true;
  loading.value = true;
  error.value = '';
  const previous = tx.value;
  tx.value = null;
  const oldView = uacView;
  uacView = undefined;
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
  if (provider) await selectProvider(provider);
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
    <button
      v-if="uacActive"
      type="button"
      class="auriga-login-back"
      :disabled="busy"
      @click="backToProviders"
    >
      {{ t('login.back') }}
    </button>
    <PortalProviders
      v-if="!loading && !tx && !uacActive"
      :providers="providers"
      @select="selectProvider"
    />
    <template v-else-if="step()">
      <fieldset class="auriga-login-step" :disabled="busy">
        <StepContainer
          :step="step()!"
          @submit="onSubmit"
          @retry="onRetry"
          @complete="onComplete"
        />
      </fieldset>
      <button
        v-if="providers.length > 1 && step()!.type !== 'complete'"
        type="button"
        :disabled="busy"
        class="auriga-login-back"
        @click="backToProviders"
      >
        {{ t('login.back') }}
      </button>
    </template>
    <a v-if="!deviceContinuation" class="home-link" href="/">{{
      locale === 'en' ? 'Back to home' : '返回首页'
    }}</a>
  </section>
</template>

<style scoped>
.home-link {
  display: block;
  text-align: center;
  margin-top: 2rem;
  color: var(--auriga-muted);
  font-size: 13px;
  text-decoration: none;
}
.auriga-login-step {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
</style>
