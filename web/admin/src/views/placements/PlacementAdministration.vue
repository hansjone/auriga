<script setup lang="ts">
import {
  computed,
  onMounted,
  onBeforeUnmount,
  ref,
  shallowRef,
  inject,
} from 'vue';
import { routeLocationKey } from 'vue-router';
import { securityApi } from '../../admin/securityApi';
import { wizardMessages } from './wizardMessages';
import { useI18n } from 'vue-i18n';
import { HintTrigger, isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  placementAdminApi as api,
  intentKey,
  selectable,
  type Plan,
  type PlanRequest,
  type PlanProgress,
  type Capacity,
  type Candidate,
  type Preflight,
  type Summary,
} from './api';
import { adminPlacementMessages } from './adminMessages';
import { placementMessages } from './messages';
const { t, te } = useI18n({
  useScope: 'local',
  messages: {
    'zh-CN': {
      ...adminPlacementMessages['zh-CN'],
      ...wizardMessages['zh-CN'],
      risk: placementMessages['zh-CN'],
    },
    en: {
      ...adminPlacementMessages.en,
      ...wizardMessages.en,
      risk: placementMessages.en,
    },
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
const route = inject(routeLocationKey, undefined);
const step = ref(1),
  risksAccepted = ref(false);
const rows = ref<PlanRequest['members']>([]);
const page = ref<Candidate[]>([]),
  total = ref(0),
  offset = ref(0),
  query = ref('');
const known = ref<Record<string, Candidate>>({});
const history = ref<Summary[]>([]),
  preflight = ref<Preflight>();
const listError = ref(''),
  historyError = ref(''),
  portal = ref('');
const entitlements = ref<Record<string, boolean>>({});
function dedicatedAllowed(id: string) {
  return entitlements.value[id] ?? known.value[id]?.dedicatedEntitled ?? false;
}
const localDeadline = ref('');
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
const pageSize = 50;
function memberName(id: string) {
  const c = known.value[id];
  return (
    plan.value?.members.find((m) => m.memberId === id)?.displayName ||
    c?.displayName ||
    c?.email ||
    t('unnamed')
  );
}
function instanceName(id: string) {
  return (
    plan.value?.members.find((m) => m.legacyInstanceId === id)
      ?.legacyInstanceName ||
    Object.values(known.value)
      .flatMap((c) => c.legacyInstances)
      .find((i) => i.instanceId === id)?.name ||
    t('unknown')
  );
}
function toggle(c: Candidate) {
  if (!editable.value || !selectable(c)) return;
  if (rows.value.some((m) => m.memberId === c.memberId))
    rows.value = rows.value.filter((m) => m.memberId !== c.memberId);
  else if (rows.value.length < 1000)
    rows.value.push({
      memberId: c.memberId,
      legacyInstanceId: c.legacyInstances[0].instanceId,
      targetMode: 'shared',
    });
  preflight.value = undefined;
}
async function discover(next = 0, memberId?: string) {
  listError.value = '';
  try {
    const value = await api.candidates(
      query.value.trim(),
      next,
      abort.signal,
      memberId,
    );
    if (!alive) return;
    page.value = value.items;
    total.value = value.total;
    offset.value = next;
    for (const c of value.items) known.value[c.memberId] = c;
    const match = memberId && value.items.find((c) => c.memberId === memberId);
    if (match && !rows.value.some((m) => m.memberId === memberId)) {
      if (selectable(match) && canWrite.value)
        rows.value.push({
          memberId: match.memberId,
          legacyInstanceId: match.legacyInstances[0].instanceId,
          targetMode: 'shared',
        });
    }
  } catch (e) {
    if (alive) {
      page.value = [];
      total.value = 0;
      listError.value = fail(e);
    }
  }
}
async function reloadHistory() {
  if (busy.value || pending.value) return;
  busy.value = true;
  try {
    if (await access()) await refreshHistory();
  } catch (e) {
    if (alive) historyError.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
}
async function refreshHistory() {
  try {
    const value = await api.history(abort.signal);
    if (alive) {
      history.value = value.items;
      historyError.value = '';
    }
  } catch (e) {
    if (alive) historyError.value = fail(e);
  }
}
async function browse(next: number) {
  if (busy.value || pending.value) return;
  busy.value = true;
  try {
    if (await access()) await discover(next);
  } catch (e) {
    if (alive) listError.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
}
function localTime(epoch?: number) {
  if (epoch === undefined) return '';
  const date = new Date(epoch * 1000);
  if (Number.isNaN(date.getTime())) return '';
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 19);
}
function receivePlan(value: Plan) {
  plan.value = value;
  planId.value = value.planId;
  reason.value = value.reason;
  localDeadline.value = localTime(value.exportUntil);
  rows.value = value.members.map(
    ({ memberId, legacyInstanceId, targetMode }) => ({
      memberId,
      legacyInstanceId,
      targetMode,
    }),
  );
  confirmed.value = false;
  risksAccepted.value = false;
}
async function checkRows() {
  preflight.value = undefined;
  const value = await api.preflight(
    rows.value.map((m) => ({ ...m })),
    abort.signal,
  );
  if (!alive) return;
  if (
    value.items.length !== rows.value.length ||
    value.items.some((v, i) => v.memberId !== rows.value[i].memberId)
  )
    throw new Error('binding');
  for (const item of value.items)
    entitlements.value[item.memberId] = item.dedicatedEntitled;
  preflight.value = value;
}
const passed = computed(
  () =>
    !!preflight.value?.items.length &&
    preflight.value.items.every((m) => m.eligible),
);
async function reviewTargets() {
  if (busy.value || pending.value || !rows.value.length || !canWrite.value)
    return;
  busy.value = true;
  step.value = 2;
  error.value = '';
  try {
    if (await access()) await checkRows();
  } catch (e) {
    if (alive) error.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
}
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
  if (!canRead.value || (write && !canWrite.value)) {
    page.value = [];
    known.value = {};
    entitlements.value = {};
    history.value = [];
    preflight.value = undefined;
    plan.value = undefined;
    snapshot.value = undefined;
    portal.value = '';
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
    ...(localDeadline.value
      ? {
          exportUntil:
            localDeadline.value === localTime(plan.value?.exportUntil) &&
            plan.value?.exportUntil !== undefined
              ? plan.value.exportUntil
              : Math.floor(new Date(localDeadline.value).getTime() / 1000),
        }
      : {}),
  };
  if (
    (body.exportUntil !== undefined &&
      (!Number.isSafeInteger(body.exportUntil) || body.exportUntil < 0)) ||
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
async function readPlan(id: string) {
  if (busy.value || pending.value || !uuid(id)) return;
  busy.value = true;
  error.value = '';
  notice.value = '';
  confirmed.value = false;
  risksAccepted.value = false;
  plan.value = undefined;
  planId.value = id;
  progress.value = undefined;
  etag.value = '';
  preflight.value = undefined;
  try {
    if (!(await access())) return;
    const value = await api.plan(id, abort.signal);
    if (!alive) return;
    if (value.data.planId !== id) throw new Error('binding');
    receivePlan(value.data);
    etag.value = value.etag;
    if (value.data.state === 'draft') {
      step.value = 4;
      await checkRows();
    } else step.value = 5;
  } catch (e) {
    if (alive) error.value = fail(e);
  } finally {
    if (alive) busy.value = false;
  }
  if (alive && step.value === 5 && plan.value) await readProgress();
}
function reset() {
  if (busy.value || pending.value) return;
  plan.value = undefined;
  progress.value = undefined;
  etag.value = '';
  planId.value = '';
  reason.value = '';
  entitlements.value = {};
  rows.value = [];
  step.value = 1;
  risksAccepted.value = false;
  preflight.value = undefined;
  localDeadline.value = '';
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
        !confirmed.value ||
        !risksAccepted.value ||
        !passed.value ||
        step.value !== 4 ||
        (plan.value.exportUntil !== undefined &&
          plan.value.exportUntil > now.value))
    )
      return;
    if (
      kind !== 'start' &&
      (!editable.value ||
        !passed.value ||
        step.value !== 3 ||
        (plan.value && !etag.value))
    )
      return;
    try {
      const body = request();
      // Starting always applies to the saved authoritative list, never unsaved edits.
      if (
        kind === 'start' &&
        JSON.stringify(body) !==
          JSON.stringify({
            reason: plan.value!.reason,
            members: plan.value!.members.map(
              ({ memberId, legacyInstanceId, targetMode }) => ({
                memberId,
                legacyInstanceId,
                targetMode,
              }),
            ),
            ...(plan.value!.exportUntil !== undefined
              ? { exportUntil: plan.value!.exportUntil }
              : {}),
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
      if (value.members.some((m) => m.state === 'succeeded')) void loadPortal();
      if (plan.value) plan.value = { ...plan.value, state: value.state };
      etag.value = '';
      notice.value = t('registeredOnly');
      step.value = 5;
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
      etag.value = tagged.etag;
      receivePlan(tagged.data);
      step.value = tagged.data.state === 'draft' ? 4 : 5;
      if (step.value === 4) await checkRows();
    }
    pending.value = undefined;
    confirmed.value = false;
    risksAccepted.value = false;
    await refreshHistory();
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
    if (!progress.value || value.revision >= progress.value.revision) {
      progress.value = value;
      if (value.members.some((m) => m.state === 'succeeded')) void loadPortal();
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
async function loadPage() {
  await capacity();
  if (!alive || !canRead.value) return;
  busy.value = true;
  const member = route?.query.memberId;
  try {
    await Promise.all([
      discover(0, typeof member === 'string' ? member : undefined),
      refreshHistory(),
    ]);
  } finally {
    if (alive) busy.value = false;
  }
}
let portalAttempted = false;
async function loadPortal() {
  if (portalAttempted || !permissions.value.includes('org.policies.read'))
    return;
  portalAttempted = true;
  try {
    const url = new URL((await securityApi.entries()).portalOrigin);
    if (alive && ['https:', 'http:'].includes(url.protocol))
      portal.value = url.origin;
  } catch {
    /* The member detail link stays available when deployment settings are restricted. */
  }
}
const poll = globalThis.setInterval(() => {
  if (
    step.value === 5 &&
    (plan.value?.state === 'running' ||
      progress.value?.members.some(
        (m) => !['succeeded', 'failed', 'blocked'].includes(m.state),
      ))
  )
    void readProgress();
}, 5000);
onMounted(loadPage);
onBeforeUnmount(() => {
  alive = false;
  abort.abort();
  globalThis.clearInterval(clock);
  globalThis.clearInterval(poll);
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
      <details>
        <summary>{{ t('technical') }}</summary>
        {{ pool.physicalInstanceId }}
      </details>
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
    <header class="section-head">
      <h3>{{ t('impact') }}</h3>
      <HintTrigger>
        <p>{{ risk('impact') }}</p>
        <p>{{ t('retained') }}</p>
      </HintTrigger>
    </header>
    <article v-for="impact in impacts" :key="impact.operationId" role="status">
      <p>
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
        {{ impact.affected.map((v) => memberName(v.memberId)).join(', ') }}
      </p>
      <details>
        <summary>{{ t('technical') }}</summary>
        {{ impact.physicalInstanceId }} / {{ impact.operationId }} /
        {{ impact.affected.map((v) => v.memberId).join(', ') }}
      </details>
    </article>
    <section v-if="canRead" id="migration-wizard">
      <header class="section-head">
        <h3>{{ t('wizard') }}</h3>
        <HintTrigger
          ><p>{{ risk('policy') }}</p>
          <p>{{ risk('risks') }}</p>
          <p>{{ risk('impact') }}</p></HintTrigger
        >
      </header>
      <ol class="steps">
        <li
          v-for="(key, i) in [
            'discover',
            'targets',
            'window',
            'review',
            'track',
          ]"
          :key="key"
          :aria-current="step === i + 1 ? 'step' : undefined"
        >
          {{ t(key) }}
        </li>
      </ol>

      <fieldset :disabled="busy || !!pending">
        <template v-if="step === 1">
          <form @submit.prevent="browse(0)">
            <label
              >{{ t('search')
              }}<input v-model="query" maxlength="256" type="search" /></label
            ><button>{{ t('find') }}</button>
          </form>
          <p v-if="listError" role="alert">{{ listError }}</p>
          <p v-else-if="!page.length">{{ t('noCandidates') }}</p>
          <article v-for="c in page" :key="c.memberId">
            <label
              ><input
                type="checkbox"
                :checked="rows.some((m) => m.memberId === c.memberId)"
                :disabled="
                  !canWrite ||
                  !selectable(c) ||
                  (!rows.some((m) => m.memberId === c.memberId) &&
                    rows.length >= 1000)
                "
                @change="toggle(c)"
              />
              <strong>{{ c.displayName || c.email || t('unnamed') }}</strong> ·
              {{ c.email || t('unknown') }} ·
              {{
                c.memberType && te('types.' + c.memberType)
                  ? t('types.' + c.memberType)
                  : t('unknown')
              }}</label
            >
            <span
              >{{ t(c.currentMode) }} ·
              {{
                c.eligibility === 'eligible' && !selectable(c)
                  ? t('inactive')
                  : c.eligibilityReason
              }}</span
            >
            <p v-for="i in c.legacyInstances" :key="i.instanceId">
              {{ i.name }} · {{ i.phase }} ·
              {{ t(i.ready ? 'ready' : 'notReady') }}
            </p>
            <details>
              <summary>{{ t('technical') }}</summary>
              <code
                >{{ c.memberId }}
                <span v-for="i in c.legacyInstances" :key="i.instanceId">
                  / {{ i.instanceId }}</span
                ></code
              >
            </details>
          </article>
          <p>{{ t('count', { total, selected: rows.length }) }}</p>
          <button :disabled="!offset" @click="browse(offset - pageSize)">
            {{ t('prevPage') }}
          </button>
          <button
            :disabled="offset + pageSize >= total"
            @click="browse(offset + pageSize)"
          >
            {{ t('nextPage') }}
          </button>
          <button :disabled="!rows.length || !canWrite" @click="reviewTargets">
            {{ t('targets') }}
          </button>
        </template>
        <template v-if="step === 2">
          <p>{{ risk('policy') }}</p>
          <article v-for="row in rows" :key="row.memberId">
            <strong>{{ memberName(row.memberId) }}</strong> ·
            {{ instanceName(row.legacyInstanceId) }}
            <label
              >{{ t('legacy') }} → {{ t('mode') }}
              <select v-model="row.targetMode" @change="preflight = undefined">
                <option value="shared">{{ t('shared') }}</option>
                <option
                  value="dedicated"
                  :disabled="!dedicatedAllowed(row.memberId)"
                >
                  {{ t('dedicated') }}
                </option>
              </select></label
            >
            <p v-if="!dedicatedAllowed(row.memberId)">{{ t('grantFirst') }}</p>
            <p v-if="preflight">
              {{
                preflight.items.find((i) => i.memberId === row.memberId)?.reason
              }}
            </p>
            <button
              @click="
                rows = rows.filter((m) => m.memberId !== row.memberId);
                preflight = undefined;
              "
            >
              {{ t('remove') }}
            </button>
          </article>
          <p v-if="preflight">{{ t('capacityUnknown') }}</p>
          <button
            @click="
              step = 1;
              preflight = undefined;
            "
          >
            {{ t('back') }}
          </button>
          <button :disabled="!rows.length" @click="reviewTargets">
            {{ t('check') }}
          </button>
          <button :disabled="!passed" @click="step = 3">
            {{ t('window') }}
          </button>
        </template>
        <template v-if="step === 3">
          <p>{{ t('exportGuide') }}</p>
          <label
            >{{ t('deadline') }} ({{ zone }})<input
              v-model="localDeadline"
              type="datetime-local"
              step="1"
          /></label>
          <p>{{ t('optionalDeadline') }}</p>
          <label
            >{{ t('reason')
            }}<textarea v-model="reason" maxlength="512" required />
          </label>
          <button @click="step = 2">{{ t('back') }}</button>
          <button
            :disabled="
              !editable || !passed || !reason.trim() || (!!plan && !etag)
            "
            @click="write(plan ? 'update' : 'create')"
          >
            {{ t('saveDraft') }}
          </button>
        </template>
        <template v-if="step === 4 && plan">
          <h4>{{ plan.reason }} · {{ t(plan.state) }}</h4>
          <p>
            {{ t('deadline') }} ·
            {{
              plan.exportUntil !== undefined
                ? time(plan.exportUntil)
                : t('manualWindow')
            }}
          </p>
          <ul>
            <li v-for="m in plan.members" :key="m.memberId">
              {{ memberName(m.memberId) }} ·
              {{ instanceName(m.legacyInstanceId) }} → {{ t(m.targetMode) }}
              <span
                v-if="
                  preflight?.items.find(
                    (i) => i.memberId === m.memberId && !i.eligible,
                  )
                "
              >
                ·
                {{
                  preflight.items.find((i) => i.memberId === m.memberId)?.reason
                }}</span
              >
            </li>
          </ul>
          <p>
            {{ t('affected') }} ·
            {{
              impacts.length
                ? impacts
                    .flatMap((i) =>
                      i.affected.map((a) => memberName(a.memberId)),
                    )
                    .join(', ')
                : t(stale ? 'unknown' : 'noObservedImpact')
            }}
          </p>
          <ul>
            <li
              v-for="key in [
                'discard',
                'archive',
                'emptyWorkspace',
                'poolStop',
              ]"
              :key="key"
            >
              {{ t(key) }}
            </li>
          </ul>
          <label
            ><input
              v-model="confirmed"
              type="checkbox"
              :disabled="!editable || !etag"
            />{{ t('confirmWindow') }}</label
          >
          <label
            ><input
              v-model="risksAccepted"
              type="checkbox"
              :disabled="!editable || !etag"
            />{{ t('confirmRisks') }}</label
          >
          <p v-if="plan.exportUntil !== undefined && plan.exportUntil > now">
            {{ t('waitDeadline') }}
          </p>
          <button
            :disabled="!editable"
            @click="
              confirmed = false;
              risksAccepted = false;
              reviewTargets();
            "
          >
            {{ t('editDraft') }}
          </button>
          <button
            :disabled="
              !editable ||
              !etag ||
              !confirmed ||
              !risksAccepted ||
              !passed ||
              (plan.exportUntil !== undefined && plan.exportUntil > now)
            "
            @click="write('start')"
          >
            {{ t('start') }}
          </button>
        </template>
        <button v-if="step === 5" @click="readProgress">
          {{ t('progress') }}
        </button>
        <details v-if="rows.length && step !== 1">
          <summary>{{ t('technical') }}</summary>
          <code>{{ planId }}</code>
          <p v-for="m in rows" :key="m.memberId">
            {{ memberName(m.memberId) }} · {{ m.memberId }} /
            {{ m.legacyInstanceId }}
          </p>
        </details>
      </fieldset>
      <section class="history">
        <header class="section-head">
          <h4>{{ t('history') }}</h4>
          <button :disabled="busy || !!pending" @click="reloadHistory">
            {{ t('refreshHistory') }}
          </button>
          <button :disabled="busy || !!pending || !canWrite" @click="reset">
            {{ t('newPlan') }}
          </button>
        </header>
        <p v-if="historyError" role="alert">{{ historyError }}</p>
        <p v-else-if="!history.length">{{ t('noHistory') }}</p>
        <article v-for="h in history" :key="h.planId">
          <strong>{{ h.reason }}</strong> · {{ t(h.state) }} ·
          {{ t('membersCount', { n: h.memberCount }) }} ·
          {{
            h.exportUntil !== undefined
              ? time(h.exportUntil)
              : t('manualWindow')
          }}
          <button :disabled="busy || !!pending" @click="readPlan(h.planId)">
            {{ t('resume') }}
          </button>
        </article>
      </section>
    </section>
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
      <h4>{{ t(progress.state) }}</h4>
      <article v-for="member in progress.members" :key="member.memberId">
        <p>
          {{ member.displayName || memberName(member.memberId) }} ·
          {{ member.legacyInstanceName || '' }} ·
          {{
            member.action === 'archive-reset' && member.state === 'preparing'
              ? t('archiving')
              : risk('states.' + member.state)
          }}
        </p>
        <p v-if="member.failureCode">
          {{ risk('failures.' + member.failureCode) }} · {{ t('recovery') }}
        </p>
        <p>
          {{ t('sessions') }}:
          {{ t(member.sessionsDiscarded ? 'yes' : 'no') }} ·
          {{ t('workspace') }}: {{ t(member.workspaceEmpty ? 'yes' : 'no') }}
        </p>
        <details>
          <summary>{{ t('technical') }}</summary>
          <code
            >{{ member.memberId }} /
            {{ member.operationId || t('unknown') }}</code
          >
        </details>
        <template v-if="member.state === 'succeeded'"
          ><RouterLink :to="'/members/' + member.memberId">{{
            t('memberDetail')
          }}</RouterLink>
          <a
            v-if="portal"
            :href="portal"
            target="_blank"
            rel="noopener noreferrer"
            >{{ t('portal') }}</a
          ></template
        >
      </article>
    </div>
  </section>
</template>
<style scoped>
.placement-admin {
  border-top: 1px solid var(--auriga-border);
  margin-top: 1.5rem;
  padding-top: 1rem;
  line-height: 1.55;
}
.section-head {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  margin: 1rem 0 0.5rem;
}
.section-head h3 {
  margin: 0;
}
article {
  border-bottom: 1px solid var(--auriga-border);
  padding: 0.65rem 0;
  overflow-wrap: anywhere;
}
label {
  display: block;
  margin: 0.5rem 0;
  font-size: 13px;
  color: var(--auriga-muted);
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
  color: var(--auriga-text);
  background: var(--auriga-field-bg, var(--auriga-surface));
  border: 1px solid var(--auriga-border);
  border-radius: 8px;
  padding: 0.45rem 0.65rem;
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
.steps {
  display: flex;
  gap: 0.8rem 1.5rem;
  flex-wrap: wrap;
  padding: 0.75rem 1.7rem;
  border-block: 1px solid var(--auriga-border);
  list-style-position: inside;
}
.steps li[aria-current='step'] {
  font-weight: 700;
  color: var(--auriga-accent);
}
.history {
  margin-top: 1.5rem;
}
summary {
  cursor: pointer;
  color: var(--auriga-muted);
  font-size: 0.875rem;
}
</style>
