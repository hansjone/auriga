<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { apiClient } from '../api';
import { createWorkspaceApi, enterInstance, type Instance } from '../workspace';

const { t } = useI18n({
  messages: {
    'zh-CN': {
      title: '我的实例',
      empty: '尚无实例，启动时将创建。',
      loading: '正在读取实例…',
      waiting: '正在等待实例就绪…',
      start: '启动并进入',
      enter: '进入实例',
      retry: '重试',
      cancel: '取消等待',
      cancelled: '已取消等待，可重新读取状态。',
      error: '无法完成请求，请重试。',
      disabled: '实例服务未启用，请联系管理员。',
      unavailable: '实例服务暂时不可用，请稍后重试。',
      timeout: '等待超时，请重试。',
      failed: '实例启动失败，请重试。',
      expired: '登录已失效，请重新登录。',
      refresh: '刷新状态',
      phases: {
        scheduling: '等待调度',
        absent: '尚未运行',
        preparing: '准备中',
        migrating: '迁移中',
        starting: '启动中',
        running: '运行中',
        pending_restart: '待重启',
        stopping: '停止中',
        stopped: '已停止',
        deleting: '删除中',
        failed: '失败',
      },
    },
    en: {
      title: 'My instance',
      empty: 'No instance yet. Starting will create one.',
      loading: 'Loading instance…',
      waiting: 'Waiting for the instance to be ready…',
      start: 'Start and enter',
      enter: 'Enter instance',
      retry: 'Retry',
      cancel: 'Cancel waiting',
      cancelled: 'Waiting cancelled. Refresh to check the current state.',
      error: 'Request failed. Please retry.',
      disabled:
        'The instance service is not enabled. Please contact an administrator.',
      unavailable:
        'The instance service is temporarily unavailable. Please retry later.',
      timeout: 'Waiting timed out. Please retry.',
      failed: 'Instance failed to start. Please retry.',
      expired: 'Your session expired. Please sign in again.',
      refresh: 'Refresh status',
      phases: {
        scheduling: 'Scheduling',
        absent: 'Not running',
        preparing: 'Preparing',
        migrating: 'Migrating',
        starting: 'Starting',
        running: 'Running',
        pending_restart: 'Restart pending',
        stopping: 'Stopping',
        stopped: 'Stopped',
        deleting: 'Deleting',
        failed: 'Failed',
      },
    },
  },
});
const api = createWorkspaceApi(apiClient);
const instance = ref<Instance | null>(null);
const busy = ref(false);
const loaded = ref(false);
const message = ref('');
const detail = ref('');
const expired = ref(false);
function isReady(value: Instance | null) {
  return (
    !!value?.ready &&
    (value.phase === 'running' || value.phase === 'pending_restart')
  );
}
const ready = computed(() => isReady(instance.value));
let controller: InstanceType<typeof globalThis.AbortController> | undefined;
let timeout: ReturnType<typeof setTimeout> | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;
let epoch = 0;

function stop() {
  epoch++;
  controller?.abort();
  globalThis.clearTimeout(timeout);
  globalThis.clearTimeout(timer);
  busy.value = false;
}
function cancel() {
  stop();
  message.value = 'cancelled';
}
function fail(error: unknown) {
  message.value = 'error';
  if (isApiError(error)) {
    if (error.status === 401) message.value = 'expired';
    else if (
      error.status === 503 &&
      error.problem.code === 'common.unavailable'
    ) {
      message.value =
        error.problem.params?.service === 'workspace' &&
        error.problem.params?.reason === 'disabled'
          ? 'disabled'
          : 'unavailable';
    }
  }
  expired.value = message.value === 'expired';
  detail.value = isApiError(error) ? error.problem.code : '';
}
async function run(open: boolean) {
  if (busy.value || expired.value) return;
  stop();
  const version = epoch;
  const abort = new globalThis.AbortController();
  controller = abort;
  busy.value = true;
  message.value = '';
  detail.value = '';
  timeout = setTimeout(() => {
    if (version !== epoch) return;
    stop();
    message.value = 'timeout';
  }, 120_000);
  const current = () => version === epoch && !abort.signal.aborted;
  async function observe(value: Instance) {
    if (!current()) return;
    instance.value = value;
    loaded.value = true;
    if (isReady(value)) {
      stop();
      if (open) enterInstance(value.id);
    } else if (value.phase === 'failed' || value.error) {
      stop();
      message.value = 'failed';
      detail.value = value.error?.code ?? '';
    } else if (value.desired === 'stopped' || value.phase === 'deleting') {
      stop();
    } else {
      timer = setTimeout(() => {
        void poll(value.id);
      }, 2000);
    }
  }
  async function poll(id: string) {
    try {
      await observe(await api.get(id, abort.signal));
    } catch (error) {
      if (!current()) return;
      stop();
      fail(error);
    }
  }
  try {
    if (open) {
      await observe((await api.open(abort.signal)).instance);
    } else {
      const result = await api.list(abort.signal);
      if (!current()) return;
      loaded.value = true;
      instance.value = result.items[0] ?? null;
      if (instance.value) await observe(instance.value);
      else stop();
    }
  } catch (error) {
    if (!current()) return;
    stop();
    fail(error);
  }
}
function enter() {
  if (!busy.value && !expired.value && ready.value && instance.value)
    void run(true);
}
onMounted(() => void run(false));
onBeforeUnmount(stop);
</script>

<template>
  <section :aria-busy="busy">
    <h2>{{ t('title') }}</h2>
    <p v-if="instance">
      {{ instance.name }} · {{ t(`phases.${instance.phase}`) }}
    </p>
    <p v-else-if="loaded" class="muted">{{ t('empty') }}</p>
    <p v-if="busy" role="status">{{ t(loaded ? 'waiting' : 'loading') }}</p>
    <p v-if="message" role="alert">
      {{ t(message) }} <span v-if="detail">({{ detail }})</span>
    </p>
    <ElButton v-if="busy" @click="cancel">{{ t('cancel') }}</ElButton>
    <template v-else-if="!expired">
      <ElButton v-if="ready && !message" type="primary" @click="enter">{{
        t('enter')
      }}</ElButton>
      <ElButton
        v-else-if="
          loaded &&
          (!instance ||
            instance.phase === 'stopped' ||
            instance.phase === 'absent' ||
            instance.phase === 'failed')
        "
        type="primary"
        @click="run(true)"
        >{{ t('start') }}</ElButton
      >
      <ElButton @click="run(false)">{{
        t(message ? 'retry' : 'refresh')
      }}</ElButton>
    </template>
  </section>
</template>

<style scoped>
.muted {
  color: var(--auriga-muted);
}
</style>
