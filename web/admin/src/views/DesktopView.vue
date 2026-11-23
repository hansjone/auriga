<script setup lang="ts">
/* global File, Blob, Event, HTMLInputElement, TextEncoder, btoa, atob */
import { computed, onMounted, ref, shallowRef } from 'vue';
import { isApiError } from '@auriga/shared';
import { useAdminI18n } from '../admin/messages';
import { adminApi } from '../admin/api';
import { sha256 } from '../admin/resourceHash';
import { devicesApi, type AdminDevice } from '../admin/devicesApi';
import { apiClient } from '../api';
import type { components } from '../../../shared/src/generated/resource';

type Ledger = components['schemas']['DesktopReleaseLedger'];
type Manifest = components['schemas']['DesktopManifest'];
type Preflight = components['schemas']['DesktopReleasePreflight'];
const { t } = useAdminI18n();
const ledger = shallowRef<Ledger>();
const allowed = ref(false);
const loading = ref(true);
const busy = ref(false);
const checking = ref(false);
const loadError = ref('');
const actionError = ref('');
const notice = ref('');
const selfCheck = ref('');
const manifest = shallowRef<File>();
const archive = shallowRef<File>();
const preview = shallowRef<Manifest>();
const validationError = ref('');
const validated = ref(false);
const confirmed = ref(false);
const progress = ref(0);
const deviceAllowed = ref(false);
const devices = shallowRef<AdminDevice[]>();
const deviceError = ref('');
let selection = 0;
let manifestText = '';
const base = '/api/resource/v1/admin/desktop/releases';
const channels = ['stable', 'beta'] as const;
const items = computed(() =>
  [...(ledger.value?.items ?? [])].sort(
    (a, b) => b.manifest.sequence - a.manifest.sequence,
  ),
);
const distribution = computed(() => {
  const counts = new Map<string, number>();
  for (const device of devices.value ?? []) {
    if (device.status !== 'active') continue;
    const version = device.clientVersion?.trim() || '';
    counts.set(version, (counts.get(version) ?? 0) + 1);
  }
  return [...counts].sort(([a], [b]) => a.localeCompare(b));
});
const updateSummary = computed(() => {
  const result = {
    reported: 0,
    updated: 0,
    pending: 0,
    unknown: 0,
    unreported: 0,
    failed: 0,
    rolledBack: 0,
  };
  for (const device of devices.value ?? []) {
    if (device.status !== 'active') continue;
    const report = device.desktopReport;
    if (!report) {
      result.unreported++;
      continue;
    }
    result.reported++;
    const latest = ledger.value?.channels?.[report.channel];
    if (!latest) result.unknown++;
    else if (report.clientVersion === latest.version) result.updated++;
    else result.pending++;
    if (
      report.lastUpdate?.result === 'failed' ||
      report.lastUpdate?.result === 'rolled_back'
    )
      result.failed++;
    if (report.lastUpdate?.result === 'rolled_back') result.rolledBack++;
  }
  return result;
});
function reason(error: unknown) {
  if (!isApiError(error)) return t('desktopServiceError');
  let key = 'desktopRequestError';
  if (error.problem.code === 'iam.step_up_required')
    key = 'desktopStepUpRequired';
  else if (error.status === 401) key = 'desktopLoginRequired';
  else if (error.status === 403) key = 'forbidden';
  else if (error.status >= 500) key = 'desktopServiceError';
  else if (error.problem.code === 'common.conflict')
    key = 'desktopPolicyConflict';
  else if (error.problem.code === 'common.validation_failed')
    key = 'desktopSignatureInvalid';
  return `${t(key)} (${error.status} / ${error.problem.code})`;
}
function resetValidation() {
  selection++;
  validated.value = false;
  confirmed.value = false;
  preview.value = undefined;
  validationError.value = '';
  progress.value = 0;
}
function selected(event: Event, kind: 'manifest' | 'archive') {
  resetValidation();
  const file = (event.target as HTMLInputElement).files?.[0];
  if (kind === 'manifest') manifest.value = file;
  else archive.value = file;
}

