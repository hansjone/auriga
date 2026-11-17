<script setup lang="ts">
/* global File, Blob, Event, HTMLInputElement, TextEncoder, btoa, atob */
import { computed, onMounted, ref, shallowRef } from 'vue';
import { isApiError } from '@auriga/shared';
import { useAdminI18n } from '../admin/messages';
import { adminApi } from '../admin/api';
import { devicesApi, type AdminDevice } from '../admin/devicesApi';
import { apiClient } from '../api';
import { requestStepUp } from '../admin/stepUp';
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
const verifiedAt = ref(0);
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
const identityFresh = computed(() => verifiedAt.value > 0);
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

// Incremental SHA-256 keeps EXE memory bounded and also works on HTTP admin origins.
// Full chunks are a multiple of the 64-byte compression block. Only the tail is padded.
async function sha256(
  file: Blob,
  update: (percentage: number) => void = () => {},
) {
  const constants = [
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ];
  const hash = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ]);
  const words = new Uint32Array(64);
  const rotate = (value: number, bits: number) =>
    (value >>> bits) | (value << (32 - bits));
  function block(bytes: DataView, offset: number) {
    for (let i = 0; i < 16; i++) words[i] = bytes.getUint32(offset + i * 4);
    for (let i = 16; i < 64; i++) {
      const previous = words[i - 15]!;
      const recent = words[i - 2]!;
      const sigma0 =
        rotate(previous, 7) ^ rotate(previous, 18) ^ (previous >>> 3);
      const sigma1 = rotate(recent, 17) ^ rotate(recent, 19) ^ (recent >>> 10);
      words[i] = words[i - 16]! + sigma0 + words[i - 7]! + sigma1;
    }
    let [a, b, c, d, e, f, g, h] = Array.from(hash) as [
      number,
      number,
      number,
      number,
      number,
      number,
      number,
      number,
    ];
    for (let i = 0; i < 64; i++) {
      const sum1 = rotate(e, 6) ^ rotate(e, 11) ^ rotate(e, 25);
      const choice = (e & f) ^ (~e & g);
      const temp1 = (h + sum1 + choice + constants[i]! + words[i]!) >>> 0;
      const sum0 = rotate(a, 2) ^ rotate(a, 13) ^ rotate(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + sum0 + majority) >>> 0;
    }
    [a, b, c, d, e, f, g, h].forEach((value, i) => {
      hash[i] = hash[i]! + value;
    });
  }
  let tail = new Uint8Array();
  const chunkSize = 4 * 1024 * 1024;
  for (let offset = 0; offset < file.size; offset += chunkSize) {
    const bytes = new Uint8Array(
      await file.slice(offset, offset + chunkSize).arrayBuffer(),
    );
    const full = bytes.length - (bytes.length % 64);
    const data = new DataView(bytes.buffer);
    for (let start = 0; start < full; start += 64) block(data, start);
    tail = bytes.slice(full);
    update(
      Math.round(
        (Math.min(offset + bytes.length, file.size) / file.size) * 100,
      ),
    );
  }
  const padded = new Uint8Array(tail.length < 56 ? 64 : 128);
  padded.set(tail);
  padded[tail.length] = 0x80;
  const ending = new DataView(padded.buffer);
  ending.setUint32(
    padded.length - 8,
    Math.floor((file.size * 8) / 0x100000000),
  );
  ending.setUint32(padded.length - 4, (file.size * 8) >>> 0);
  for (let offset = 0; offset < padded.length; offset += 64)
    block(ending, offset);
  return Array.from(hash, (value) => value.toString(16).padStart(8, '0')).join(
    '',
  );
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
async function verifyIdentity() {
  busy.value = true;
  actionError.value = '';
  try {
    await requestStepUp();
    verifiedAt.value = Date.now();
  } catch (error) {
    actionError.value = reason(error);
  } finally {
    busy.value = false;
  }
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
          <button
            type="button"
            :disabled="busy || checking"
            @click="verifyIdentity"
          >
            {{ t('desktopVerify') }}
          </button>
        </div>
        <p>{{ t(identityFresh ? 'desktopVerified' : 'desktopStepUpHint') }}</p>
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
  font-size: 1.6rem;
  font-weight: 650;
}
h2 {
  font-size: 1.15rem;
  font-weight: 600;
}
h3 {
  margin: 0;
  font-size: 1rem;
}
p {
  margin-block: 0.6rem;
}
section > section {
  margin-top: 2rem;
}
.page-head,
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
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
  padding: 1.25rem;
  background: var(--auriga-surface);
  border: 1px solid var(--auriga-border);
  border-radius: 12px;
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
