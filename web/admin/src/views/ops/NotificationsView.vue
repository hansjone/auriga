<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue';
import {
  opsApi,
  type OpsChannel,
  type OpsChannelInput,
} from '../../admin/opsApi';
import { useOpsI18n } from '../../admin/opsMessages';
import { useRelayUi } from '../models/relayUi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import '../../admin/management.css';
const { t } = useOpsI18n(),
  ui = useRelayUi(t);
const { loading, busy, error, notice } = ui;
const items = ref<OpsChannel[]>([]),
  editing = ref(false),
  key = ref('');
const blank = (): OpsChannelInput => ({
  name: '',
  url: '',
  bearer: '',
  enabled: false,
  minSeverity: 'warning',
});
const draft = ref<OpsChannelInput>(blank());
const pending = ref<{ run: () => Promise<void>; clear: boolean }>();
function clear() {
  draft.value = blank();
  editing.value = false;
  key.value = '';
}
onBeforeUnmount(clear);
async function load() {
  const data = await ui.read('ops.notifications.read', () =>
    opsApi.channels(ui.signal),
  );
  items.value = data?.items ?? [];
  if (!data) clear();
}
onMounted(load);
watch(
  () => ui.can('ops.notifications.read'),
  (allowed) => {
    if (!allowed) {
      items.value = [];
      clear();
      pending.value = undefined;
    }
  },
);
watch(
  () => ui.can('ops.notifications.write'),
  (allowed) => {
    if (!allowed) {
      clear();
      pending.value = undefined;
    }
  },
);
function edit(row?: OpsChannel) {
  clear();
  key.value = row?.id ?? '';
  draft.value = {
    ...blank(),
    name: row?.name ?? '',
    enabled: row?.enabled ?? false,
    minSeverity: row?.minSeverity ?? 'warning',
  };
  editing.value = true;
}
function save() {
  const body = { ...draft.value },
    id = key.value;
  pending.value = {
    run: async () => {
      if (id) await opsApi.save(id, body, ui.signal);
      else await opsApi.create(body, ui.signal);
    },
    clear: true,
  };
}
function askTest(id: string) {
  pending.value = { run: () => opsApi.test(id, ui.signal), clear: false };
}
function askRemove(id: string) {
  pending.value = { run: () => opsApi.remove(id, ui.signal), clear: false };
}
async function confirm() {
  const p = pending.value;
  if (!p) return;
  await ui.write(
    'ops.notifications.write',
    p.run,
    () => {
      if (p.clear) clear();
    },
    load,
  );
  pending.value = undefined;
}
</script>
<template>
  <section class="management">
    <header class="head">
      <h1>{{ t('notifications') }}</h1>
      <button :disabled="loading || busy || !!pending" @click="load">
        {{ t('refresh') }}
      </button>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p class="muted">{{ t('smtp') }}</p>
    <p class="muted">{{ t('routeHint') }}</p>
    <button
      :disabled="
        busy || loading || !!pending || !ui.can('ops.notifications.write')
      "
      @click="edit()"
    >
      {{ t('new') }}
    </button>
    <table>
      <thead>
        <tr>
          <th>{{ t('name') }}</th>
          <th>{{ t('status') }}</th>
          <th>{{ t('minSeverity') }}</th>
          <th>{{ t('edit') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in items" :key="row.id">
          <td>{{ row.name }}</td>
          <td>
            {{ t('configured') }} / {{ row.enabled ? t('enabled') : '-' }}
          </td>
          <td>{{ t(row.minSeverity) }}</td>
          <td>
            <button
              :disabled="
                busy || !!pending || !ui.can('ops.notifications.write')
              "
              @click="edit(row)"
            >
              {{ t('edit') }}</button
            ><button
              :disabled="
                busy || !!pending || !ui.can('ops.notifications.write')
              "
              @click="askTest(row.id)"
            >
              {{ t('test') }}</button
            ><button
              :disabled="
                busy || !!pending || !ui.can('ops.notifications.write')
              "
              @click="askRemove(row.id)"
            >
              {{ t('remove') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-if="!loading && !error && !items.length">{{ t('empty') }}</p>
    <form v-if="editing" class="details edit-form" @submit.prevent="save">
      <fieldset
        :disabled="busy || !!pending || !ui.can('ops.notifications.write')"
      >
        <p>{{ t('secretHint') }}</p>
        <label class="label-row"
          >{{ t('name')
          }}<input
            v-model="draft.name"
            name="name"
            required
            maxlength="80" /></label
        ><label class="label-row"
          >{{ t('url')
          }}<input
            v-model="draft.url"
            name="url"
            type="url"
            required
            maxlength="2048"
            autocomplete="off" /></label
        ><label class="label-row"
          >{{ t('bearer')
          }}<input
            v-model="draft.bearer"
            name="bearer"
            type="password"
            maxlength="4096"
            autocomplete="new-password" /></label
        ><label class="label-row"
          ><input v-model="draft.enabled" type="checkbox" />{{
            t('enabled')
          }}</label
        ><label class="label-row"
          >{{ t('minSeverity')
          }}<select v-model="draft.minSeverity">
            <option value="warning">{{ t('warning') }}</option>
            <option value="critical">{{ t('critical') }}</option>
          </select></label
        ><button type="submit">{{ t('save') }}</button
        ><button type="button" @click="clear">{{ t('cancel') }}</button>
      </fieldset>
    </form>
    <ActionConfirm
      :message="pending ? t('confirmText') : undefined"
      :busy="busy"
      @confirm="confirm"
      @cancel="pending = undefined"
    />
  </section>
</template>
