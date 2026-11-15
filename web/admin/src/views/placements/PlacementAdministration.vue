<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, shallowRef } from 'vue';
import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  placementAdminApi as api,
  intentKey,
  type Plan,
  type PlanRequest,
  type PlanProgress,
  type Capacity,
} from './api';
import { adminPlacementMessages } from './adminMessages';
import { placementMessages } from './messages';
const { t, te } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      ...adminPlacementMessages['zh-CN'],
      risk: placementMessages['zh-CN'],
    },
    en: { ...adminPlacementMessages.en, risk: placementMessages.en },
  },
});
const risk = (key: string) => t('risk.' + key);
const permissions = ref<string[]>([]),
  busy = ref(false),
  error = ref(''),
  capacityError = ref(''),
  notice = ref('');
const canRead = computed(() =>
  permissions.value.includes('workspace.placements.read'),
);
const canWrite = computed(() =>
  permissions.value.includes('workspace.placements.write'),
);
const plan = ref<Plan>(),
  etag = ref(''),
  planId = ref(''),
  reason = ref(''),
  confirmed = ref(false);
const rows = ref<PlanRequest['members']>([
  { memberId: '', legacyInstanceId: '', targetMode: 'shared' },
]);
const progress = ref<PlanProgress>(),
  snapshot = ref<Capacity>();
