<script setup lang="ts">
/* global File, Event, HTMLInputElement */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  resourceCatalogApi as api,
  type CatalogVersion,
  type CatalogReview,
  type CatalogPackage,
  type CatalogDistribution,
} from '../../admin/resourceCatalogApi';
import {
  publicationReady,
  riskOptions,
  distributionApplied,
  textDiff,
} from '../../admin/resourceCatalogPolicy';
import { skillPreview } from '../../admin/resourceSkillPreview';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
import { strongEtag } from '../../admin/resourceApi';
import { requestStepUp } from '../../admin/stepUp';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import '../../admin/resources.css';
const props = defineProps<{
  phase: 'directory' | 'review' | 'published' | 'distribution';
}>();
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
  versionNumber = ref(''),
  digest = ref(''),
  reason = ref('');
const manifest = shallowRef<File>(),
  artifacts = shallowRef<File[]>([]);
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
let epoch = 0,
  detailEpoch = 0,
  alive = true;
const locked = computed(() => busy.value || !!pending.value);
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
        ? await api.packages(target)
        : props.phase === 'review'
          ? await api.reviews(target)
          : props.phase === 'published'
            ? await api.published(target)
            : await api.distributions(target);
    if (!alive || e !== epoch) return;
    if (props.phase === 'directory')
      packages.value = (page.items as CatalogPackage[]).filter(
        (p) => p.kind === 'skill',
      );
    else if (props.phase === 'review')
      reviews.value = (page.items as CatalogReview[]).filter(
        (r) => r.version.kind === 'skill',
      );
    else if (props.phase === 'published')
      versions.value = (page.items as CatalogVersion[]).filter(
        (v) => v.kind === 'skill',
      );
    else
      distributions.value = (page.items as CatalogDistribution[]).filter((d) =>
        d.selections.some((s) => s.identity.kind === 'skill'),
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
      versions.value = page.items.filter((v) => v.kind === 'skill');
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
    const [version, evidence] = await Promise.all([
      api.version(v.packageId, v.id),
      r ? api.review(r.reviewId) : Promise.resolve(undefined),
    ]);
    if (!alive || e !== detailEpoch) return;
    if (
      version.data.kind !== 'skill' ||
      (evidence &&
        (evidence.data.version.id !== version.data.id ||
          evidence.data.version.packageId !== version.data.packageId ||
          evidence.data.version.kind !== 'skill' ||
          evidence.data.version.revision !== version.data.revision ||
          evidence.data.version.digest !== version.data.digest))
    )
      throw new Error('Stale evidence');
    selected.value = version.data;
    versionEtag.value = version.etag;
    review.value = evidence?.data;
    reviewEtag.value = evidence?.etag ?? null;
    risk.value = version.data.risk.level;
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
    notice.value = t(props.phase === 'distribution' ? 'accepted' : 'saved');
    busy.value = false;
    await load(cursor.value);
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
    key = globalThis.crypto.randomUUID();
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
    (!can('resource.packages.upload') || v.state !== 'draft')
  )
    return;
  if (
    (mode === 'withdrawn' || mode === 'malicious') &&
    !can('resource.versions.publish')
  )
    return;
  const key = globalThis.crypto.randomUUID();
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
      kind: 'skill' as const,
      visibility: visibility.value,
      groupIds:
        visibility.value === 'groups'
          ? groups.value
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean)
          : [],
    },
    key = globalThis.crypto.randomUUID();
  prepare(
    `${t('create')} · ${body.name} · ${t(body.visibility)} · ${body.groupIds.join(', ')}`,
    async () => {
      const p = await api.create(body, key);
      if (alive) pid.value = p.id;
    },
  );
}
function choose(event: Event, type: 'manifest' | 'artifacts') {
  const list = Array.from((event.target as HTMLInputElement).files ?? []);
  if (type === 'manifest') manifest.value = list[0];
  else artifacts.value = list;
}
async function upload() {
  const file = manifest.value,
    parts = [...artifacts.value],
    p = pid.value.trim(),
    number = versionNumber.value.trim(),
    hash = digest.value.trim();
  if (locked.value || !can('resource.packages.upload')) return;
  if (
    !file ||
    !p ||
    !number ||
    !/^sha256:[0-9a-f]{64}$/.test(hash) ||
    file.size > 262144 ||
    !parts.length ||
    parts.length > 32 ||
    parts.some((f) => f.size > 536870912) ||
    parts.reduce((n, f) => n + f.size, file.size) > 1073741824
  ) {
    error.value = t('invalid');
    return;
  }
  busy.value = true;
  try {
    const pack = await api.get(p);
    if (!alive) return;
    if (pack.data.kind !== 'skill' || !strongEtag(pack.etag))
      throw new Error('Invalid package');
    const etag = pack.etag,
      key = globalThis.crypto.randomUUID(),
      body = { version: number, expectedDigest: hash };
    busy.value = false;
    prepare(`${t('upload')} · ${pack.data.name} ${number} · ${hash}`, () =>
      api.upload(p, body, file, parts, etag, key),
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
      pack.data.kind !== 'skill' ||
      value.kind !== 'skill' ||
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
            (s) => s.identity.kind !== 'skill' || s.identity.packageId !== p,
          ) ?? []),
          {
            identity: {
              tenantId: pack.data.tenantId,
              packageId: p,
              versionId: v,
              kind: 'skill' as const,
              version: value.version,
              manifestDigest: value.digest,
            },
            action: act,
          },
        ],
      },
      etag = existing?.etag ?? '"0"',
      key = globalThis.crypto.randomUUID();
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
  () => props.phase,
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
          query: { kind: 'skill' },
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
        <form v-if="can('resource.packages.upload')" @submit.prevent="create">
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
        <form v-if="can('resource.packages.upload')" @submit.prevent="upload">
          <label>{{ t('packageId') }}<input v-model="pid" required /></label
          ><label
            >{{ t('version') }}<input v-model="versionNumber" required /></label
          ><label>{{ t('digest') }}<input v-model="digest" required /></label
          ><label
            >{{ t('manifest')
            }}<input
              type="file"
              accept=".json"
              required
              @change="choose($event, 'manifest')" /></label
          ><label
            >{{ t('artifacts')
            }}<input
              type="file"
              multiple
              required
              @change="choose($event, 'artifacts')" /></label
          ><button>{{ t('upload') }}</button>
        </form>
      </fieldset>
      <form
        v-if="phase === 'distribution'"
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
              <td>{{ p.level.toUpperCase() }}</td>
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
        <button :disabled="locked" @click="preview()">
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
        <template v-if="review"
          ><h3>{{ t('history') }}</h3>
          <p>{{ review.reviewId }} / {{ review.authorId }}</p>
          <p v-for="(d, index) in review.decisions" :key="index">
            {{ d.reviewerId }} · {{ t(d.role) }} · {{ d.decision }} ·
            {{ d.reason }} · {{ d.versionRevision }} · {{ d.createdAt }}
          </p>
          <p v-if="author">{{ t('author') }}</p>
          <p v-if="!ready">{{ t('quorum') }}</p></template
        >
        <fieldset :disabled="locked">
          <label
            >{{ t('reason')
            }}<input v-model="reason" data-testid="catalog-reason"
          /></label>
          <template v-if="review && can('resource.versions.review')"
            ><label
              >{{ t('role')
              }}<select v-model="role">
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
            v-if="selected.state === 'draft' && can('resource.packages.upload')"
            :disabled="!reason.trim() || !strongEtag(versionEtag)"
            @click="versionAction('submit')"
          >
            {{ t('submit') }}
          </button>
          <template
            v-if="
              selected.state === 'published' && can('resource.versions.publish')
            "
            ><p>{{ t('withdrawNote') }}</p>
            <button
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
</style>
