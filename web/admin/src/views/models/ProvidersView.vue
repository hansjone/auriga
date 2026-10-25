<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import {
  modelProvidersApi as api,
  type Provider,
  type Model,
  type Credential,
  type ProviderInput,
  type ModelInput,
} from '../../admin/modelProvidersApi';
import { useModelI18n } from './messages';
import {
  providerDraft,
  providerInput,
  modelInput,
  modelPatch,
  modelDraft,
  validProvider,
  validModel,
  parseGrants,
  providerNumbers,
  modelNumbers,
  priceNumbers,
  uuid,
} from './forms';
const { t } = useModelI18n();
const providers = shallowRef<Provider[]>([]);
const models = shallowRef<Model[]>([]);
const loading = ref(false);
const busy = ref(false);
const error = ref('');
const notice = ref('');
const readbackFailed = ref(false);
const editor = ref<'provider' | 'model' | 'credential' | 'grants' | ''>('');
const original = ref('');
const etag = ref('');
const provider = ref<ProviderInput>(providerDraft());
const model = ref<ModelInput>(modelDraft(''));
const selected = ref('');
const secret = ref('');
const credentialId = ref('');
const credentialEtag = ref('');
const reason = ref('');
const credential = shallowRef<Credential>();
const grantText = ref('');
const pending = shallowRef<{ message: string; run: () => Promise<void> }>();
const lifetime = new globalThis.AbortController();
const baselineModel = shallowRef<ModelInput>();
let alive = true;
onBeforeUnmount(() => {
  alive = false;
  lifetime.abort();
  secret.value = '';
  pending.value = undefined;
});
function failure(e: unknown) {
  if (!isApiError(e)) return t('failed');
  const code = e.problem.code;
  const key =
    code === 'iam.step_up_required'
      ? 'stepup'
      : e.status === 412
        ? 'conflict'
        : e.status === 409
          ? 'stateConflict'
          : e.status === 400
            ? 'invalid'
            : e.status === 429
              ? 'limited'
              : e.status === 403
                ? 'forbidden'
                : 'failed';
  // Never render Problem detail/title, which could contain submitted credentials.
  return t(key);
}
async function load() {
  if (loading.value || !alive) return;
  loading.value = true;
  error.value = '';
  try {
    const readProviders = async () => {
      const rows: Provider[] = [];
      let cursor: string | undefined;
      const seen = new Set<string>();
      do {
        const result = await api.providers(cursor, lifetime.signal);
        rows.push(...result.data.items);
        cursor = result.data.nextCursor;
        if (cursor && seen.has(cursor)) throw new Error('Repeated cursor');
        if (cursor) seen.add(cursor);
      } while (cursor && alive);
      return rows;
    };
    const readModels = async () => {
      const rows: Model[] = [];
      let cursor: string | undefined;
      const seen = new Set<string>();
      do {
        const result = await api.models(cursor, lifetime.signal);
        rows.push(...result.data.items);
        cursor = result.data.nextCursor;
        if (cursor && seen.has(cursor)) throw new Error('Repeated cursor');
        if (cursor) seen.add(cursor);
      } while (cursor && alive);
      return rows;
    };
    const [ps, ms] = await Promise.all([readProviders(), readModels()]);
    if (alive) {
      providers.value = ps;
      models.value = ms;
      readbackFailed.value = false;
    }
  } catch (e) {
    if (alive) {
      error.value = failure(e);
      throw e;
    }
  } finally {
    if (alive) loading.value = false;
  }
}
async function refresh() {
  try {
    await load();
    if (editor.value === 'credential' && credentialId.value)
      await readCredential();
    else if (original.value) await reloadConflict();
  } catch {
    /* Error is shown by load. */
  }
}
onMounted(() => void refresh());
function close() {
  if (busy.value) return;
  secret.value = '';
  editor.value = '';
  pending.value = undefined;
}
function reset(mode: typeof editor.value) {
  secret.value = '';
  error.value = '';
  notice.value = '';
  original.value = '';
  etag.value = '';
  pending.value = undefined;
  editor.value = mode;
}
function newProvider() {
  reset('provider');
  provider.value = providerDraft();
}
async function editProvider(p: Provider) {
  busy.value = true;
  error.value = '';
  try {
    const result = await api.provider(p.id, lifetime.signal);
    if (!alive) return;
    reset('provider');
    original.value = p.id;
    etag.value = result.etag ?? result.data.etag;
    provider.value = providerInput(result.data);
  } catch (e) {
    if (alive) {
      error.value = failure(e);
      readbackFailed.value = true;
    }
  } finally {
    if (alive) busy.value = false;
  }
}
function newModel(p: Provider) {
  reset('model');
  model.value = modelDraft(p.id);
}
async function editModel(m: Pick<Model, 'id'>) {
  busy.value = true;
  try {
    const result = await api.model(m.id, lifetime.signal);
    if (!alive) return;
    reset('model');
    original.value = m.id;
    model.value = modelInput(result.data);
    baselineModel.value = modelInput(result.data);
    etag.value = result.etag ?? result.data.etag;
  } catch (e) {
    if (alive) {
      error.value = failure(e);
      readbackFailed.value = true;
    }
  } finally {
    if (alive) busy.value = false;
  }
}
async function credentials(p: Provider) {
  reset('credential');
  selected.value = p.id;
  credential.value = undefined;
  credentialId.value = p.activeCredentialId ?? '';
  credentialEtag.value = '';
  reason.value = '';
  if (credentialId.value) await readCredential();
}
async function readCredential() {
  credential.value = undefined;
  credentialEtag.value = '';
  secret.value = '';
  if (!uuid.test(credentialId.value)) {
    error.value = t('invalid');
    return;
  }
  busy.value = true;
  try {
    const result = await api.credential(
      selected.value,
      credentialId.value,
      lifetime.signal,
    );
    if (!alive) return;
    credential.value = result.data;
    credentialEtag.value = result.etag ?? '';
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive) busy.value = false;
  }
}
async function grants(m: Pick<Model, 'id'>) {
  busy.value = true;
  try {
    const result = await api.readGrants(m.id, lifetime.signal);
    if (!alive) return;
    reset('grants');
    original.value = m.id;
    etag.value = result.etag ?? '';
    grantText.value = result.data.items
      .map((g) => g.kind + ' ' + g.id)
      .join('\n');
  } catch (e) {
    if (alive) {
      error.value = failure(e);
      readbackFailed.value = true;
    }
  } finally {
    if (alive) busy.value = false;
  }
}
async function reloadConflict() {
  const mode = editor.value,
    resourceId = original.value;
  if (mode === 'model') await editModel({ id: resourceId });
  else if (mode === 'grants') await grants({ id: resourceId });
  else if (mode === 'credential') await readCredential();
  else if (mode === 'provider') {
    const p = providers.value.find((p) => p.id === resourceId);
    if (p) await editProvider(p);
  }
}
async function write(action: () => Promise<void>) {
  if (busy.value || loading.value || readbackFailed.value || !alive) return;
  busy.value = true;
  error.value = '';
  notice.value = '';
  try {
    await action();
    if (alive) notice.value = t('saved');
  } catch (e) {
    if (alive) {
      if (isApiError(e) && (e.status === 409 || e.status === 412))
        await reloadConflict();
      if (alive) error.value = failure(e);
    }
  } finally {
    if (alive) {
      busy.value = false;
      pending.value = undefined;
    }
  }
}
async function readAfterWrite() {
  try {
    await load();
  } catch {
    if (alive) {
      readbackFailed.value = true;
      error.value = t('readback');
    }
  }
}
function saveProvider() {
  if (
    !validProvider(provider.value) ||
    (original.value && !etag.value.trim())
  ) {
    error.value = t('invalid');
    return;
  }
  const body = { ...provider.value };
  const currentId = original.value;
  const version = etag.value;
  const key = globalThis.crypto.randomUUID();
  const action = async () => {
    if (currentId)
      await api.patchProvider(currentId, body, version, lifetime.signal);
    else await api.createProvider(body, key, lifetime.signal);
    if (!alive) return;
    editor.value = '';
    await readAfterWrite();
  };
  if (currentId && !body.enabled)
    pending.value = { message: t('disableConfirm'), run: action };
  else void write(action);
}
function saveModel() {
  if (!validModel(model.value) || (original.value && !etag.value.trim())) {
    error.value = t('invalid');
    return;
  }
  const body = { ...model.value, price: { ...model.value.price } };
  const currentId = original.value;
  const version = etag.value;
  const patch = baselineModel.value
    ? modelPatch(baselineModel.value, body)
    : {};
  if (currentId && !Object.keys(patch).length) {
    notice.value = t('noChanges');
    return;
  }
  const key = globalThis.crypto.randomUUID();
  const action = async () => {
    const result = currentId
      ? await api.patchModel(currentId, patch, version, lifetime.signal)
      : await api.createModel(body, key, lifetime.signal);
    if (!alive) return;
    if (!result.data) return;
    editor.value = '';
    await readAfterWrite();
  };
  if (currentId && !body.enabled)
    pending.value = { message: t('disableConfirm'), run: action };
  else void write(action);
}
function createCredential() {
  if (!secret.value.trim() || secret.value.length > 16384 || busy.value) {
    error.value = t('invalid');
    return;
  }
  const value = secret.value;
  secret.value = '';
  const providerId = selected.value;
  const key = globalThis.crypto.randomUUID();
  void write(async () => {
    const result = await api.createCredential(
      providerId,
      { secret: value },
      key,
      lifetime.signal,
    );
    if (!alive) return;
    credential.value = result.data;
    credentialId.value = result.data.id;
    credentialEtag.value = result.etag ?? '';
  });
}
function askCredential(action: 'activate' | 'revoke') {
  if (
    !credential.value ||
    credential.value.id !== credentialId.value ||
    (action === 'activate'
      ? credential.value.state !== 'staged'
      : credential.value.state === 'revoked')
  )
    return;
  if (
    !uuid.test(credentialId.value) ||
    !credentialEtag.value.trim() ||
    !reason.value.trim() ||
    reason.value.length > 1024
  ) {
    error.value = t('invalid');
    return;
  }
  secret.value = '';
  const providerId = selected.value,
    cid = credentialId.value,
    version = credentialEtag.value,
    body = { reason: reason.value.trim() };
  const key = globalThis.crypto.randomUUID();
  pending.value = {
    message: t(action === 'revoke' ? 'danger' : 'activateConfirm'),
    run: async () => {
      const result = await api.credentialAction(
        providerId,
        cid,
        action,
        body,
        version,
        key,
        lifetime.signal,
      );
      if (!alive) return;
      credential.value = result.data;
      credentialId.value = result.data.id;
      credentialEtag.value = result.etag ?? '';
      await readAfterWrite();
    },
  };
}
function saveGrants() {
  const body = parseGrants(grantText.value);
  const mid = original.value;
  const version = etag.value;
  if (!body || !version.trim()) {
    error.value = t('invalid');
    return;
  }
  pending.value = {
    message: t('replaceConfirm'),
    run: async () => {
      const result = await api.grants(mid, body, version, lifetime.signal);
      if (!alive) return;
      if (!result.data) return;
      editor.value = '';
      await readAfterWrite();
    },
  };
}
</script>

