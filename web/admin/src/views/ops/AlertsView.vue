<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { opsApi, type OpsAlert } from '../../admin/opsApi';
import { useOpsI18n } from '../../admin/opsMessages';
import { useRelayUi } from '../models/relayUi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import '../../admin/management.css';
const { t } = useOpsI18n();
const ui = useRelayUi(t);
const { loading, busy, error, notice } = ui;
const items = ref<OpsAlert[]>([]),
  severity = ref(''),
  status = ref('');
const pending = ref<{
  id: string;
  action: 'acknowledge' | 'silence' | 'unsilence';
}>();
const visible = computed(() =>
  items.value.filter(
    (r) =>
      (!severity.value || r.severity === severity.value) &&
      (!status.value || (status.value === 'resolved') === !!r.resolved_at),
  ),
);
async function load() {
  const data = await ui.read('ops.alerts.read', () => opsApi.alerts(ui.signal));
  items.value = data?.items ?? [];
}
onMounted(load);
watch(
  () => ui.can('ops.alerts.read'),
  (allowed) => {
    if (!allowed) {
      items.value = [];
      pending.value = undefined;
    }
  },
);
async function confirm() {
  const p = pending.value;
  if (!p) return;
  await ui.write(
    'ops.alerts.write',
    () => opsApi.action(p.id, p.action, ui.signal),
    () => {},
    load,
  );
  pending.value = undefined;
}
const date = (v: string | null) => (v ? new Date(v).toLocaleString() : '');
</script>
<template>
  <section class="management">
    <header class="head">
      <h1>{{ t('alerts') }}</h1>
      <button :disabled="loading || busy" @click="load">
        {{ t('refresh') }}
      </button>
    </header>
    <p role="note" class="muted">{{ t('signalHint') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <div class="actions">
      <label
        >{{ t('severity') }}
        <select v-model="severity">
          <option value="">{{ t('all') }}</option>
          <option value="warning">{{ t('warning') }}</option>
          <option value="critical">{{ t('critical') }}</option>
        </select></label
      ><label
        >{{ t('status') }}
        <select v-model="status">
          <option value="">{{ t('all') }}</option>
          <option value="active">{{ t('active') }}</option>
          <option value="resolved">{{ t('resolved') }}</option>
        </select></label
      >
    </div>
    <table>
      <thead>
        <tr>
          <th>{{ t('source') }}</th>
          <th>{{ t('subject') }}</th>
          <th>{{ t('severity') }}</th>
          <th>{{ t('status') }}</th>
          <th>{{ t('first') }}</th>
          <th>{{ t('last') }}</th>
          <th>{{ t('confirm') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in visible" :key="row.id">
          <td>{{ t(row.source) }}</td>
          <td>{{ row.subject }}</td>
          <td>{{ t(row.severity) }}</td>
          <td>
            {{ t(row.resolved_at ? 'resolved' : 'active') }}
            <span v-if="row.acknowledged_at">{{ t('acknowledged') }}</span
            ><span
              v-if="
                row.silenced_until &&
                Date.parse(row.silenced_until) > Date.now()
              "
              >{{ t('silenced') }} {{ date(row.silenced_until) }}</span
            >
          </td>
          <td>{{ date(row.first_seen) }}</td>
          <td>{{ date(row.last_seen) }}</td>
          <td>
            <button
              :disabled="
                busy ||
                loading ||
                !!pending ||
                !ui.can('ops.alerts.write') ||
                !!row.acknowledged_at
              "
              @click="pending = { id: row.id, action: 'acknowledge' }"
            >
              {{ t('acknowledge') }}</button
            ><button
              :disabled="
                busy || loading || !!pending || !ui.can('ops.alerts.write')
              "
              @click="pending = { id: row.id, action: 'silence' }"
            >
              {{ t('silence') }}</button
            ><button
              v-if="row.silenced_until"
              :disabled="
                busy || loading || !!pending || !ui.can('ops.alerts.write')
              "
              @click="pending = { id: row.id, action: 'unsilence' }"
            >
              {{ t('unsilence') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-if="!loading && !error && !visible.length">{{ t('empty') }}</p>
    <p class="muted">{{ t('historyHint') }}</p>
    <ActionConfirm
      :message="pending ? t('confirmText') : undefined"
      :busy="busy"
      @confirm="confirm"
      @cancel="pending = undefined"
    />
  </section>
</template>
