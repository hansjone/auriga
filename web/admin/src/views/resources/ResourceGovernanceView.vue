<script setup lang="ts">
/* global File, Event, HTMLInputElement */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  resourceApi,
  strongEtag,
  type ResourceVersion,
  type GovernanceDetail,
  type ResourceDistribution,
  type DistributionInput,
  type AdminPackagePatch,
  type UploadMetadata,
} from '../../admin/resourceApi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import { requestStepUp } from '../../admin/stepUp';
import { useAdminI18n } from '../../admin/messages';
import '../../admin/resources.css';
const props = defineProps<{ phase: 'review' | 'published' | 'distribution' }>();
const { t } = useAdminI18n();
const items = shallowRef<ResourceVersion[]>([]),
  distributions = shallowRef<ResourceDistribution[]>([]),
  detail = shallowRef<GovernanceDetail>();
const membership =
  shallowRef<Awaited<ReturnType<typeof adminApi.membership>>>();
const error = ref(''),
  notice = ref(''),
  loading = ref(false),
  busy = ref(false),
  allowed = ref(false),
  verifyNeeded = ref(false);
const nextCursor = ref<string>(),
  history = ref<(string | undefined)[]>([]),
  cursor = ref<string>();
const reason = ref(''),
  level = ref<'l1' | 'l2' | 'l3'>('l3'),
  scope = ref<'tenant' | 'members' | 'groups'>('tenant'),
  groups = ref('');
const packageId = ref(''),
  uploadVersion = ref(''),
  digest = ref(''),
  range = ref('*'),
  notes = ref('');
const os = ref<'linux' | 'windows' | 'any'>('any'),
  arch = ref<'amd64' | 'arm64' | 'any'>('any');
const archive = shallowRef<File>(),
  uploaded = shallowRef<ResourceVersion>();
const existingVersion = ref(''),
  personal = ref(false);
const instance = ref(''),
  distributionPackage = ref(''),
  distributionVersion = ref(''),
  action = ref<'install' | 'enable' | 'disable' | 'remove'>('install');
const pending = shallowRef<{
  message: string;
  run: () => Promise<unknown>;
  started: boolean;
  at: number;
}>();
const confirming = ref(false);
let epoch = 0,
  detailEpoch = 0,
  alive = true;