const impacts = ref<Capacity['impacts']>([]);
const now = ref(Date.now() / 1000);
const stale = computed(
  () =>
    !snapshot.value ||
    snapshot.value.observedAt > now.value ||
    snapshot.value.validUntil <= now.value,
);
const pending = shallowRef<{
  kind: 'create' | 'update' | 'start';
  id: string;
  body: PlanRequest;
  etag: string;
  key: string;
}>();
const editable = computed(
  () =>
    canWrite.value &&
    !busy.value &&
    !pending.value &&
    (plan.value
      ? plan.value.state === 'draft'
      : !planId.value && !progress.value),
);
let alive = true;
const abort = new globalThis.AbortController();
const clock = globalThis.setInterval(() => {
  now.value = Date.now() / 1000;
}, 1000);
function fail(e: unknown) {
  return t('failed') + (isApiError(e) ? ` (${e.problem.code})` : '');
}
async function access(write = false) {
  const value = await adminApi.membership();
  if (!alive) return false;
  permissions.value = value.permissions;
  if (!(write ? canWrite.value : canRead.value)) {
    plan.value = undefined;
    snapshot.value = undefined;
    impacts.value = [];
    progress.value = undefined;
    rows.value = [];
    etag.value = '';
    confirmed.value = false;
    return false;
  }
  return true;
}
function number(value: unknown) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
    ? String(value)
    : t('unknown');
}
function time(value: number) {
  return new Date(value * 1000).toLocaleString();
}
async function capacity() {
  if (busy.value) return;
  busy.value = true;
  capacityError.value = '';
  try {
    if (!(await access())) return;
    const value = await api.capacity(abort.signal);
    if (!alive) return;
    snapshot.value = value;
    now.value = Date.now() / 1000;
    const seen = new Map(impacts.value.map((v) => [v.operationId, v]));
    for (const impact of value.impacts) {
      const previous = seen.get(impact.operationId);
      if (!previous || impact.observedAt >= previous.observedAt)
        seen.set(impact.operationId, impact);
    }
    impacts.value = [...seen.values()];
  } catch (e) {
    if (alive) {
      snapshot.value = undefined;
      capacityError.value = fail(e);
    }
  } finally {
    if (alive) busy.value = false;
  }
}
function uuid(s: string) {
  return /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(s);
}
function request(): PlanRequest {
  const body = {
    reason: reason.value.trim(),
    members: rows.value.map((r) => ({ ...r })),
  };
  if (
    !body.reason ||
    body.reason.length > 512 ||
    !body.members.length ||
    body.members.length > 1000 ||
    body.members.some(
      (r) =>
        !uuid(r.memberId) ||
        !uuid(r.legacyInstanceId) ||
        !['shared', 'dedicated'].includes(r.targetMode),
    ) ||
    new Set(body.members.map((r) => r.memberId)).size !== body.members.length ||
    new Set(body.members.map((r) => r.legacyInstanceId)).size !==
      body.members.length
  )
    throw new Error('invalid');
  return body;
}
async function readPlan() {
  if (busy.value || pending.value || !uuid(planId.value)) return;
  busy.value = true;
  error.value = '';
  notice.value = '';
  confirmed.value = false;
  plan.value = undefined;
  etag.value = '';
  progress.value = undefined;
  try {
    if (!(await access())) return;
    const value = await api.plan(planId.value, abort.signal);
    if (!alive) return;
    if (value.data.planId !== planId.value) throw new Error('binding');
    plan.value = value.data;
    etag.value = value.etag;
    reason.value = value.data.reason;
    rows.value = value.data.members.map((r) => ({ ...r }));
  } catch (e) {
    if (alive) error.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
}
function reset() {
  if (busy.value || pending.value) return;
  plan.value = undefined;
  progress.value = undefined;
  etag.value = '';
  planId.value = '';
  reason.value = '';
  rows.value = [{ memberId: '', legacyInstanceId: '', targetMode: 'shared' }];
  confirmed.value = false;
  notice.value = '';
  error.value = '';
}
function checkProgress(value: PlanProgress, expected: Plan) {
  if (
    value.planId !== expected.planId ||
    value.members.length !== expected.members.length ||
    value.members.some(
      (row, i) =>
        row.memberId !== expected.members[i].memberId ||
        (['blocked', 'failed'].includes(row.state) && !row.failureCode) ||
        (row.state === 'succeeded' &&
          (!row.operationId ||
            !row.sessionsDiscarded ||
            !row.workspaceEmpty ||
            row.failureCode)),
    ) ||
    (value.state === 'completed' &&
      !value.members.every((row) => row.state === 'succeeded'))
  )
    throw new Error('shared.invalid_progress');
  const ids = value.members.flatMap((row) =>
    row.operationId ? [row.operationId] : [],
  );
  if (new Set(ids).size !== ids.length)
    throw new Error('shared.invalid_progress');
}
async function write(kind: 'create' | 'update' | 'start') {
  if (busy.value || !canWrite.value) return;
  if (!pending.value) {
    if (
      kind === 'start' &&
      (!plan.value ||
        plan.value.state !== 'draft' ||
        !etag.value ||
        !confirmed.value)
    )
      return;
    if (kind !== 'start' && !editable.value) return;
    try {
      const body = request();
      // Starting always applies to the saved authoritative list, never unsaved edits.
      if (
        kind === 'start' &&
        JSON.stringify(body) !==
          JSON.stringify({
            reason: plan.value!.reason,
            members: plan.value!.members,
          })
      )
        throw new Error('invalid');
      pending.value = {
        kind,
        id: plan.value?.planId || '',
        body,
        etag: etag.value,
        key: intentKey(),
      };
    } catch {
      error.value = t('invalid');
      return;
    }
  }
  const intent = pending.value;
  busy.value = true;
  error.value = '';
  notice.value = '';
  try {
    if (!(await access(true))) return;
    if (intent.kind === 'start') {
      const value = await api.start(
        intent.id,
        intent.etag,
        intent.key,
        abort.signal,
      );
      if (!alive) return;
      if (!plan.value || value.planId !== intent.id) throw new Error('binding');
      checkProgress(value, plan.value);
      progress.value = value;
      if (plan.value) plan.value = { ...plan.value, state: value.state };
      etag.value = '';
      notice.value = t('registeredOnly');
    } else {
      const value =
        intent.kind === 'create'
          ? await api.create(intent.body, intent.key, abort.signal)
          : await api.update(
              intent.id,
              intent.body,
              intent.etag,
              intent.key,
              abort.signal,
            );
      if (!alive) return;
      if (intent.kind === 'update' && value.planId !== intent.id)
        throw new Error('binding');
      planId.value = value.planId;
      plan.value = value;
      etag.value = '';
      notice.value = t('saved');
      // Response body revisions are not ETags; read the server's opaque label.
      pending.value = undefined;
      const tagged = await api.plan(value.planId, abort.signal);
      if (!alive) return;
      if (tagged.data.planId !== value.planId) throw new Error('binding');
      plan.value = tagged.data;
      etag.value = tagged.etag;
      rows.value = tagged.data.members.map((r) => ({ ...r }));
      reason.value = tagged.data.reason;
    }
    pending.value = undefined;
    confirmed.value = false;
  } catch (e) {
    if (alive) {
      error.value = fail(e);
      if (
        isApiError(e) &&
        e.status >= 400 &&
        e.status < 500 &&
        ![408, 429].includes(e.status)
      ) {
        pending.value = undefined;
        etag.value = '';
        confirmed.value = false;
        error.value += ' ' + t('reload');
      }
    }
  } finally {
    if (alive) busy.value = false;
  }
}
async function readProgress() {
  if (busy.value || pending.value || !uuid(planId.value)) return;
  busy.value = true;
  error.value = '';
  try {
    if (!(await access())) return;
    if (!plan.value) {
      const original = await api.plan(planId.value, abort.signal);
      if (!alive) return;
      if (original.data.planId !== planId.value) throw new Error('binding');
      plan.value = original.data;
    }
    const value = await api.progress(planId.value, abort.signal);
    if (!alive) return;
    checkProgress(value, plan.value);
    if (
      value.planId !== planId.value ||
      (value.state === 'completed' &&
        !value.members.every(
          (r) =>
            r.state === 'succeeded' && r.workspaceEmpty && r.sessionsDiscarded,
        ))
    )
      throw new Error('invalid');
    if (
      plan.value &&
      (value.members.length !== plan.value.members.length ||
        value.members.some(
          (r, i) => r.memberId !== plan.value!.members[i].memberId,
        ))
    )
      throw new Error('binding');
    if (!progress.value || value.revision >= progress.value.revision) {
      progress.value = value;
      if (plan.value) plan.value = { ...plan.value, state: value.state };
      etag.value = '';
      confirmed.value = false;
    }
  } catch (e) {
    if (alive) error.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
}
onMounted(capacity);
onBeforeUnmount(() => {
  alive = false;
  abort.abort();
  globalThis.clearInterval(clock);
});
</script>
<template>
  <section class="placement-admin" :aria-busy="busy">
    <h2>{{ t('title') }}</h2>
    <p v-if="!canRead">{{ t('denied') }}</p>
    <h3>{{ t('capacity') }}</h3>
    <button type="button" :disabled="busy" @click="capacity">
      {{ t('refresh') }}
    </button>
    <p v-if="capacityError" role="alert">
      {{ capacityError }} · {{ t('unknown') }}
    </p>
    <p v-if="snapshot">{{ t('observed') }}: {{ time(snapshot.observedAt) }}</p>
    <p v-if="snapshot && stale" role="alert">{{ t('stale') }}</p>
    <p v-if="snapshot && !snapshot.pools.length">{{ t('empty') }}</p>
    <article v-for="pool in snapshot?.pools" :key="pool.physicalInstanceId">
      <h4>{{ pool.physicalInstanceId }}</h4>
      <p>
        {{ t('registered') }}: {{ number(pool.registered) }} /
        {{ number(pool.registeredLimit) }} · {{ t('active') }}:
        {{ number(pool.active) }} / {{ number(pool.activeLimit) }}
      </p>
      <p>
        {{ t('memory') }}: {{ number(pool.memoryUsedBytes) }} /
        {{ number(pool.memoryLimitBytes) }}
      </p>
      <p>
        {{ t('accepting') }}:
        {{
          stale
            ? t('unknown')
            : typeof pool.accepting === 'boolean'
              ? t(pool.accepting ? 'yes' : 'no')
              : t('unknown')
        }}
      </p>
    </article>
    <h3>{{ t('impact') }}</h3>
    <p>{{ risk('impact') }}</p>
    <p>{{ t('retained') }}</p>
    <article v-for="impact in impacts" :key="impact.operationId" role="status">
      <p>
        {{ impact.physicalInstanceId }} · {{ impact.operationId }} ·
        {{
          te('reasons.' + impact.reason)
            ? t('reasons.' + impact.reason)
            : t('unknown')
        }}
        ·
        {{
          te('impactStates.' + impact.state)
            ? t('impactStates.' + impact.state)
            : t('unknown')
        }}
        · {{ time(impact.observedAt) }}
      </p>
      <p>
        {{ t('affected') }}:
        {{ impact.affected.map((v) => v.memberId).join(', ') }}
      </p>
    </article>
    <h3>{{ t('plans') }}</h3>
    <p>{{ t('help') }}</p>
    <label
      >{{ t('planId')
      }}<input
        v-model="planId"
        :disabled="busy || !!pending"
        @input="
          plan = undefined;
          etag = '';
          confirmed = false;
          progress = undefined;
        "
    /></label>
    <button
      type="button"
      :disabled="busy || !!pending || !canRead"
      @click="readPlan"
    >
      {{ t('load') }}
    </button>
    <button
      type="button"
      :disabled="busy || !!pending || !canRead"
      @click="readProgress"
    >
      {{ t('progress') }}
    </button>
    <button
      type="button"
      :disabled="busy || !!pending || !canWrite"
      @click="reset"
    >
      {{ t('newPlan') }}
    </button>
    <p v-if="plan">{{ plan.planId }} · {{ t(plan.state) }}</p>
    <form @submit.prevent="write(plan ? 'update' : 'create')">
      <fieldset :disabled="!editable">
        <div v-for="(row, index) in rows" :key="index" class="member-row">
          <label
            >{{ t('member') }}<input v-model="row.memberId" required
          /></label>
          <label
            >{{ t('instance') }}<input v-model="row.legacyInstanceId" required
          /></label>
          <label
            >{{ t('mode')
            }}<select v-model="row.targetMode">
              <option value="shared">{{ t('shared') }}</option>
              <option value="dedicated">{{ t('dedicated') }}</option>
            </select></label
          >
          <button type="button" @click="rows.splice(index, 1)">
            {{ t('remove') }}
          </button>
        </div>
        <button
          type="button"
          :disabled="rows.length >= 1000"
          @click="
            rows.push({
              memberId: '',
              legacyInstanceId: '',
              targetMode: 'shared',
            })
          "
        >
          {{ t('add') }}
        </button>
        <label
          >{{ t('reason')
          }}<textarea v-model="reason" required maxlength="512" />
        </label>
        <button :disabled="!!plan && !etag">{{ t('save') }}</button>
      </fieldset>
    </form>
    <p>{{ risk('policy') }}</p>
    <div class="rebuild-risks">
      <h4>{{ risk('title') }}</h4>
      <p>{{ risk('risks') }}</p>
      <p>{{ risk('impact') }}</p>
    </div>
    <label
      ><input
        v-model="confirmed"
        type="checkbox"
        :disabled="!editable || !plan || !etag"
      />{{ t('confirm') }}</label
    >
    <button
      type="button"
      :disabled="!editable || !plan || !etag || !confirmed"
      @click="write('start')"
    >
      {{ t('start') }}
    </button>
    <p v-if="pending" role="alert">
      {{ t('uncertain') }} <code>{{ pending.key.slice(0, 8) }}</code>
    </p>
    <button
      v-if="pending"
      type="button"
      :disabled="busy || !canWrite"
      @click="write(pending.kind)"
    >
      {{ t('retry') }}
    </button>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <div v-if="progress" role="status">
      <h4>{{ progress.planId }} · {{ t(progress.state) }}</h4>
      <article v-for="member in progress.members" :key="member.memberId">
        <p>
          {{ member.memberId }} · {{ member.operationId || t('unknown') }} ·
          {{ risk('states.' + member.state) }}
        </p>
        <p v-if="member.failureCode">
          {{ risk('failures.' + member.failureCode) }}
        </p>
        <p>
          {{ t('sessions') }}:
          {{ t(member.sessionsDiscarded ? 'yes' : 'no') }} ·
          {{ t('workspace') }}: {{ t(member.workspaceEmpty ? 'yes' : 'no') }}
        </p>
      </article>
    </div>
  </section>
</template>
<style scoped>
.placement-admin {
  border-top: 1px solid var(--auriga-border);
  margin-top: 2rem;
  padding-top: 1rem;
  line-height: 1.7;
}
article {
  border-bottom: 1px solid var(--auriga-border);
  padding: 0.5rem 0;
  overflow-wrap: anywhere;
}
label {
  display: block;
  margin: 0.5rem 0;
}
input:not([type='checkbox']),
textarea,
select {
  display: block;
  max-width: 100%;
  width: 100%;
  box-sizing: border-box;
}
button,
input,
textarea,
select {
  font: inherit;
  color: inherit;
  background: var(--auriga-surface);
  border: 1px solid var(--auriga-border);
  border-radius: 6px;
  padding: 0.4rem;
}
button {
  cursor: pointer;
  margin: 0.3rem;
}
button:disabled {
  opacity: 0.5;
  cursor: default;
}
fieldset {
  border: 0;
  padding: 0;
  min-width: 0;
}
.member-row {
  display: flex;
  align-items: end;
  gap: 1rem;
  flex-wrap: wrap;
}
.member-row label {
  flex: 1 1 14rem;
}
code {
  overflow-wrap: anywhere;
}
</style>
