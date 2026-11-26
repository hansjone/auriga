<script setup lang="ts">
/* global File, Event, HTMLInputElement, DragEvent */
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
import {
  resourceCatalogApi as api,
  catalogKey,
  catalogRevisionEtag,
  type CatalogPackage,
  type CatalogVersion,
} from '../../admin/resourceCatalogApi';
import { strongEtag } from '../../admin/etag';
import {
  parseResourceSource,
  finalizeResourceUpload,
  UploadParseError,
  type ResourceSource,
} from '../../admin/resourceUploadParser';
import type {
  CatalogKind,
  PreparedUpload,
} from '../../admin/resourceCatalogUpload';
import { catalogMessages } from '../../admin/resourceCatalogMessages';
import { requestStepUp } from '../../admin/stepUp';
const props = defineProps<{
  kind: CatalogKind;
  author: string;
  canSubmit: boolean;
  disabled?: boolean;
  upgradeTarget?: {
    packageId: string;
    name: string;
    latestVersion?: string;
    expectedPackageName?: string;
  };
}>();
const emit = defineEmits<{
  completed: [];
  locked: [value: boolean];
  resumeDraft: [version: CatalogVersion];
  cancelUpgrade: [];
}>();
const { t } = useI18n({ useScope: 'local', messages: catalogMessages });
const source = shallowRef<ResourceSource>();
const manifest = ref<ResourceSource['manifest']>();
const savedDraft = shallowRef<CatalogVersion>();
const parsing = ref(false),
  busy = ref(false),
  error = ref(''),
  notice = ref(''),
  confirmed = ref(false),
  stepup = ref(false);
const visibility = ref<'tenant' | 'members' | 'groups'>('tenant'),
  groups = ref('');
const tools = ref(''),
  hosts = ref(''),
  changeNotes = ref(''),
  submitReason = ref('');
const folderInput = ref<HTMLInputElement>(),
  archiveInput = ref<HTMLInputElement>();
const filename = ref('');
let epoch = 0,
  alive = true;
