<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue';
import {
  relayApi,
  type RelayProvider,
  type RelayProviderInput,
  type RelayPool,
} from '../../admin/relayApi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import { useRelayI18n } from './relayMessages';
import { useRelayUi } from './relayUi';
import './modelUsage.css';
const requestKey = ref('');
const { t } = useRelayI18n();
const ui = useRelayUi(t);
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
const pending = shallowRef<{ message: string; run: () => Promise<void> }>();
const freshPool = (): RelayPool => ({
  id: '',
  enabled: true,
  contextWindow: 32768,
  maxOutputTokens: 4096,
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
  const result = await ui.read('ai.providers.read', () =>
    Promise.all([relayApi.providers(ui.signal), relayApi.pools(ui.signal)]),
  );
  if (result) {
    providers.value = result[0].items;
    pools.value = result[1].items;
  }
}
onMounted(load);
onBeforeUnmount(() => {
  secret.value = '';
  pending.value = undefined;
});
function close() {
  editor.value = '';
  secret.value = '';
  draft.value = undefined;
}
watch(
  () => ui.can('ai.providers.read'),
  (allowed) => {
    if (!allowed) {
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
  requestKey.value = globalThis.crypto.randomUUID();
  close();
  selected.value = row?.id ?? '';
  provider.value = row
    ? { ...row.configuration }
    : { name: '', kind: 'openai-compatible', baseUrl: '', enabled: true };
  editor.value = 'provider';
}
function editPool(row?: RelayPool) {
  requestKey.value = globalThis.crypto.randomUUID();
  close();
  selected.value = row?.id ?? '';
  draft.value = row ? globalThis.structuredClone(row) : freshPool();
  editor.value = 'pool';
}
function editCredential(row: RelayProvider) {
  requestKey.value = globalThis.crypto.randomUUID();
  close();
  selected.value = row.id;
  provider.value = { ...row.configuration };
  editor.value = 'credential';
}
function askProvider() {
  const body = { ...provider.value };
  if (!body.name.trim() || body.name !== body.name.trim()) {
    error.value = t('invalid');
    return;
  }
  const id = selected.value,
    key = requestKey.value;
  pending.value = {
    message: t('confirmSave', { name: body.name }),
    run: () =>
      ui.write(
        'ai.providers.write',
        () => relayApi.saveProvider(id, body, key, ui.signal),
        close,
        load,
      ),
  };
}
function askCredential() {
  if (!secret.value.trim()) {
    error.value = t('invalid');
    return;
  }
  const value = secret.value,
    id = selected.value,
    key = requestKey.value;
  secret.value = '';
  pending.value = {
    message: t('confirmSecret', { name: provider.value.name }),
    run: () =>
      ui.write(
        'ai.credentials.rotate',
        () => relayApi.credential(id, value, key, ui.signal),
        close,
        load,
      ),
  };
}
function askPool() {
  if (!draft.value) return;
  const body = JSON.parse(JSON.stringify(draft.value)) as RelayPool;
  const ints = [
    body.contextWindow,
    body.maxOutputTokens,
    body.price.inputMicrosPerMillionTokens,
    body.price.outputMicrosPerMillionTokens,
  ];
  if (
    !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(body.id) ||
    ints.some((v) => !Number.isSafeInteger(v) || v < 0) ||
    body.contextWindow <= body.maxOutputTokens ||
    body.routes.length < 1 ||
    body.routes.length > 8 ||
    new Set(body.routes.map((r) => r.providerId)).size !== body.routes.length ||
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
  const key = requestKey.value;
  pending.value = {
    message: t('confirmSave', { name: body.id }),
    run: () =>
      ui.write(
        'ai.providers.write',
        () => relayApi.savePool(body, key, ui.signal),
        close,
        load,
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
  <section class="model-usage">
    <header>
      <h1>{{ t('providers') }}</h1>
      <button :disabled="loading || busy || !!pending" @click="load">
        {{ t('refresh') }}
      </button>
    </header>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <div class="usage-filters">
      <button
        data-testid="add-provider"
        :disabled="
          loading || busy || !!pending || !ui.can('ai.providers.write')
        "
        @click="editProvider()"
      >
        {{ t('addProvider') }}</button
      ><button
        data-testid="add-pool"
        :disabled="
          loading || busy || !!pending || !ui.can('ai.providers.write')
        "
        @click="editPool()"
      >
        {{ t('addPool') }}
      </button>
    </div>
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
            <td>{{ row.configuration.name }}</td>
            <td>{{ row.configuration.kind }}</td>
            <td>{{ row.configuration.baseUrl }}</td>
            <td>{{ t(row.configuration.enabled ? 'enabled' : 'disabled') }}</td>
            <td>
              {{
                t(row.hasCredential ? 'credentialReady' : 'credentialMissing')
              }}
            </td>
            <td>
              <button
                data-testid="edit-provider"
                :disabled="busy || !!pending || !ui.can('ai.providers.write')"
                @click="editProvider(row)"
              >
                {{ t('edit') }}</button
              ><button
                :disabled="
                  busy || !!pending || !ui.can('ai.credentials.rotate')
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
    <form
      v-if="editor === 'provider'"
      data-testid="save-provider"
      @submit.prevent="askProvider"
    >
      <fieldset :disabled="busy || !!pending">
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
    <form v-if="editor === 'credential'" @submit.prevent="askCredential">
      <fieldset :disabled="busy || !!pending">
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
    <h2>{{ t('pools') }}</h2>
    <p>{{ t('poolNote') }}</p>
    <div class="usage-table">
      <table>
        <thead>
          <tr>
            <th>{{ t('poolId') }}</th>
            <th>{{ t('status') }}</th>
            <th>{{ t('routes') }}</th>
            <th>{{ t('edit') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="pool in pools" :key="pool.id">
            <td>{{ pool.id }}</td>
            <td>{{ t(pool.enabled ? 'enabled' : 'disabled') }}</td>
            <td>
              <div v-for="route in pool.routes" :key="route.providerId">
                {{
                  providers.find((p) => p.id === route.providerId)
                    ?.configuration.name ?? route.providerId
                }}
                / {{ route.model }} · {{ route.weight }}
              </div>
            </td>
            <td>
              <button
                data-testid="edit-pool"
                :disabled="busy || !!pending || !ui.can('ai.providers.write')"
                @click="editPool(pool)"
              >
                {{ t('edit') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <form
      v-if="editor === 'pool' && draft"
      data-testid="pool-form"
      @submit.prevent="askPool"
    >
      <fieldset :disabled="busy || !!pending">
        <legend>{{ t('pools') }}</legend>
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
        ><label
          >{{ t('context')
          }}<input
            v-model.number="draft.contextWindow"
            name="contextWindow"
            type="number"
            min="2"
            max="1048576"
            required /></label
        ><label
          >{{ t('output')
          }}<input
            v-model.number="draft.maxOutputTokens"
            name="maxOutputTokens"
            type="number"
            min="1"
            max="32768"
            required
        /></label>
        <div
          v-for="(route, index) in draft.routes"
          :key="index"
          class="usage-filters"
        >
          <label
            >{{ t('providers')
            }}<select
              v-model="route.providerId"
              :name="'routeProvider' + index"
              required
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
              required
              maxlength="256" /></label
          ><label
            >{{ t('weight')
            }}<input
              v-model.number="route.weight"
              :name="'routeWeight' + index"
              type="number"
              min="0"
              step="any"
              required /></label
          ><button type="button" @click="draft.routes.splice(index, 1)">
            {{ t('remove') }}
          </button>
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
            min="0"
            max="9007199254740991"
            required /></label
        ><label
          >{{ t('priceOutput')
          }}<input
            v-model.number="draft.price.outputMicrosPerMillionTokens"
            name="outputPrice"
            type="number"
            min="0"
            max="9007199254740991"
            required /></label
        ><label
          >{{ t('priceVersion')
          }}<input
            v-model="draft.price.version"
            name="priceVersion"
            maxlength="128"
            required /></label
        ><button type="submit">{{ t('save') }}</button
        ><button type="button" @click="close">{{ t('close') }}</button>
      </fieldset>
    </form>
    <p>{{ t('limitNote') }}</p>
    <ActionConfirm
      :message="pending?.message"
      :busy="busy"
      @confirm="confirm"
      @cancel="pending = undefined"
    />
  </section>
</template>
