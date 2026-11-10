<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
} from 'vue';
import { isApiError } from '@auriga/shared';
import {
  relayApi,
  type RelayProvider,
  type RelayProviderInput,
  type RelayPool,
} from '../../admin/relayApi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import AdminDialog from '../../admin/AdminDialog.vue';
import { useRelayI18n } from './relayMessages';
import { useRelayUi } from './relayUi';
import { useRelayWriteIntent, type RelayWriteAction } from './relayWriteIntent';
import './modelUsage.css';
import './relayManagement.css';
import ModelImportPanel from './ModelImportPanel.vue';
import RelayWriteHint from './RelayWriteHint.vue';
import PoolCapabilitiesEditor from './PoolCapabilitiesEditor.vue';
import {
  createCapabilityPolicy,
  normalizeBlankCapabilities,
  reconcileCapabilitySource,
  validCapabilityPolicy,
  type CatalogState,
} from './poolCapabilities';
const importProvider = ref(''),
  poolSearch = ref(''),
  capabilityNotice = ref('');
const importSession = ref(0),
  importRunning = ref(false);
const { t } = useRelayI18n();
const ui = useRelayUi(t);
const writeIntent = useRelayWriteIntent(ui);
const { unresolved } = writeIntent;
const { loading, busy, error, notice } = ui;
const providers = shallowRef<RelayProvider[]>([]),
  pools = shallowRef<RelayPool[]>([]);
const editor = ref<'provider' | 'pool' | 'credential' | ''>(''),
  selected = ref(''),
  secret = ref('');
