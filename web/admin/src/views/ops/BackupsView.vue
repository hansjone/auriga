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
const retentionCount = ref('7'),
  retentionDays = ref('30'),
  minFreeMiB = ref('1024');
watch(value, (current) => {
  if (current) {
    enabled.value = current.plan.enabled;
    interval.value = String(current.plan.intervalHours);
    retentionCount.value = String(current.plan.retentionCount ?? 7);
    retentionDays.value = String(current.plan.retentionDays ?? 30);
    minFreeMiB.value = String(current.plan.minFreeMiB ?? 1024);
  }
});
function askPlan() {
  if (!value.value) return;
  if (
    !/^\d+$/.test(interval.value) ||
    Number(interval.value) < 1 ||
    Number(interval.value) > 720 ||
    ![
      [retentionCount.value, 1, 365],
      [retentionDays.value, 1, 3650],
      [minFreeMiB.value, 64, 1048576],
    ].every(
      ([value, min, max]) =>
        /^\d+$/.test(String(value)) &&
        Number(value) >= Number(min) &&
        Number(value) <= Number(max),
    )
  ) {
    invalid.value = t('opsPlanInvalid');
    return;
  }
  invalid.value = '';
  const plan = {
    enabled: enabled.value,
    intervalHours: Number(interval.value),
    revision: value.value.plan.revision,
    retentionCount: Number(retentionCount.value),
    retentionDays: Number(retentionDays.value),
    minFreeMiB: Number(minFreeMiB.value),
  };
  ask(
    `${t('opsPlanConfirm')} ${t(enabled.value ? 'opsPlanEnabled' : 'opsPlanDisabled')} · ${t('opsInterval')}: ${plan.intervalHours}; ${t('opsRetentionCount')}: ${plan.retentionCount}; ${t('opsRetentionDays')}: ${plan.retentionDays}; ${t('opsFreeMiB')}: ${plan.minFreeMiB}`,
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
function remove(id: string) {
  ask(t('opsDeleteConfirm') + ' ' + id, 'ops.backups.write', (signal) =>
    opsRuntimeApi.remove(id, signal),
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
      <div class="label-row">
        <label
          >{{ t('opsRetentionCount')
          }}<ElInput
            v-model="retentionCount"
            data-testid="retention-count"
            :disabled="
              busy || !!pending || !permissions.includes('ops.backups.write')
            "
        /></label>
        <label
          >{{ t('opsRetentionDays')
          }}<ElInput
            v-model="retentionDays"
            data-testid="retention-days"
            :disabled="
              busy || !!pending || !permissions.includes('ops.backups.write')
            "
        /></label>
        <label
          >{{ t('opsFreeMiB')
          }}<ElInput
            v-model="minFreeMiB"
            data-testid="min-free-mib"
            :disabled="
              busy || !!pending || !permissions.includes('ops.backups.write')
            "
        /></label>
      </div>
      <p
        v-for="alert in value.alerts || []"
        :key="alert"
        role="alert"
        class="error"
      >
        {{ t('opsStorageAlert') }} {{ alert }}
      </p>
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
              v-if="row.status === 'succeeded' && !row.deleteRequestedAt"
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
        ><ElTableColumn :label="t('opsCoverage')"
          ><template #default="{ row }"
            >{{ row.databases?.join(', ') || t('opsLegacy') }}
            {{ row.includesRoles ? '+ roles' : '' }}</template
          ></ElTableColumn
        >
        <ElTableColumn :label="t('opsDelete')"
          ><template #default="{ row }"
            ><ElButton
              v-if="row.status === 'succeeded'"
              :disabled="
                busy ||
                !!pending ||
                uncertain ||
                !!row.deleteRequestedAt ||
                !permissions.includes('ops.backups.write')
              "
              @click="remove(row.id)"
              >{{ t('opsDelete') }}</ElButton
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <h2>{{ t('opsAudit') }}</h2>
      <ElTable
        :data="[...(value.audit || [])].reverse().slice(0, 50)"
        :empty-text="t('empty')"
        ><ElTableColumn prop="at" :label="t('opsCreated')" /><ElTableColumn
          prop="actor"
          :label="t('name')" /><ElTableColumn
          prop="backupId"
          :label="t('opsBackupPlan')" /><ElTableColumn
          prop="action"
          :label="t('opsAudit')" /><ElTableColumn
          prop="result"
          :label="t('status')" /><ElTableColumn
          prop="bytes"
          :label="t('opsBytes')"
      /></ElTable>
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