async function validate() {
  const json = manifest.value;
  const exe = archive.value;
  const state = ledger.value;
  if (!json || !exe || !state || checking.value || busy.value) return;
  resetValidation();
  const current = selection;
  checking.value = true;
  try {
    if (
      !json.size ||
      json.size > 32768 ||
      !exe.size ||
      exe.size > 4294967296 ||
      !exe.name.toLowerCase().endsWith('.exe')
    )
      throw new Error(t('desktopFilesInvalid'));
    const text = await json.text();
    if (new TextEncoder().encode(text).length > 32768)
      throw new Error(t('desktopFilesInvalid'));
    const value: Manifest = JSON.parse(text);
    if (
      !value ||
      ![1, 2].includes(value.schema) ||
      value.product !== 'Auriga Desktop' ||
      value.platform !== 'win-x64' ||
      !channels.includes(value.channel) ||
      !Number.isSafeInteger(value.sequence) ||
      value.sequence <= (state.highestSequence ?? 0) ||
      typeof value.version !== 'string' ||
      !value.version ||
      typeof value.minimumVersion !== 'string' ||
      !value.minimumVersion ||
      !Number.isSafeInteger(value.sizeBytes) ||
      value.sizeBytes < 1 ||
      value.sizeBytes > 4294967296 ||
      !value.signature ||
      value.signature.alg !== 'Ed25519' ||
      value.signature.kid !== 'auriga-desktop-release-1' ||
      !/^[a-f0-9]{64}$/.test(value.sha256) ||
      (value.schema === 2 &&
        (!Number.isInteger(value.rolloutPercentage) ||
          value.rolloutPercentage! < 0 ||
          value.rolloutPercentage! > 100))
    )
      throw new Error(t('desktopManifestInvalid'));
    preview.value = value;
    if (exe.size !== value.sizeBytes) throw new Error(t('desktopSizeMismatch'));
    const digest = await sha256(exe, (percentage) => {
      progress.value = percentage;
    });
    if (digest !== value.sha256) throw new Error(t('desktopHashMismatch'));
    if (!state.publicKeySpki || !state.publicKeyFingerprint)
      throw new Error(t('desktopKeyMissing'));
    const keyBytes = Uint8Array.from(atob(state.publicKeySpki), (character) =>
      character.charCodeAt(0),
    );
    if ((await sha256(new Blob([keyBytes]))) !== state.publicKeyFingerprint)
      throw new Error(t('desktopKeyMismatch'));
    // Server preflight uses the exact selected JSON bytes and the registered Ed25519 key.
    // It handles strict JSON/signature/policy checks before any EXE upload on HTTP too.
    const result = await apiClient.request<Preflight>(`${base}/preflight`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: text,
    });
    if (result.publicKeyFingerprint !== state.publicKeyFingerprint)
      throw new Error(t('desktopKeyMismatch'));
    if (current !== selection) return;
    manifestText = text;
    validated.value = true;
  } catch (error) {
    if (current === selection)
      validationError.value = isApiError(error)
        ? reason(error)
        : error instanceof SyntaxError
          ? t('desktopManifestInvalid')
          : error instanceof Error
            ? error.message
            : t('desktopFilesInvalid');
  } finally {
    checking.value = false;
  }
}
async function loadLedger() {
  loading.value = true;
  loadError.value = '';
  validated.value = false;
  confirmed.value = false;
  try {
    ledger.value = await apiClient.get<Ledger>(base, { cache: 'no-store' });
  } catch (error) {
    ledger.value = undefined;
    loadError.value = reason(error);
    throw error;
  } finally {
    loading.value = false;
  }
}
async function loadDevices() {
  if (!deviceAllowed.value) return;
  deviceError.value = '';
  try {
    devices.value = (await devicesApi.list()).items;
  } catch (error) {
    devices.value = undefined;
    deviceError.value = reason(error);
  }
}
async function refresh() {
  await Promise.allSettled([loadLedger(), loadDevices()]);
}
async function withdraw(sequence: number) {
  if (busy.value || checking.value) return;
  busy.value = true;
  actionError.value = '';
  notice.value = '';
  selfCheck.value = '';
  try {
    await apiClient.delete(`${base}/${sequence}`);
    notice.value = t('desktopWithdrawCompleted');
    try {
      await loadLedger();
    } catch {
      notice.value = t('desktopWithdrawCompletedRefreshFailed');
    }
  } catch (error) {
    actionError.value = reason(error);
  } finally {
    busy.value = false;
  }
}
async function publish() {
  if (
    !validated.value ||
    !confirmed.value ||
    !archive.value ||
    !preview.value ||
    busy.value ||
    checking.value
  )
    return;
  const release = preview.value;
  const exe = archive.value;
  const bytes = new TextEncoder().encode(manifestText);
  const header = btoa(
    Array.from(bytes, (byte) => String.fromCharCode(byte)).join(''),
  )
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '');
  busy.value = true;
  actionError.value = '';
  notice.value = '';
  selfCheck.value = '';
  try {
    await apiClient.request(base, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/octet-stream',
        'X-Desktop-Manifest': header,
      },
      body: exe,
    });
    validated.value = false;
    confirmed.value = false;
    notice.value = t('desktopPublished');
    try {
      const latest = await apiClient.get<Manifest>(
        `/api/resource/v1/desktop/latest?channel=${release.channel}`,
        { cache: 'no-store' },
      );
      if (JSON.stringify(latest) !== JSON.stringify(release)) {
        // Property order is irrelevant to signed integer-canonical JSON.
        const canonical = (value: unknown): string => {
          if (value && typeof value === 'object' && !Array.isArray(value))
            return (
              '{' +
              Object.keys(value)
                .sort()
                .map(
                  (key) =>
                    JSON.stringify(key) +
                    ':' +
                    canonical((value as Record<string, unknown>)[key]),
                )
                .join(',') +
              '}'
            );
          return JSON.stringify(value);
        };
        if (canonical(latest) !== canonical(release))
          throw new Error('latest mismatch');
      }
      selfCheck.value = t('desktopLatestPassed');
    } catch (error) {
      selfCheck.value = `${t('desktopLatestFailed')} ${reason(error)}`;
    }
    try {
      await loadLedger();
    } catch {
      notice.value = t('desktopPublishedRefreshFailed');
    }
  } catch (error) {
    actionError.value = reason(error);
  } finally {
    busy.value = false;
  }
}
onMounted(async () => {
  try {
    const permissions = (await adminApi.membership()).permissions;
    allowed.value = permissions.includes('resource.versions.publish');
    deviceAllowed.value = permissions.includes('iam.network.manage');
    if (allowed.value) await refresh();
  } catch (error) {
    loadError.value = reason(error);
  } finally {
    loading.value = false;
  }
});
</script>

