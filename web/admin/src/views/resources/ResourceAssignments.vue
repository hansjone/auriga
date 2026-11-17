<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  resourceCatalogApi as api,
  catalogKey,
  type CatalogAssignment,
} from '../../admin/resourceCatalogApi';
import type { CatalogKind } from '../../admin/resourceCatalogUpload';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
import { requestStepUp } from '../../admin/stepUp';
import ActionConfirm from '../../admin/ActionConfirm.vue';
const props = defineProps<{
  kind: CatalogKind;
  member?: Awaited<ReturnType<typeof adminApi.membership>>;
  disabled?: boolean;
}>();
const emit = defineEmits<{ locked: [value: boolean] }>();
const { t } = useI18n({ useScope: 'local', messages: catalogMessages });
const items = shallowRef<CatalogAssignment[]>([]),
  detail = shallowRef<CatalogAssignment>();
const pid = ref(''),
  vid = ref(''),
  scope = ref<CatalogAssignment['scope']>('tenant'),
  target = ref(''),
  mode = ref<CatalogAssignment['installationMode']>('optional');
const loading = ref(false),
  busy = ref(false),
  error = ref(''),
  notice = ref(''),
  verifyNeeded = ref(false),
  confirming = ref(false);
const cursor = ref<string>(),
  next = ref<string>(),
  history = ref<(string | undefined)[]>([]);
const pending = shallowRef<{
  label: string;
  run: () => Promise<unknown>;
  started: boolean;
  at: number;
}>();
const locked = computed(
  () => busy.value || !!pending.value || !!props.disabled,
);
const allowed = computed(() =>
  props.member?.permissions.includes('resource.distributions.manage'),
);
let alive = true,
  epoch = 0;
