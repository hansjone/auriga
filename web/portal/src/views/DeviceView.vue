<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  createApiClient,
  createIamApi,
  isApiError,
  StepContainer,
  type ClientStep,
  type StepInput,
  type LoginTransaction,
  type Me,
  type IAMComponents,
} from '@auriga/shared';
import { startRedirectStepUp } from '../../../shared/src/steps/redirectStepUp';
import { navigationCorrelationId } from '../../../shared/src/api/correlation';
import { loginInfoText } from '../../../shared/src/native-login/login-info';
import LoginView from './LoginView.vue';

const route = useRoute(),
  { locale } = useI18n();
const words = computed(() =>
  locale.value.startsWith('en')
    ? {
        title: 'Confirm this device',
        code: 'Device code',
        enter: 'Enter the code shown on your device',
        open: 'Continue',
        verify:
          'Compare the code and type it again. Approve only a device you started yourself.',
        approve: 'Approve device',
        deny: 'Reject device',
        expired: 'This device request expired or is unavailable.',
        failed: 'The request could not be confirmed. Refresh and try again.',
        approved: 'Device approved. Return to your desktop.',
        denied: 'Device request rejected.',
        checking: 'Checking device request',
        login: 'Sign in to continue',
        stepup: 'Additional verification is required',
        popup: 'Open verification',
        cancel: 'Cancel verification',
        changed: 'Your sign-in session changed. Refresh this page.',
        scopes: 'Requested permissions',
        expires: 'Expires',
        ip: 'Requesting IP',
      }
    : {
        title: '确认登录设备',
        code: '设备用户码',
        enter: '输入当前设备显示的用户码',
        open: '继续',
        verify:
          '请与发起设备逐字核对，并重新输入用户码。只批准由本人发起的设备。',
        approve: '确认批准设备',
        deny: '拒绝设备',
        expired: '设备请求已过期或不可用。',
        failed: '请求结果尚未确认，请刷新页面后重试。',
        approved: '设备已批准，请返回桌面客户端。',
        denied: '已拒绝设备请求。',
        checking: '正在读取设备请求',
        login: '请先登录再继续',
        stepup: '请完成额外验证',
        popup: '打开验证页面',
        cancel: '取消验证',
        changed: '登录身份已变化，请刷新本页。',
        scopes: '请求的权限',
        expires: '有效期',
        ip: '发起端 IP',
      },
);
const normalize = (value: string) =>
  value.trim().toUpperCase().replaceAll('-', '');
function openCode() {
  if (valid(normalize(entered.value)))
    globalThis.location.assign('/device/' + normalize(entered.value));
}
const valid = (value: string) => /^[BCDFGHJKLMNPQRSTVWXZ]{8}$/.test(value);
const code = normalize(
  typeof route.params.userCode === 'string' ? route.params.userCode : '',
);
const typed = ref('');
const entered = ref(
  typeof route.query.user_code === 'string' ? route.query.user_code : '',
);
const info = ref<IAMComponents['schemas']['DeviceRequest']>();
const me = ref<Me>();
const status = ref<
  | 'checking'
  | 'login'
  | 'ready'
  | 'expired'
  | 'failed'
  | 'approved'
  | 'denied'
  | 'changed'
  | 'stepup'
>(valid(code) ? 'checking' : 'ready');
const busy = ref(false),
  tx = ref<LoginTransaction>(),
  popupActive = ref(false);
let alive = true,
  version = 0,
  pendingApproval = false,
  retried = false;