<template>
  <section class="desktop-console" :aria-busy="loading || busy">
    <header class="page-head">
      <div>
        <h1>{{ t('desktopTitle') }}</h1>
        <p class="muted">{{ t('desktopIntro') }}</p>
      </div>
      <button
        v-if="allowed"
        type="button"
        :disabled="loading || busy || checking"
        @click="refresh"
      >
        {{ t('refresh') }}
      </button>
    </header>
    <p v-if="loading">{{ t('loading') }}</p>
    <p v-if="loadError" class="error" role="alert">{{ loadError }}</p>
    <p v-if="!loading && !loadError && !allowed" role="alert">
      {{ t('forbidden') }}
    </p>
    <template v-if="allowed">
      <p v-if="actionError" class="error" role="alert">{{ actionError }}</p>
      <p v-if="notice" role="status">{{ notice }}</p>
      <p v-if="selfCheck" role="status">{{ selfCheck }}</p>
      <section v-if="ledger" aria-labelledby="channel-title">
        <h2 id="channel-title">{{ t('desktopChannels') }}</h2>
        <div class="channels">
          <article v-for="channel in channels" :key="channel" class="channel">
            <h3>
              {{ t(channel === 'stable' ? 'desktopStable' : 'desktopBeta') }}
            </h3>
            <template v-if="ledger.channels?.[channel]">
              <p class="version">
                {{ ledger.channels[channel]!.version }}
                <small>#{{ ledger.channels[channel]!.sequence }}</small>
              </p>
              <p>
                {{
                  t('desktopPolicySummary', {
                    channel: t(
                      channel === 'stable' ? 'desktopStable' : 'desktopBeta',
                    ),
                    minimumVersion: ledger.channels[channel]!.minimumVersion,
                    percentage:
                      ledger.channels[channel]!.rolloutPercentage ?? 100,
                  })
                }}
              </p>
              <p v-if="ledger.channels[channel]!.rollbackFrom">
                {{
                  t('desktopRollbackSource', {
                    version: ledger.channels[channel]!.rollbackFrom,
                  })
                }}
              </p>
              <p class="muted">
                {{ t('desktopExpires') }}
                {{ ledger.channels[channel]!.notAfter }}
              </p>
              <p
                v-if="
                  Date.parse(ledger.channels[channel]!.notAfter) <= Date.now()
                "
                class="error"
              >
                {{ t('desktopExpired') }}
              </p>
            </template>
            <p v-else>{{ t('desktopNoActive') }}</p>
          </article>
        </div>
      </section>
      <section class="workflow" aria-labelledby="workflow-title">
        <h2 id="workflow-title">{{ t('desktopWorkflow') }}</h2>
        <ol>
          <li>
            <strong>{{ t('desktopBuild') }}</strong>
            <p>{{ t('desktopBuildHelp') }}</p>
          </li>
          <li>
            <strong>{{ t('desktopSign') }}</strong>
            <p>{{ t('desktopSignHelp') }}</p>
          </li>
          <li>
            <strong>{{ t('desktopUpload') }}</strong>
            <p>{{ t('desktopUploadHelp') }}</p>
          </li>
          <li>
            <strong>{{ t('desktopUpdate') }}</strong>
            <p>{{ t('desktopUpdateHelp') }}</p>
          </li>
        </ol>
        <a
          href="https://github.com/hansjone/auriga/blob/main/MAINTAIN.md#客户端发布windows-开发机"
          target="_blank"
          rel="noopener noreferrer"
          >{{ t('desktopGuide') }}</a
        >
      </section>
      <section aria-labelledby="upload-title">
        <div class="section-head">
          <h2 id="upload-title">{{ t('desktopUpload') }}</h2>
        </div>
        <p class="muted">
          {{ t('desktopPolicyInstructions') }}
          {{ t('desktopMandatoryInstructions') }}
        </p>
        <form @submit.prevent="publish">
          <div class="files">
            <label
              >{{ t('desktopManifest')
              }}<input
                type="file"
                accept=".json,application/json"
                :disabled="busy || checking"
                @change="selected($event, 'manifest')"
            /></label>
            <label
              >{{ t('desktopArchive')
              }}<input
                type="file"
                accept=".exe"
                :disabled="busy || checking"
                @change="selected($event, 'archive')"
            /></label>
          </div>
          <button
            type="button"
            :disabled="!manifest || !archive || !ledger || busy || checking"
            @click="validate"
          >
            {{ t(checking ? 'desktopChecking' : 'desktopValidate') }}
          </button>
          <p v-if="checking" role="status">
            {{ t('desktopHashProgress', { percentage: progress }) }}
          </p>
          <p
            v-if="validationError"
            data-test="validation-error"
            class="error"
            role="alert"
          >
            {{ validationError }}
          </p>
          <dl v-if="preview" class="preview">
            <dt>{{ t('desktopVersionSequence') }}</dt>
            <dd>{{ preview.version }} / #{{ preview.sequence }}</dd>
            <dt>{{ t('desktopPolicy') }}</dt>
            <dd>
              {{
                t('desktopPolicySummary', {
                  channel: t(
                    preview.channel === 'beta'
                      ? 'desktopBeta'
                      : 'desktopStable',
                  ),
                  minimumVersion: preview.minimumVersion,
                  percentage: preview.rolloutPercentage ?? 100,
                })
              }}
            </dd>
            <dt>rollbackFrom</dt>
            <dd>{{ preview.rollbackFrom ?? '—' }}</dd>
            <dt>{{ t('desktopPackageSize') }}</dt>
            <dd>
              {{ archive?.size.toLocaleString() }} /
              {{ preview.sizeBytes.toLocaleString() }} bytes
            </dd>
            <dt>SHA256</dt>
            <dd>
              <code>{{ preview.sha256 }}</code>
            </dd>
            <dt>{{ t('desktopSigningKey') }}</dt>
            <dd>
              <code>{{ ledger?.publicKeyFingerprint }}</code>
            </dd>
          </dl>
          <p v-if="validated" role="status">
            {{ t('desktopValidationPassed') }}
          </p>
          <label class="confirmation"
            ><input
              v-model="confirmed"
              type="checkbox"
              :disabled="!validated || busy || checking"
            />{{ t('desktopConfirm') }}</label
          >
          <button
            type="submit"
            :disabled="!validated || !confirmed || busy || checking"
          >
            {{ t('desktopPublish') }}
          </button>
        </form>
      </section>
      <section v-if="ledger" aria-labelledby="ledger-title">
        <h2 id="ledger-title">{{ t('desktopLedger') }}</h2>
        <p v-if="!items.length">{{ t('desktopEmpty') }}</p>
        <article
          v-for="item in items"
          :key="item.manifest.sequence"
          class="release"
        >
          <div class="section-head">
            <h3>
              {{ item.manifest.version }}
              <small>#{{ item.manifest.sequence }}</small>
            </h3>
            <span v-if="item.withdrawn">{{ t('desktopWithdrawn') }}</span
            ><button
              v-else
              type="button"
              :disabled="busy || checking"
              @click="withdraw(item.manifest.sequence)"
            >
              {{ t('desktopWithdraw') }}
            </button>
          </div>
          <p>
            {{
              t('desktopPolicySummary', {
                channel: t(
                  item.manifest.channel === 'beta'
                    ? 'desktopBeta'
                    : 'desktopStable',
                ),
                minimumVersion: item.manifest.minimumVersion,
                percentage: item.manifest.rolloutPercentage ?? 100,
              })
            }}
          </p>
          <p v-if="item.manifest.rollbackFrom">
            {{
              t('desktopRollbackSource', {
                version: item.manifest.rollbackFrom,
              })
            }}
          </p>
          <p class="muted">
            {{ (item.manifest.sizeBytes / 1048576).toFixed(1) }} MiB ·
            {{ item.manifest.publishedAt }}
          </p>
          <p v-if="item.manifest.notes">{{ item.manifest.notes }}</p>
          <code>{{ item.manifest.sha256 }}</code>
        </article>
      </section>
      <section data-test="devices" aria-labelledby="devices-title">
        <h2 id="devices-title">{{ t('desktopDevices') }}</h2>
        <p class="muted">{{ t('desktopDeviceSource') }}</p>
        <p v-if="!deviceAllowed">{{ t('desktopDevicePermission') }}</p>
        <p v-else-if="deviceError" class="error" role="alert">
          {{ deviceError }}
        </p>
        <template v-else-if="devices">
          <p v-if="!updateSummary.reported">
            {{ t('desktopNoReports') }}
          </p>
          <template v-else>
            <dl class="distribution" data-test="update-summary">
              <dt>{{ t('desktopUpdatedDevices') }}</dt>
              <dd data-test="updated">
                {{ t('desktopDeviceCount', { count: updateSummary.updated }) }}
              </dd>
              <dt>{{ t('desktopPendingDevices') }}</dt>
              <dd data-test="pending">
                {{ t('desktopDeviceCount', { count: updateSummary.pending }) }}
              </dd>
              <dt>{{ t('desktopUnknownTarget') }}</dt>
              <dd data-test="unknown">
                {{ t('desktopDeviceCount', { count: updateSummary.unknown }) }}
              </dd>
              <dt>{{ t('desktopFailedDevices') }}</dt>
              <dd data-test="failed">
                {{ t('desktopDeviceCount', { count: updateSummary.failed }) }}
              </dd>
              <dt>{{ t('desktopRolledBackDevices') }}</dt>
              <dd data-test="rolled-back">
                {{
                  t('desktopDeviceCount', { count: updateSummary.rolledBack })
                }}
              </dd>
            </dl>
            <p class="muted">{{ t('desktopUpdateCountsHelp') }}</p>
          </template>
          <p data-test="unreported">
            {{
              t('desktopUnreportedDevices', { count: updateSummary.unreported })
            }}
          </p>
          <dl class="distribution">
            <template v-for="[version, count] in distribution" :key="version"
              ><dt>{{ version || t('desktopUnreported') }}</dt>
              <dd>{{ t('desktopDeviceCount', { count }) }}</dd></template
            >
          </dl>
        </template>
        <p v-if="deviceAllowed && devices && !updateSummary.reported">
          {{ t('desktopFailureReports') }}
        </p>
      </section>
    </template>
  </section>
