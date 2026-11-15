<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { readResource } from '../api';
import type { Provider } from './providersApi';
import { uacApi, type UacConfig, type DiagnosticSettings } from './uacApi';
import { useOrgActions, orgError } from './useOrgActions';
import { useAdminI18n } from './messages';
import { hasPermission } from './permissions';
import ActionConfirm from './ActionConfirm.vue';
const props = defineProps<{ providerId: string }>();
const emit = defineEmits<{ saved: [enabled: boolean, etag: string | null] }>();
const { t } = useAdminI18n();
const access = useOrgActions('iam.providers.read', 'iam.providers.write', t);
const { canRead, canWrite, locked, busy, pending, error, notice } = access;
const config = ref<UacConfig>();
const settings = ref<DiagnosticSettings>();
const configTag = ref<string | null>(null);
const diagnosticTag = ref<string | null>(null);
const originalEnabled = ref(false);
const loading = ref(true);
const correlationId = ref('');
const sinceSeconds = ref(7200);
const now = ref(Date.now());
const lifetime = new globalThis.AbortController();
let writes = new globalThis.AbortController();
const timer = globalThis.setInterval(() => {
  now.value = Date.now();
}, 1000);
const remaining = computed(
  () =>
    Math.max(
      0,
      Math.ceil(
        (Date.parse(settings.value?.expiresAt ?? '') - now.value) / 1000,
      ),
    ) || 0,
);
const diagnosticsOn = computed(
  () => !!settings.value?.enabled && remaining.value > 0,
);
const canDownload = computed(
  () =>
    canRead.value &&
    ['audit.events.read', 'ops.alerts.read'].every((p) =>
      hasPermission(access.permissions.value, p),
    ),
);
watch(
  canWrite,
  (allowed) => {
    if (!allowed) {
      writes.abort();
      writes = new globalThis.AbortController();
    }
  },
  { flush: 'sync' },
);
watch(
  canRead,
  (allowed) => {
    if (!allowed) {
      config.value = undefined;
      settings.value = undefined;
      configTag.value = null;
      diagnosticTag.value = null;
    }
  },
  { flush: 'sync' },
);
onBeforeUnmount(() => {
  lifetime.abort();
  writes.abort();
  globalThis.clearInterval(timer);
});
function failure(e: unknown) {
  if (
    isApiError(e) &&
    (e.status === 401 ||
      (e.status === 403 && e.problem.code !== 'iam.step_up_required'))
  )
    access.permissions.value = [];
  return orgError(e, t);
}
async function readConfig() {
  configTag.value = null;
  config.value = undefined;
  let result;
  try {
    result = await uacApi.get(props.providerId, lifetime.signal);
  } catch (e) {
    if (!isApiError(e) || e.status !== 404) throw e;
    if (!access.isAlive() || !canRead.value || lifetime.signal.aborted) return;
    const draft = await readResource<Provider>(
      `/api/iam/v1/admin/providers/${encodeURIComponent(props.providerId)}`,
      lifetime.signal,
    );
    // Only an existing, disabled, empty UAC placeholder may initialize a draft.
    if (
      draft.data.connectorType !== 'uac' ||
      draft.data.enabled ||
      Object.keys(draft.data.config).length
    )
      throw new Error('Invalid UAC placeholder');
    result = {
      data: { enabled: false, baseUrl: '', systemCode: '' },
      etag: draft.etag,
    };
  }
  if (!access.isAlive() || !canRead.value) return;
  config.value = result.data;
  configTag.value = result.etag;
  originalEnabled.value = result.data.enabled;
}
async function readDiagnostics() {
  diagnosticTag.value = null;
  const result = await uacApi.diagnostics(props.providerId, lifetime.signal);
  if (!access.isAlive() || !canRead.value) return;
  settings.value = result.data;
  diagnosticTag.value = result.etag;
}
async function load() {
  loading.value = true;
  error.value = '';
  try {
    if (!(await access.authorize())) return;
    const results = await Promise.allSettled([readConfig(), readDiagnostics()]);
    for (const r of results)
      if (r.status === 'rejected' && access.isAlive())
        error.value = failure(r.reason);
  } catch (e) {
    if (access.isAlive()) error.value = failure(e);
  } finally {
    if (access.isAlive()) loading.value = false;
  }
}
onMounted(() => void load());
function save() {
  if (
    !canWrite.value ||
    locked.value ||
    loading.value ||
    !config.value ||
    !configTag.value
  )
    return;
  const body: UacConfig = {
    enabled: config.value.enabled,
    baseUrl: config.value.baseUrl.trim(),
    systemCode: config.value.systemCode.trim(),
  };
  const tag = configTag.value;
  const signal = writes.signal;
  access.ask(
    originalEnabled.value && !body.enabled
      ? '停用会撤销该身份源的已有会话，确认保存？'
      : '确认保存 UAC 配置？',
    async () => {
      if (!canWrite.value || signal.aborted) return;
      try {
        await uacApi.save(props.providerId, body, tag, signal);
      } catch (e) {
        if (!access.isAlive() || signal.aborted) return;
        if (isApiError(e) && e.status === 412) {
          error.value = '配置已被他人修改，已重新加载，请核对后再保存。';
          try {
            await readConfig();
          } catch (readError) {
            error.value = `配置已被他人修改，重新加载失败。${failure(readError)}`;
          }
        } else error.value = failure(e);
        return;
      }
      if (!access.isAlive() || signal.aborted) return;
      notice.value = 'UAC 配置已保存';
      try {
        await readConfig();
        if (config.value && access.isAlive() && !signal.aborted)
          emit('saved', config.value.enabled, configTag.value);
      } catch (e) {
        error.value = `写请求已成功，重新加载后才能继续保存。${failure(e)}`;
      }
    },
  );
}
function diagnostics(enabled: boolean) {
  if (!canWrite.value || locked.value || loading.value || !diagnosticTag.value)
    return;
  const tag = diagnosticTag.value;
  const signal = writes.signal;
  access.ask(enabled ? '确认开启诊断 900 秒？' : '确认关闭诊断？', async () => {
    if (!canWrite.value || signal.aborted) return;
    try {
      await uacApi.setDiagnostics(
        props.providerId,
        { enabled, ttlSeconds: 900 },
        tag,
        signal,
      );
    } catch (e) {
      if (!access.isAlive() || signal.aborted) return;
      if (isApiError(e) && e.status === 412) {
        error.value = '诊断配置已被他人修改，已重新加载。';
        try {
          await readDiagnostics();
        } catch (readError) {
          error.value = `诊断配置已被他人修改，重新加载失败。${failure(readError)}`;
        }
      } else error.value = failure(e);
      return;
    }
    if (!access.isAlive() || signal.aborted) return;
    notice.value = '诊断设置已保存';
    try {
      await readDiagnostics();
    } catch (e) {
      error.value = `写请求已成功，请重新加载诊断设置。${failure(e)}`;
    }
  });
}
function download() {
  if (!canDownload.value || locked.value || loading.value) return;
  const correlation = correlationId.value.trim();
  if (
    !Number.isInteger(sinceSeconds.value) ||
    sinceSeconds.value < 1 ||
    sinceSeconds.value > 86400 ||
    (correlation &&
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
        correlation,
      ))
  ) {
    error.value = '请输入有效的关联 ID 和 1–86400 秒时间窗。';
    return;
  }
  const body = {
    providerId: props.providerId,
    sinceSeconds: sinceSeconds.value,
    ...(correlation ? { correlationId: correlation } : {}),
  };
  access.ask(
    '确认下载诊断包？',
    async () => {
      if (!canDownload.value) return;
      try {
        const response = await uacApi.bundle(body, lifetime.signal);
        const blob = await response.blob();
        if (!access.isAlive() || !canDownload.value) return;
        if (!response.headers.get('Content-Type')?.includes('application/zip'))
          throw new Error('Invalid ZIP');
        const url = URL.createObjectURL(blob);
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = 'uac-diagnostics.zip';
        document.body.append(anchor);
        anchor.click();
        anchor.remove();
        globalThis.setTimeout(() => URL.revokeObjectURL(url), 1000);
        notice.value =
          response.headers.get('X-Auriga-Diagnostic-Partial') === 'true'
            ? '诊断包缺少部分来源，请查看 ZIP 中的 manifest。'
            : '诊断包已下载';
      } catch (e) {
        if (access.isAlive()) error.value = failure(e);
      }
    },
    'iam.providers.read',
  );
}
</script>

