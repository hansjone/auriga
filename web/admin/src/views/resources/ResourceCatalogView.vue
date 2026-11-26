<script setup lang="ts">
/* global HTMLElement */
import {
  computed,
  nextTick,
  onBeforeUnmount,
  ref,
  shallowRef,
  watch,
} from 'vue';
import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  resourceCatalogApi as api,
  catalogKey,
  catalogRevisionEtag,
  type CatalogVersion,
  type CatalogReview,
  type CatalogPackage,
  type CatalogDistribution,
} from '../../admin/resourceCatalogApi';
import {
  publicationReady,
  publicationBlockers,
  riskOptions,
  distributionApplied,
  textDiff,
} from '../../admin/resourceCatalogPolicy';
import {
  skillPreview,
  archivePreviewWithMeta,
  type PreviewFile,
} from '../../admin/resourceSkillPreview';
import type { CatalogKind } from '../../admin/resourceCatalogUpload';
import ResourceUploadPanel from './ResourceUploadPanel.vue';
import ResourceAssignments from './ResourceAssignments.vue';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
import { strongEtag } from '../../admin/etag';
import { requestStepUp } from '../../admin/stepUp';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import '../../admin/resources.css';
const props = defineProps<{
  phase: 'directory' | 'review' | 'published' | 'distribution';
  kind?: CatalogKind;
}>();
const kind = computed(() => props.kind ?? 'skill');
const { t } = useI18n({ useScope: 'local', messages: catalogMessages });
const member = shallowRef<Awaited<ReturnType<typeof adminApi.membership>>>();
const packages = shallowRef<CatalogPackage[]>([]),
  reviews = shallowRef<CatalogReview[]>([]),
  versions = shallowRef<CatalogVersion[]>([]),
  distributions = shallowRef<CatalogDistribution[]>([]);
const selected = shallowRef<CatalogVersion>(),
  review = shallowRef<CatalogReview>();
const detailPanel = ref<HTMLElement>();
const reviewEtag = ref<string | null>(null),
  versionEtag = ref<string | null>(null);
const loading = ref(false),
  busy = ref(false),
  error = ref(''),
  notice = ref(''),
  verifyNeeded = ref(false);
const next = ref<string>(),
  cursor = ref<string>(),
  history = ref<(string | undefined)[]>([]);
const activePackage = shallowRef<CatalogPackage>();
const versionNext = ref<string>(),
  versionCursor = ref<string>(),
  versionHistory = ref<(string | undefined)[]>([]),
  versionsLoading = ref(false);
const pid = ref(''),
  vid = ref(''),
  reason = ref('');
const uploadLocked = ref(false);
const role = ref<'general' | 'security'>('general'),
  risk = ref<'r1' | 'r2' | 'r3'>('r1');
const instance = ref(''),
  currentDistribution = ref(''),
  action = ref<'install' | 'enable' | 'disable' | 'remove'>('install');
const files = shallowRef<PreviewFile[]>([]),
  previous = shallowRef<PreviewFile[]>([]),
  previousVersion = shallowRef<CatalogVersion>(),
  currentTruncated = ref(false),
  previousTruncated = ref(false),
  previewTruncated = computed(
    () => currentTruncated.value || previousTruncated.value,
  ),
  comparison = ref(''),
  previewError = ref('');
const upgradeTarget = shallowRef<{
  packageId: string;
  name: string;
  latestVersion?: string;
  expectedPackageName?: string;
}>();
const uploadPanelAnchor = ref<HTMLElement>();
const pending = shallowRef<{
  label: string;
  run: () => Promise<unknown>;
  at: number;
  started: boolean;
}>();
const confirming = ref(false);
const assignmentLocked = ref(false);
let epoch = 0,
  detailEpoch = 0,
  previewEpoch = 0,
  compareEpoch = 0,
  alive = true;