// Each stage keeps its exact request bytes, precondition and idempotency key.
// A manual retry resumes the stage rather than recreating an uploaded version.
type Intent = {
  draft: PreparedUpload;
  submit: boolean;
  visibility: 'tenant' | 'members' | 'groups';
  groupIds: string[];
  createKey: string;
  uploadKey: string;
  submitKey: string;
  pack?: CatalogPackage;
  etag?: string;
  version?: CatalogVersion;
  reason: string;
  at: number;
};
const intent = shallowRef<Intent>();
const locked = computed(() => busy.value || !!intent.value);
const range = computed({
  get: () =>
    manifest.value?.kind === 'plugin'
      ? manifest.value.plugin.dshVersionRange
      : manifest.value?.kind === 'skill'
        ? manifest.value.skill.dshVersionRange
        : '',
  set: (v: string) => {
    if (manifest.value?.kind === 'plugin')
      manifest.value.plugin.dshVersionRange = v;
    else if (manifest.value?.kind === 'skill')
      manifest.value.skill.dshVersionRange = v;
  },
});
const functionalTags = computed(() =>
  manifest.value?.kind === 'plugin'
    ? (manifest.value.plugin.tags ?? []).join(', ')
    : '',
);
function updateFunctionalTags(event: Event) {
  if (manifest.value?.kind !== 'plugin') return;
  manifest.value.plugin.tags = (event.target as HTMLInputElement).value
    .split(/[,，]/)
    .map((tag) => tag.trim().toLowerCase())
    .filter(Boolean);
  confirmed.value = false;
}
watch(locked, (v) => emit('locked', v));
watch(
  () => props.upgradeTarget,
  () => {
    source.value = undefined;
    manifest.value = undefined;
    savedDraft.value = undefined;
    filename.value = '';
    confirmed.value = false;
    error.value = '';
    notice.value = '';
  },
);
watch(
  [tools, hosts, () => JSON.stringify(manifest.value?.permissions.dataAccess)],
  () => {
    confirmed.value = false;
  },
);
function message(e: unknown) {
  if (e instanceof UploadParseError)
    return t('parse_' + e.code, { path: e.path });
  if (isApiError(e)) {
    if (e.problem.code === 'iam.step_up_required') {
      stepup.value = true;
      return t('stepup');
    }
    if (e.status === 409 || e.status === 412) return t('uploadConflict');
    if (e.status === 403) return t('forbidden');
  }
  return t('failed');
}
async function choose(files: File[]) {
  if (locked.value || props.disabled || !files.length) return;
  const e = ++epoch;
  parsing.value = true;
  source.value = undefined;
  savedDraft.value = undefined;
  manifest.value = undefined;
  confirmed.value = false;
  error.value = '';
  notice.value = '';
  filename.value =
    files.length === 1
      ? files[0]!.name
      : files[0]!.webkitRelativePath.split('/')[0] + ' · ' + files.length;
  try {
    const value = await parseResourceSource(files, props.kind, props.author);
    if (!alive || e !== epoch) return;
    if (props.upgradeTarget) {
      if (
        props.kind === 'plugin' &&
        value.manifest.kind === 'plugin' &&
        props.upgradeTarget.expectedPackageName
      ) {
        if (
          value.manifest.plugin.packageName !==
          props.upgradeTarget.expectedPackageName
        ) {
          error.value = t('upgradeNameMismatch', {
            name: props.upgradeTarget.name,
          });
          return;
        }
      } else if (value.manifest.name !== props.upgradeTarget.name) {
        error.value = t('upgradeNameMismatch', {
          name: props.upgradeTarget.name,
        });
        return;
      }
    }
    source.value = value;
    manifest.value = JSON.parse(JSON.stringify(value.manifest));
    if (
      props.upgradeTarget &&
      props.kind === 'plugin' &&
      props.upgradeTarget.expectedPackageName &&
      manifest.value
    ) {
      manifest.value.name = props.upgradeTarget.name;
    }
    tools.value = value.manifest.permissions.tools.join(', ');
    hosts.value = value.manifest.permissions.outboundHosts.join(', ');
  } catch (e2) {
    if (alive && e === epoch) error.value = message(e2);
  } finally {
    if (alive && e === epoch) parsing.value = false;
  }
}
function change(e: Event) {
  const input = e.target as HTMLInputElement;
  void choose(Array.from(input.files ?? []));
  input.value = '';
}
function drop(e: DragEvent) {
  if (props.kind === 'plugin')
    void choose(Array.from(e.dataTransfer?.files ?? []));
}
async function findPackage(name: string) {
  let cursor: string | undefined;
  const seen = new Set<string>();
  do {
    const page = await api.packages(cursor, props.kind);
    if (!alive) throw new Error('View closed');
    const match = page.items.find(
      (p) => p.kind === props.kind && p.name === name,
    );
    if (match) return match;
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error('Repeated cursor');
    if (cursor) seen.add(cursor);
  } while (cursor);
}
async function duplicate(p: string, version: string) {
  let cursor: string | undefined;
  const seen = new Set<string>();
  do {
    const page = await api.versions(p, cursor);
    if (!alive) throw new Error('View closed');
    if (page.items.some((v) => v.version === version)) return true;
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error('Repeated cursor');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return false;
}
async function start(submit: boolean) {
  if (
    locked.value ||
    props.disabled ||
    parsing.value ||
    !confirmed.value ||
    !source.value ||
    !manifest.value ||
    !!savedDraft.value ||
    (submit && !props.canSubmit)
  )
    return;
  busy.value = true;
  error.value = '';
  notice.value = '';
  try {
    const m = JSON.parse(
      JSON.stringify(manifest.value),
    ) as ResourceSource['manifest'];
    const split = (s: string) =>
      s
        .split(/[,\n]/)
        .map((v) => v.trim())
        .filter(Boolean);
    m.permissions.tools = split(tools.value);
    m.permissions.outboundHosts = split(hosts.value);
    const groupIds = visibility.value === 'groups' ? split(groups.value) : [];
    if (
      visibility.value === 'groups' &&
      (!groupIds.length ||
        groupIds.some(
          (v) =>
            !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
              v,
            ),
        ))
    )
      throw new UploadParseError('groupsInvalid');
    const draft = await finalizeResourceUpload(source.value, m);
    if (!alive) return;
    if (props.upgradeTarget) {
      if (
        props.kind === 'plugin' &&
        draft.manifest.kind === 'plugin' &&
        props.upgradeTarget.expectedPackageName &&
        draft.manifest.plugin.packageName !==
          props.upgradeTarget.expectedPackageName
      ) {
        error.value = t('upgradeNameMismatch', {
          name: props.upgradeTarget.name,
        });
        return;
      }
      if (draft.manifest.name !== props.upgradeTarget.name) {
        error.value = t('upgradeNameMismatch', {
          name: props.upgradeTarget.name,
        });
        return;
      }
    }
    const notes = changeNotes.value.trim();
    if (notes) draft.metadata = { ...draft.metadata, changeNotes: notes };
    const reasonText = submitReason.value.trim() || t('uploadReviewReason');
    intent.value = {
      draft,
      submit,
      visibility: visibility.value,
      groupIds,
      createKey: catalogKey(),
      uploadKey: catalogKey(),
      submitKey: catalogKey(),
      reason: reasonText,
      at: Date.now(),
    };
  } catch (e) {
    if (alive) error.value = message(e);
  } finally {
    if (alive) busy.value = false;
  }
  if (intent.value) await run();
}
async function run() {
  const i = intent.value;
  if (!i || busy.value) return;
  if (Date.now() - i.at >= 86400000) {
    intent.value = undefined;
    error.value = t('expired');
    return;
  }
  busy.value = true;
  error.value = '';
  stepup.value = false;
  try {
    if (!i.pack) {
      if (props.upgradeTarget) {
        const target = await api.get(props.upgradeTarget.packageId);
        if (!alive) return;
        i.pack = target.data;
      } else {
        i.pack = await findPackage(i.draft.manifest.name);
        if (!alive) return;
        if (!i.pack)
          i.pack = await api.create(
            {
              name: i.draft.manifest.name,
              kind: props.kind,
              visibility: i.visibility,
              groupIds: i.groupIds,
            },
            i.createKey,
          );
        if (!alive) return;
      }
    }
    if (!i.version) {
      if (!i.etag) {
        if (await duplicate(i.pack.id, i.draft.manifest.version)) {
          error.value = t('versionExists', {
            version: i.draft.manifest.version,
          });
          intent.value = undefined;
          return;
        }
        const current = await api.get(i.pack.id);
        if (!alive) return;
        if (
          current.data.kind !== props.kind ||
          current.data.name !== i.draft.manifest.name ||
          !strongEtag(current.etag)
        )
          throw new Error('Invalid package');
        i.etag = current.etag;
      }
      i.version = await api.upload(
        i.pack.id,
        i.draft.metadata,
        i.draft.file,
        i.draft.artifacts,
        i.etag,
        i.uploadKey,
      );
      if (!alive) return;
    }
    if (
      i.version.packageId !== i.pack.id ||
      i.version.kind !== props.kind ||
      i.version.version !== i.draft.manifest.version
    )
      throw new Error('Mismatched version');
    if (i.submit) {
      const etag = catalogRevisionEtag(i.version.revision);
      if (!etag) throw new Error('Invalid revision');
      await api.submit(i.pack.id, i.version.id, i.reason, etag, i.submitKey);
    }
    if (!alive) return;
    notice.value = t(i.submit ? 'uploadSubmitted' : 'uploadSaved');
    intent.value = undefined;
    source.value = undefined;
    manifest.value = undefined;
    changeNotes.value = '';
    submitReason.value = '';
    confirmed.value = false;
    busy.value = false;
    await nextTick();
    if (alive) emit('completed');
  } catch (e) {
    if (alive) {
      error.value = (i.version ? t('draftSavedRetry') + ' ' : '') + message(e);
      if (
        isApiError(e) &&
        e.status < 500 &&
        e.status !== 429 &&
        e.problem.code !== 'iam.step_up_required'
      ) {
        intent.value = undefined;
        if (i.version) {
          savedDraft.value = i.version;
          error.value = t('draftSubmissionRejected') + ' ' + message(e);
        } else if (!i.pack && e.status === 409) {
          // A create conflict may belong to a package hidden from this member.
          // Re-read only; do not replay the failed write automatically.
          try {
            const visible = await findPackage(i.draft.manifest.name);
            if (alive)
              error.value = t(visible ? 'uploadConflict' : 'nameUnavailable');
          } catch {
            /* Keep the original conflict if this read cannot complete. */
          }
        }
      }
    }
  } finally {
    if (alive) busy.value = false;
  }
}
async function verify() {
  try {
    await requestStepUp();
    stepup.value = false;
  } catch (e) {
    error.value = message(e);
  }
}
onBeforeUnmount(() => {
  alive = false;
  ++epoch;
  emit('locked', false);
});
</script>
<template>
  <article class="upload-panel">
    <header>
      <h2>{{ t('uploadTitle', { kind: t(kind) }) }}</h2>
      <p>{{ t(kind === 'plugin' ? 'uploadPluginHint' : 'uploadSkillHint') }}</p>
      <div
        v-if="upgradeTarget"
        data-testid="upgrade-banner"
        class="upgrade-banner"
      >
        <div>
          <strong>{{
            t('upgradingTitle', { name: upgradeTarget.name })
          }}</strong>
          <small v-if="upgradeTarget.latestVersion">{{
            t('upgradingCurrent', { version: upgradeTarget.latestVersion })
          }}</small>
        </div>
        <button
          type="button"
          :disabled="locked || disabled"
          @click="emit('cancelUpgrade')"
        >
          {{ t('cancelUpgrade') }}
        </button>
      </div>
    </header>
    <div class="upload-drop" @dragover.prevent @drop.prevent="drop">
      <input
        v-if="kind === 'skill'"
        ref="folderInput"
        data-testid="catalog-folder"
        class="file-input"
        type="file"
        webkitdirectory
        multiple
        :disabled="locked || disabled"
        :aria-label="t('folder')"
        @change="change"
      />
      <input
        ref="archiveInput"
        data-testid="catalog-artifacts"
        class="file-input"
        type="file"
        :accept="kind === 'plugin' ? '.tgz' : '.tgz,.tar.gz'"
        :disabled="locked || disabled"
        :aria-label="t('chooseArchive')"
        @change="change"
      />
      <button
        type="button"
        :disabled="locked || disabled"
        @click="kind === 'skill' ? folderInput?.click() : archiveInput?.click()"
      >
        {{ t(kind === 'skill' ? 'folder' : 'choosePlugin') }}
      </button>
      <button
        v-if="kind === 'skill'"
        type="button"
        :disabled="locked || disabled"
        @click="archiveInput?.click()"
      >
        {{ t('chooseArchive') }}
      </button>
      <span>{{
        filename || t(kind === 'plugin' ? 'dropPlugin' : 'skillFilesHint')
      }}</span>
    </div>
    <p v-if="parsing" role="status">{{ t('checkingUpload') }}</p>
    <ul
      v-if="manifest && source?.warnings?.length && !savedDraft"
      data-testid="upload-preview-notices"
      class="upload-notices"
    >
      <li v-for="warning in source.warnings" :key="warning">
        {{ t('preview_' + warning) }}
      </li>
    </ul>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <button v-if="stepup" type="button" :disabled="busy" @click="verify">
      {{ t('stepup') }}
    </button>
    <button
      v-if="intent && !busy"
      data-testid="upload-retry"
      type="button"
      @click="run"
    >
      {{ t('retry') }}
    </button>
    <p v-if="savedDraft" class="saved-draft">
      {{ savedDraft.manifest.name }} · {{ savedDraft.version }}
      <button
        data-testid="upload-open-draft"
        type="button"
        :disabled="locked || disabled"
        @click="emit('resumeDraft', savedDraft)"
      >
        {{ t('openSavedDraft') }}
      </button>
    </p>
    <form
      v-if="manifest && !savedDraft"
      data-testid="catalog-upload"
      @submit.prevent="start(true)"
    >
      <fieldset
        :disabled="locked || disabled"
        data-testid="catalog-upload-preview"
      >
        <div class="upload-grid">
          <label
            >{{ t('name')
            }}<input
              v-model="manifest.name"
              data-testid="upload-name"
              maxlength="64"
              :readonly="!!upgradeTarget"
              required
          /></label>
          <label
            >{{ t('version')
            }}<input
              v-model="manifest.version"
              data-testid="upload-version"
              maxlength="128"
              required
              :readonly="kind === 'plugin'"
            /><small v-if="kind === 'plugin'">{{
              t('pluginVersionHint')
            }}</small></label
          >
          <label class="full-width"
            >{{ t('description')
            }}<textarea
              v-model="manifest.description"
              data-testid="upload-description"
              rows="2"
              maxlength="1024"
              required
            />
          </label>
          <label
            >{{ t('authorName')
            }}<input
              v-model="manifest.author.displayName"
              data-testid="upload-author"
              maxlength="128"
              required
            /><small>{{ t('submitterAutoHint') }}</small></label
          >
          <label
            >{{ t('license')
            }}<input
              v-model="manifest.license"
              data-testid="upload-license"
              maxlength="128"
              required
          /></label>
          <label
            >{{ t('dshRange')
            }}<input
              v-model="range"
              data-testid="upload-range"
              maxlength="128"
              required
          /></label>
          <label v-if="manifest.kind === 'plugin'">
            {{ t('functionalTags') }}
            <input
              :value="functionalTags"
              data-testid="upload-functional-tags"
              maxlength="164"
              @change="updateFunctionalTags"
            />
            <small>{{ t('functionalTagsHint') }}</small>
          </label>
          <label
            >{{ t('visibility')
            }}<select v-model="visibility">
              <option
                v-for="s in ['tenant', 'members', 'groups']"
                :key="s"
                :value="s"
              >
                {{ t(s) }}
              </option></select
            ><small>{{ t('visibilityHint') }}</small></label
          >
          <label v-if="visibility === 'groups'" class="full-width"
            >{{ t('groupIds') }}<input v-model="groups" required
          /></label>
          <label class="full-width"
            >{{ t('changeNotes')
            }}<textarea
              v-model="changeNotes"
              data-testid="upload-change-notes"
              rows="2"
              maxlength="4096"
              :placeholder="t('changeNotesPlaceholder')"
            />
          </label>
          <label v-if="canSubmit" class="full-width"
            >{{ t('submitReason')
            }}<input
              v-model="submitReason"
              data-testid="upload-submit-reason"
              maxlength="1024"
              :placeholder="t('submitReasonPlaceholder')"
            />
          </label>
        </div>
        <section class="upload-permissions">
          <h3>{{ t('permissions') }}</h3>
          <p>{{ t('permissionsHint') }}</p>
          <div class="upload-grid">
            <label
              >{{ t('permissionTools')
              }}<input v-model="tools" :placeholder="t('none')"
            /></label>
            <label
              >{{ t('permissionHosts')
              }}<input v-model="hosts" :placeholder="t('none')"
            /></label>
          </div>
          <div class="permission-checks">
            <label
              v-for="value in [
                'workspace_read',
                'workspace_write',
                'user_selected_files',
              ] as const"
              :key="value"
              ><input
                v-model="manifest.permissions.dataAccess"
                type="checkbox"
                :value="value"
              />{{ t(value) }}</label
            >
          </div>
          <label class="permission-confirm"
            ><input
              v-model="confirmed"
              data-testid="permissions-confirm"
              type="checkbox"
            />{{ t('permissionConfirm') }}</label
          >
        </section>
        <details>
          <summary>{{ t('advanced') }}</summary>
          <p v-if="source?.embedded">{{ t('embeddedHint') }}</p>
          <p v-if="kind === 'skill'">{{ t('snapshotHint') }}</p>
          <p v-if="manifest.kind === 'plugin'">
            {{ t('npmName') }}: {{ manifest.plugin.packageName }}
          </p>
          <p>
            {{ t('signature') }}: {{ t('unsigned') }} ·
            {{ manifest.signature.algorithm }}
          </p>
        </details>
      </fieldset>
      <footer class="upload-actions">
        <button
          data-testid="upload-submit"
          class="catalog-primary"
          :disabled="!confirmed || locked || disabled || !canSubmit || parsing"
        >
          {{ t('uploadAndSubmit') }}</button
        ><button
          data-testid="upload-draft"
          type="button"
          :disabled="!confirmed || locked || disabled || parsing"
          @click="start(false)"
        >
          {{ t('saveDraft') }}
        </button>
      </footer>
    </form>
  </article>