const permission = computed(() =>
  props.phase === 'review'
    ? 'resource.versions.review'
    : props.phase === 'published'
      ? 'resource.versions.publish'
      : 'resource.distributions.manage',
);
const locked = computed(() => busy.value || !!pending.value);
const can = (p: string) => membership.value?.permissions.includes(p) ?? false;
const author = computed(
  () =>
    !!detail.value && detail.value.authorId === membership.value?.member?.id,
);
const pendingReview = computed(
  () =>
    detail.value?.reviews
      .filter(
        (r) =>
          r.review.state === 'pending' &&
          r.review.versionRevision === detail.value?.version.revision,
      )
      .at(-1)?.review,
);
const fileChanges = computed(() => {
  const before = new Map(
    (detail.value?.previousFiles ?? []).map((f) => [f.path, f]),
  );
  const current = new Set((detail.value?.files ?? []).map((f) => f.path));
  return [
    ...(detail.value?.files ?? []).map((f) => ({
      ...f,
      change:
        before.get(f.path)?.digest === f.digest
          ? 'marketUnchangedFile'
          : 'marketChangedFile',
    })),
    ...[...before.values()]
      .filter((f) => !current.has(f.path))
      .map((f) => ({ ...f, change: 'marketRemovedFile' })),
  ];
});
function failure(e: unknown, writing = false) {
  if (!isApiError(e))
    return t(writing ? 'marketUncertain' : 'marketLoadFailed');
  if (e.problem.code === 'iam.step_up_required') {
    verifyNeeded.value = true;
    return t('resourceStepup');
  }
  return t(
    e.status === 409 || e.status === 412
      ? 'conflict'
      : e.status === 403
        ? 'forbidden'
        : e.status === 404
          ? 'resourceMissing'
          : e.status === 503
            ? 'resourceUnavailable'
            : e.status === 429
              ? 'limited'
              : 'failed',
  );
}
async function load(target?: string, direction?: 'next' | 'previous') {
  if (locked.value) return;
  const request = ++epoch,
    phase = props.phase;
  ++detailEpoch;
  items.value = [];
  distributions.value = [];
  detail.value = undefined;
  nextCursor.value = undefined;
  allowed.value = false;
  error.value = '';
  loading.value = true;
  try {
    const member = await adminApi.membership();
    if (!alive || request !== epoch) return;
    membership.value = member;
    if (!member.permissions.includes(permission.value)) {
      error.value = t('forbidden');
      return;
    }
    allowed.value = true;
    const page =
      phase === 'distribution'
        ? await resourceApi.distributions(target)
        : await resourceApi.governance(phase, target);
    if (!alive || request !== epoch) return;
    if (phase === 'distribution')
      distributions.value = page.items as ResourceDistribution[];
    else items.value = page.items as ResourceVersion[];
    nextCursor.value = page.nextCursor;
    if (direction === 'next') history.value.push(cursor.value);
    if (direction === 'previous') history.value.pop();
    cursor.value = target;
  } catch (e) {
    if (alive && request === epoch) error.value = failure(e);
  } finally {
    if (alive && request === epoch) loading.value = false;
  }
}
async function inspect(row: ResourceVersion) {
  if (locked.value || props.phase === 'distribution') return;
  const request = ++detailEpoch,
    phase = props.phase;
  detail.value = undefined;
  error.value = '';
  reason.value = '';
  try {
    const result = await resourceApi.governanceDetail(
      phase,
      row.packageId,
      row.id,
    );
    if (!alive || request !== detailEpoch) return;
    detail.value = result;
    const p = result.resourcePackage;
    level.value = p.level === 'l1' || p.level === 'l2' ? p.level : 'l3';
    scope.value =
      p.visibility === 'groups' || p.visibility === 'members'
        ? p.visibility
        : 'tenant';
    groups.value = p.groupIds.join(', ');
  } catch (e) {
    if (alive && request === detailEpoch) error.value = failure(e);
  }
}
function key() {
  const bytes = new Uint8Array(24);
  globalThis.crypto.getRandomValues(bytes);
  return [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
}
function prepare(label: string, target: string, run: () => Promise<unknown>) {
  if (locked.value) return;
  pending.value = {
    message: t('marketConfirm', {
      target,
      action: label,
      reason: reason.value.trim() || '—',
    }),
    run,
    started: false,
    at: Date.now(),
  };
  confirming.value = true;
  error.value = '';
}
async function execute() {
  const intent = pending.value;
  if (!intent || busy.value) return;
  if (Date.now() - intent.at >= 86400000) {
    error.value = t('resourceIntentExpired');
    confirming.value = false;
    return;
  }
  confirming.value = false;
  busy.value = true;
  intent.started = true;
  try {
    await intent.run();
    if (!alive) return;
    pending.value = undefined;
    notice.value = t('marketSaved');
    error.value = '';
    reason.value = '';
    busy.value = false;
    await load(cursor.value);
  } catch (e) {
    if (!alive) return;
    error.value = failure(e, true);
    if (
      isApiError(e) &&
      e.status < 500 &&
      e.problem.code !== 'iam.step_up_required'
    )
      pending.value = undefined;
  } finally {
    if (alive) busy.value = false;
  }
}
async function verify() {
  if (busy.value) return;
  try {
    await requestStepUp();
    verifyNeeded.value = false;
    if (!pending.value) await load(cursor.value);
  } catch (e) {
    error.value = failure(e);
  }
}
function decide(decision: 'approve' | 'reject') {
  const d = detail.value,
    review = pendingReview.value,
    why = reason.value.trim();
  if (!d || !review || author.value || !why || locked.value) return;
  const body = {
      decision,
      reason: why,
      versionRevision: d.version.revision,
      digest: d.version.digest,
    },
    token = key();
  prepare(
    t(decision === 'approve' ? 'marketApprove' : 'marketReject'),
    `${d.resourcePackage.name} ${d.version.version}`,
    () => resourceApi.decide(review.id, body, `"${review.revision}"`, token),
  );
}
function publish() {
  const d = detail.value,
    why = reason.value.trim();
  if (!d || author.value || !why) return;
  const token = key();
  prepare(
    t('marketPublish'),
    `${d.resourcePackage.name} ${d.version.version}`,
    () =>
      resourceApi.publish(
        d.resourcePackage.id,
        d.version.id,
        why,
        `"${d.version.revision}"`,
        token,
      ),
  );
}
function withdraw(mode: 'withdrawn' | 'malicious') {
  const d = detail.value,
    why = reason.value.trim();
  if (!d || !why) return;
  const token = key();
  prepare(
    t(mode === 'malicious' ? 'marketRevoke' : 'marketWithdraw'),
    `${d.resourcePackage.name} ${d.version.version}`,
    () =>
      resourceApi.withdraw(
        d.resourcePackage.id,
        d.version.id,
        mode,
        why,
        `"${d.version.revision}"`,
        token,
      ),
  );
}
function govern() {
  const d = detail.value,
    why = reason.value.trim();
  if (!d || !why || !can('resource.levels.admin')) return;
  const body: AdminPackagePatch = {
    level: level.value,
    visibility: scope.value,
    groupIds:
      scope.value === 'groups'
        ? groups.value
            .split(',')
            .map((s) => s.trim())
            .filter(Boolean)
        : [],
    reason: why,
  };
  prepare(
    t('marketGovern'),
    `${d.resourcePackage.name}: ${d.resourcePackage.level} → ${body.level}, ${d.resourcePackage.visibility} → ${body.visibility}`,
    () =>
      resourceApi.govern(
        d.resourcePackage.id,
        body,
        `"${d.resourcePackage.revision}"`,
      ),
  );
}
async function chooseFile(event: Event) {
  archive.value = (event.target as HTMLInputElement).files?.[0];
  digest.value = '';
  const file = archive.value;
  if (!file || file.size > 67108864 || file.size < 1) {
    archive.value = undefined;
    error.value = t('marketUploadInvalid');
    return;
  }
  if (globalThis.crypto.subtle) {
    const bytes = await globalThis.crypto.subtle.digest(
      'SHA-256',
      await file.arrayBuffer(),
    );
    if (alive && file === archive.value)
      digest.value =
        'sha256:' +
        [...new Uint8Array(bytes)]
          .map((b) => b.toString(16).padStart(2, '0'))
          .join('');
  }
}
async function upload() {
  if (locked.value) return;
  const file = archive.value,
    pid = packageId.value.trim(),
    version = uploadVersion.value.trim(),
    expectedDigest = digest.value.trim();
  if (
    !file ||
    !version ||
    !pid ||
    !/^sha256:[0-9a-f]{64}$/.test(expectedDigest)
  ) {
    error.value = t('marketUploadInvalid');
    return;
  }
  busy.value = true;
  try {
    const pack = await resourceApi.get(pid);
    if (!alive) return;
    if (!strongEtag(pack.etag)) throw new Error('Missing ETag');
    const etag = pack.etag,
      metadata: UploadMetadata = {
        version,
        expectedDigest,
        manifest: {
          name: pack.data.name,
          dshVersionRange: range.value,
          os: os.value,
          arch: arch.value,
        },
        ...(notes.value.trim() ? { changeNotes: notes.value.trim() } : {}),
      },
      token = key();
    busy.value = false;
    prepare(
      t('marketUpload'),
      `${pack.data.name} ${version} ${expectedDigest}`,
      async () => {
        const result = await resourceApi.upload(
          pid,
          metadata,
          file,
          etag,
          token,
        );
        if (alive) {
          uploaded.value = result;
          personal.value =
            pack.data.level === 'l4' && pack.data.visibility === 'private';
        }
      },
    );
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive) busy.value = false;
  }
}
async function recoverDraft() {
  if (locked.value || !packageId.value.trim() || !existingVersion.value.trim())
    return;
  busy.value = true;
  try {
    const pid = packageId.value.trim();
    const [pack, version] = await Promise.all([
      resourceApi.get(pid),
      resourceApi.version(pid, existingVersion.value.trim()),
    ]);
    if (!alive) return;
    if (
      pack.data.ownerId !== membership.value?.member.id ||
      !['draft', 'rejected'].includes(version.state)
    )
      throw new Error('Unavailable draft');
    uploaded.value = version;
    reason.value = '';
    personal.value =
      pack.data.level === 'l4' && pack.data.visibility === 'private';
    error.value = '';
  } catch (e) {
    if (alive) error.value = isApiError(e) ? failure(e) : t('resourceMissing');
  } finally {
    if (alive) busy.value = false;
  }
}
function resetDraft() {
  const v = uploaded.value,
    why = reason.value.trim();
  if (!v || v.state !== 'rejected' || !why) return;
  const token = key();
  prepare(t('marketResetDraft'), v.version, async () => {
    const result = await resourceApi.draft(
      v.packageId,
      v.id,
      why,
      `"${v.revision}"`,
      token,
    );
    if (alive) uploaded.value = result;
  });
}
function signPersonal() {
  const v = uploaded.value,
    why = reason.value.trim();
  if (!v || !personal.value || v.state !== 'draft' || !why) return;
  const token = key();
  prepare(
    t('marketPersonalSign'),
    `${v.version} · ${t('marketUnreviewed')}`,
    async () => {
      await resourceApi.personalSign(
        v.packageId,
        v.id,
        why,
        `"${v.revision}"`,
        token,
      );
      if (alive) uploaded.value = undefined;
    },
  );
}
function submit() {
  const v = uploaded.value,
    why = reason.value.trim();
  if (!v || !why) return;
  const token = key();
  prepare(t('marketSubmit'), v.version, async () => {
    await resourceApi.submit(v.packageId, v.id, why, `"${v.revision}"`, token);
    if (alive) uploaded.value = undefined;
  });
}
function distribute() {
  if (
    !instance.value.trim() ||
    !distributionPackage.value.trim() ||
    !distributionVersion.value.trim()
  )
    return;
  const body: DistributionInput = {
      instanceId: instance.value.trim(),
      selections: [
        {
          packageId: distributionPackage.value.trim(),
          versionId: distributionVersion.value.trim(),
          action: action.value,
        },
      ],
    },
    token = key();
  prepare(
    t('marketRegister'),
    `${body.instanceId}: ${body.selections[0]!.packageId} / ${body.selections[0]!.versionId} / ${t('marketDistributionActions.' + action.value)}`,
    () => resourceApi.distribute(body, token),
  );
}
watch(
  () => props.phase,
  () => {
    history.value = [];
    cursor.value = undefined;
    items.value = [];
    distributions.value = [];
    detail.value = undefined;
    allowed.value = false;
    ++epoch;
    ++detailEpoch;
    if (!pending.value) void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  alive = false;
  ++epoch;
  ++detailEpoch;
  pending.value = undefined;
  archive.value = undefined;
});
</script>
<template>
  <section class="resource-page market-governance">
    <header class="market-toolbar">
      <h1>{{ t('market' + phase) }}</h1>
      <button :disabled="loading || locked" @click="load(cursor)">
        {{ t('refresh') }}
      </button>
    </header>
    <nav class="market-tabs" :aria-label="t('marketNavigation')">
      <RouterLink to="/resources">{{ t('marketDirectory') }}</RouterLink
      ><RouterLink
        v-for="tab in ['review', 'published', 'distribution']"
        :key="tab"
        :to="'/resources/' + tab"
        >{{ t('market' + tab) }}</RouterLink
      >
    </nav>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button v-if="verifyNeeded" :disabled="busy" @click="verify">
      {{ t('marketVerify') }}
    </button>
    <p v-if="pending?.started && !busy" role="status">
      {{ t('marketUncertain') }}
      <button @click="confirming = true">{{ t('marketContinue') }}</button>
    </p>
    <template v-if="allowed">
      <template v-if="phase === 'distribution'">
        <p>{{ t('marketDistributionNote') }}</p>
        <form class="market-form" @submit.prevent="distribute">
          <fieldset :disabled="locked">
            <legend>{{ t('marketRegister') }}</legend>
            <label
              >{{ t('marketInstance')
              }}<input v-model="instance" required /></label
            ><label
              >{{ t('marketPackage')
              }}<input v-model="distributionPackage" required /></label
            ><label
              >{{ t('marketVersion') }} ID<input
                v-model="distributionVersion"
                required
            /></label>
            <label
              >{{ t('marketAction')
              }}<select v-model="action">
                <option
                  v-for="v in ['install', 'enable', 'disable', 'remove']"
                  :key="v"
                  :value="v"
                >
                  {{ t('marketDistributionActions.' + v) }}
                </option>
              </select></label
            ><button type="submit">{{ t('marketRegister') }}</button>
          </fieldset>
        </form>
        <div class="market-table">
          <table>
            <thead>
              <tr>
                <th>{{ t('marketInstance') }}</th>
                <th>{{ t('marketSelection') }}</th>
                <th>{{ t('marketPackage') }} / {{ t('marketVersion') }}</th>
                <th>{{ t('status') }}</th>
                <th>{{ t('marketGeneration') }}</th>
                <th>{{ t('details') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in distributions" :key="row.id">
                <td>{{ row.instanceId }}</td>
                <td>{{ row.selectionRevision }}</td>
                <td>
                  <ul v-if="row.selections?.length">
                    <li
                      v-for="selection in row.selections"
                      :key="selection.packageId"
                    >
                      {{ selection.packageId }} / {{ selection.versionId }}
                    </li>
                  </ul>
                  <span v-else>—</span>
                </td>
                <td>{{ t('marketDistributionStates.' + row.state) }}</td>
                <td>
                  {{ row.generation ?? '—' }} /
                  {{ row.observedGeneration ?? '—' }}
                </td>
                <td>{{ row.errorCode ?? '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!loading && !distributions.length">{{ t('empty') }}</p>
      </template>
      <template v-else>
        <p>{{ t('marketScope') }}</p>
        <form
          v-if="phase === 'review' && can('resource.packages.upload')"
          class="market-form"
          @submit.prevent="upload"
        >
          <fieldset :disabled="locked">
            <legend>{{ t('marketUpload') }}</legend>
            <label
              >{{ t('marketPackage')
              }}<input v-model="packageId" required /></label
            ><label
              >{{ t('marketVersion')
              }}<input
                v-model="uploadVersion"
                maxlength="128"
                required /></label
            ><label
              >{{ t('marketArchive')
              }}<input
                type="file"
                accept=".tgz,.gz,application/gzip"
                required
                @change="chooseFile" /></label
            ><label
              >{{ t('marketDigest')
              }}<input
                v-model="digest"
                required
                pattern="sha256:[0-9a-f]{64}" /></label
            ><label
              >{{ t('marketRange')
              }}<input v-model="range" required maxlength="128" /></label
            ><label
              >OS<select v-model="os">
                <option>any</option>
                <option>linux</option>
                <option>windows</option>
              </select></label
            ><label
              >Arch<select v-model="arch">
                <option>any</option>
                <option>amd64</option>
                <option>arm64</option>
              </select></label
            ><label
              >{{ t('marketChanges')
              }}<textarea v-model="notes" maxlength="4096" /></label
            ><button type="submit">{{ t('marketUpload') }}</button>
          </fieldset>
        </form>
        <form
          v-if="phase === 'review' && can('resource.packages.upload')"
          class="market-form"
          @submit.prevent="recoverDraft"
        >
          <fieldset :disabled="locked">
            <legend>{{ t('marketRecoverDraft') }}</legend>
            <label
              >{{ t('marketPackage') }}<input v-model="packageId" required
            /></label>
            <label
              >{{ t('marketVersion') }} ID<input
                v-model="existingVersion"
                required
            /></label>
            <button type="submit">{{ t('marketRecoverDraft') }}</button>
          </fieldset>
        </form>
        <div v-if="uploaded" class="market-form">
          <p>{{ t('marketUploadReady') }} {{ uploaded.id }}</p>
          <p v-if="personal">{{ t('marketUnreviewed') }}</p>
          <label
            >{{ t('marketReason')
            }}<textarea
              v-model="reason"
              :disabled="locked"
              maxlength="1024"
            /></label
          ><button
            v-if="uploaded.state === 'draft' && !personal"
            :disabled="
              locked || !reason.trim() || !can('resource.versions.submit')
            "
            @click="submit"
          >
            {{ t('marketSubmit') }}
          </button>
          <button
            v-if="uploaded.state === 'rejected'"
            :disabled="locked || !reason.trim()"
            @click="resetDraft"
          >
            {{ t('marketResetDraft') }}
          </button>
          <button
            v-if="uploaded.state === 'draft' && personal"
            :disabled="locked || !reason.trim()"
            @click="signPersonal"
          >
            {{ t('marketPersonalSign') }}
          </button>
        </div>
        <div class="market-table">
          <table>
            <thead>
              <tr>
                <th>{{ t('marketPackage') }}</th>
                <th>{{ t('marketVersion') }}</th>
                <th>{{ t('status') }}</th>
                <th>{{ t('marketCreated') }}</th>
                <th>{{ t('details') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in items" :key="row.id">
                <td>{{ row.packageId }}</td>
                <td>{{ row.version }}</td>
                <td>{{ t('resourceStateLabels.' + row.state) }}</td>
                <td>{{ row.createdAt }}</td>
                <td>
                  <button
                    data-testid="detail"
                    :disabled="locked"
                    @click="inspect(row)"
                  >
                    {{ t('details') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="!loading && !items.length">{{ t('empty') }}</p>
        <aside v-if="detail" :aria-label="t('details')">
          <header class="market-toolbar">
            <h2>
              {{ detail.resourcePackage.name }} · {{ detail.version.version }}
            </h2>
            <button :disabled="locked" @click="detail = undefined">
              {{ t('close') }}
            </button>
          </header>
          <dl>
            <dt>{{ t('marketDigest') }}</dt>
            <dd>{{ detail.version.digest }}</dd>
            <dt>{{ t('marketSize') }}</dt>
            <dd>{{ detail.version.sizeBytes }}</dd>
            <dt>{{ t('marketRevision') }}</dt>
            <dd>{{ detail.version.revision }}</dd>
            <dt>{{ t('marketChanges') }}</dt>
            <dd>
              {{ detail.previousVersion?.version ?? t('marketNoChanges') }} →
              {{ detail.version.version }}
              <p>{{ detail.metadata?.changeNotes }}</p>
            </dd>
          </dl>
          <p v-if="!detail.metadata || !detail.files">
            {{ t('marketNoEvidence') }}
          </p>
          <h3>{{ t('marketPermissions') }}</h3>
          <ul v-if="detail.metadata?.permissions.length">
            <li v-for="p in detail.metadata.permissions" :key="p">{{ p }}</li>
          </ul>
          <p v-else>{{ t('marketNoPermissions') }}</p>
          <h3>{{ t('marketFiles') }}</h3>
          <div class="market-table">
            <table>
              <tbody>
                <tr v-for="f in fileChanges" :key="f.path">
                  <td>{{ f.path }}</td>
                  <td>{{ f.sizeBytes }}</td>
                  <td class="market-digest">{{ f.digest }}</td>
                  <td>{{ t(f.change) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <h3>{{ t('marketHistory') }}</h3>
          <ul>
            <li v-for="record in detail.reviews" :key="record.review.id">
              {{ record.review.state }} · {{ record.reviewerId ?? '—' }} ·
              {{ record.reason }} · {{ record.review.digest }} /
              {{ record.review.versionRevision }}
            </li>
          </ul>
          <label
            >{{ t('marketReason')
            }}<textarea
              v-model="reason"
              data-testid="reason"
              :disabled="locked"
              maxlength="1024"
            />
          </label>
          <p v-if="author">{{ t('marketAuthorBlocked') }}</p>
          <div
            v-if="phase === 'review' && pendingReview && !author"
            class="market-toolbar"
          >
            <button
              data-testid="approve"
              :disabled="locked || !reason.trim() || !detail.metadata"
              @click="decide('approve')"
            >
              {{ t('marketApprove') }}</button
            ><button
              :disabled="locked || !reason.trim() || !detail.metadata"
              @click="decide('reject')"
            >
              {{ t('marketReject') }}
            </button>
          </div>
          <template v-if="phase === 'published'"
            ><button
              v-if="detail.version.state === 'approved' && !author"
              :disabled="locked || !reason.trim()"
              @click="publish"
            >
              {{ t('marketPublish') }}</button
            ><template
              v-if="['published', 'deprecated'].includes(detail.version.state)"
              ><p>{{ t('marketWithdrawNote') }}</p>
              <div class="market-toolbar">
                <button
                  v-if="detail.version.state !== 'withdrawn'"
                  :disabled="locked || !reason.trim()"
                  @click="withdraw('withdrawn')"
                >
                  {{ t('marketWithdraw') }}</button
                ><button
                  class="market-danger"
                  :disabled="locked || !reason.trim()"
                  @click="withdraw('malicious')"
                >
                  {{ t('marketRevoke') }}
                </button>
              </div></template
            >
            <form
              v-if="
                can('resource.levels.admin') &&
                membership?.member.roleKeys?.includes('owner') &&
                detail.resourcePackage.level !== 'l0'
              "
              class="market-form"
              @submit.prevent="govern"
            >
              <fieldset :disabled="locked">
                <legend>{{ t('marketGovern') }}</legend>
                <label
                  >{{ t('resourceLevel')
                  }}<select v-model="level">
                    <option>l1</option>
                    <option>l2</option>
                    <option>l3</option>
                  </select></label
                ><label
                  >{{ t('resourceVisibility')
                  }}<select v-model="scope">
                    <option
                      v-for="v in ['tenant', 'members', 'groups']"
                      :key="v"
                      :value="v"
                    >
                      {{ t('resourceVisibilityLabels.' + v) }}
                    </option>
                  </select></label
                ><label v-if="scope === 'groups'"
                  >{{ t('resourceGroups')
                  }}<input v-model="groups" required /></label
                ><button :disabled="!reason.trim()" type="submit">
                  {{ t('marketGovern') }}
                </button>
              </fieldset>
            </form>
          </template>
        </aside>
      </template>
      <div class="market-toolbar">
        <button
          :disabled="!history.length || loading || locked"
          @click="load(history[history.length - 1], 'previous')"
        >
          {{ t('marketPrevious') }}</button
        ><button
          data-testid="next"
          :disabled="!nextCursor || loading || locked"
          @click="load(nextCursor, 'next')"
        >
          {{ t('marketNext') }}
        </button>
      </div>
    </template>
    <ActionConfirm
      :message="confirming ? pending?.message : undefined"
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
.market-governance {
  padding: 24px;
}
.market-toolbar,
.market-tabs {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 20px;
  flex-wrap: wrap;
}
.market-toolbar h1,
.market-toolbar h2 {
  flex: 1;
  margin: 0;
}
.market-tabs {
  border-bottom: 1px solid var(--border-color, #304253);
  padding-bottom: 12px;
}
.market-table {
  overflow-x: auto;
  max-height: 420px;
}
table {
  width: 100%;
  text-align: left;
  border-collapse: collapse;
}
th,
td {
  padding: 12px;
  border-bottom: 1px solid var(--border-color, #304253);
}
aside,
.market-form {
  margin: 24px 0;
  padding: 20px;
  border: 1px solid var(--border-color, #304253);
  border-radius: 16px;
}
dd,
li {
  overflow-wrap: anywhere;
  margin: 4px 0 16px;
}
.market-digest {
  max-width: 240px;
  overflow-wrap: anywhere;
  font-family: monospace;
}
fieldset {
  display: flex;
  flex-wrap: wrap;
  align-items: end;
  gap: 16px;
  border: 0;
  padding: 0;
}
label {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 12px;
}
textarea {
  min-width: 300px;
  min-height: 64px;
}
input,
select,
textarea {
  color: var(--text-color, #e6edf5);
  background: var(--panel-color, #152333);
  border: 1px solid var(--border-color, #304253);
  border-radius: 8px;
  padding: 8px 12px;
}
.market-danger {
  color: #e5a6a6;
}
</style>
