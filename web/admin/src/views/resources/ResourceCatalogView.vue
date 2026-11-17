<script setup lang="ts">
/* global File, Event, HTMLInputElement */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
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
import { skillPreview } from '../../admin/resourceSkillPreview';
import {
  packSkillFolder,
  prepareFolderUpload,
  prepareUpload,
  type CatalogKind,
  type PreparedUpload,
} from '../../admin/resourceCatalogUpload';
import ResourceAssignments from './ResourceAssignments.vue';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
import { strongEtag } from '../../admin/resourceApi';
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
const name = ref(''),
  visibility = ref<'tenant' | 'members' | 'groups'>('tenant'),
  groups = ref('');
const pid = ref(''),
  vid = ref(''),
  reason = ref('');
const manifest = shallowRef<File>(),
  artifacts = shallowRef<File[]>([]);
const prepared = shallowRef<PreparedUpload>();
const folderPayload = shallowRef<File>();
const uploadError = ref(''),
  preparing = ref(false);
let fileEpoch = 0;
const role = ref<'general' | 'security'>('general'),
  risk = ref<'r1' | 'r2' | 'r3'>('r1');
const instance = ref(''),
  currentDistribution = ref(''),
  action = ref<'install' | 'enable' | 'disable' | 'remove'>('install');
const files = shallowRef<{ path: string; text: string }[]>([]),
  previous = shallowRef<{ path: string; text: string }[]>([]),
  comparison = ref(''),
  previewError = ref('');
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
  alive = true;