const provider = ref<RelayProviderInput>({
  name: '',
  kind: 'openai-compatible',
  baseUrl: '',
  enabled: true,
});
const draft = ref<RelayPool>();
const poolsReady = ref(false);
const pending = shallowRef<RelayWriteAction>();
function canPrepare() {
  if (busy.value || pending.value) return false;
  if (unresolved.value) {
    error.value = t('writeUncertain');
    return false;
  }
  return true;
}
const catalogs = ref<Record<string, CatalogState>>({});
const catalogLoading = computed(() =>
  Object.values(catalogs.value).some((catalog) => catalog.loading),
);
const catalogRequests = new Set<globalThis.AbortController>();
let catalogEpoch = 0;
function clearCatalogs() {
  catalogEpoch++;
  for (const controller of catalogRequests) controller.abort();
  catalogRequests.clear();
  catalogs.value = {};
}
async function fetchModels(providerId: string) {
  if (
    !providerId ||
    busy.value ||
    pending.value ||
    catalogs.value[providerId]?.loading ||
    !ui.can('ai.providers.read')
  )
    return;
  const epoch = catalogEpoch;
  const controller = new globalThis.AbortController();
  catalogRequests.add(controller);
  catalogs.value[providerId] = { items: [], loading: true, error: '' };
  try {
    const result = await relayApi.providerModels(providerId, controller.signal);
    if (epoch !== catalogEpoch) return;
    catalogs.value[providerId] = {
      items: result.items,
      loading: false,
      error: '',
    };
  } catch (e) {
    if (epoch !== catalogEpoch) return;
    const reason = isApiError(e) ? e.problem.params?.reason : undefined;
    const key =
      typeof reason === 'string' &&
      [
        'credential_missing',
        'provider_disabled',
        'unsupported',
        'upstream_unauthorized',
        'upstream_rate_limited',
        'upstream_timeout',
        'upstream_invalid_response',
        'catalog_too_large',
      ].includes(reason)
        ? 'catalog_' + reason
        : 'catalog_failed';
    catalogs.value[providerId] = {
      items: [],
      loading: false,
      error:
        isApiError(e) && [401, 403].includes(e.status)
          ? t('forbidden')
          : t(key),
    };
  } finally {
    catalogRequests.delete(controller);
    if (
      epoch === catalogEpoch &&
      !catalogLoading.value &&
      capabilityNotice.value === t('capabilityRefreshPending')
    )
      capabilityNotice.value = '';
  }
}
function providerChanged(index: number) {
  if (draft.value) draft.value.routes[index]!.model = '';
}
function modelChanged(model: string) {
  if (draft.value && !draft.value.id && !selected.value) {
    const proposed = model.replace(/[^A-Za-z0-9._-]/g, '-').slice(0, 128);
    if (/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(proposed))
      draft.value.id = proposed;
  }
}
const freshPool = (): RelayPool => ({
  id: '',
  enabled: true,
  contextWindow: 0,
  maxOutputTokens: 0,
  capabilities: {
    sourceProviderId: '',
    sourceModel: '',
    contextWindowMode: 'inherit',
    maxOutputTokensMode: 'inherit',
  },
  routes: [],
  price: {
    currency: 'USD',
    inputMicrosPerMillionTokens: 0,
    outputMicrosPerMillionTokens: 0,
    version: globalThis.crypto.randomUUID(),
    effectiveAt: new Date().toISOString(),
  },
});
async function load() {
  poolsReady.value = false;
  if (draft.value?.id === 'auto' && !unresolved.value) {
    close();
    pending.value = undefined;
  }
  const result = await ui.read('ai.providers.read', () =>
    Promise.all([relayApi.providers(ui.signal), relayApi.pools(ui.signal)]),
  );
  if (result) {
    providers.value = result[0].items;
    pools.value = result[1].items;
    poolsReady.value = true;
  }
}
onMounted(load);
onBeforeUnmount(() => {
  clearCatalogs();
  secret.value = '';
  pending.value = undefined;
});
// An open editor hides the page behind its modal: the uncertain-write notice
// moves into the dialog, and the dialog cannot be dismissed (which would reset
// the write intent) while a write runs, awaits confirmation or is unresolved.
const dialogOpen = computed(() => !!importProvider.value || !!editor.value);
const dialogLocked = computed(
  () => busy.value || !!pending.value || !!unresolved.value,
);
function close() {
  writeIntent.reset();
  importProvider.value = '';
  importRunning.value = false;
  capabilityNotice.value = '';
  clearCatalogs();
  editor.value = '';
  secret.value = '';
  draft.value = undefined;
}
watch(
  () => ui.can('ai.providers.read'),
  (allowed) => {
    if (!allowed) {
      poolsReady.value = false;
      providers.value = [];
      pools.value = [];
      close();
      pending.value = undefined;
    }
  },
);
watch(
  () => ui.can('ai.credentials.rotate'),
  (allowed) => {
    if (!allowed && editor.value === 'credential') {
      close();
      pending.value = undefined;
    }
  },
);
function editProvider(row?: RelayProvider) {
  if (!canPrepare()) return;
  close();
  selected.value = row?.id ?? '';
  provider.value = row
    ? { ...row.configuration }
    : { name: '', kind: 'openai-compatible', baseUrl: '', enabled: true };
  editor.value = 'provider';
}
function editPool(row?: RelayPool) {
  if (!canPrepare()) return;
  // A failed or in-flight snapshot never proves that auto is absent.
  if (!poolsReady.value || loading.value) return;
  close();
  selected.value = row?.id ?? '';
  draft.value = row ? globalThis.structuredClone(row) : freshPool();
  draft.value.capabilities ??= createCapabilityPolicy(draft.value, 'manual');
  editor.value = 'pool';
}
function editCredential(row: RelayProvider) {
  if (!canPrepare()) return;
  close();
  selected.value = row.id;
  provider.value = { ...row.configuration };
  editor.value = 'credential';
}
function askProvider() {
  if (!canPrepare()) return;
  const body = { ...provider.value };
  if (!body.name.trim() || body.name !== body.name.trim()) {
    error.value = t('invalid');
    return;
  }
  const id = selected.value;
  pending.value = writeIntent.createAction(
    'ai.providers.write',
    t('confirmSave', { name: body.name }),
    (key) => relayApi.saveProvider(id, body, key, ui.signal),
    close,
    load,
  );
}
function askCredential() {
  if (!canPrepare()) return;
  if (!secret.value.trim()) {
    error.value = t('invalid');
    return;
  }
  const value = secret.value,
    id = selected.value;
  secret.value = '';
  pending.value = writeIntent.createAction(
    'ai.credentials.rotate',
    t('confirmSecret', { name: provider.value.name }),
    (key) => relayApi.credential(id, value, key, ui.signal),
    close,
    load,
  );
}
function askPool() {
  if (!draft.value || !canPrepare()) return;
  if (catalogLoading.value) {
    capabilityNotice.value = t('capabilityRefreshPending');
    return;
  }
  if (draft.value.id === 'auto' && (!poolsReady.value || loading.value)) return;
  const createOnly = draft.value.id === 'auto' && !selected.value;
  const body = JSON.parse(JSON.stringify(draft.value)) as RelayPool;
  normalizeBlankCapabilities(body);
  body.routes.forEach((route) => {
    route.model = route.model.trim();
  });
  if (body.capabilities) reconcileCapabilitySource(body);
  const routeIdentities = new Set<string>();
  const duplicate = body.routes.findIndex((route) => {
    const identity = JSON.stringify([route.providerId, route.model]);
    if (routeIdentities.has(identity)) return true;
    routeIdentities.add(identity);
    return false;
  });
  if (duplicate >= 0) {
    error.value = t('duplicateRoute', { number: duplicate + 1 });
    return;
  }
  const ints = [
    body.contextWindow,
    body.maxOutputTokens,
    body.price.inputMicrosPerMillionTokens,
    body.price.outputMicrosPerMillionTokens,
  ];
  if (
    !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(body.id) ||
    !validCapabilityPolicy(body) ||
    ints.some((v) => !Number.isSafeInteger(v) || v < 0) ||
    body.contextWindow === 1 ||
    (body.contextWindow > 0 && body.maxOutputTokens >= body.contextWindow) ||
    body.price.inputMicrosPerMillionTokens < 1 ||
    body.price.outputMicrosPerMillionTokens < 1 ||
    body.routes.length < 1 ||
    body.routes.length > 8 ||
    body.routes.some(
      (r) =>
        !r.providerId ||
        !r.model.trim() ||
        !Number.isFinite(r.weight) ||
        r.weight < 0,
    ) ||
    body.routes.every((r) => r.weight === 0)
  ) {
    error.value = t('invalid');
    return;
  }
  pending.value = writeIntent.createAction(
    'ai.providers.write',
    t('confirmSave', { name: body.id }),
    (key) =>
      createOnly
        ? relayApi.createPool(body, key, ui.signal)
        : relayApi.savePool(body, key, ui.signal),
    close,
    load,
  );
}
const visiblePools = computed(() => {
  const query = poolSearch.value.toLowerCase();
  return pools.value.filter((pool) =>
    [
      pool.id,
      ...pool.routes.flatMap((r) => [
        r.model,
        providers.value.find((p) => p.id === r.providerId)?.configuration
          .name ?? '',
      ]),
    ].some((value) => value.toLowerCase().includes(query)),
  );
});
function openImport(row: RelayProvider) {
  if (importRunning.value || !canPrepare()) return;
  close();
  importSession.value++;
  importProvider.value = row.id;
}
function accessChanged(permissions: string[]) {
  ui.permissions.value = permissions;
}
async function applyCapabilities() {
  if (pending.value || busy.value) return;
  const policy = draft.value?.capabilities;
  if (!policy?.sourceProviderId || !policy.sourceModel) return;
  const providerId = policy.sourceProviderId,
    model = policy.sourceModel;
  await fetchModels(providerId);
  if (
    draft.value?.capabilities !== policy ||
    policy.sourceProviderId !== providerId ||
    policy.sourceModel !== model
  )
    return;
  capabilityNotice.value = t(
    catalogs.value[policy.sourceProviderId]?.error
      ? 'capabilityRefreshFailed'
      : 'capabilityApplied',
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
  <section class="model-usage relay-providers">
    <header>
      <h1>{{ t('providers') }}</h1>
      <button :disabled="loading || busy || !!pending" @click="load">
        {{ t('refresh') }}
      </button>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <RelayWriteHint
      v-if="unresolved && !dialogOpen"
      :disabled="busy || !!pending || !ui.can(unresolved.permission)"
      @retry="pending = unresolved"
    />
    <p>{{ t('providersIntro') }}</p>
    <div class="usage-filters">
      <button
        data-testid="add-provider"
        :disabled="
          loading ||
          busy ||
          !!pending ||
          !!unresolved ||
          !ui.can('ai.providers.write')
        "
        @click="editProvider()"
      >
        {{ t('addProvider') }}</button
      ><button
        data-testid="add-pool"
        :disabled="
          !poolsReady ||
          loading ||
          busy ||
          !!pending ||
          !!unresolved ||
          !ui.can('ai.providers.write')
        "
        @click="editPool()"
      >
        {{ t('addPool') }}
      </button>
    </div>
    <h2>{{ t('providerSection') }}</h2>
    <div class="usage-table">
      <table>
        <thead>
          <tr>
            <th>{{ t('name') }}</th>
            <th>{{ t('kind') }}</th>
            <th>{{ t('url') }}</th>
            <th>{{ t('status') }}</th>
            <th>{{ t('credential') }}</th>
            <th>{{ t('edit') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in providers" :key="row.id">
            <td class="relay-strong">{{ row.configuration.name }}</td>
            <td>{{ row.configuration.kind }}</td>
            <td>{{ row.configuration.baseUrl }}</td>
            <td>
              <span
                class="relay-badge"
                :data-state="row.configuration.enabled ? 'on' : 'off'"
                >{{
                  t(row.configuration.enabled ? 'enabled' : 'disabled')
                }}</span
              >
            </td>
            <td :class="{ 'relay-error': !row.hasCredential }">
              {{
                t(row.hasCredential ? 'credentialReady' : 'credentialMissing')
              }}
            </td>
            <td>
              <button
                data-testid="import-provider"
                :disabled="
                  busy ||
                  importRunning ||
                  !!pending ||
                  !!unresolved ||
                  !ui.can('ai.providers.write') ||
                  !row.hasCredential ||
                  !row.configuration.enabled
                "
                @click="openImport(row)"
              >
                {{ t('importModels') }}
              </button>
              <button
                data-testid="edit-provider"
                :disabled="
                  busy ||
                  !!pending ||
                  !!unresolved ||
                  !ui.can('ai.providers.write')
                "
                @click="editProvider(row)"
              >
                {{ t('edit') }}</button
              ><button
                :disabled="
                  busy ||
                  !!pending ||
                  !!unresolved ||
                  !ui.can('ai.credentials.rotate')
                "
                @click="editCredential(row)"
              >
                {{ t('credential') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <AdminDialog
      :open="!!importProvider"
      :locked="importRunning || dialogLocked"
      :title="t('importModels')"
      width="min(1100px, 94vw)"
      @close="close"
    >
      <ModelImportPanel
        v-if="importProvider"
        :key="importSession"
        :providers="providers"
        :pools="pools"
        :provider-id="importProvider"
        :permissions="ui.permissions.value"
        @close="close"
        @imported="load"
        @access="accessChanged"
        @running="importRunning = $event"
      />
    </AdminDialog>
    <AdminDialog
      :open="editor === 'provider'"
      :locked="dialogLocked"
      :title="t(selected ? 'edit' : 'addProvider')"
      @close="close"
    >
      <form
        v-if="editor === 'provider'"
        data-testid="save-provider"
        @submit.prevent="askProvider"
      >
        <RelayWriteHint
          v-if="unresolved"
          :disabled="busy || !!pending || !ui.can(unresolved.permission)"
          @retry="pending = unresolved"
        />
        <fieldset :disabled="busy || !!pending || !!unresolved">
          <legend>{{ t(selected ? 'edit' : 'addProvider') }}</legend>
          <label
            >{{ t('name')
            }}<input
              v-model="provider.name"
              name="providerName"
              required
              maxlength="128" /></label
          ><label
            >{{ t('kind')
            }}<select v-model="provider.kind" name="providerKind">
              <option
                v-for="kind in [
                  'openai-compatible',
                  'openai',
                  'minimax',
                  'deepseek',
                  'anthropic',
                  'gemini',
                ] as const"
                :key="kind"
                :value="kind"
              >
                {{ kind }}
              </option>
            </select></label
          ><label
            >{{ t('url')
            }}<input
              v-model="provider.baseUrl"
              name="providerUrl"
              type="url"
              required
          /></label>
          <p>{{ t('apiRootNote') }}</p>
          <label
            ><input
              v-model="provider.enabled"
              name="providerEnabled"
              type="checkbox"
            />{{ t('enabled') }}</label
          ><button type="submit">{{ t('save') }}</button
          ><button type="button" @click="close">{{ t('close') }}</button>
        </fieldset>
      </form>
    </AdminDialog>
    <AdminDialog
      :open="editor === 'credential'"
      :locked="dialogLocked"
      :title="t('credential') + ' · ' + provider.name"
      width="520px"
      @close="close"
    >
      <form v-if="editor === 'credential'" @submit.prevent="askCredential">
        <RelayWriteHint
          v-if="unresolved"
          :disabled="busy || !!pending || !ui.can(unresolved.permission)"
          @retry="pending = unresolved"
        />
        <fieldset :disabled="busy || !!pending || !!unresolved">
          <legend>{{ t('credential') }} · {{ provider.name }}</legend>
          <p>{{ t('secretNote') }}</p>
          <label
            >{{ t('secret')
            }}<input
              v-model="secret"
              name="providerSecret"
              type="password"
              data-sensitive
              autocomplete="new-password"
              required /></label
          ><button type="submit">{{ t('save') }}</button
          ><button type="button" @click="close">{{ t('close') }}</button>
        </fieldset>
      </form>
    </AdminDialog>
    <h2>{{ t('pools') }}</h2>
    <p>{{ t('poolNote') }}</p>
    <label class="relay-search"
      >{{ t('publicSearch')
      }}<input
        v-model="poolSearch"
        name="poolSearch"
        type="search"
        :placeholder="t('publicSearch')"
    /></label>
    <p v-if="!pools.length && !loading">{{ t('modelEmpty') }}</p>
    <div class="usage-table">
      <table>
        <thead>
          <tr>
            <th>{{ t('poolId') }}</th>
            <th>{{ t('status') }}</th>
            <th>{{ t('routes') }}</th>
            <th>{{ t('capabilityColumn') }}</th>
            <th>{{ t('edit') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="pool in visiblePools" :key="pool.id" :data-pool="pool.id">
            <td class="relay-strong">
              {{ pool.id
              }}<span
                v-if="pool.id === 'auto'"
                class="relay-badge relay-default"
                >{{ t('defaultPool') }}</span
              >
            </td>
            <td>
              <span
                class="relay-badge"
                :data-state="pool.enabled ? 'on' : 'off'"
                >{{ t(pool.enabled ? 'enabled' : 'disabled') }}</span
              >
            </td>
            <td>
              <div
                v-for="route in pool.routes"
                :key="JSON.stringify([route.providerId, route.model])"
              >
                {{
                  providers.find((p) => p.id === route.providerId)
                    ?.configuration.name ?? route.providerId
                }}
                / {{ route.model }} · {{ route.weight }}
              </div>
            </td>
            <td>
              {{
                pool.contextWindow
                  ? pool.contextWindow.toLocaleString()
                  : t('unknownContext')
              }}
              /
              {{
                pool.maxOutputTokens
                  ? pool.maxOutputTokens.toLocaleString()
                  : t('unknown')
              }}
            </td>
            <td>
              <button
                data-testid="edit-pool"
                :disabled="
                  busy ||
                  !!pending ||
                  !!unresolved ||
                  !ui.can('ai.providers.write') ||
                  !poolsReady ||
                  loading
                "
                @click="editPool(pool)"
              >
                {{ t('edit') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <AdminDialog
      :open="editor === 'pool' && !!draft"
      :locked="dialogLocked"
      :title="
        t(selected ? 'edit' : 'addPool') + (draft?.id ? ' · ' + draft.id : '')
      "
      width="min(920px, 94vw)"
      @close="close"
    >
      <form
        v-if="editor === 'pool' && draft"
        data-testid="pool-form"
        @submit.prevent="askPool"
      >
        <RelayWriteHint
          v-if="unresolved"
          :disabled="busy || !!pending || !ui.can(unresolved.permission)"
          @retry="pending = unresolved"
        />
        <fieldset :disabled="busy || !!pending || !!unresolved">
          <legend>{{ t('pools') }}</legend>
          <p>{{ t('aliasNote') }}</p>
          <label
            >{{ t('poolId')
            }}<input
              v-model="draft.id"
              name="poolId"
              :disabled="!!selected"
              pattern="[A-Za-z0-9][A-Za-z0-9._-]{0,127}"
              required /></label
          ><label
            ><input
              v-model="draft.enabled"
              name="poolEnabled"
              type="checkbox"
            />{{ t('enabled') }}</label
          >
          <PoolCapabilitiesEditor
            v-model="draft"
            :providers="providers"
            :catalogs="catalogs"
            @refresh="applyCapabilities"
          />
          <p v-if="capabilityNotice" class="relay-wide" role="status">
            {{ capabilityNotice }}
          </p>
          <div
            v-for="(route, index) in draft.routes"
            :key="index"
            class="usage-filters relay-route"
          >
            <strong class="relay-route-title">{{
              t('routeNumber', { number: index + 1 })
            }}</strong>
            <label
              >{{ t('providers')
              }}<select
                v-model="route.providerId"
                :name="'routeProvider' + index"
                required
                @change="providerChanged(index)"
              >
                <option disabled value="">{{ t('providers') }}</option>
                <option v-for="p in providers" :key="p.id" :value="p.id">
                  {{ p.configuration.name }}
                </option>
              </select></label
            ><label
              >{{ t('model')
              }}<input
                v-model="route.model"
                :name="'routeModel' + index"
                :list="'route-model-catalog-' + index"
                required
                maxlength="256"
                @change="modelChanged(route.model)"
              />
              <datalist :id="'route-model-catalog-' + index">
                <option
                  v-for="model in catalogs[route.providerId]?.items ?? []"
                  :key="model.id"
                  :value="model.id"
                />
              </datalist>
            </label>
            <label
              >{{ t('weight')
              }}<input
                v-model.number="route.weight"
                :name="'routeWeight' + index"
                type="number"
                min="0"
                max="1000"
                step="any"
                required
            /></label>
            <div class="relay-route-actions">
              <button
                type="button"
                :data-testid="'fetch-provider-models-' + index"
                :disabled="
                  !route.providerId || catalogs[route.providerId]?.loading
                "
                @click="fetchModels(route.providerId)"
              >
                {{
                  t(
                    catalogs[route.providerId]?.loading
                      ? 'loading'
                      : 'fetchModels',
                  )
                }}
              </button>
              <button type="button" @click="draft.routes.splice(index, 1)">
                {{ t('remove') }}
              </button>
              <p v-if="route.model && draft.id" data-testid="model-mapping">
                {{ route.model }} → {{ draft.id }}
              </p>
            </div>
            <p class="relay-route-hint">{{ t('catalogHint') }}</p>
            <p v-if="catalogs[route.providerId]?.error" role="alert">
              {{ catalogs[route.providerId]?.error }}
            </p>
            <p
              v-else-if="
                catalogs[route.providerId] &&
                !catalogs[route.providerId]?.loading &&
                !catalogs[route.providerId]?.items.length
              "
            >
              {{ t('catalogEmpty') }}
            </p>
          </div>
          <button
            type="button"
            :disabled="draft.routes.length >= 8"
            @click="draft.routes.push({ providerId: '', model: '', weight: 1 })"
          >
            {{ t('addRoute') }}</button
          ><label
            >{{ t('priceInput')
            }}<input
              v-model.number="draft.price.inputMicrosPerMillionTokens"
              name="inputPrice"
              type="number"
              min="1"
              max="9007199254740991"
              required /></label
          ><label
            >{{ t('priceOutput')
            }}<input
              v-model.number="draft.price.outputMicrosPerMillionTokens"
              name="outputPrice"
              type="number"
              min="1"
              max="9007199254740991"
              required
          /></label>
          <p class="relay-wide">{{ t('priceNote') }}</p>
          <button
            type="submit"
            data-testid="save-pool"
            :disabled="catalogLoading"
          >
            {{ t('save') }}</button
          ><button type="button" @click="close">{{ t('close') }}</button>
        </fieldset>
      </form>
    </AdminDialog>
    <p>{{ t('limitNote') }}</p>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="pending = undefined"
    />
  </section>
</template>
