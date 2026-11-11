<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import {
  ElButton,
  ElInput,
  ElCheckbox,
  ElTable,
  ElTableColumn,
} from 'element-plus';
import { opsRuntimeApi } from '../../admin/opsRuntimeApi';
import { useOpsRuntime } from '../../admin/useOpsRuntime';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import { useAdminI18n } from '../../admin/messages';
import '../../admin/management.css';
const { t } = useAdminI18n();
const {
  value,
  permissions,
  busy,
  error,
  notice,
  uncertain,
  pending,
  load,
  ask,
  confirm,
} = useOpsRuntime('ops.backups.read', opsRuntimeApi.backups, t);
const enabled = ref(false),
  interval = ref('24'),
  invalid = ref('');
watch(value, (current) => {
  if (current) {
    enabled.value = current.plan.enabled;
    interval.value = String(current.plan.intervalHours);
  }
});
function askPlan() {
  if (!value.value) return;
  if (
    !/^\d+$/.test(interval.value) ||
    Number(interval.value) < 1 ||
    Number(interval.value) > 720
  ) {
    invalid.value = t('opsPlanInvalid');
    return;
  }
  invalid.value = '';
  const plan = {
    enabled: enabled.value,
    intervalHours: Number(interval.value),
    revision: value.value.plan.revision,
  };
  ask(
    `${t('opsPlanConfirm')} ${t(enabled.value ? 'opsPlanEnabled' : 'opsPlanDisabled')} · ${t('opsInterval')}: ${plan.intervalHours}`,
    'ops.backups.write',
    (signal) => opsRuntimeApi.plan(plan, signal),
  );
}
function trigger() {
  const key = globalThis.crypto.randomUUID();
  ask(t('opsBackupConfirm'), 'ops.backups.write', (signal) =>
    opsRuntimeApi.trigger(key, signal),
  );
}
function download(id: string) {
  ask(t('opsDownloadConfirm'), 'ops.backups.download', (signal) =>
    opsRuntimeApi.download(id, signal),
  );
}
onMounted(load);
</script>
<template>
  <section class="management">
    <h1>{{ t('opsBackups') }}</h1>
    <ElButton :disabled="busy || !!pending" @click="load">{{
      t('refresh')
    }}</ElButton>
    <p v-if="busy" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert" class="error">
      {{ error }} {{ t('opsSetup') }}
    </p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="invalid" role="alert" class="error">{{ invalid }}</p>
    <section v-if="value" class="details">
      <h2>{{ t('opsBackupPlan') }}</h2>
      <p>
        {{ t('lastUpdated') }}: {{ value.worker?.reportedAt || t('unknown') }}
      </p>
      <ElCheckbox
        v-model="enabled"
        :disabled="
          busy ||
          !!pending ||
          uncertain ||
          !permissions.includes('ops.backups.write')
        "
        >{{ t('opsPlanEnabled') }}</ElCheckbox
      >
      <label
        >{{ t('opsInterval')
        }}<ElInput
          v-model="interval"
          data-testid="backup-interval"
          :disabled="
            busy ||
            !!pending ||
            uncertain ||
            !permissions.includes('ops.backups.write')
          "
      /></label>
      <p>{{ t('opsNext') }}: {{ value.plan.nextAt || t('unknown') }}</p>
      <div class="actions">
        <ElButton
          data-testid="save-backup-plan"
          :disabled="
            busy ||
            !!pending ||
            uncertain ||
            !permissions.includes('ops.backups.write')
          "
          @click="askPlan"
          >{{ t('save') }}</ElButton
        ><ElButton
          data-testid="trigger-backup"
          :disabled="
            busy ||
            !!pending ||
            uncertain ||
            !permissions.includes('ops.backups.write')
          "
          @click="trigger"
          >{{ t('opsBackupNow') }}</ElButton
        >
      </div>
      <ElTable
        :data="[...value.items].reverse().slice(0, 50)"
        :empty-text="t('empty')"
        ><ElTableColumn
          prop="createdAt"
          :label="t('opsCreated')"
        /><ElTableColumn prop="status" :label="t('status')" /><ElTableColumn
          prop="sizeBytes"
          :label="t('opsBytes')"
        /><ElTableColumn prop="sha256" label="SHA-256" /><ElTableColumn
          prop="reason"
          :label="t('reasons')"
        /><ElTableColumn :label="t('backupDownload')"
          ><template #default="{ row }"
            ><ElButton
              v-if="row.status === 'succeeded'"
              :disabled="
                busy ||
                !!pending ||
                uncertain ||
                !permissions.includes('ops.backups.download')
              "
              @click="download(row.id)"
              >{{ t('backupDownload') }}</ElButton
            ></template
          ></ElTableColumn
        ></ElTable
      >
    </section>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="!busy && (pending = undefined)"
    />
    <section
      v-for="part in ['Scope', 'Download', 'Restore']"
      :key="part"
      class="details"
    >
      <h2>{{ t(`backup${part}`) }}</h2>
      <p>{{ t(`backup${part}Text`) }}</p>
    </section>
  </section>
</template>