const locked = computed(
  () => busy.value || !!pending.value || assignmentLocked.value,
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
  selected.value = undefined;
  review.value = undefined;
  reviewEtag.value = null;
  versionEtag.value = null;
  files.value = [];
  previous.value = [];
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
function create() {
  if (
    !can('resource.packages.upload') ||
    !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name.value) ||
    name.value.length > 64
  ) {
    error.value = t('invalid');
    return;
  }
  const body = {
      name: name.value,
      kind: kind.value,
      visibility: visibility.value,
      groupIds:
        visibility.value === 'groups'
          ? groups.value
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean)
          : [],
    },
    key = catalogKey();
  prepare(
    `${t('create')} · ${body.name} · ${t(body.visibility)} · ${body.groupIds.join(', ')}`,
    async () => {
      const p = await api.create(body, key);
      if (alive) pid.value = p.id;
      return p;
    },
  );
}
async function choose(event: Event, type: 'manifest' | 'artifacts' | 'folder') {
  if (locked.value) return;
  const e = ++fileEpoch;
  prepared.value = undefined;
  uploadError.value = '';
  preparing.value = true;
  const list = Array.from((event.target as HTMLInputElement).files ?? []);
  try {
    if (type === 'manifest') manifest.value = list[0];
    else if (type === 'folder') {
      artifacts.value = [];
      folderPayload.value = undefined;
      const packed = await packSkillFolder(list);
      if (!alive || e !== fileEpoch) return;
      artifacts.value = [packed];
      folderPayload.value = packed;
    } else {
      artifacts.value = list;
      folderPayload.value = undefined;
    }
    if (!alive || e !== fileEpoch) return;
    if (manifest.value && artifacts.value.length) {
      const result = folderPayload.value
        ? await prepareFolderUpload(manifest.value, folderPayload.value)
        : await prepareUpload(manifest.value, artifacts.value, kind.value);
      if (alive && e === fileEpoch) prepared.value = result;
    }
  } catch {
    if (alive && e === fileEpoch) uploadError.value = t('uploadInvalid');
  } finally {
    if (alive && e === fileEpoch) preparing.value = false;
  }
}
async function upload() {
  const draft = prepared.value,
    p = pid.value.trim();
  if (locked.value || preparing.value || !can('resource.packages.upload'))
    return;
  if (!draft || !p) {
    error.value = t('invalid');
    return;
  }
  busy.value = true;
  try {
    const pack = await api.get(p);
    if (!alive) return;
    if (
      pack.data.kind !== kind.value ||
      pack.data.name !== draft.manifest.name ||
      !strongEtag(pack.etag)
    )
      throw new Error('Invalid package');
    const etag = pack.etag,
      key = catalogKey(),
      body = { ...draft.metadata };
    busy.value = false;
    prepare(
      `${t('upload')} · ${pack.data.name} ${body.version} · ${body.expectedDigest}`,
      () => api.upload(p, body, draft.file, draft.artifacts, etag, key),
    );
  } catch (e) {
    if (alive) error.value = failure(e);
  } finally {
    if (alive) busy.value = false;
  }
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
async function preview(compare = false) {
  const v = selected.value;
  if (!v || locked.value) return;
  const e = detailEpoch;
  previewError.value = '';
  try {
    const value = compare
      ? (await api.version(v.packageId, comparison.value.trim())).data
      : v;
    if (value.kind !== 'skill' || value.packageId !== v.packageId)
      throw new Error('Invalid comparison');
    const payload = value.manifest.artifacts.find((a) => a.role === 'payload');
    if (!payload) throw new Error('Missing payload');
    const result = await skillPreview(
      await api.artifact(value.packageId, value.id, payload.id),
    );
    if (alive && e === detailEpoch) {
      if (compare) previous.value = result;
      else files.value = result;
    }
  } catch {
    if (alive && e === detailEpoch) previewError.value = t('previewFailed');
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
  ++fileEpoch;
  pending.value = undefined;
});
</script>
<template>
  <section class="resource-page catalog-page">
    <header class="market-toolbar">
      <h1>{{ t(phase) }}</h1>
      <button :disabled="loading || locked" @click="load(cursor)">
        {{ t('refresh') }}
      </button>
    </header>
    <nav class="market-tabs">
      <RouterLink
        v-for="tab in ['directory', 'review', 'published', 'distribution']"
        :key="tab"
        :to="{
          path: tab === 'directory' ? '/resources' : '/resources/' + tab,
          query: { kind },
        }"
        >{{ t(tab) }}</RouterLink
      >
    </nav>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button v-if="verifyNeeded" @click="verify">{{ t('stepup') }}</button>
    <p v-if="pending?.started && !busy" role="status">
      {{ t('uncertain') }}
      <button @click="confirming = true">{{ t('retry') }}</button>
    </p>
    <template v-if="can(permission)">
      <fieldset
        v-if="phase === 'directory'"
        :disabled="locked || loading"
        class="market-form"
      >
        <form
          v-if="can('resource.packages.upload')"
          data-testid="catalog-create"
          @submit.prevent="create"
        >
          <label>{{ t('name') }}<input v-model="name" required /></label
          ><label
            >{{ t('visibility')
            }}<select v-model="visibility">
              <option
                v-for="s in ['tenant', 'members', 'groups']"
                :key="s"
                :value="s"
              >
                {{ t(s) }}
              </option>
            </select></label
          ><label v-if="visibility === 'groups'"
            >{{ t('groupIds') }}<input v-model="groups" required /></label
          ><button>{{ t('create') }}</button>
        </form>
        <form
          v-if="can('resource.packages.upload')"
          data-testid="catalog-upload"
          @submit.prevent="upload"
        >
          <label>{{ t('packageId') }}<input v-model="pid" required /></label
          ><label
            >{{ t('manifest')
            }}<input
              data-testid="catalog-manifest"
              type="file"
              accept=".json"
              required
              @change="choose($event, 'manifest')" /></label
          ><label
            >{{ t(kind === 'plugin' ? 'npmPackage' : 'artifacts')
            }}<input
              data-testid="catalog-artifacts"
              type="file"
              :accept="kind === 'plugin' ? '.tgz' : '.gz,.tgz'"
              @change="choose($event, 'artifacts')" /></label
          ><label v-if="kind === 'skill'"
            >{{ t('folder')
            }}<input
              data-testid="catalog-folder"
              type="file"
              webkitdirectory
              multiple
              @change="choose($event, 'folder')"
          /></label>
          <p v-if="preparing" role="status">{{ t('checkingUpload') }}</p>
          <p v-if="uploadError" role="alert">{{ uploadError }}</p>
          <p v-if="folderPayload">{{ t('folderManifest') }}</p>
          <dl v-if="prepared" data-testid="catalog-upload-preview">
            <dt>{{ t('name') }}</dt>
            <dd>{{ prepared.manifest.name }}</dd>
            <dt>{{ t('version') }}</dt>
            <dd>{{ prepared.manifest.version }}</dd>
            <template v-if="prepared.manifest.kind === 'plugin'"
              ><dt>{{ t('npmName') }}</dt>
              <dd>{{ prepared.manifest.plugin.packageName }}</dd></template
            >
            <dt>{{ t('digest') }}</dt>
            <dd>{{ prepared.metadata.expectedDigest }}</dd>
            <dt>{{ t('signature') }}</dt>
            <dd>
              {{ t('unsigned') }} · {{ prepared.manifest.signature.algorithm }}
            </dd>
            <dt>{{ t('artifacts') }}</dt>
            <dd v-for="a in prepared.manifest.artifacts" :key="a.id">
              {{ a.id }} · {{ a.sizeBytes }} · {{ a.digest }}
            </dd>
          </dl>
          <button :disabled="!prepared || preparing">{{ t('upload') }}</button>
        </form>
      </fieldset>
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
      <div class="market-table">
        <table>
          <thead>
            <tr>
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
              </td>
              <td>{{ p.kind }}</td>
              <td>{{ t(p.visibility) }}</td>
            </tr>
            <tr v-for="r in reviews" :key="r.reviewId">
              <td>
                <button
                  data-testid="catalog-review"
                  :disabled="locked"
                  @click="inspect(r.version, r)"
                >
                  {{ r.version.manifest.name }}</button
                ><small>{{ r.reviewId }}</small>
              </td>
              <td>{{ r.version.version }}</td>
              <td>{{ t(r.status) }}</td>
            </tr>
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
            <tr v-for="d in distributions" :key="d.distributionId">
              <td>
                {{ d.instanceId }}<small>{{ d.distributionId }}</small>
                <button
                  :disabled="locked"
                  @click="inspectDistribution(d.distributionId)"
                >
                  {{ t('select') }}
                </button>
              </td>
              <td>
                <p v-for="s in d.selections" :key="s.identity.versionId">
                  {{ s.identity.kind }} {{ s.identity.packageId }} /
                  {{ s.identity.version }} / {{ s.identity.versionId }}
                </p>
              </td>
              <td>
                {{
                  distributionApplied(d)
                    ? t('applied')
                    : d.state === 'applied'
                      ? t('applying')
                      : t(d.state, d.state)
                }}<small>{{ d.selectionRevision }} / {{ d.generation }}</small>
                <p
                  v-for="r in d.reports"
                  :key="r.clientId + r.identity.versionId"
                >
                  {{ r.reportSource }} / {{ r.state }} /
                  {{ r.observedGeneration }} <span>{{ r.errorCode }}</span>
                </p>
              </td>
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
        {{ t('empty') }}
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
      <article v-if="selected" class="catalog-detail">
        <h2>{{ selected.manifest.name }} {{ selected.version }}</h2>
        <p>{{ selected.digest }}</p>
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
        <h3>{{ t('permissions') }}</h3>
        <pre>{{ JSON.stringify(selected.manifest.permissions, null, 2) }}</pre>
        <h3>{{ t('manifest') }}</h3>
        <pre>{{ JSON.stringify(selected.manifest, null, 2) }}</pre>
        <template v-if="selected.kind === 'skill'"
          ><button :disabled="locked" @click="preview()">
            {{ t('preview') }}
          </button>
          <p>{{ t('previewNote') }}</p>
          <p v-if="previewError" role="alert">{{ previewError }}</p>
          <section v-for="f in files" :key="f.path">
            <h3>{{ f.path }}</h3>
            <pre data-testid="skill-text">{{ f.text }}</pre>
          </section>
          <label
            >{{ t('before')
            }}<input v-model="comparison" :disabled="locked" /></label
          ><button
            :disabled="locked || !comparison.trim()"
            @click="preview(true)"
          >
            {{ t('compare') }}
          </button>
          <section v-if="previous.length">
            <h3>{{ t('changes') }}</h3>
            <div v-for="f in changes" :key="f.path">
              <h4>{{ f.path }}</h4>
              <pre
                v-for="line in f.lines.filter((l) => l.changed)"
                :key="line.line"
                >{{ line.line }} − {{ line.before }}
{{ line.line }} + {{ line.after }}</pre>
            </div>
          </section>
        </template>
        <template v-if="review"
          ><h3>{{ t('history') }}</h3>
          <p>{{ review.reviewId }} / {{ review.authorId }}</p>
          <p v-for="(d, index) in review.decisions" :key="index">
            {{ d.reviewerId }} · {{ t(d.role) }} · {{ d.decision }} ·
            {{ d.reason }} · {{ d.versionRevision }} · {{ d.createdAt }}
          </p>
          <p v-if="author">{{ t('author') }}</p>
          <p v-for="issue in publicationIssues" :key="issue.code">
            {{ t(issue.code, issue) }}
          </p></template
        >
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
              @click="versionAction('withdrawn')"
            >
              {{ t('withdraw') }}</button
            ><button
              :disabled="!reason.trim() || !strongEtag(versionEtag)"
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
.catalog-detail {
  margin-top: 24px;
  padding: 24px;
  border: 1px solid var(--auriga-border);
  border-radius: 16px;
  background: var(--auriga-surface);
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
.catalog-page .market-tabs,
.catalog-page :deep(.resource-pager) {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}
.catalog-page h1 {
  font-size: 22px;
  margin: 0;
}
.catalog-page :deep(h2) {
  font-size: 18px;
}
.catalog-page :deep(.market-tabs a) {
  color: var(--auriga-accent);
  padding: 8px 0;
}
.catalog-page :deep(.market-tabs a.router-link-active) {
  font-weight: 600;
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
