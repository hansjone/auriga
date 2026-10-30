<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  StepContainer,
  isApiError,
  mapProblemMessage,
  type IAMComponents,
  type LoginTransaction,
  type Me,
  type StepInput,
  type ClientStep,
} from '@auriga/shared';
import { iamApi } from '../../api';
import { messages } from './messages';
import {
  RedirectStepUpError,
  startRedirectStepUp,
} from '../../../../shared/src/steps/redirectStepUp';

const { t } = useI18n({ useScope: 'local', messages });
const { t: globalT } = useI18n({ useScope: 'global' });
const me = shallowRef<Me>();
const sessions = shallowRef<IAMComponents['schemas']['SessionView'][]>();
const enrollment = shallowRef<IAMComponents['schemas']['TotpEnrollment']>();
const tx = shallowRef<LoginTransaction>();
const popupActive = ref(false);
let popup: ReturnType<typeof startRedirectStepUp> | undefined;
let popupReturnTo = '';
const step = () => tx.value?.step as ClientStep | undefined;
const currentPassword = ref(''),
  newPassword = ref(''),
  code = ref('');
const loading = ref(false),
  busy = ref(false),
  error = ref(''),
  notice = ref('');
let alive = true;
let version = 0;
let pending: (() => Promise<void>) | undefined;
let owner: { account: string; session: string } | undefined;
const describeError = (e: unknown) =>
  e instanceof RedirectStepUpError
    ? t(e.message)
    : isApiError(e)
      ? e.problem.code === 'iam.step_up_required'
        ? t('stepupRequired')
        : mapProblemMessage(e.problem, globalT)
      : e instanceof Error
        ? e.message
        : t('failed');