</template>
<style scoped>
.upload-panel {
  border: 1px solid var(--auriga-border);
  background: var(--auriga-surface);
  border-radius: 12px;
  padding: 24px;
  margin-bottom: 24px;
}
.upload-notices {
  color: var(--auriga-muted);
  padding-left: 20px;
  line-height: 1.7;
}
.saved-draft {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.upload-panel h2 {
  margin: 0 0 8px;
  font-size: 18px;
}
.upload-panel h3 {
  font-size: 14px;
  margin: 0 0 6px;
}
.upload-panel p,
.upload-panel small {
  color: var(--auriga-muted);
  line-height: 1.6;
}
.upload-panel header p {
  margin: 0 0 20px;
}
.upgrade-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  margin-bottom: 16px;
  border: 1px solid var(--auriga-accent);
  border-radius: 8px;
  background: var(--admin-blue-soft);
}
.upgrade-banner strong {
  display: block;
}
.upload-drop {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  border: 1px dashed var(--auriga-border-strong);
  border-radius: 8px;
  padding: 24px;
  background: var(--auriga-field-bg);
}
.upload-drop span {
  color: var(--auriga-muted);
}
.file-input {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
.upload-panel fieldset {
  border: 0 !important;
  padding: 0 !important;
  margin: 20px 0 !important;
  min-width: 0;
}
.upload-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 24px;
}
.upload-panel .upload-grid label {
  display: flex;
  margin: 0;
  flex-direction: column;
  gap: 8px;
}
.full-width {
  grid-column: 1/-1;
}
.upload-panel textarea {
  resize: vertical;
  font: inherit;
  color: var(--auriga-text);
  background: var(--auriga-field-bg);
  border: 1px solid var(--auriga-border-strong);
  border-radius: 6px;
  padding: 8px;
}
.upload-permissions {
  margin: 24px 0;
  padding: 20px 0;
  border-top: 1px solid var(--auriga-border);
  border-bottom: 1px solid var(--auriga-border);
}
.upload-permissions p {
  margin: 0 0 16px;
}
.permission-checks {
  display: flex;
  flex-wrap: wrap;
  gap: 20px;
  margin-top: 12px;
}
.upload-panel .permission-checks label,
.upload-panel .permission-confirm {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 8px;
}
.upload-panel .permission-confirm {
  margin-top: 16px;
}
.upload-actions {
  display: flex;
  gap: 12px;
}

.upload-panel summary {
  cursor: pointer;
  color: var(--auriga-muted);
}
.upload-panel [role='alert'] {
  color: var(--auriga-danger);
}
@media (max-width: 700px) {
  .upload-grid {
    grid-template-columns: 1fr;
  }
  .upload-panel {
    padding: 16px;
  }
  .upload-drop {
    padding: 16px;
  }
}
</style>
