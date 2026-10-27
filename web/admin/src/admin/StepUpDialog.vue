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
    },
    en: {
      title: 'Verify your identity to continue',
      cancel: 'Cancel operation',
      busy: 'Verifying…',
      failed: 'Verification failed. Cancel and try again.',
      cancelled: 'Operation cancelled',
      changed: 'Session changed. Start the operation again.',
    },
  },
});
const route = useRoute();
const visible = ref(false),
  busy = ref(false),
  error = ref('');
const tx = shallowRef<LoginTransaction>();
const step = () => tx.value?.step as ClientStep | undefined;
let version = 0;
let pending:
  { resolve: () => void; reject: (error: Error) => void } | undefined;
let owner: { account: string; session: string } | undefined;

function cancel() {
  version++;
  const id = tx.value?.status === 'pending' ? tx.value.id : undefined;
  pending?.reject(new Error(t('cancelled')));
  pending = undefined;
  owner = undefined;
  tx.value = undefined;
  visible.value = false;
  busy.value = false;
  if (id) void iamApi.cancelLoginTransaction(id).catch(() => {});
}
async function accept(value: LoginTransaction, current: number) {
  if (current !== version) {
    if (value.status === 'pending')
      void iamApi.cancelLoginTransaction(value.id).catch(() => {});
    return;
  }
  tx.value = value;
  if (
    value.status !== 'completed' ||
    value.purpose !== 'stepup' ||
    value.step.type !== 'complete'
  )
    return;
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
function failure(value: unknown) {
  error.value = isApiError(value)
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
        const value = await iamApi.createLoginTransaction({
          providerId: me.session.providerId,
          purpose: 'stepup',
          returnTo: route.path,
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
    <div :inert="busy">
      <StepContainer
        v-if="step() && step()!.type !== 'complete'"
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