const locked = computed(
  () =>
    busy.value ||
    !!pending.value ||
    assignmentLocked.value ||
    uploadLocked.value,
);
const can = (p: string) => member.value?.permissions.includes(p) ?? false;
const permission = computed(() =>
  props.phase === 'review'
    ? 'resource.versions.review'
    : props.phase === 'published'
      ? 'resource.versions.publish'
      : props.phase === 'distribution'
        ? 'resource.distributions.manage'
        : 'resource.packages.read',
);
const author = computed(
  () => !!review.value && review.value.authorId === member.value?.member.id,
);
const ready = computed(
  () =>
    !!review.value &&
    publicationReady(review.value, member.value?.member.id ?? ''),
);
const publicationIssues = computed(() =>
  review.value
    ? publicationBlockers(review.value, member.value?.member.id ?? '')
    : [],
);
const reviewProgress = computed(() => {
  if (!review.value) return undefined;
  return reviewStats(review.value);
});
function reviewStats(r: CatalogReview) {
  const v = r.version,
    riskInfo = v.risk;
  const current = r.decisions.filter(
    (d) =>
      d.versionRevision === v.revision &&
      d.digest === v.digest &&
      d.reviewerId !== r.authorId,
  );
  const latest = new Map(current.map((d) => [d.reviewerId, d]));
  const approvals = [...latest.values()].filter(
    (d) => d.decision === 'approve',
  );
  const high =
    v.kind === 'plugin' ||
    riskInfo.level !== 'r1' ||
    riskInfo.minimumLevel !== 'r1';
  const required = Math.max(riskInfo.requiredReviewers, high ? 2 : 1);
  const secRequired = Math.max(
    riskInfo.requiredSecurityReviewers,
    high ? 1 : 0,
  );
  const secActual = approvals.filter((d) => d.role === 'security').length;
  return {
    actual: approvals.length,
    required,
    secActual,
    secRequired,
  };
}
function formatPerson(name?: string, department?: string, fallbackId?: string) {
  const label = name?.trim() || fallbackId || t('unknownSubmitter');
  return department?.trim() ? `${label} · ${department.trim()}` : label;
}
function formatTime(iso?: string) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}
function hasAnyPermissions(p: CatalogVersion['manifest']['permissions']) {
  return (
    p.tools.length > 0 || p.outboundHosts.length > 0 || p.dataAccess.length > 0
  );
}
const permissionChanges = computed(() => {
  if (!selected.value || !previousVersion.value) return undefined;
  const curr = selected.value.manifest.permissions;
  const prev = previousVersion.value.manifest.permissions;
  const diffList = (a: string[], b: string[]) => ({
    added: b.filter((x) => !a.includes(x)),
    removed: a.filter((x) => !b.includes(x)),
  });
  const tools = diffList(prev.tools, curr.tools);
  const hosts = diffList(prev.outboundHosts, curr.outboundHosts);
  const data = diffList(prev.dataAccess, curr.dataAccess);
  const added = [
    ...tools.added.map((x) => `${t('permissionTools')}: ${x}`),
    ...hosts.added.map((x) => `${t('permissionHosts')}: ${x}`),
    ...data.added.map((x) => t(x)),
  ];
  const removed = [
    ...tools.removed.map((x) => `${t('permissionTools')}: ${x}`),
    ...hosts.removed.map((x) => `${t('permissionHosts')}: ${x}`),
    ...data.removed.map((x) => t(x)),
  ];
  return { added, removed, unchanged: !added.length && !removed.length };
});
async function startUpgrade(p: CatalogPackage) {
  let expectedPackageName: string | undefined;
  error.value = '';
  if (p.kind === 'plugin') {
    const knownVersionId = p.latestVersion?.versionId ?? p.defaultVersionId;
    try {
      if (knownVersionId) {
        const res = await api.version(p.id, knownVersionId);
        if (res.data.manifest.kind === 'plugin')
          expectedPackageName = res.data.manifest.plugin.packageName;
      } else {
        let vCursor: string | undefined;
        const vSeen = new Set<string>();
        let newest: CatalogVersion | undefined;
        do {
          const page = await api.versions(p.id, vCursor);
          if (page.items.length) newest = page.items.at(-1);
          vCursor = page.nextCursor;
          if (vCursor && vSeen.has(vCursor)) break;
          if (vCursor) vSeen.add(vCursor);
        } while (vCursor);
        if (newest?.manifest.kind === 'plugin')
          expectedPackageName = newest.manifest.plugin.packageName;
      }
    } catch (err) {
      if (alive) error.value = failure(err);
      return;
    }
  }
  if (!alive) return;
  upgradeTarget.value = {
    packageId: p.id,
    name: p.name,
    latestVersion: p.latestVersion?.version ?? p.market?.version,
    ...(expectedPackageName ? { expectedPackageName } : {}),
  };
  void nextTick(() =>
    uploadPanelAnchor.value?.scrollIntoView?.({ block: 'start' }),
  );
}
const distributionDetail = shallowRef<CatalogDistribution>();
async function inspectDistribution(id: string) {
  if (locked.value) return;
  const e = ++detailEpoch;
  distributionDetail.value = undefined;
  try {
    const result = await api.distribution(id);
    if (alive && e === detailEpoch) distributionDetail.value = result.data;
  } catch (err) {
    if (alive && e === detailEpoch) error.value = failure(err);
  }
}
const changes = computed(() =>
  [
    ...new Set([
      ...previous.value.map((f) => f.path),
      ...files.value.map((f) => f.path),
    ]),
  ].map((path) => ({
    path,
    lines: textDiff(
      previous.value.find((f) => f.path === path)?.text ?? '',
      files.value.find((f) => f.path === path)?.text ?? '',
    ),
  })),
);
function failure(e: unknown) {
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
          : e.status === 503
            ? 'unavailable'
            : 'failed',
    );
  }
  return t('failed');
}
function clearDetail() {
  ++detailEpoch;
  ++previewEpoch;
  ++compareEpoch;
  selected.value = undefined;
  review.value = undefined;
  reviewEtag.value = null;
  versionEtag.value = null;
  files.value = [];
  previous.value = [];
  previousVersion.value = undefined;
  currentTruncated.value = false;
  previousTruncated.value = false;
  comparison.value = '';
  previewError.value = '';
  distributionDetail.value = undefined;
}
async function load(target?: string, direction?: 'next' | 'previous') {
  if (locked.value) return;
  const e = ++epoch;
  clearDetail();
  loading.value = true;
  error.value = '';
  packages.value = [];
  activePackage.value = undefined;
  versionNext.value = undefined;
  versionHistory.value = [];
  reviews.value = [];
  versions.value = [];
  distributions.value = [];
  next.value = undefined;
  try {
    const m = await adminApi.membership();
    if (!alive || e !== epoch) return;
    member.value = m;
    if (!can(permission.value)) {
      error.value = t('forbidden');
      return;
    }
    const page =
      props.phase === 'directory'
        ? await api.packages(target, kind.value)
        : props.phase === 'review'
          ? await api.reviews(target, kind.value)
          : props.phase === 'published'
            ? await api.published(target, kind.value)
            : await api.distributions(target, kind.value);
    if (!alive || e !== epoch) return;
    if (props.phase === 'directory')
      packages.value = (page.items as CatalogPackage[]).filter(
        (p) => p.kind === kind.value,
      );
    else if (props.phase === 'review')
      reviews.value = (page.items as CatalogReview[]).filter(
        (r) => r.version.kind === kind.value,
      );
    else if (props.phase === 'published')
      versions.value = (page.items as CatalogVersion[]).filter(
        (v) => v.kind === kind.value,
      );
    else
      distributions.value = (page.items as CatalogDistribution[]).filter((d) =>
        d.selections.some((s) => s.identity.kind === kind.value),
      );
    next.value = page.nextCursor;
    if (direction === 'next') history.value.push(cursor.value);
    if (direction === 'previous') history.value.pop();
    cursor.value = target;
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive && e === epoch) loading.value = false;
  }
}
async function openPackage(
  p: CatalogPackage,
  target?: string,
  direction?: 'next' | 'previous',
) {
  if (locked.value) return;
  clearDetail();
  const e = detailEpoch;
  pid.value = p.id;
  if (p.id !== activePackage.value?.id) {
    versionHistory.value = [];
    versionCursor.value = undefined;
  }
  activePackage.value = p;
  versionsLoading.value = true;
  try {
    const page = await api.versions(p.id, target);
    if (alive && e === detailEpoch) {
      versions.value = page.items.filter((v) => v.kind === kind.value);
      versionNext.value = page.nextCursor;
      if (direction === 'next') versionHistory.value.push(versionCursor.value);
      if (direction === 'previous') versionHistory.value.pop();
      versionCursor.value = target;
    }
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive) versionsLoading.value = false;
  }
}
async function resumeDraft(v: CatalogVersion) {
  await inspect(v);
  await nextTick();
  if (selected.value?.id === v.id)
    detailPanel.value?.scrollIntoView?.({ block: 'start' });
}
async function inspect(v: CatalogVersion, r?: CatalogReview) {
  if (locked.value) return;
  clearDetail();
  const e = detailEpoch;
  error.value = '';
  reason.value = '';
  try {
    const reviewId =
      r?.reviewId ??
      (props.phase === 'published' && v.state === 'approved'
        ? v.reviewId
        : undefined);
    const evidence = reviewId ? await api.review(reviewId) : undefined;
    // Unpublished member reads are intentionally author-only. Reviewers use
    // the authorized admin evidence; approved publication rows are snapshots.
    const version = evidence
      ? {
          data: evidence.data.version,
          etag: catalogRevisionEtag(evidence.data.version.revision),
        }
      : props.phase === 'published' && v.state === 'approved'
        ? { data: v, etag: catalogRevisionEtag(v.revision) }
        : await api.version(v.packageId, v.id);
    if (!alive || e !== detailEpoch) return;
    if (
      version.data.kind !== kind.value ||
      version.data.manifest.kind !== kind.value ||
      version.data.id !== v.id ||
      version.data.packageId !== v.packageId ||
      (evidence &&
        (evidence.data.reviewId !== reviewId ||
          version.data.revision !== v.revision ||
          version.data.digest !== v.digest))
    )
      throw new Error('Stale evidence');
    selected.value = version.data;
    versionEtag.value = version.etag;
    review.value = evidence?.data;
    reviewEtag.value = evidence?.etag ?? null;
    risk.value = riskOptions(
      evidence?.data ?? ({ version: version.data } as CatalogReview),
    )[0]!;
  } catch (err) {
    if (alive && e === detailEpoch) error.value = failure(err);
  }
}
function prepare(label: string, run: () => Promise<unknown>) {
  if (locked.value) return;
  pending.value = { label, run, at: Date.now(), started: false };
  confirming.value = true;
}
async function execute() {
  const intent = pending.value;
  if (!intent || busy.value) return;
  if (Date.now() - intent.at >= 86400000) {
    pending.value = undefined;
    confirming.value = false;
    clearDetail();
    error.value = t('expired');
    return;
  }
  busy.value = true;
  confirming.value = false;
  intent.started = true;
  try {
    const result = await intent.run();
    if (!alive) return;
    pending.value = undefined;
    notice.value = t(props.phase === 'distribution' ? 'accepted' : 'saved');
    busy.value = false;
    await load(cursor.value);
    if (result && typeof result === 'object') {
      if ('reviewId' in result) {
        const r = result as CatalogReview;
        await inspect(r.version, r);
      } else if ('manifest' in result) await inspect(result as CatalogVersion);
      else if ('ownerId' in result && 'kind' in result)
        await openPackage(result as CatalogPackage);
    }
  } catch (e) {
    if (!alive) return;
    error.value = failure(e);
    if (
      isApiError(e) &&
      e.status < 500 &&
      e.problem.code !== 'iam.step_up_required'
    ) {
      pending.value = undefined;
      clearDetail();
    }
  } finally {
    if (alive) busy.value = false;
  }
}
function decide(decision: 'approve' | 'reject') {
  const r = review.value,
    etag = reviewEtag.value,
    why = reason.value.trim();
  if (
    !r ||
    author.value ||
    !why ||
    !strongEtag(etag) ||
    !can('resource.versions.review') ||
    r.status !== 'pending' ||
    !riskOptions(r).includes(risk.value)
  )
    return;
  const body = {
      decision,
      reason: why,
      role: role.value,
      riskLevel: risk.value,
      versionRevision: r.version.revision,
      digest: r.version.digest,
    },
    key = catalogKey();
  prepare(
    `${t(decision)} · ${r.version.manifest.name} ${r.version.version} · ${why}`,
    () => api.decide(r.reviewId, body, etag, key),
  );
}
function versionAction(mode: 'submit' | 'publish' | 'withdrawn' | 'malicious') {
  const v = selected.value,
    etag = versionEtag.value,
    why = reason.value.trim();
  if (!v || !why || !strongEtag(etag)) return;
  if (mode === 'publish' && (!ready.value || !can('resource.versions.publish')))
    return;
  if (
    mode === 'submit' &&
    (!can('resource.versions.submit') || v.state !== 'draft')
  )
    return;
  if (
    (mode === 'withdrawn' || mode === 'malicious') &&
    !can('resource.versions.publish')
  )
    return;
  const key = catalogKey();
  prepare(
    `${t(mode === 'withdrawn' ? 'withdraw' : mode === 'malicious' ? 'revoke' : mode)} · ${v.manifest.name} ${v.version} · ${why}`,
    () =>
      mode === 'publish'
        ? api.publish(v.packageId, v.id, why, etag, key)
        : mode === 'submit'
          ? api.submit(v.packageId, v.id, why, etag, key)
          : api.withdraw(v.packageId, v.id, mode, why, etag, key),
  );
}
async function distribute() {
  const p = pid.value.trim(),
    v = vid.value.trim(),
    target = instance.value.trim(),
    prior = currentDistribution.value.trim(),
    act = action.value;
  if (
    locked.value ||
    !p ||
    !v ||
    !target ||
    !can('resource.distributions.manage')
  )
    return;
  busy.value = true;
  try {
    const [pack, version, existing] = await Promise.all([
      api.get(p),
      api.version(p, v),
      prior ? api.distribution(prior) : Promise.resolve(undefined),
    ]);
    if (!alive) return;
    const value = version.data;
    if (
      pack.data.kind !== kind.value ||
      value.kind !== kind.value ||
      value.packageId !== p ||
      value.id !== v ||
      (['install', 'enable'].includes(act) && value.state !== 'published') ||
      (existing &&
        (existing.data.instanceId !== target || !strongEtag(existing.etag)))
    )
      throw new Error('Invalid selection');
    const body = {
        instanceId: target,
        selections: [
          ...(existing?.data.selections.filter(
            (s) => s.identity.kind !== kind.value || s.identity.packageId !== p,
          ) ?? []),
          {
            identity: {
              tenantId: pack.data.tenantId,
              packageId: p,
              versionId: v,
              kind: kind.value,
              version: value.version,
              manifestDigest: value.digest,
            },
            action: act,
          },
        ],
      },
      etag = existing?.etag ?? '"0"',
      key = catalogKey();
    busy.value = false;
    prepare(
      `${t('distribute')} · ${target} · ${value.manifest.name} ${value.version} · ${t(act)}`,
      () => api.distribute(body, etag, key),
    );
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive) busy.value = false;
  }
}
async function preview(compare = false, targetVersionId?: string) {
  const v = selected.value;
  if (!v || locked.value) return;
  const e = detailEpoch;
  const reqEpoch = compare ? ++compareEpoch : ++previewEpoch;
  const isCurrent = () =>
    alive &&
    e === detailEpoch &&
    (compare ? reqEpoch === compareEpoch : reqEpoch === previewEpoch);
  previewError.value = '';
  try {
    if (compare && !files.value.length) {
      const currEpoch = ++previewEpoch;
      const currPayload = v.manifest.artifacts.find(
        (a) => a.role === 'payload',
      );
      if (currPayload) {
        const currResp = await api.artifact(v.packageId, v.id, currPayload.id);
        if (v.kind === 'plugin') {
          const currMeta = await archivePreviewWithMeta(currResp, 'plugin');
          if (alive && e === detailEpoch && currEpoch === previewEpoch) {
            files.value = currMeta.files;
            currentTruncated.value = currMeta.truncated;
          }
        } else {
          const currFiles = await skillPreview(currResp);
          if (alive && e === detailEpoch && currEpoch === previewEpoch) {
            files.value = currFiles;
            currentTruncated.value = false;
          }
        }
      }
    }
    const compareId = (targetVersionId ?? comparison.value).trim();
    const value = compare
      ? (await api.version(v.packageId, compareId)).data
      : v;
    if (
      !['skill', 'plugin'].includes(value.kind) ||
      value.kind !== v.kind ||
      value.packageId !== v.packageId
    )
      throw new Error('Invalid comparison');
    const payload = value.manifest.artifacts.find((a) => a.role === 'payload');
    if (!payload) throw new Error('Missing payload');
    const resp = await api.artifact(value.packageId, value.id, payload.id);
    let result: PreviewFile[];
    let truncated = false;
    if (value.kind === 'plugin') {
      const meta = await archivePreviewWithMeta(resp, 'plugin');
      result = meta.files;
      truncated = meta.truncated;
    } else {
      result = await skillPreview(resp);
    }
    if (isCurrent()) {
      if (compare) {
        previous.value = result;
        previousVersion.value = value;
        previousTruncated.value = truncated;
        comparison.value = compareId;
      } else {
        files.value = result;
        currentTruncated.value = truncated;
      }
    }
  } catch {
    if (isCurrent()) previewError.value = t('previewFailed');
  }
}
async function verify() {
  try {
    await requestStepUp();
    verifyNeeded.value = false;
  } catch (e) {
    error.value = failure(e);
  }
}
watch(
  () => [props.phase, kind.value],
  () => {
    cursor.value = undefined;
    history.value = [];
    void load();
  },
  { immediate: true },
);
onBeforeUnmount(() => {
  alive = false;
  ++epoch;
  ++detailEpoch;
  pending.value = undefined;
});
</script>
<template>
  <section class="resource-page catalog-page">
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button v-if="verifyNeeded" @click="verify">{{ t('stepup') }}</button>
    <p v-if="pending?.started && !busy" role="status">
      {{ t('uncertain') }}
      <button @click="confirming = true">{{ t('retry') }}</button>
    </p>
    <template v-if="can(permission)">
      <div ref="uploadPanelAnchor">
        <ResourceUploadPanel
          v-if="phase === 'directory' && can('resource.packages.upload')"
          :key="kind"
          :kind="kind"
          :author="member?.member.displayName || ''"
          :can-submit="can('resource.versions.submit')"
          :disabled="busy || !!pending || loading"
          :upgrade-target="upgradeTarget"
          @locked="uploadLocked = $event"
          @cancel-upgrade="upgradeTarget = undefined"
          @completed="
            upgradeTarget = undefined;
            load(cursor);
          "
          @resume-draft="resumeDraft($event)"
        />
      </div>
      <ResourceAssignments
        v-if="phase === 'distribution'"
        :kind="kind"
        :member="member"
        :disabled="busy || !!pending || loading"
        @locked="assignmentLocked = $event"
      />
      <form
        v-if="phase === 'distribution'"
        data-testid="catalog-distribute"
        class="market-form"
        @submit.prevent="distribute"
      >
        <fieldset :disabled="locked || loading">
          <legend>{{ t('distribute') }}</legend>
          <label>{{ t('instance') }}<input v-model="instance" required /></label
          ><label>{{ t('packageId') }}<input v-model="pid" required /></label
          ><label>{{ t('versionId') }}<input v-model="vid" required /></label
          ><label
            >{{ t('currentDistribution')
            }}<input v-model="currentDistribution" /></label
          ><label
            >{{ t('action')
            }}<select v-model="action">
              <option
                v-for="a in ['install', 'enable', 'disable', 'remove']"
                :key="a"
                :value="a"
              >
                {{ t(a) }}
              </option>
            </select></label
          ><button>{{ t('distribute') }}</button>
        </fieldset>
      </form>
      <header class="market-toolbar catalog-list-toolbar">
        <h2>{{ t('listTitle') }}</h2>
        <button :disabled="loading || locked" @click="load(cursor)">
          {{ t('refresh') }}
        </button>
      </header>
      <div class="market-table">
        <table>
          <thead>
            <tr v-if="phase === 'directory'">
              <th>{{ t('name') }}</th>
              <th>{{ t('latestVersionCol') }}</th>
              <th>{{ t('submitter') }}</th>
              <th>{{ t('visibility') }}</th>
              <th>{{ t('actionsCol') }}</th>
            </tr>
            <tr v-else-if="phase === 'review'">
              <th>{{ t('name') }}</th>
              <th>{{ t('version') }}</th>
              <th>{{ t('submitter') }}</th>
              <th>{{ t('reviewProgress') }}</th>
              <th>{{ t('status') }}</th>
            </tr>
            <tr v-else>
              <th>{{ t('name') }}</th>
              <th>{{ t('version') }}</th>
              <th>{{ t('status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in packages" :key="p.id">
              <td>
                <button :disabled="locked" @click="openPackage(p)">
                  {{ p.name }}
                </button>
                <small class="catalog-desc">{{
                  p.latestVersion?.description ||
                  p.market?.description ||
                  t('noDescription')
                }}</small>
              </td>
              <td>
                <template v-if="p.latestVersion">
                  v{{ p.latestVersion.version }} ·
                  {{ t(p.latestVersion.state) }}
                </template>
                <template v-else-if="p.market">
                  v{{ p.market.version }} · {{ t('published') }}
                </template>
                <template v-else>—</template>
              </td>
              <td>
                {{
                  formatPerson(
                    p.latestVersion?.authorName ?? p.market?.authorName,
                    p.latestVersion?.authorDepartment ??
                      p.market?.authorDepartment,
                    p.ownerId,
                  )
                }}
              </td>
              <td>{{ t(p.visibility) }}</td>
              <td>
                <button
                  v-if="
                    can('resource.packages.upload') &&
                    p.ownerId === member?.member.id
                  "
                  data-testid="catalog-upgrade"
                  :disabled="locked"
                  @click="startUpgrade(p)"
                >
                  {{ t('upgradeAction') }}
                </button>
              </td>
            </tr>
            <tr v-for="r in reviews" :key="r.reviewId">
              <td>
                <button
                  data-testid="catalog-review"
                  :disabled="locked"
                  @click="inspect(r.version, r)"
                >
                  {{ r.version.manifest.name }}
                </button>
                <small class="catalog-desc">{{
                  r.version.manifest.description || t('noDescription')
                }}</small>
              </td>
              <td>
                <span>v{{ r.version.version }}</span>
                <span
                  v-if="r.previousPublishedVersion"
                  class="catalog-badge catalog-badge-upgrade"
                >
                  {{
                    t('upgradeBadge', { version: r.previousPublishedVersion })
                  }}
                </span>
              </td>
              <td>
                {{
                  formatPerson(
                    r.authorName ?? r.version.authorName,
                    r.authorDepartment ?? r.version.authorDepartment,
                    r.authorId,
                  )
                }}
                <small v-if="r.submittedAt">{{
                  formatTime(r.submittedAt)
                }}</small>
              </td>
              <td>
                <span class="catalog-badge">{{
                  r.version.risk.level.toUpperCase()
                }}</span>
                <small>{{ t('reviewProgressSummary', reviewStats(r)) }}</small>
              </td>
              <td>{{ t(r.status) }}</td>
            </tr>
            <tr v-for="v in phase === 'directory' ? [] : versions" :key="v.id">
              <td>
                <button
                  data-testid="catalog-version"
                  :disabled="locked"
                  @click="inspect(v)"
                >
                  {{ v.manifest.name }}
                </button>
                <small class="catalog-desc">{{
                  v.manifest.description || t('noDescription')
                }}</small>
              </td>
              <td>v{{ v.version }}</td>
              <td>{{ t(v.state) }}</td>
            </tr>
            <tr v-for="d in distributions" :key="d.distributionId">
              <td>
                {{ d.instanceId }}
                <button
                  :disabled="locked"
                  @click="inspectDistribution(d.distributionId)"
                >
                  {{ t('select') }}
                </button>
              </td>
              <td>
                <p v-for="s in d.selections" :key="s.identity.versionId">
                  {{ t(s.identity.kind) }} ? {{ s.identity.version }}
                </p>
              </td>
              <td>
                {{
                  distributionApplied(d)
                    ? t('applied')
                    : d.state === 'applied'
                      ? t('applying')
                      : t(d.state, d.state)
                }}
                <p
                  v-for="r in d.reports"
                  :key="r.clientId + r.identity.versionId"
                >
                  {{ t(r.reportSource) }} / {{ t(r.state) }}
                  <span>{{ r.errorCode }}</span>
                </p>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="phase === 'directory' && activePackage" class="market-table">
        <table>
          <thead>
            <tr>
              <th>{{ t('name') }}</th>
              <th>{{ t('version') }}</th>
              <th>{{ t('status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="v in versions" :key="v.id">
              <td>
                <button
                  data-testid="catalog-version"
                  :disabled="locked"
                  @click="inspect(v)"
                >
                  {{ v.manifest.name }}
                </button>
              </td>
              <td>{{ v.version }}</td>
              <td>{{ t(v.state) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <article v-if="distributionDetail" class="catalog-detail">
        <h2>
          {{ t('distributionResult') }} · {{ distributionDetail.instanceId }}
        </h2>
        <p>
          {{ t('status') }}:
          {{
            distributionApplied(distributionDetail)
              ? t('applied')
              : distributionDetail.state === 'applied'
                ? t('applying')
                : t(distributionDetail.state)
          }}
        </p>
        <p>{{ t('targetOwner') }}: {{ distributionDetail.ownerId }}</p>
        <p>
          {{ t('distributionRevision') }}:
          {{ distributionDetail.selectionRevision }} /
          {{ distributionDetail.generation }}
        </p>
        <ul>
          <li
            v-for="s in distributionDetail.selections"
            :key="s.identity.packageId"
          >
            {{ t(s.identity.kind) }} · {{ s.identity.packageId }} ·
            {{ s.identity.version }} · {{ t(s.action)
            }}<small>{{ s.identity.manifestDigest }}</small>
          </li>
        </ul>
        <h3>{{ t('reports') }}</h3>
        <p v-if="!distributionDetail.reports.length">
          {{ t('awaitingReports') }}
        </p>
        <ul>
          <li
            v-for="r in distributionDetail.reports"
            :key="r.clientId + r.identity.versionId"
          >
            {{ r.clientId }} · {{ t(r.reportSource) }} · {{ t(r.state) }} ·
            {{ r.observedAt
            }}<small>{{ r.identity.packageId }} · {{ r.errorCode }}</small>
          </li>
        </ul>
      </article>
      <p
        v-if="
          !loading &&
          !packages.length &&
          !reviews.length &&
          !versions.length &&
          !distributions.length
        "
      >
        {{
          t(
            phase === 'directory'
              ? kind === 'plugin'
                ? 'emptyPlugin'
                : 'emptySkill'
              : phase === 'review'
                ? 'emptyReview'
                : phase === 'published'
                  ? 'emptyPublished'
                  : 'emptyDistribution',
          )
        }}
      </p>
      <footer class="resource-pager">
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
      </footer>
      <footer
        v-if="phase === 'directory' && activePackage"
        class="resource-pager"
      >
        <span>{{ activePackage.name }} · {{ t('version') }}</span>
        <button
          :disabled="locked || versionsLoading || !versionHistory.length"
          @click="openPackage(activePackage, versionHistory.at(-1), 'previous')"
        >
          {{ t('previous') }}
        </button>
        <button
          :disabled="locked || versionsLoading || !versionNext"
          @click="openPackage(activePackage, versionNext, 'next')"
        >
          {{ t('next') }}
        </button>
      </footer>
      <article v-if="selected" ref="detailPanel" class="catalog-detail">
        <header class="catalog-detail-header">
          <div>
            <h2>{{ selected.manifest.name }} {{ selected.version }}</h2>
            <p class="catalog-detail-desc" data-testid="catalog-description">
              {{ selected.manifest.description || t('noDescription') }}
            </p>
          </div>
          <div class="catalog-badges">
            <span class="catalog-badge">{{ t(selected.kind) }}</span>
            <span class="catalog-badge">{{
              selected.risk.level.toUpperCase()
            }}</span>
            <span class="catalog-badge">{{ t(selected.state) }}</span>
            <span
              v-if="review?.previousPublishedVersion"
              data-testid="catalog-upgrade-badge"
              class="catalog-badge catalog-badge-upgrade"
            >
              {{
                t('upgradeBadge', {
                  version: review.previousPublishedVersion,
                })
              }}
            </span>
          </div>
        </header>

        <dl class="catalog-meta-grid" data-testid="catalog-meta">
          <div>
            <dt>{{ t('submitter') }}</dt>
            <dd>
              {{
                formatPerson(
                  review?.authorName ?? selected.authorName,
                  review?.authorDepartment ?? selected.authorDepartment,
                  review?.authorId ?? selected.authorId,
                )
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t(review ? 'submittedAt' : 'createdAt') }}</dt>
            <dd>
              {{
                review
                  ? review.submittedAt
                    ? formatTime(review.submittedAt)
                    : t('unknownTime')
                  : formatTime(selected.createdAt)
              }}
            </dd>
          </div>
          <div>
            <dt>{{ t('license') }}</dt>
            <dd>{{ selected.manifest.license }}</dd>
          </div>
          <div>
            <dt>{{ t('dshRange') }}</dt>
            <dd>
              {{
                selected.manifest.kind === 'plugin'
                  ? selected.manifest.plugin.dshVersionRange
                  : selected.manifest.kind === 'skill'
                    ? selected.manifest.skill.dshVersionRange
                    : '—'
              }}
            </dd>
          </div>
          <div v-if="selected.manifest.kind === 'plugin'">
            <dt>{{ t('npmName') }}</dt>
            <dd>{{ selected.manifest.plugin.packageName }}</dd>
          </div>
          <div
            v-if="
              selected.manifest.kind === 'plugin' &&
              selected.manifest.plugin.tags?.length
            "
          >
            <dt>{{ t('functionalTags') }}</dt>
            <dd class="catalog-badges">
              <span
                v-for="tag in selected.manifest.plugin.tags"
                :key="tag"
                class="catalog-badge"
                >{{ tag }}</span
              >
            </dd>
          </div>
        </dl>

        <section
          v-if="selected.changeNotes || review?.submitReason"
          class="catalog-notes"
          data-testid="catalog-notes"
        >
          <div v-if="selected.changeNotes">
            <h3>{{ t('changeNotes') }}</h3>
            <p>{{ selected.changeNotes }}</p>
          </div>
          <div v-if="review?.submitReason">
            <h3>{{ t('submitReason') }}</h3>
            <p>{{ review.submitReason }}</p>
          </div>
        </section>

        <section class="catalog-permissions" data-testid="catalog-permissions">
          <h3>{{ t('permissions') }}</h3>
          <p v-if="!hasAnyPermissions(selected.manifest.permissions)">
            {{ t('noExtraPermissions') }}
          </p>
          <dl v-else class="catalog-meta-grid">
            <div>
              <dt>{{ t('permissionTools') }}</dt>
              <dd>
                {{
                  selected.manifest.permissions.tools.length
                    ? selected.manifest.permissions.tools.join(', ')
                    : t('none')
                }}
              </dd>
            </div>
            <div>
              <dt>{{ t('permissionHosts') }}</dt>
              <dd>
                {{
                  selected.manifest.permissions.outboundHosts.length
                    ? selected.manifest.permissions.outboundHosts.join(', ')
                    : t('none')
                }}
              </dd>
            </div>
            <div>
              <dt>{{ t('permissions') }}</dt>
              <dd>
                {{
                  selected.manifest.permissions.dataAccess.length
                    ? selected.manifest.permissions.dataAccess
                        .map((item) => t(item))
                        .join(', ')
                    : t('none')
                }}
              </dd>
            </div>
          </dl>
        </section>

        <details>
          <summary>{{ t('advanced') }}</summary>
          <p v-if="review">{{ t('reviewId') }}: {{ review.reviewId }}</p>
          <p>{{ t('packageId') }}: {{ selected.packageId }}</p>
          <p>{{ t('versionId') }}: {{ selected.id }}</p>
          <p>{{ t('digest') }}: {{ selected.digest }}</p>
          <p v-if="selected.manifest.kind === 'plugin'">
            {{ t('npmName') }}: {{ selected.manifest.plugin.packageName }}
          </p>
          <p>
            {{ t('signature') }}:
            {{ selected.resourceEnvelope ? t('signed') : t('unsigned') }}
          </p>
          <pre v-if="selected.resourceEnvelope">{{
            JSON.stringify(selected.resourceEnvelope, null, 2)
          }}</pre>
          <p>
            {{ t('risk') }}: {{ selected.risk.level.toUpperCase() }} /
            {{ selected.risk.minimumLevel.toUpperCase() }}
          </p>
          <p>
            {{ t('reviewers') }}: {{ selected.risk.requiredReviewers }} /
            {{ selected.risk.requiredSecurityReviewers }}
          </p>
          <h3>{{ t('manifest') }}</h3>
          <pre>{{ JSON.stringify(selected.manifest, null, 2) }}</pre>
        </details>

        <section
          v-if="selected.kind === 'skill' || selected.kind === 'plugin'"
          class="catalog-preview"
        >
          <div class="market-toolbar">
            <button
              data-testid="catalog-preview-btn"
              :disabled="locked"
              @click="preview()"
            >
              {{ t(selected.kind === 'skill' ? 'preview' : 'previewArchive') }}
            </button>
            <button
              v-if="review?.previousPublishedVersionId"
              data-testid="catalog-compare-previous"
              :disabled="locked"
              @click="preview(true, review.previousPublishedVersionId)"
            >
              {{
                t('comparePrevious', {
                  version: review.previousPublishedVersion,
                })
              }}
            </button>
          </div>
          <p>{{ t('previewNote') }}</p>
          <p
            v-if="previewTruncated"
            role="alert"
            data-testid="preview-truncated"
          >
            {{ t('previewTruncated') }}
          </p>
          <p v-if="previewError" role="alert">{{ previewError }}</p>
          <section v-for="f in files" :key="f.path">
            <h3>{{ f.path }}</h3>
            <pre data-testid="skill-text">{{ f.text }}</pre>
          </section>
          <div class="market-toolbar">
            <label
              >{{ t('before')
              }}<input v-model="comparison" :disabled="locked" /></label
            ><button
              :disabled="locked || !comparison.trim()"
              @click="preview(true)"
            >
              {{ t('compare') }}
            </button>
          </div>
          <section v-if="permissionChanges" class="catalog-permission-diff">
            <h3>{{ t('permissionDiff') }}</h3>
            <p v-if="permissionChanges.unchanged">
              {{ t('permissionUnchanged') }}
            </p>
            <template v-else>
              <p v-if="permissionChanges.added.length">
                <strong>{{ t('permissionAdded') }}:</strong>
                {{ permissionChanges.added.join('；') }}
              </p>
              <p v-if="permissionChanges.removed.length">
                <strong>{{ t('permissionRemoved') }}:</strong>
                {{ permissionChanges.removed.join('；') }}
              </p>
            </template>
          </section>
          <section v-if="previous.length">
            <h3>{{ t('changes') }}</h3>
            <div v-for="f in changes" :key="f.path">
              <h4>{{ f.path }}</h4>
              <pre
                v-for="line in f.lines.filter((l) => l.changed).slice(0, 200)"
                :key="line.line"
                >{{ line.line }} − {{ line.before }}
{{ line.line }} + {{ line.after }}</pre>
              <p v-if="f.lines.filter((l) => l.changed).length > 200">
                {{
                  t('diffTruncated', {
                    count: f.lines.filter((l) => l.changed).length - 200,
                  })
                }}
              </p>
            </div>
          </section>
        </section>

        <section v-if="review" class="catalog-review-history">
          <h3>{{ t('history') }}</h3>
          <p v-if="reviewProgress" class="catalog-progress">
            {{ t('reviewProgress') }}：{{
              t('reviewProgressSummary', reviewProgress)
            }}
          </p>
          <p v-if="!review.decisions.length">{{ t('noDecisionsYet') }}</p>
          <ul v-else class="catalog-decision-list">
            <li v-for="(d, index) in review.decisions" :key="index">
              <strong>{{
                formatPerson(d.reviewerName, d.reviewerDepartment, d.reviewerId)
              }}</strong>
              · {{ t(d.role) }} · {{ t(d.decision) }} · {{ d.reason }}
              <small>{{ formatTime(d.createdAt) }}</small>
            </li>
          </ul>
          <p
            v-for="issue in publicationIssues"
            :key="issue.code"
            class="catalog-blocker"
          >
            {{ t(issue.code, issue) }}
          </p>
        </section>
        <fieldset :disabled="locked">
          <label
            >{{ t('reason')
            }}<input v-model="reason" data-testid="catalog-reason"
          /></label>
          <template v-if="review && can('resource.versions.review')"
            ><label
              >{{ t('role')
              }}<select v-model="role" data-testid="catalog-role">
                <option value="general">{{ t('general') }}</option>
                <option value="security">{{ t('security') }}</option>
              </select></label
            ><label
              >{{ t('risk')
              }}<select v-model="risk" data-testid="catalog-risk">
                <option v-for="r in riskOptions(review)" :key="r" :value="r">
                  {{ r.toUpperCase() }}
                </option>
              </select></label
            ><button
              data-testid="catalog-approve"
              :disabled="
                author ||
                !reason.trim() ||
                !strongEtag(reviewEtag) ||
                review.status !== 'pending'
              "
              @click="decide('approve')"
            >
              {{ t('approve') }}</button
            ><button
              :disabled="
                author ||
                !reason.trim() ||
                !strongEtag(reviewEtag) ||
                review.status !== 'pending'
              "
              @click="decide('reject')"
            >
              {{ t('reject') }}
            </button></template
          >
          <button
            v-if="review && can('resource.versions.publish')"
            class="catalog-primary"
            data-testid="catalog-publish"
            :disabled="!ready || !reason.trim() || !strongEtag(versionEtag)"
            @click="versionAction('publish')"
          >
            {{ t('publish') }}
          </button>
          <button
            v-if="selected.state === 'draft' && can('resource.versions.submit')"
            :disabled="!reason.trim() || !strongEtag(versionEtag)"
            @click="versionAction('submit')"
          >
            {{ t('submit') }}
          </button>
          <template
            v-if="
              ['published', 'deprecated', 'withdrawn'].includes(
                selected.state,
              ) && can('resource.versions.publish')
            "
            ><p>{{ t('withdrawNote') }}</p>
            <button
              v-if="selected.state !== 'withdrawn'"
              :disabled="!reason.trim() || !strongEtag(versionEtag)"
              class="catalog-danger"
              @click="versionAction('withdrawn')"
            >
              {{ t('withdraw') }}</button
            ><button
              :disabled="!reason.trim() || !strongEtag(versionEtag)"
              class="catalog-danger"
              @click="versionAction('malicious')"
            >
              {{ t('revoke') }}
            </button></template
          >
        </fieldset>
      </article>
    </template>
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
.catalog-page :deep(.market-form fieldset),
.catalog-page :deep(.catalog-detail > fieldset) {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
}
.catalog-page :deep(.market-form fieldset > label),
.catalog-page :deep(.catalog-detail > fieldset > label) {
  display: flex;
  margin: 0;
}
.catalog-page :deep(.market-form fieldset > button) {
  justify-self: start;
  align-self: end;
}
@media (max-width: 700px) {
  .catalog-page :deep(.market-form fieldset),
  .catalog-page :deep(.catalog-detail > fieldset) {
    grid-template-columns: 1fr;
  }
}

.catalog-list-toolbar {
  justify-content: space-between;
}
.catalog-list-toolbar h2 {
  margin: 0;
}
.catalog-page :deep(.catalog-primary) {
  background: var(--auriga-accent);
  color: var(--auriga-on-accent);
  border-color: var(--auriga-accent);
}
.catalog-page :deep(.catalog-danger) {
  color: var(--auriga-danger);
  border-color: var(--auriga-danger);
}
.catalog-page :deep(details) {
  margin: 16px 0;
}
.catalog-page :deep(summary) {
  cursor: pointer;
  color: var(--auriga-muted);
}
.catalog-detail {
  margin-top: 24px;
  padding: 24px;
  border: 1px solid var(--auriga-border);
  border-radius: 16px;
  background: var(--auriga-surface);
}
.catalog-detail-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.catalog-detail-header h2 {
  margin: 0 0 6px;
}
.catalog-detail-desc {
  margin: 0;
  color: var(--auriga-muted);
  line-height: 1.6;
}
.catalog-desc {
  margin-top: 4px;
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.catalog-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}
.catalog-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  border: 1px solid var(--auriga-border-strong);
  background: var(--auriga-field-bg);
  color: var(--auriga-text);
}
.catalog-badge-upgrade {
  border-color: var(--auriga-accent);
  color: var(--auriga-accent);
  background: var(--admin-blue-soft);
}
.catalog-meta-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px 20px;
  margin: 16px 0;
  padding: 14px 16px;
  border-radius: 10px;
  background: var(--auriga-field-bg);
}
.catalog-meta-grid dt {
  font-size: 12px;
  color: var(--auriga-muted);
}
.catalog-meta-grid dd {
  margin: 4px 0 0;
}
.catalog-notes,
.catalog-permissions,
.catalog-preview,
.catalog-review-history {
  margin: 16px 0;
  padding-top: 12px;
  border-top: 1px solid var(--auriga-border);
}
.catalog-notes p {
  white-space: pre-wrap;
  margin: 6px 0 12px;
}
.catalog-decision-list {
  padding-left: 18px;
  line-height: 1.7;
}
.catalog-blocker {
  color: var(--auriga-muted);
  margin: 4px 0;
}
.catalog-detail pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 440px;
  overflow: auto;
  padding: 12px;
  background: var(--auriga-field-bg);
  border-radius: 8px;
}
.catalog-page small {
  display: block;
  color: var(--auriga-muted);
}
.catalog-page label {
  display: inline-flex;
  flex-direction: column;
  gap: 4px;
  margin: 6px 12px 6px 0;
}
.catalog-page fieldset {
  border: 1px solid var(--auriga-border);
  border-radius: 12px;
  margin: 16px 0;
  padding: 16px;
}
.catalog-page :deep(.market-toolbar),
.catalog-page :deep(.resource-pager) {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.catalog-page :deep(h2) {
  font-size: 18px;
}
.catalog-page :deep(button),
.catalog-page :deep(input),
.catalog-page :deep(select) {
  font: inherit;
  color: var(--auriga-text);
  background: var(--auriga-field-bg);
  border: 1px solid var(--auriga-border-strong);
  border-radius: 6px;
  padding: 7px 10px;
  max-width: 100%;
  box-sizing: border-box;
}
.catalog-page :deep(button) {
  cursor: pointer;
}
.catalog-page :deep(button:hover:not(:disabled)) {
  background: var(--admin-blue-soft);
}
.catalog-page :deep(:disabled) {
  opacity: 0.55;
  cursor: default;
}
.catalog-page :deep(:focus-visible) {
  outline: 2px solid var(--auriga-accent);
  outline-offset: 2px;
}
.catalog-page :deep(label) {
  display: inline-flex;
  flex-direction: column;
  gap: 6px;
  margin: 6px 12px 6px 0;
  max-width: 100%;
}
.catalog-page :deep(fieldset) {
  border: 1px solid var(--auriga-border);
  border-radius: 8px;
  margin: 16px 0;
  padding: 16px;
  min-width: 0;
}
.catalog-page :deep(.market-table) {
  overflow-x: auto;
  margin: 16px 0;
  border: 1px solid var(--auriga-border);
  border-radius: 8px;
}
.catalog-page :deep(table) {
  border-collapse: collapse;
  width: 100%;
  text-align: left;
}
.catalog-page :deep(th) {
  background: var(--admin-soft);
  font-weight: 500;
  color: var(--auriga-muted);
}
.catalog-page :deep(th),
.catalog-page :deep(td) {
  padding: 10px 14px;
  border-bottom: 1px solid var(--auriga-border);
  vertical-align: top;
}
.catalog-page :deep(dd) {
  overflow-wrap: anywhere;
  margin: 4px 0 12px;
}
.catalog-page :deep([role='alert']) {
  color: var(--auriga-danger);
}
.catalog-page :deep([role='status']) {
  color: var(--auriga-muted);
}
</style>