watch(
  () => busy.value || !!pending.value,
  (v) => emit('locked', v),
);
function fail(e: unknown) {
  if (isApiError(e)) {
    if (e.problem.code === 'iam.step_up_required') {
      verifyNeeded.value = true;
      return t('stepup');
    }
    return t(
      e.status === 409 || e.status === 412
        ? 'conflict'
        : e.status === 403
          ? 'forbidden'
          : 'failed',
    );
  }
  return t('failed');
}
async function load(c?: string, direction?: 'next' | 'previous') {
  if (!allowed.value || locked.value) return;
  const e = ++epoch;
  loading.value = true;
  error.value = '';
  detail.value = undefined;
  items.value = [];
  next.value = undefined;
  try {
    const page = await api.assignments(c);
    if (!alive || e !== epoch) return;
    items.value = page.items.filter((r) => r.identity.kind === props.kind);
    next.value = page.nextCursor;
    if (direction === 'next') history.value.push(cursor.value);
    if (direction === 'previous') history.value.pop();
    cursor.value = c;
  } catch (err) {
    if (alive && e === epoch) error.value = fail(err);
  } finally {
    if (alive && e === epoch) loading.value = false;
  }
}
function prepare(label: string, run: () => Promise<unknown>) {
  pending.value = { label, run, started: false, at: Date.now() };
  confirming.value = true;
}
async function create() {
  if (locked.value || loading.value || !allowed.value) return;
  const p = pid.value.trim(),
    v = vid.value.trim(),
    s = scope.value,
    targetId = s === 'tenant' ? props.member?.tenant?.id : target.value.trim(),
    installationMode = mode.value;
  if (!p || !v || !targetId) {
    error.value = t('invalid');
    return;
  }
  busy.value = true;
  error.value = '';
  try {
    const [pack, version] = await Promise.all([api.get(p), api.version(p, v)]);
    if (!alive) return;
    const value = version.data;
    if (
      pack.data.id !== p ||
      pack.data.kind !== props.kind ||
      value.kind !== props.kind ||
      value.id !== v ||
      value.packageId !== p ||
      value.state !== 'published'
    )
      throw new Error('Invalid assignment');
    const body = {
        identity: {
          tenantId: pack.data.tenantId,
          kind: props.kind,
          packageId: p,
          versionId: v,
          version: value.version,
          manifestDigest: value.digest,
        },
        scope: s,
        targetId,
        installationMode,
      },
      key = catalogKey();
    prepare(
      `${t('assign')} · ${p} ${value.version} · ${t(s)} ${targetId} · ${t(installationMode)}`,
      () => api.assign(body, key),
    );
  } catch (e) {
    if (alive) error.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
}
function remove(rule: CatalogAssignment) {
  if (
    locked.value ||
    !allowed.value ||
    !rule.active ||
    !Number.isSafeInteger(rule.revision) ||
    rule.revision < 1
  )
    return;
  // Assignment list exposes the CAS revision; the contract has no per-rule GET.
  const key = catalogKey(),
    etag = `"${rule.revision}"`;
  prepare(
    `${t('removeAssignment')} · ${rule.identity.packageId} · ${t(rule.scope)} ${rule.targetId} · ${t(rule.installationMode)}`,
    () => api.removeAssignment(rule.id, etag, key),
  );
}
async function execute() {
  const intent = pending.value;
  if (!intent || busy.value || props.disabled) return;
  if (Date.now() - intent.at >= 86400000) {
    pending.value = undefined;
    confirming.value = false;
    error.value = t('expired');
    return;
  }
  busy.value = true;
  confirming.value = false;
  intent.started = true;
  try {
    await intent.run();
    if (!alive) return;
    pending.value = undefined;
    busy.value = false;
    notice.value = t('assignmentAccepted');
    await load(cursor.value);
  } catch (e) {
    if (!alive) return;
    error.value = fail(e);
    if (
      isApiError(e) &&
      e.status < 500 &&
      e.problem.code !== 'iam.step_up_required'
    ) {
      pending.value = undefined;
      detail.value = undefined;
    }
  } finally {
    if (alive) busy.value = false;
  }
}
async function verify() {
  try {
    await requestStepUp();
    verifyNeeded.value = false;
  } catch (e) {
    if (alive) error.value = fail(e);
  }
}
watch(
  () => [props.kind, allowed.value, props.disabled],
  () => {
    history.value = [];
    cursor.value = undefined;
    void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  alive = false;
  ++epoch;
  pending.value = undefined;
  emit('locked', false);
});
</script>
<template>
  <section class="assignment-panel">
    <header class="market-toolbar">
      <h2>{{ t('assignments') }}</h2>
      <button :disabled="locked || loading" @click="load(cursor)">
        {{ t('refresh') }}
      </button>
    </header>
    <p>{{ t('modeNote') }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <button v-if="verifyNeeded" @click="verify">{{ t('stepup') }}</button>
    <p v-if="pending?.started && !busy">
      {{ t('uncertain') }}
      <button data-testid="assignment-retry" @click="confirming = true">
        {{ t('retry') }}
      </button>
    </p>
    <form @submit.prevent="create">
      <fieldset :disabled="locked || loading || !allowed">
        <label
          >{{ t('packageId')
          }}<input v-model="pid" data-testid="assignment-package" required
        /></label>
        <label
          >{{ t('versionId')
          }}<input v-model="vid" data-testid="assignment-version" required
        /></label>
        <label
          >{{ t('scope')
          }}<select v-model="scope" data-testid="assignment-scope">
            <option
              v-for="s in ['tenant', 'member', 'group', 'instance']"
              :key="s"
              :value="s"
            >
              {{ t(s) }}
            </option>
          </select></label
        >
        <label v-if="scope !== 'tenant'"
          >{{ t('target') }}<input v-model="target" required
        /></label>
        <p v-else>{{ t('target') }}: {{ member?.tenant?.id }}</p>
        <label
          >{{ t('installationMode')
          }}<select v-model="mode" data-testid="assignment-mode">
            <option
              v-for="m in ['required', 'default', 'optional']"
              :key="m"
              :value="m"
            >
              {{ t(m) }}
            </option>
          </select></label
        >
        <button>{{ t('assign') }}</button>
      </fieldset>
    </form>
    <div class="market-table">
      <table>
        <thead>
          <tr>
            <th>{{ t('packageId') }}</th>
            <th>{{ t('scope') }}</th>
            <th>{{ t('installationMode') }}</th>
            <th>{{ t('status') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in items" :key="r.id">
            <td>
              <button
                data-testid="assignment-detail"
                :disabled="locked"
                @click="detail = r"
              >
                {{ r.identity.packageId }}</button
              ><small>{{ r.identity.version }}</small>
            </td>
            <td>
              {{ t(r.scope) }}<small>{{ r.targetId }}</small>
            </td>
            <td>{{ t(r.installationMode) }}</td>
            <td>{{ t(r.active ? 'activeRule' : 'inactiveRule') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-if="!items.length && !loading">{{ t('empty') }}</p>
    <div class="resource-pager">
      <button
        :disabled="locked || loading || !history.length"
        @click="load(history.at(-1), 'previous')"
      >
        {{ t('previous') }}</button
      ><button
        :disabled="locked || loading || !next"
        @click="load(next, 'next')"
      >
        {{ t('next') }}
      </button>
    </div>
    <article v-if="detail">
      <h3>{{ t('assignmentDetail') }}</h3>
      <dl>
        <dt>{{ t('packageId') }}</dt>
        <dd>{{ detail.identity.packageId }}</dd>
        <dt>{{ t('versionId') }}</dt>
        <dd>{{ detail.identity.versionId }} / {{ detail.identity.version }}</dd>
        <dt>{{ t('digest') }}</dt>
        <dd>{{ detail.identity.manifestDigest }}</dd>
        <dt>{{ t('scope') }}</dt>
        <dd>{{ t(detail.scope) }} / {{ detail.targetId }}</dd>
        <dt>{{ t('installationMode') }}</dt>
        <dd>{{ t(detail.installationMode) }}</dd>
      </dl>
      <button
        v-if="detail.active"
        data-testid="assignment-remove"
        :disabled="locked"
        @click="remove(detail)"
      >
        {{ t('removeAssignment') }}
      </button>
    </article>
    <ActionConfirm
      v-if="confirming"
      :message="pending?.label"
      :busy="busy"
      @confirm="execute"
      @cancel="
        confirming = false;
        !pending?.started && (pending = undefined);
      "
    />
  </section>
</template>
<style scoped>
.assignment-panel {
  margin: 20px 0;
}
dd {
  overflow-wrap: anywhere;
}
article {
  padding: 16px;
  border: 1px solid var(--auriga-border);
}
</style>