async function reload() {
  loading.value = true;
  error.value = '';
  try {
    const [identity, list] = await Promise.all([
      iamApi.getMe(),
      iamApi.listMySessions(),
    ]);
    if (!alive) return;
    me.value = identity;
    sessions.value = list.items;
  } catch (e) {
    if (alive) {
      me.value = undefined;
      sessions.value = undefined;
      error.value = describeError(e);
    }
  } finally {
    if (alive) loading.value = false;
  }
}
function clearEnrollment() {
  enrollment.value = undefined;
  code.value = '';
}
function cancelVerification() {
  version++;
  popup?.close();
  popup = undefined;
  popupActive.value = false;
  const id = tx.value?.status === 'pending' ? tx.value.id : undefined;
  tx.value = undefined;
  pending = undefined;
  owner = undefined;
  busy.value = false;
  currentPassword.value = '';
  newPassword.value = '';
  if (id) void iamApi.cancelLoginTransaction(id).catch(() => {});
}
async function complete(current: number) {
  const identity = await iamApi.getMe();
  if (!alive || current !== version) return;
  if (
    identity.account.id !== owner?.account ||
    identity.session.id !== owner?.session
  ) {
    cancelVerification();
    error.value = t('changed');
    return;
  }
  const operation = pending;
  me.value = identity;
  pending = undefined;
  tx.value = undefined;
  owner = undefined;
  // This is the sole retry; a second denial is shown to the user.
  await operation?.();
}
async function accept(value: LoginTransaction, current: number) {
  if (!alive || current !== version) {
    if (value.status === 'pending')
      void iamApi.cancelLoginTransaction(value.id).catch(() => {});
    return;
  }
  tx.value = value;
  if (
    value.status === 'completed' &&
    value.purpose === 'stepup' &&
    value.step.type === 'complete'
  )
    await complete(current);
}
async function run(operation: () => Promise<void>) {
  if (busy.value || pending || !me.value) return;
  busy.value = true;
  error.value = '';
  notice.value = '';
  const current = version;
  try {
    await operation();
  } catch (e) {
    if (!alive || current !== version) return;
    if (
      isApiError(e) &&
      e.status === 403 &&
      e.problem.code === 'iam.step_up_required'
    ) {
      pending = operation;
      owner = { account: me.value.account.id, session: me.value.session.id };
      try {
        const nonce = Array.from(
          globalThis.crypto.getRandomValues(new Uint8Array(32)),
          (byte) => byte.toString(16).padStart(2, '0'),
        ).join('');
        popupReturnTo = `/account/security?aurigaStepUp=${nonce}`;
        await accept(
          await iamApi.createLoginTransaction({
            providerId: me.value.session.providerId,
            purpose: 'stepup',
            returnTo: popupReturnTo,
          }),
          current,
        );
      } catch (failure) {
        if (alive && current === version) {
          error.value = describeError(failure);
          pending = undefined;
        }
      }
    } else error.value = describeError(e);
  } finally {
    if (alive && current === version) busy.value = false;
  }
}
function openPopup() {
  if (!tx.value || busy.value || popupActive.value) return;
  const current = version;
  error.value = '';
  try {
    popup = startRedirectStepUp(tx.value, popupReturnTo, (id) =>
      iamApi.getLoginTransaction(id),
    );
    popupActive.value = true;
    void popup.done
      .then(async (value) => {
        if (!alive || current !== version) return;
        busy.value = true;
        if (value) await accept(value, current);
        else await complete(current);
      })
      .catch((e: unknown) => {
        if (alive && current === version) error.value = describeError(e);
      })
      .finally(() => {
        if (alive && current === version) {
          popupActive.value = false;
          popup = undefined;
          busy.value = false;
        }
      });
  } catch (e) {
    error.value = describeError(e);
  }
}
async function submit(input: StepInput) {
  if (!tx.value || busy.value) return;
  const current = version;
  busy.value = true;
  error.value = '';
  try {
    await accept(
      await iamApi.advanceLoginTransaction(tx.value.id, input),
      current,
    );
  } catch (e) {
    if (alive && current === version) error.value = describeError(e);
  } finally {
    if (alive && current === version) busy.value = false;
  }
}
function beginEnrollment() {
  void run(async () => {
    // A first-time account may have enrolled TOTP inside the IAM step-up flow.
    if (me.value?.mfa.totp) {
      notice.value = t('ready');
      await reload();
      return;
    }
    const value = await iamApi.beginTotpEnrollment();
    if (alive) enrollment.value = value;
  });
}
function confirmEnrollment() {
  if (!enrollment.value || !/^\d{6}$/.test(code.value)) return;
  const id = enrollment.value.enrollmentId,
    value = code.value;
  void run(async () => {
    await iamApi.confirmTotpEnrollment(id, { code: value });
    if (!alive) return;
    clearEnrollment();
    notice.value = t('ready');
    await reload();
  });
}
function password() {
  if (!currentPassword.value || !newPassword.value) return;
  const body = {
    currentPassword: currentPassword.value,
    newPassword: newPassword.value,
  };
  void run(async () => {
    try {
      await iamApi.changePassword(body);
    } finally {
      currentPassword.value = '';
      newPassword.value = '';
    }
    if (alive) {
      notice.value = t('saved');
      await reload();
    }
  });
}
function revoke() {
  void run(async () => {
    await iamApi.revokeOtherSessions();
    if (alive) {
      notice.value = t('saved');
      await reload();
    }
  });
}
onMounted(reload);
onBeforeUnmount(() => {
  alive = false;
  cancelVerification();
  clearEnrollment();
  me.value = undefined;
  sessions.value = undefined;
});
</script>