<template>
  <section class="uac-settings">
    <h3>UAC 配置</h3>
    <p v-if="loading" role="status">正在加载…</p>
    <p v-if="error" class="resource-error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <ElButton :disabled="locked || loading" @click="load">重新加载</ElButton>
    <p v-if="!loading && !canRead" role="alert">无权查看身份源</p>
    <form v-if="config && canRead" class="uac-form" @submit.prevent="save">
      <p v-if="!loading && !configTag" class="resource-error" role="alert">
        未取得配置版本，请重新加载后保存。
      </p>
      <label
        ><input
          v-model="config.enabled"
          type="checkbox"
          :disabled="!canWrite || locked || loading"
          data-testid="uac-enabled"
        />启用 UAC</label
      >
      <label
        >服务地址<input
          v-model="config.baseUrl"
          placeholder="https://identity.example.com"
          :disabled="!canWrite || locked || loading"
          data-testid="uac-base-url"
      /></label>
      <label
        >系统标识<input
          v-model="config.systemCode"
          placeholder="example"
          autocomplete="off"
          :disabled="!canWrite || locked || loading"
          data-testid="uac-system-code"
      /></label>
      <p class="resource-muted">停用会撤销该身份源的已有会话。</p>
      <ElButton
        v-if="canWrite"
        :disabled="locked || loading || !configTag"
        data-testid="uac-save"
        type="primary"
        @click="save"
        >保存 UAC 配置</ElButton
      >
    </form>
    <section v-if="canRead" class="uac-diagnostics">
      <h3>诊断</h3>
      <p v-if="settings" role="status">
        {{ diagnosticsOn ? `已开启，剩余 ${remaining} 秒` : '已关闭' }}
      </p>
      <p v-else>诊断状态未能读取，请重新加载。</p>
      <div v-if="canWrite" class="resource-actions">
        <ElButton
          :disabled="locked || loading || !diagnosticTag"
          data-testid="uac-diagnostics-on"
          @click="diagnostics(true)"
          >开启 900 秒</ElButton
        >
        <ElButton
          :disabled="locked || loading || !diagnosticTag"
          data-testid="uac-diagnostics-off"
          @click="diagnostics(false)"
          >关闭诊断</ElButton
        >
      </div>
      <form v-if="canDownload" class="uac-form" @submit.prevent="download">
        <label
          >关联 ID（可选）<input
            v-model="correlationId"
            :disabled="locked || loading"
        /></label>
        <label
          >最近时间窗（秒）<input
            v-model.number="sinceSeconds"
            type="number"
            min="1"
            max="86400"
            :disabled="locked || loading"
        /></label>
        <ElButton
          :disabled="locked || loading"
          data-testid="uac-download"
          @click="download"
          >下载诊断包</ElButton
        >
      </form>
      <p v-else class="resource-muted">
        下载诊断包还需审计事件和告警查看权限。
      </p>
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="access.confirm"
      @cancel="access.pending.value = undefined"
    />
  </section>
</template>

<style scoped>
.uac-form {
  display: grid;
  gap: 12px;
  margin: 16px 0;
}
.uac-form label {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  align-items: center;
  font-size: 12px;
}
.uac-form input:not([type='checkbox']) {
  box-sizing: border-box;
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
  background: var(--auriga-surface);
  color: var(--auriga-text);
  font: inherit;
}
.uac-form input:disabled {
  opacity: 0.55;
}
.uac-diagnostics {
  border-top: 1px solid var(--auriga-border);
  margin-top: 24px;
  padding-top: 12px;
}
</style>