</template>

<style scoped>
.desktop-console {
  max-width: 1240px;
  margin-inline: auto;
  line-height: 1.6;
}
h1 {
  margin: 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.03em;
}
h2 {
  font-size: 1.0625rem;
  font-weight: 600;
}
h3 {
  margin: 0;
  font-size: 1rem;
}
p {
  margin-block: 0.55rem;
}
section > section {
  margin-top: 1.75rem;
}
.page-head,
.section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}
.page-head p {
  margin: 0.35rem 0 0;
  color: var(--auriga-muted);
  font-size: 13px;
}
.muted,
dt,
small {
  color: var(--auriga-muted);
}
.error {
  color: var(--auriga-danger);
}
.channels,
.files {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1.25rem;
}
.channel {
  padding: 1.125rem 1.2rem;
  background: var(--auriga-surface);
  border: 1px solid var(--auriga-border);
  border-radius: 10px;
}
.version {
  font-size: 1.8rem;
  font-weight: 600;
}
.version small {
  font-size: 0.9rem;
  font-weight: 400;
}
.workflow {
  border-block: 1px solid var(--auriga-border);
  padding-block: 0.75rem 1.25rem;
}
ol {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  padding-left: 1.4rem;
  gap: 2rem;
}
li p {
  color: var(--auriga-muted);
  font-size: 0.9rem;
}
form {
  display: grid;
  justify-items: start;
  gap: 1rem;
}
.files {
  width: 100%;
}
.files label {
  display: grid;
  gap: 0.5rem;
}
input[type='file'] {
  max-width: 100%;
  color: var(--auriga-text);
}
button {
  font: inherit;
  padding: 0.45rem 1rem;
  border: 1px solid var(--auriga-border-strong);
  border-radius: 7px;
  background: var(--auriga-surface);
  color: var(--auriga-text);
  cursor: pointer;
}
button[type='submit'] {
  background: var(--auriga-accent);
  color: #fff;
}
button:disabled {
  opacity: 0.5;
  cursor: default;
}
button:focus-visible,
input:focus-visible,
a:focus-visible {
  outline: 2px solid var(--auriga-accent-line);
  outline-offset: 3px;
}
a {
  color: var(--auriga-accent-line);
}
.preview,
.distribution {
  display: grid;
  grid-template-columns: minmax(130px, auto) minmax(0, 1fr);
  gap: 0.55rem 1.5rem;
  width: 100%;
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
code {
  overflow-wrap: anywhere;
}
.confirmation {
  display: flex;
  align-items: start;
  gap: 0.5rem;
}
.release {
  border-top: 1px solid var(--auriga-border);
  padding-block: 1rem;
}
@media (max-width: 760px) {
  .channels,
  .files,
  ol {
    grid-template-columns: 1fr;
  }
  ol {
    gap: 0.7rem;
  }
  .page-head {
    align-items: start;
  }
  .preview {
    grid-template-columns: 1fr;
    gap: 0.25rem;
  }
  .preview dd {
    margin-bottom: 0.75rem;
  }
}
</style>