let owner: { account: string; session: string; tenant: string } | undefined;
let expiry: ReturnType<typeof setTimeout> | undefined;
let popup: ReturnType<typeof startRedirectStepUp> | undefined;
let popupReturn = '';
const correlation = ref(navigationCorrelationId(document));
const api = createIamApi(
  createApiClient({
    getLocale: () => locale.value,
    fetch: (input, init) => {
      const url = new URL(String(input), globalThis.location.href);
      if (url.origin !== globalThis.location.origin)
        return Promise.reject(new Error('Same origin required'));
      return globalThis.fetch(input, {
        ...init,
        credentials: 'same-origin',
        redirect: 'error',
      });
    },
  }),
);
const current = (v: number) => alive && version === v;
const step = computed(() => tx.value?.step as ClientStep | undefined);
const confirmed = computed(
  () =>
    valid(code) &&
    normalize(typed.value) === code &&
    status.value === 'ready' &&
    !busy.value,
);
function identify(value: Me) {
  return (
    value.account.status === 'active' &&
    value.session.current &&
    value.session.audience === 'portal'
  );
}
function sameOwner(value: Me) {
  return (
    identify(value) &&
    owner?.account === value.account.id &&
    owner.session === value.session.id &&
    owner.tenant === value.account.tenantId
  );
}
function stopVerification() {
  popup?.close();
  popup = undefined;
  popupActive.value = false;
  pendingApproval = false;
  tx.value = undefined;
}
function expired() {
  version++;
  globalThis.clearTimeout(expiry);
  stopVerification();
  info.value = undefined;
  typed.value = '';
  status.value = 'expired';
  busy.value = false;
}
function handle(error: unknown) {
  if (isApiError(error) && [404, 409, 410].includes(error.status)) expired();
  else if (isApiError(error) && error.status === 401) {
    me.value = undefined;
    info.value = undefined;
    typed.value = '';
    status.value = 'login';
  } else status.value = 'failed';
}
async function load() {
  if (!valid(code) || !alive || busy.value) return;
  const v = ++version;
  busy.value = true;
  globalThis.clearTimeout(expiry);
  try {
    const identity = await api.getMe();
    if (!current(v)) return;
    if (!identify(identity)) throw new Error('Portal session required');
    const request = await api.getDeviceRequest(code);
    if (!current(v)) return;
    const remaining = Date.parse(request.expiresAt) - Date.now();
    if (
      normalize(request.userCode) !== code ||
      !Number.isFinite(remaining) ||
      remaining <= 0
    ) {
      expired();
      return;
    }
    me.value = identity;
    info.value = request;
    owner = {
      account: identity.account.id,
      session: identity.session.id,
      tenant: identity.account.tenantId,
    };
    status.value = 'ready';
    expiry = setTimeout(expired, Math.min(remaining, 2147483647));
  } catch (error) {
    if (current(v)) handle(error);
  } finally {
    if (current(v)) busy.value = false;
  }
}
async function authorize(approve: boolean, retry = false) {
  if (!alive || busy.value || !info.value || (approve && !confirmed.value))
    return;
  const v = version;
  busy.value = true;
  try {
    const identity = await api.getMe();
    if (!current(v)) return;
    if (!sameOwner(identity)) {
      stopVerification();
      info.value = undefined;
      typed.value = '';
      status.value = 'changed';
      return;
    }
    if (Date.parse(info.value.expiresAt) <= Date.now()) {
      expired();
      return;
    }
    if (approve) await api.approveDeviceRequest(code);
    else await api.denyDeviceRequest(code);
    if (!current(v)) return;
    globalThis.clearTimeout(expiry);
    stopVerification();
    info.value = undefined;
    typed.value = '';
    status.value = approve ? 'approved' : 'denied';
  } catch (error) {
    if (!current(v)) return;
    if (
      approve &&
      !retry &&
      isApiError(error) &&
      error.status === 403 &&
      error.problem.code === 'iam.step_up_required' &&
      me.value
    ) {
      pendingApproval = true;
      retried = false;
      const nonce = globalThis.crypto.randomUUID();
      popupReturn = '/device?aurigaStepUp=' + nonce;
      try {
        await accept(
          await api.createLoginTransaction({
            providerId: me.value.session.providerId,
            purpose: 'stepup',
            returnTo: popupReturn,
          }),
          v,
        );
      } catch (failure) {
        if (current(v)) {
          pendingApproval = false;
          handle(failure);
        }
      }
    } else handle(error);
  } finally {
    if (current(v)) busy.value = false;
  }
}
async function accept(value: LoginTransaction, v: number) {
  if (!current(v)) return;
  if (
    value.purpose !== 'stepup' ||
    !['pending', 'completed'].includes(value.status) ||
    value.provider.id !== me.value?.session.providerId
  )
    throw new Error('Invalid verification');
  tx.value = value;
  status.value = 'stepup';
  if (value.status === 'completed' && value.step.type === 'complete')
    await completed(v);
}
async function completed(v = version) {
  if (!current(v) || !pendingApproval || retried) return;
  retried = true;
  tx.value = undefined;
  pendingApproval = false;
  status.value = 'ready';
  busy.value = false;
  await authorize(true, true);
}
async function submit(input: StepInput) {
  if (!tx.value || busy.value) return;
  const v = version;
  busy.value = true;
  try {
    await accept(await api.advanceLoginTransaction(tx.value.id, input), v);
  } catch (error) {
    if (current(v)) handle(error);
  } finally {
    if (current(v)) busy.value = false;
  }
}
function openPopup() {
  if (!tx.value || busy.value || popupActive.value) return;
  const v = version;
  try {
    popup = startRedirectStepUp(tx.value, popupReturn, (id) =>
      api.getLoginTransaction(id),
    );
    popupActive.value = true;
    void popup.done
      .then(async (value) => {
        if (current(v)) {
          if (value) await accept(value, v);
          else await completed(v);
        }
      })
      .catch(() => {
        if (current(v)) {
          pendingApproval = false;
          status.value = 'failed';
        }
      })
      .finally(() => {
        if (current(v)) {
          popupActive.value = false;
          popup = undefined;
        }
      });
  } catch {
    status.value = 'failed';
    pendingApproval = false;
  }
}
function cancelVerification() {
  const previous = tx.value;
  version++;
  stopVerification();
  status.value = 'ready';
  busy.value = false;
  if (previous?.status === 'pending')
    void api.cancelLoginTransaction(previous.id).catch(() => {});
}
function leaveDocument() {
  if (!alive) return;
  alive = false;
  version++;
  globalThis.clearTimeout(expiry);
  const previous = tx.value;
  stopVerification();
  if (previous?.status === 'pending')
    void api.cancelLoginTransaction(previous.id).catch(() => {});
}
function restoreDocument(event: globalThis.PageTransitionEvent) {
  if (event.persisted) globalThis.location.reload();
}
watch(
  () => route.fullPath,
  () => {
    leaveDocument();
    if (route.path === '/device' || route.path.startsWith('/device/'))
      globalThis.location.assign(route.fullPath);
  },
);
onMounted(() => {
  globalThis.addEventListener('pagehide', leaveDocument);
  globalThis.addEventListener('pageshow', restoreDocument);
  void load();
});
onUnmounted(() => {
  leaveDocument();
  globalThis.removeEventListener('pagehide', leaveDocument);
  globalThis.removeEventListener('pageshow', restoreDocument);
});
</script>