<template>
  <section class="resource-page model-providers-page">
    <header class="resource-head">
      <h1>{{ t('title') }}</h1>
      <div class="resource-actions">
        <ElButton :disabled="busy || loading || !!pending" @click="refresh">{{
          t('refresh')
        }}</ElButton
        ><ElButton
          :disabled="busy || loading || readbackFailed || !!pending"
          @click="newProvider"
          >{{ t('create') }}</ElButton
        >
      </div>
    </header>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <fieldset
      :disabled="busy || loading || readbackFailed || !!pending"
      class="page-content"
    >
      <p v-if="!loading && !providers.length">{{ t('empty') }}</p>
      <article v-for="p in providers" :key="p.id" class="provider-card">
        <header>
          <h2>{{ p.name }}</h2>
          <span>{{ t(p.enabled ? 'enabled' : 'disabled') }}</span
          ><ElButton @click="editProvider(p)">{{ t('edit') }}</ElButton
          ><ElButton @click="credentials(p)">{{ t('credentials') }}</ElButton
          ><ElButton @click="newModel(p)"
            >{{ t('create') }} {{ t('models') }}</ElButton
          >
        </header>
        <p>{{ p.baseUrl }}</p>
        <table>
          <thead>
            <tr>
              <th>{{ t('id') }}</th>
              <th>{{ t('upstreamModel') }}</th>
              <th>{{ t('contextWindow') }}</th>
              <th>{{ t('maxOutputTokens') }}</th>
              <th>{{ t('enabled') }}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="m in models.filter((m) => m.providerId === p.id)"
              :key="m.id"
            >
              <td>{{ m.id }}<br />{{ m.displayName }}</td>
              <td>{{ m.upstreamModel }}</td>
              <td>{{ m.contextWindow }}</td>
              <td>{{ m.maxOutputTokens }}</td>
              <td>{{ t(m.enabled ? 'enabled' : 'disabled') }}</td>
              <td>
                <ElButton @click="editModel(m)">{{ t('edit') }}</ElButton
                ><ElButton @click="grants(m)">{{ t('grants') }}</ElButton>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="!models.some((m) => m.providerId === p.id)">
          {{ t('empty') }}
        </p>
      </article>
      <article
        v-if="models.some((m) => !providers.some((p) => p.id === m.providerId))"
        class="provider-card"
      >
        <h2>{{ t('orphan') }}</h2>
        <div
          v-for="m in models.filter(
            (m) => !providers.some((p) => p.id === m.providerId),
          )"
          :key="m.id"
        >
          {{ m.id }} <ElButton @click="editModel(m)">{{ t('edit') }}</ElButton
          ><ElButton @click="grants(m)">{{ t('grants') }}</ElButton>
        </div>
      </article>
    </fieldset>
    <section
      v-if="editor"
      role="dialog"
      :aria-label="
        t(
          editor === 'provider'
            ? 'providers'
            : editor === 'model'
              ? 'models'
              : editor === 'credential'
                ? 'credentials'
                : 'grants',
        )
      "
      class="provider-card editor"
    >
      <fieldset :disabled="busy || loading || !!pending || readbackFailed">
        <form
          v-if="editor === 'provider'"
          novalidate
          @submit.prevent="saveProvider"
        >
          <h2>{{ t('providers') }}</h2>
          <label
            >{{ t('name')
            }}<input v-model="provider.name" name="name" maxlength="128"
          /></label>
          <label
            >{{ t('baseUrl')
            }}<input v-model="provider.baseUrl" name="baseUrl" type="url"
          /></label>
          <label
            >{{ t('kind')
            }}<select v-model="provider.kind">
              <option value="openai_compatible">OpenAI compatible</option>
            </select></label
          >
          <label v-for="[key, max] in providerNumbers" :key="key"
            >{{ t(key)
            }}<input
              v-model.number="provider[key]"
              :name="key"
              type="number"
              min="1"
              :max="max"
          /></label>
          <label
            ><input v-model="provider.enabled" type="checkbox" />{{
              t('enabled')
            }}</label
          >
          <ElButton native-type="submit">{{ t('save') }}</ElButton>
        </form>
        <form
          v-else-if="editor === 'model'"
          novalidate
          @submit.prevent="saveModel"
        >
          <h2>{{ t('models') }}</h2>
          <label
            >{{ t('id')
            }}<input
              v-model="model.id"
              name="modelId"
              :disabled="!!original"
              maxlength="128"
          /></label>
          <label
            >{{ t('providers')
            }}<select v-model="model.providerId">
              <option v-for="p in providers" :key="p.id" :value="p.id">
                {{ p.name }}
              </option>
            </select></label
          >
          <label
            >{{ t('displayName')
            }}<input v-model="model.displayName" name="displayName"
          /></label>
          <label
            >{{ t('upstreamModel')
            }}<input
              v-model="model.upstreamModel"
              name="upstreamModel"
              maxlength="128"
          /></label>
          <label v-for="key in modelNumbers" :key="key"
            >{{ t(key)
            }}<input
              v-model.number="model[key]"
              :name="key"
              type="number"
              min="1"
              :max="Number.MAX_SAFE_INTEGER"
          /></label>
          <label
            >{{ t('currency')
            }}<input
              v-model="model.price.currency"
              name="currency"
              maxlength="3"
          /></label>
          <label v-for="key in priceNumbers" :key="key"
            >{{ t(key)
            }}<input
              v-model.number="model.price[key]"
              :name="key"
              type="number"
              min="0"
              :max="Number.MAX_SAFE_INTEGER"
          /></label>
          <label
            >{{ t('version')
            }}<input
              v-model="model.price.version"
              name="priceVersion"
              maxlength="128"
          /></label>
          <label
            >{{ t('effectiveAt')
            }}<input v-model="model.price.effectiveAt" name="effectiveAt"
          /></label>
          <label
            ><input v-model="model.enabled" type="checkbox" />{{
              t('enabled')
            }}</label
          >
          <ElButton native-type="submit">{{ t('save') }}</ElButton>
        </form>
        <div v-else-if="editor === 'credential'">
          <h2>{{ t('credentials') }}</h2>
          <p>{{ t('secretHint') }}</p>
          <label
            >{{ t('secret')
            }}<input
              v-model="secret"
              type="password"
              autocomplete="new-password"
              maxlength="16384"
          /></label>
          <ElButton data-testid="create-credential" @click="createCredential">{{
            t('create')
          }}</ElButton>
          <p>{{ t('credentialHint') }}</p>
          <p v-if="credential">
            {{ credential.id }} · {{ t(credential.state) }}
          </p>
          <label
            >{{ t('credentialId')
            }}<input
              v-model="credentialId"
              name="credentialId"
              @input="
                credential = undefined;
                credentialEtag = '';
              "
          /></label>
          <ElButton data-testid="read-credential" @click="readCredential">{{
            t('refresh')
          }}</ElButton>
          <label
            >{{ t('reason')
            }}<input v-model="reason" name="reason" maxlength="1024"
          /></label>
          <ElButton
            :disabled="
              !credential ||
              credential.id !== credentialId ||
              credential.state !== 'staged' ||
              !credentialEtag
            "
            @click="askCredential('activate')"
            >{{ t('activate') }}</ElButton
          ><ElButton
            type="danger"
            :disabled="
              !credential ||
              credential.id !== credentialId ||
              credential.state === 'revoked' ||
              !credentialEtag
            "
            @click="askCredential('revoke')"
            >{{ t('revoke') }}</ElButton
          >
        </div>
        <form v-else novalidate @submit.prevent="saveGrants">
          <h2>{{ t('grants') }} · {{ original }}</h2>
          <p>{{ t('grantHint') }}</p>
          <label
            >{{ t('grantsText')
            }}<textarea v-model="grantText" name="grants" rows="5"></textarea>
          </label>
          <ElButton native-type="submit">{{ t('save') }}</ElButton>
        </form>
      </fieldset>
      <ElButton :disabled="busy" @click="close">{{ t('close') }}</ElButton>
    </section>
    <section
      v-if="pending"
      role="alertdialog"
      :aria-label="t('confirm')"
      class="provider-card"
    >
      <p>{{ pending.message }}</p>
      <ElButton :disabled="busy" @click="pending = undefined">{{
        t('cancel')
      }}</ElButton
      ><ElButton
        data-testid="confirm"
        :disabled="busy"
        @click="write(pending.run)"
        >{{ t('confirm') }}</ElButton
      >
    </section>
  </section>
</template>
<style scoped>
.page-content,
fieldset {
  border: 0;
  padding: 0;
  margin: 0;
  min-width: 0;
}
.provider-card {
  border: 1px solid var(--auriga-border);
  background: var(--auriga-surface);
  padding: 20px;
  margin-bottom: 16px;
  border-radius: 12px;
}
.provider-card header {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
h2 {
  margin: 0;
  font-size: 16px;
}
label {
  display: flex;
  gap: 8px;
  flex-direction: column;
  margin: 12px 0;
}
input,
select,
textarea {
  color: inherit;
  background: var(--auriga-surface);
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
  padding: 8px;
  font: inherit;
}
input[type='checkbox'] {
  align-self: flex-start;
}
table {
  width: 100%;
  text-align: left;
  border-collapse: collapse;
}
th,
td {
  padding: 10px 8px;
  border-bottom: 1px solid var(--auriga-border);
  overflow-wrap: anywhere;
}
.editor {
  max-width: 800px;
}
[role='alert'] {
  color: var(--el-color-danger);
}
</style>
