<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue';
import {
  relayApi,
  type RelayKey,
  type RelayKeyInput,
  type RelayPool,
} from '../../admin/relayApi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import { useRelayI18n } from './relayMessages';
import { useRelayUi } from './relayUi';
import './modelUsage.css';
import './relayManagement.css';
const requestKey = ref('');
const { t } = useRelayI18n();
const ui = useRelayUi(t);
const { loading, busy, error, notice } = ui;
const keys = shallowRef<RelayKey[]>([]),
  pools = shallowRef<RelayPool[]>([]),
  editing = ref(false),
  selected = ref<RelayKey>();
const draft = ref<RelayKeyInput>({
    name: '',
    active: true,
    monthlyBudgetMicros: 0,
    requestsPerMinute: 60,
    maxConcurrency: 32,
    maxInFlightTokens: 134217728,
    models: [],
  }),
  allModels = ref(false);
const issued = ref(''),
  modelSearch = ref('');
const publishedPools = computed(() => pools.value.filter((p) => p.enabled));
const visibleModels = computed(() =>
  publishedPools.value.filter((p) =>
    p.id.toLowerCase().includes(modelSearch.value.toLowerCase()),
  ),
);
const historicalModels = computed(() =>
  draft.value.models.filter(
    (id) => id !== '*' && !publishedPools.value.some((p) => p.id === id),
  ),
);
const selectedModels = computed(() =>
  draft.value.models.filter((id) => id !== '*'),
);
function selectVisibleModels() {
  draft.value.models = [
    ...new Set([
      ...selectedModels.value,
      ...visibleModels.value.map((p) => p.id),
    ]),
  ];
}
function removeModel(id: string) {
  draft.value.models = draft.value.models.filter((v) => v !== id);
}
watch(allModels, (allowed) => {
  if (!allowed)
    draft.value.models = draft.value.models.filter((v) => v !== '*');
});
const pending = shallowRef<{ message: string; run: () => Promise<void> }>();
function hide() {
  issued.value = '';
}
watch(
  () => ui.can('ai.providers.read'),
  (allowed) => {
    if (!allowed) {
      hide();
      keys.value = [];
      pools.value = [];
      editing.value = false;
      pending.value = undefined;
    }
  },
);
watch(
  () => ui.can('ai.providers.write'),
  (allowed) => {
    if (!allowed) hide();
  },
);
onBeforeUnmount(() => {
  hide();
  pending.value = undefined;
});
async function load(preserve = false) {
  if (!preserve) hide();
  const result = await ui.read('ai.providers.read', () =>
    Promise.all([relayApi.keys(ui.signal), relayApi.pools(ui.signal)]),
  );
  if (result) {
    keys.value = result[0].items;
    pools.value = result[1].items;
  }
}
onMounted(() => load());
function edit(row?: RelayKey) {
  modelSearch.value = '';
  requestKey.value = globalThis.crypto.randomUUID();
  hide();
  selected.value = row;
  draft.value = row
    ? {
        name: row.name,
        active: row.active,
        monthlyBudgetMicros: row.monthlyBudgetMicros,
        requestsPerMinute: row.requestsPerMinute,
        maxConcurrency:
          row.maxConcurrency ?? (row.source === 'member' ? 16 : 32),
        maxInFlightTokens:
          row.maxInFlightTokens ??
          (row.source === 'member' ? 67108864 : 134217728),
        models: [...row.models],
      }
    : {
        name: '',
        active: true,
        monthlyBudgetMicros: 0,
        requestsPerMinute: 60,
        maxConcurrency: 32,
        maxInFlightTokens: 134217728,
        models: [],
      };
  allModels.value = row?.models.includes('*') ?? false;
  editing.value = true;
}
function input(): RelayKeyInput | undefined {
  const body = {
    ...draft.value,
    models: allModels.value ? ['*'] : [...draft.value.models],
  };
  if (
    !body.name.trim() ||
    body.name.trim() !== body.name ||
    !Number.isSafeInteger(body.monthlyBudgetMicros) ||
    body.monthlyBudgetMicros < 0 ||
    body.monthlyBudgetMicros > Number.MAX_SAFE_INTEGER ||
    !Number.isSafeInteger(body.requestsPerMinute) ||
    body.requestsPerMinute < 1 ||
    body.requestsPerMinute > 1000000 ||
    !Number.isSafeInteger(body.maxConcurrency) ||
    (body.maxConcurrency ?? 0) < 1 ||
    (body.maxConcurrency ?? 0) > 100 ||
    !Number.isSafeInteger(body.maxInFlightTokens) ||
    (body.maxInFlightTokens ?? 0) < 1 ||
    (body.maxInFlightTokens ?? 0) > 1073741824 ||
    body.models.length < 1
  ) {
    error.value = t('invalid');
    return;
  }
  return body;
}
function ask() {
  const body = input();
  if (!body) return;
  hide();
  const row = selected.value,
    key = requestKey.value;
  pending.value = {
    message: t(row ? 'confirmSave' : 'confirmIssue', { name: body.name }),
    run: () =>
      row
        ? ui.write(
            'ai.providers.write',
            () => relayApi.updateKey(row.id, body, ui.signal),
            () => {
              editing.value = false;
            },
            () => load(true),
          )
        : ui.write(
            'ai.providers.write',
            () => relayApi.createKey(body, key, ui.signal),
            (result) => {
              issued.value = result.secret;
              editing.value = false;
            },
            () => load(true),
          ),
  };
}
function revoke(row: RelayKey) {
  hide();
  const body: RelayKeyInput = {
    name: row.name,
    active: false,
    monthlyBudgetMicros: row.monthlyBudgetMicros,
    requestsPerMinute: row.requestsPerMinute,
    maxConcurrency: row.maxConcurrency ?? (row.source === 'member' ? 16 : 32),
    maxInFlightTokens:
      row.maxInFlightTokens ?? (row.source === 'member' ? 67108864 : 134217728),
    models: [...row.models],
  };
  pending.value = {
    message: t('confirmRevoke', {
      name: row.source === 'member' ? row.accountId : row.name,
    }),
    run: () =>
      ui.write(
        'ai.providers.write',
        () => relayApi.updateKey(row.id, body, ui.signal),
        () => {
          editing.value = false;
        },
        () => load(true),
      ),
  };
}
async function confirm() {
  const action = pending.value;
  if (!action || busy.value) return;
  try {
    await action.run();
  } finally {
    pending.value = undefined;
  }
}
</script>
<template>
  <section class="model-usage relay-keys">
    <header>
      <h1>{{ t('keys') }}</h1>
      <button :disabled="loading || busy || !!pending" @click="load()">
        {{ t('refresh') }}
      </button>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <button
      data-testid="new-key"
      :disabled="loading || busy || !!pending || !ui.can('ai.providers.write')"
      @click="edit()"
    >
      {{ t('createKey') }}
    </button>
    <aside v-if="issued" data-sensitive>
      <h2>{{ t('issued') }}</h2>
      <input
        :value="issued"
        data-testid="issued-key"
        readonly
        autocomplete="off"
        :aria-label="t('keys')"
      />
      <p>{{ t('issuedNote') }}</p>
      <button data-testid="hide-key" @click="hide">
        {{ t('clearSecret') }}
      </button>
    </aside>
    <div class="usage-table">
      <table>
        <thead>
          <tr>
            <th>{{ t('name') }}</th>
            <th>{{ t('keyId') }}</th>
            <th>{{ t('source') }}</th>
            <th>{{ t('status') }}</th>
            <th>{{ t('budget') }}</th>
            <th>{{ t('rpm') }}</th>
            <th>{{ t('models') }}</th>
            <th>{{ t('edit') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in keys" :key="row.id">
            <td>{{ row.source === 'member' ? row.accountId : row.name }}</td>
            <td>{{ row.id }}</td>
            <td>{{ t(row.source === 'member' ? 'member' : 'external') }}</td>
            <td>{{ t(row.active ? 'active' : 'inactive') }}</td>
            <td>{{ row.monthlyBudgetMicros }}</td>
            <td>{{ row.requestsPerMinute }}</td>
            <td>
              {{
                row.models.includes('*')
                  ? t('allModels')
                  : row.models.join(', ')
              }}
            </td>
            <td>
              <button
                data-testid="edit-key"
                :disabled="busy || !!pending || !ui.can('ai.providers.write')"
                @click="edit(row)"
              >
                {{ t('edit') }}</button
              ><button
                data-testid="revoke-key"
                :disabled="busy || !row.active || !ui.can('ai.providers.write')"
                @click="revoke(row)"
              >
                {{ t('revoke') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p>{{ t('externalKeyNote') }}</p>
    <form v-if="editing" data-testid="key-form" @submit.prevent="ask">
      <fieldset :disabled="busy || !!pending">
        <legend>{{ t(selected ? 'edit' : 'createKey') }}</legend>
        <label
          >{{ t('name')
          }}<input
            v-model="draft.name"
            name="keyName"
            :disabled="selected?.source === 'member'"
            required
            maxlength="128" /></label
        ><label
          ><input v-model="draft.active" name="keyActive" type="checkbox" />{{
            t('enabled')
          }}</label
        ><label
          >{{ t('budget')
          }}<input
            v-model.number="draft.monthlyBudgetMicros"
            name="keyBudget"
            type="number"
            min="0"
            max="9007199254740991"
            required
        /></label>
        <p>{{ t('budgetNote') }}</p>
        <p
          v-if="draft.monthlyBudgetMicros === 0"
          class="relay-hint relay-wide"
          role="note"
        >
          {{ t('keyZeroBudget') }}
        </p>
        <label
          >{{ t('rpm')
          }}<input
            v-model.number="draft.requestsPerMinute"
            name="keyRpm"
            type="number"
            min="1"
            max="1000000"
            required /></label
        ><label
          >{{ t('concurrency')
          }}<input
            v-model.number="draft.maxConcurrency"
            name="keyConcurrency"
            type="number"
            min="1"
            max="100"
            required
        /></label>
        <label
          >{{ t('inFlightTokens')
          }}<input
            v-model.number="draft.maxInFlightTokens"
            name="keyInFlightTokens"
            type="number"
            min="1"
            max="1073741824"
            required
        /></label>
        <div class="relay-model-picker relay-wide">
          <label class="relay-check"
            ><input v-model="allModels" name="allModels" type="checkbox" />{{
              t('keyAllFuture')
            }}</label
          >
          <p>{{ t('keyAllFutureNote') }}</p>
          <template v-if="!allModels">
            <div class="relay-toolbar">
              <label class="relay-search"
                >{{ t('keySearch')
                }}<input
                  v-model="modelSearch"
                  name="keyModelSearch"
                  type="search" /></label
              ><button
                type="button"
                data-testid="key-select-visible"
                @click="selectVisibleModels"
              >
                {{ t('keySelectVisible') }}</button
              ><button
                type="button"
                data-testid="key-clear-models"
                @click="draft.models = []"
              >
                {{ t('keyClear') }}
              </button>
            </div>
            <div
              class="relay-model-chips"
              :aria-label="t('keySelection', { count: selectedModels.length })"
            >
              <span>{{
                t('keySelection', { count: selectedModels.length })
              }}</span
              ><button
                v-for="id in selectedModels"
                :key="id"
                type="button"
                :aria-label="t('remove') + ' ' + id"
                @click="removeModel(id)"
              >
                {{ id }} ×
              </button>
            </div>
            <div
              class="relay-model-options"
              role="group"
              :aria-label="t('models')"
            >
              <label
                v-for="pool in visibleModels"
                :key="pool.id"
                class="relay-check"
                ><input
                  v-model="draft.models"
                  name="keyModels"
                  type="checkbox"
                  :value="pool.id"
                />{{ pool.id }}</label
              >
              <p v-if="!visibleModels.length">{{ t('empty') }}</p>
            </div>
            <div v-if="historicalModels.length" class="relay-hint">
              <p>{{ t('keyHistorical') }}</p>
              <label
                v-for="id in historicalModels"
                :key="id"
                class="relay-check"
                ><input v-model="draft.models" type="checkbox" :value="id" />{{
                  id
                }}</label
              >
            </div>
          </template>
        </div>
        <button type="submit">{{ t('save') }}</button
        ><button type="button" @click="editing = false">
          {{ t('close') }}
        </button>
      </fieldset>
    </form>
    <p>{{ t('revokeNote') }}</p>
    <p>{{ t('limitNote') }}</p>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="pending = undefined"
    />
  </section>
</template>