<template>
  <section class="auriga-login" data-testid="device-confirmation">
    <h1>{{ words.title }}</h1>
    <form v-if="!valid(code)" @submit.prevent="openCode">
      <label
        >{{ words.enter }}<input v-model="entered" autocomplete="off"
      /></label>
      <button type="submit" :disabled="!valid(normalize(entered))">
        {{ words.open }}
      </button>
    </form>
    <template v-else>
      <p>
        {{ words.code }}:
        <strong>{{ code.slice(0, 4) }}-{{ code.slice(4) }}</strong>
      </p>
      <p role="status">
        {{ status === 'ready' ? words.verify : words[status] }}
      </p>
      <p v-if="correlation" data-testid="device-correlation">
        {{ correlation }}
      </p>
      <LoginView
        v-if="status === 'login'"
        :auth-api="api"
        device-continuation
        @signed-in="load"
        @auth-event="
          (event) => {
            if (event.correlationId) correlation = event.correlationId;
          }
        "
      />
      <template v-if="info && (status === 'ready' || status === 'stepup')">
        <p>{{ info.clientName[locale] || info.clientName.en }}</p>
        <p>{{ words.ip }}: {{ info.device.ip }}</p>
        <p v-if="info.device.name">{{ info.device.name }}</p>
        <p>{{ words.scopes }}: {{ info.scopes.join(', ') }}</p>
        <p>
          {{ words.expires }}:
          <time :datetime="info.expiresAt">{{ info.expiresAt }}</time>
        </p>
        <p v-if="me">
          {{ me.account.displayName }}
          {{ loginInfoText(me.uacLoginInfo, locale) }}
        </p>
        <label
          >{{ words.code
          }}<input
            v-model="typed"
            autocomplete="off"
            :disabled="busy || status !== 'ready'"
            data-testid="verify-user-code"
        /></label>
        <button type="button" :disabled="!confirmed" @click="authorize(true)">
          {{ words.approve }}
        </button>
        <button
          type="button"
          :disabled="busy || status !== 'ready'"
          @click="authorize(false)"
        >
          {{ words.deny }}
        </button>
      </template>
      <template v-if="status === 'stepup' && step">
        <button
          v-if="step.type === 'redirect'"
          type="button"
          :disabled="popupActive"
          @click="openPopup"
        >
          {{ words.popup }}
        </button>
        <fieldset v-else :disabled="busy">
          <StepContainer
            :step="step"
            @submit="submit"
            @complete="completed()"
          />
        </fieldset>
        <button type="button" :disabled="busy" @click="cancelVerification">
          {{ words.cancel }}
        </button>
      </template>
    </template>
  </section>
</template>
