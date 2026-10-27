<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ElButton, ElDialog } from 'element-plus';
import {
  StepContainer,
  isApiError,
  mapProblemMessage,
  type LoginTransaction,
  type StepInput,
  type Me,
  type ClientStep,
} from '@auriga/shared';
import { apiClient, iamApi } from '../api';
import { bindStepUp } from './stepUp';
import {
  RedirectStepUpError,
  startRedirectStepUp,
} from '../../../shared/src/steps/redirectStepUp';
const props = defineProps<{ me: Me | null }>();
const { t: globalT } = useI18n({ useScope: 'global' });

const { t } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      title: '验证身份后继续操作',
      cancel: '取消操作',
      busy: '正在验证…',
      failed: '验证失败，请取消后重试',
      cancelled: '操作已取消',
      changed: '会话已改变，请重新操作',
      openPopup: '打开验证窗口',
      popupWaiting: '请在独立窗口完成验证；当前页面及草稿会保留。',
      popupBlocked: '浏览器拦截了验证窗口。请允许弹窗后再次点击，草稿已保留。',
      popupClosed: '验证窗口已关闭，请重新打开或取消操作。',
      popupExpired: '验证已超时，请取消后重新操作，草稿已保留。',
      popupFailed: '未能核验原验证事务，请取消后重试，草稿已保留。',
    },
    en: {
      title: 'Verify your identity to continue',
      cancel: 'Cancel operation',
      busy: 'Verifying…',
      failed: 'Verification failed. Cancel and try again.',
      cancelled: 'Operation cancelled',
      changed: 'Session changed. Start the operation again.',
      openPopup: 'Open verification window',
      popupWaiting:
        'Complete verification in the separate window. This page and its draft stay open.',
      popupBlocked:
        'The verification window was blocked. Allow popups and click again. Your draft is preserved.',
      popupClosed:
        'The verification window was closed. Open it again or cancel.',
      popupExpired:
        'Verification expired. Cancel and retry. Your draft is preserved.',
      popupFailed:
        'Could not verify the original transaction. Cancel and retry. Your draft is preserved.',
    },
  },
});
const route = useRoute();
const visible = ref(false),
  busy = ref(false),
  error = ref('');
const tx = shallowRef<LoginTransaction>();
const popupActive = ref(false);
let popup: ReturnType<typeof startRedirectStepUp> | undefined;
let popupReturnTo = '';
const step = () => tx.value?.step as ClientStep | undefined;
let version = 0;
let pending:
  { resolve: () => void; reject: (error: Error) => void } | undefined;
let owner: { account: string; session: string } | undefined;

function cancel() {
  version++;
  popup?.close();
  popup = undefined;
  popupActive.value = false;
  const id = tx.value?.status === 'pending' ? tx.value.id : undefined;
  pending?.reject(new Error(t('cancelled')));
  pending = undefined;
  owner = undefined;
  tx.value = undefined;
  visible.value = false;
  busy.value = false;
  if (id) void iamApi.cancelLoginTransaction(id).catch(() => {});
}
async function complete(current: number) {
  const me = await iamApi.getMe();
  if (current !== version) return;
  if (me.account.id !== owner?.account || me.session.id !== owner?.session) {
    apiClient.invalidatePendingRequests();
    throw new Error(t('changed'));
  }
  const done = pending;
  pending = undefined;
  tx.value = undefined;
  owner = undefined;
  visible.value = false;
  done?.resolve();
}
async function accept(value: LoginTransaction, current: number) {
  if (current !== version) {
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
function failure(value: unknown) {
  error.value =
    value instanceof RedirectStepUpError
      ? t(value.message)
      : isApiError(value)
        ? mapProblemMessage(value.problem, globalT)
        : value instanceof Error
          ? value.message
          : t('failed');
}
const unbind = bindStepUp(
  () =>
    new Promise<void>((resolve, reject) => {
      cancel();
      pending = { resolve, reject };
      visible.value = true;
      error.value = '';
      busy.value = true;
      const current = version;
      void (async () => {
        const expected = props.me;
        const me = await iamApi.getMe();
        if (current !== version) return;
        if (
          !expected ||
          me.account.id !== expected.account.id ||
          me.session.id !== expected.session.id
        ) {
          apiClient.invalidatePendingRequests();
          throw new Error(t('changed'));
        }
        owner = { account: me.account.id, session: me.session.id };
        const nonce = Array.from(
          globalThis.crypto.getRandomValues(new Uint8Array(32)),
          (byte) => byte.toString(16).padStart(2, '0'),
        ).join('');
        popupReturnTo = `/overview?aurigaStepUp=${nonce}`;
        const value = await iamApi.createLoginTransaction({
          providerId: me.session.providerId,
          purpose: 'stepup',
          returnTo: popupReturnTo,
        });
        await accept(value, current);
      })()
        .catch((e: unknown) => {
          if (current === version) failure(e);
        })
        .finally(() => {
          if (current === version) busy.value = false;
        });
    }),
);
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
        if (current !== version) return;
        if (value) await accept(value, current);
        else await complete(current);
      })
      .catch((e: unknown) => {
        if (current === version) failure(e);
      })
      .finally(() => {
        if (current === version) {
          popupActive.value = false;
          popup = undefined;
        }
      });
  } catch (e) {
    failure(e);
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
    if (current === version) failure(e);
  } finally {
    if (current === version) busy.value = false;
  }
}
watch(
  () => route.fullPath,
  () => {
    apiClient.invalidatePendingRequests();
    cancel();
  },
);
onBeforeUnmount(() => {
  unbind();
  apiClient.invalidatePendingRequests();
  cancel();
});
</script>

<template>
  <ElDialog
    :model-value="visible"
    :title="t('title')"
    :close-on-click-modal="false"
    @close="cancel"
  >
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="busy" role="status">{{ t('busy') }}</p>
    <template v-if="step()?.type === 'redirect'">
      <p>{{ t('popupWaiting') }}</p>
      <ElButton
        data-action="redirect-step-up"
        :disabled="busy || popupActive"
        @click="openPopup"
        >{{ t('openPopup') }}</ElButton
      >
    </template>
    <div :inert="busy">
      <StepContainer
        v-if="step() && !['complete', 'redirect'].includes(step()!.type)"
        :step="step()!"
        @submit="submit"
        @retry="submit({ type: 'form', formId: 'retry', values: {} })"
      />
    </div>
    <template #footer
      ><ElButton @click="cancel">{{ t('cancel') }}</ElButton></template
    >
  </ElDialog>
</template>