<template>
  <main class="security auriga-glass">
    <header>
      <h1>{{ t('title') }}</h1>
      <button :disabled="loading || busy || !!pending" @click="reload">
        {{ t('refresh') }}
      </button>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <section v-if="tx" class="verification" :aria-label="t('stepup')">
      <h2>{{ t('stepup') }}</h2>
      <template v-if="step()?.type === 'redirect'">
        <p>{{ t('popupWaiting') }}</p>
        <button
          data-action="redirect-step-up"
          :disabled="busy || popupActive"
          @click="openPopup"
        >
          {{ t('openPopup') }}
        </button>
      </template>
      <div :inert="busy">
        <StepContainer
          v-if="step() && !['complete', 'redirect'].includes(step()!.type)"
          :step="step()!"
          @submit="submit"
          @retry="submit({ type: 'form', formId: 'retry', values: {} })"
        />
      </div>
      <button @click="cancelVerification">{{ t('cancel') }}</button>
    </section>
    <fieldset v-if="me" :disabled="loading || busy || !!pending || !!tx">
      <section>
        <h2>{{ t('totp') }}</h2>
        <p>{{ me.mfa.totp ? t('enabled') : t('disabled') }}</p>
        <button
          v-if="!me.mfa.totp && !enrollment"
          data-action="enroll"
          @click="beginEnrollment"
        >
          {{ t('enroll') }}
        </button>
        <form
          v-if="enrollment"
          data-form="totp"
          @submit.prevent="confirmEnrollment"
        >
          <p>{{ t('manual') }}</p>
          <p>
            {{ t('secret') }}：<code>{{ enrollment.secret }}</code>
          </p>
          <p>{{ t('expires') }}：{{ enrollment.expiresAt }}</p>
          <label
            >{{ t('code')
            }}<input
              v-model="code"
              autocomplete="one-time-code"
              inputmode="numeric"
              pattern="[0-9]{6}"
              maxlength="6"
              required /></label
          ><button type="submit">{{ t('confirm') }}</button
          ><button type="button" @click="clearEnrollment">
            {{ t('cancel') }}
          </button>
        </form>
      </section>
      <section>
        <h2>{{ t('password') }}</h2>
        <p>{{ t('passwordHint') }}</p>
        <form @submit.prevent="password">
          <label
            >{{ t('currentPassword')
            }}<input
              v-model="currentPassword"
              type="password"
              autocomplete="current-password"
              required /></label
          ><label
            >{{ t('newPassword')
            }}<input
              v-model="newPassword"
              type="password"
              autocomplete="new-password"
              required /></label
          ><button type="submit">{{ t('password') }}</button>
        </form>
      </section>
      <section>
        <h2>{{ t('sessions') }}</h2>
        <p>{{ t('revokeHint') }}</p>
        <button data-action="revoke" @click="revoke">{{ t('revoke') }}</button>
        <p v-if="sessions?.length === 0">{{ t('empty') }}</p>
        <ul>
          <li v-for="session in sessions" :key="session.id">
            <strong
              >{{ t(session.audience) }}
              {{ session.current ? t('current') : '' }}</strong
            >
            <p>{{ t('created') }}：{{ session.createdAt }}</p>
            <p>{{ t('lastSeen') }}：{{ session.lastSeenAt || t('unknown') }}</p>
            <p>{{ t('expires') }}：{{ session.expiresAt }}</p>
            <p>
              {{ t('agent') }}：{{ session.userAgent || t('unknown') }}
              {{ session.ip || '' }}
            </p>
          </li>
        </ul>
      </section>
    </fieldset>
  </main>
</template>
<style scoped>
.security {
  width: min(760px, calc(100% - 2rem));
  margin: 2rem auto;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
section {
  padding: 1rem 0;
  border-top: 1px solid var(--auriga-border);
}
fieldset {
  border: 0;
  padding: 0;
  min-width: 0;
}
label {
  display: grid;
  gap: 0.5rem;
  margin: 0.75rem 0;
}
input {
  font: inherit;
  padding: 0.6rem;
  color: inherit;
  background: transparent;
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
}
button {
  font: inherit;
  cursor: pointer;
  padding: 0.5rem 0.8rem;
  margin: 0.25rem 0.5rem 0.25rem 0;
}
li,
code {
  overflow-wrap: anywhere;
}
</style>
