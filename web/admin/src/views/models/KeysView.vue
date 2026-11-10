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
import RelayWriteHint from './RelayWriteHint.vue';
import AdminDialog from '../../admin/AdminDialog.vue';
import { useRelayI18n } from './relayMessages';
import { useRelayUi } from './relayUi';
import { useRelayWriteIntent, type RelayWriteAction } from './relayWriteIntent';
import {
  formatUsd,
  microsFromUsd,
  shortId,
  usdFromMicros,
  useMemberNames,
} from './relayFormat';
import './modelUsage.css';
import './relayManagement.css';
const { t } = useRelayI18n();
const ui = useRelayUi(t);
const writeIntent = useRelayWriteIntent(ui);
const { unresolved } = writeIntent;
const { loading, busy, error, notice } = ui;
const members = useMemberNames();
const budgetUsd = ref<number | ''>(0);
const keys = shallowRef<RelayKey[]>([]),
  pools = shallowRef<RelayPool[]>([]),
  editing = ref(false),
  selected = ref<RelayKey>();
const draft = ref<RelayKeyInput>({
    name: '',
    active: true,
    monthlyBudgetMicros: 0,
    requestsPerMinute: 0,
    maxConcurrency: 0,
    maxInFlightTokens: 0,
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
const pending = shallowRef<RelayWriteAction>();
function canPrepare() {
  if (busy.value || pending.value) return false;
  if (unresolved.value) {
    error.value = t('writeUncertain');
    return false;
  }
  return true;
}
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
      writeIntent.reset();
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
onMounted(() => {
  void load();
  void members.load(ui.signal);
});
function edit(row?: RelayKey) {
  if (!canPrepare()) return;
  writeIntent.reset();
  modelSearch.value = '';
  hide();
  selected.value = row;
  draft.value = row
    ? {
        name: row.name,
        active: row.active,
        monthlyBudgetMicros: row.monthlyBudgetMicros,
        requestsPerMinute: row.requestsPerMinute,
        maxConcurrency: row.maxConcurrency ?? 0,
        maxInFlightTokens: row.maxInFlightTokens ?? 0,
        meteringOnly: row.meteringOnly ?? false,
        models: [...row.models],
      }
    : {
        name: '',
        active: true,
        monthlyBudgetMicros: 0,
        requestsPerMinute: 0,
        maxConcurrency: 0,
        maxInFlightTokens: 0,
        models: [],
      };
  budgetUsd.value = row ? usdFromMicros(row.monthlyBudgetMicros) : 0;
  allModels.value = row?.models.includes('*') ?? false;
  editing.value = true;
}
function input(): RelayKeyInput | undefined {
  const budget = draft.value.meteringOnly
    ? draft.value.monthlyBudgetMicros
    : microsFromUsd(budgetUsd.value);
  if (budget === null) {
    error.value = t('invalid');
    return;
  }
  const body = {
    ...draft.value,
    monthlyBudgetMicros: budget,
    models: allModels.value ? ['*'] : [...draft.value.models],
  };
  if (
    !body.name.trim() ||
    body.name.trim() !== body.name ||
    !Number.isSafeInteger(body.monthlyBudgetMicros) ||
    body.monthlyBudgetMicros < 0 ||
    body.monthlyBudgetMicros > Number.MAX_SAFE_INTEGER ||
    !Number.isSafeInteger(body.requestsPerMinute) ||
    body.requestsPerMinute < 0 ||
    body.requestsPerMinute > 1000000 ||
    !Number.isSafeInteger(body.maxConcurrency) ||
    (body.maxConcurrency ?? 0) < 0 ||
    (body.maxConcurrency ?? 0) > 100 ||
    !Number.isSafeInteger(body.maxInFlightTokens) ||
    (body.maxInFlightTokens ?? 0) < 0 ||
    (body.maxInFlightTokens ?? 0) > 1073741824 ||
    body.models.length < 1
  ) {
    error.value = t('invalid');
    return;
  }
  return body;
}
function ask() {
  if (!canPrepare()) return;
  const body = input();
  if (!body) return;
  hide();
  const row = selected.value;
  const message = t(row ? 'confirmSave' : 'confirmIssue', { name: body.name });
  pending.value = row
    ? writeIntent.createAction(
        'ai.providers.write',
        message,
        () => relayApi.updateKey(row.id, body, ui.signal),
        () => {
          editing.value = false;
        },
        () => load(true),
      )
    : writeIntent.createAction(
        'ai.providers.write',
        message,
        (key) => relayApi.createKey(body, key, ui.signal),
        (result) => {
          issued.value = result.secret;
          editing.value = false;
        },
        () => load(true),
      );
}
function revoke(row: RelayKey) {
  if (!canPrepare()) return;
  hide();
  const body: RelayKeyInput = {
    name: row.name,
    active: false,
    monthlyBudgetMicros: row.monthlyBudgetMicros,
    requestsPerMinute: row.requestsPerMinute,
    maxConcurrency: row.maxConcurrency ?? 0,
    maxInFlightTokens: row.maxInFlightTokens ?? 0,
    meteringOnly: row.meteringOnly ?? false,
    models: [...row.models],
  };
  pending.value = writeIntent.createAction(
    'ai.providers.write',
    t('confirmRevoke', {
      name: members.keyLabel(row),
    }),
    () => relayApi.updateKey(row.id, body, ui.signal),
    () => {
      editing.value = false;
    },
    () => load(true),
  );
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
    <RelayWriteHint
      v-if="unresolved && !editing"
      :disabled="busy || !!pending || !ui.can(unresolved.permission)"
      @retry="pending = unresolved"
    />
    <button
      data-testid="new-key"
      :disabled="
        loading ||
        busy ||
        !!pending ||
        !!unresolved ||
        !ui.can('ai.providers.write')
      "
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
            <td class="relay-strong">{{ members.keyLabel(row) }}</td>
            <td class="relay-mono" :title="row.id">{{ shortId(row.id) }}</td>
            <td>{{ t(row.source === 'member' ? 'member' : 'external') }}</td>
            <td>
              <span
                class="relay-badge"
                :data-state="row.active ? 'on' : 'off'"
                >{{ t(row.active ? 'active' : 'inactive') }}</span
              >
            </td>
            <td>
              {{
                row.meteringOnly
                  ? t('meteringOnly')
                  : (formatUsd(row.monthlyBudgetMicros) ?? t('unknown'))
              }}
            </td>
            <td>
              {{
                row.meteringOnly || !row.requestsPerMinute
                  ? t('unlimited')
                  : row.requestsPerMinute
              }}
            </td>
            <td>
              {{
                row.models.includes('*')
                  ? t('allModels')
                  : row.models.join(', ')
              }}
            </td>
            <td class="relay-row-actions">
              <RouterLink
                data-testid="key-usage"
                :to="{ path: '/models/usage', query: { key: row.id } }"
                >{{ t('usage') }}</RouterLink
              ><button
                data-testid="edit-key"
                :disabled="
                  busy ||
                  !!pending ||
                  !!unresolved ||
                  !ui.can('ai.providers.write')
                "
                @click="edit(row)"
              >
                {{ t('edit') }}</button
              ><button
                data-testid="revoke-key"
                :disabled="
                  busy ||
                  !!pending ||
                  !!unresolved ||
                  !row.active ||
                  !ui.can('ai.providers.write')
                "
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
    <AdminDialog
      :open="editing"
      :locked="busy || !!pending || !!unresolved"
      :title="t(selected ? 'edit' : 'createKey')"
      width="min(760px, 94vw)"
      @close="editing = false"
    >
      <form v-if="editing" data-testid="key-form" @submit.prevent="ask">
        <RelayWriteHint
          v-if="unresolved"
          :disabled="busy || !!pending || !ui.can(unresolved.permission)"
          @retry="pending = unresolved"
        />
        <fieldset :disabled="busy || !!pending || !!unresolved">
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
            v-if="selected?.source === 'member'"
            class="relay-check relay-wide"
          >
            <input
              v-model="draft.meteringOnly"
              name="memberMeteringOnly"
              type="checkbox"
            />{{ t('meteringOnly') }}
          </label>
          <p v-if="selected?.source === 'member'" class="relay-wide">
            {{ t('meteringOnlyNote') }}
          </p>
          <label
            >{{ t('budget')
            }}<input
              v-model.number="budgetUsd"
              name="keyBudget"
              type="number"
              min="0"
              step="0.000001"
              max="9007199254.740991"
              :disabled="draft.meteringOnly"
              required
          /></label>
          <p>{{ t('budgetNote') }}</p>
          <p
            v-if="budgetUsd === 0 && !draft.meteringOnly"
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
              min="0"
              max="1000000"
              :disabled="draft.meteringOnly"
              required /></label
          ><label
            >{{ t('concurrency')
            }}<input
              v-model.number="draft.maxConcurrency"
              name="keyConcurrency"
              type="number"
              min="0"
              max="100"
              :disabled="draft.meteringOnly"
              required
          /></label>
          <label
            >{{ t('inFlightTokens')
            }}<input
              v-model.number="draft.maxInFlightTokens"
              name="keyInFlightTokens"
              type="number"
              min="0"
              max="1073741824"
              :disabled="draft.meteringOnly"
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
                :aria-label="
                  t('keySelection', { count: selectedModels.length })
                "
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
                  ><input
                    v-model="draft.models"
                    type="checkbox"
                    :value="id"
                  />{{ id }}</label
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
    </AdminDialog>
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
