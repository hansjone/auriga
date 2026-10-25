<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue';
import { ElButton, ElInput, ElInputNumber } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { adminApi } from '../../admin/api';
import {
  nodesApi,
  type JoinToken,
  type JoinMaterial,
} from '../../admin/nodesApi';
import ActionConfirm from '../../admin/ActionConfirm.vue';
import { useNodesI18n } from './messages';
import { nodeProgress } from './state';

const props = defineProps<{
  canWrite: boolean;
  refreshKey: number;
  locked?: boolean;
}>();
const { t, failure } = useNodesI18n();
const tokens = shallowRef<JoinToken[]>([]);
const material = shallowRef<JoinMaterial>();
const pending = shallowRef<JoinToken>();
const name = ref('');
const ttl = ref(60);
const busy = ref(false);
const loading = ref(false);
const error = ref('');
const notice = ref('');
const denied = ref(false);
const enabled = computed(() => props.canWrite && !denied.value);
const locked = computed(
  () => !!props.locked || busy.value || loading.value || !!pending.value,
);
let alive = true;
let epoch = 0;
let listVersion = 0;

function clear() {
  material.value = undefined;
  notice.value = '';
}
function reject(e: unknown) {
  error.value = failure(e);
  if (isApiError(e) && (e.status === 401 || e.status === 403)) {
    denied.value = true;
    clear();
    tokens.value = [];
    pending.value = undefined;
  }
}
async function load() {
  if (!enabled.value || busy.value || pending.value) return;
  const version = ++listVersion;
  const generation = epoch;
  loading.value = true;
  error.value = '';
  try {
    const result = await nodesApi.tokens();
    if (alive && generation === epoch && version === listVersion) {
      // Keep only public metadata, even if a server sends additional fields.
      tokens.value = result.items.map(
        ({ id, name, expiresAt, createdBy, createdAt }) => ({
          id,
          name,
          expiresAt,
          createdBy,
          createdAt,
        }),
      );
    }
  } catch (e) {
    if (alive && generation === epoch && version === listVersion) {
      tokens.value = [];
      reject(e);
    }
  } finally {
    if (alive && generation === epoch && version === listVersion)
      loading.value = false;
  }
}
async function authorized() {
  const membership = await adminApi.membership();
  if (!alive || !enabled.value) return false;
  if (!membership.permissions.includes('workspace.nodes.admin')) {
    denied.value = true;
    clear();
    tokens.value = [];
    pending.value = undefined;
    error.value = t('forbidden');
    return false;
  }
  return true;
}
async function create() {
  if (
    locked.value ||
    !enabled.value ||
    material.value ||
    !Number.isInteger(ttl.value) ||
    ttl.value < 5 ||
    ttl.value > 1440 ||
    name.value.length > 64
  )
    return;
  const generation = epoch;
  busy.value = true;
  error.value = '';
  notice.value = '';
  let created = false;
  try {
    if (!(await authorized()) || generation !== epoch) return;
    const result = await nodesApi.create({
      name: name.value.trim() || undefined,
      ttlMinutes: ttl.value,
    });
    if (alive && generation === epoch && enabled.value) {
      material.value = result;
      created = true;
    }
  } catch (e) {
    if (alive && generation === epoch) reject(e);
  } finally {
    if (alive && generation === epoch) {
      busy.value = false;
      if (created) void load();
    }
  }
}
async function revoke() {
  const target = pending.value;
  if (!target || busy.value || !enabled.value) return;
  const generation = epoch;
  busy.value = true;
  error.value = '';
  try {
    if (!(await authorized()) || generation !== epoch) return;
    await nodesApi.revokeToken(target.id);
    if (alive && generation === epoch) {
      if (material.value?.id === target.id) clear();
      tokens.value = tokens.value.filter((token) => token.id !== target.id);
    }
  } catch (e) {
    if (alive && generation === epoch) reject(e);
  } finally {
    if (alive && generation === epoch) {
      busy.value = false;
      pending.value = undefined;
    }
  }
}
async function copy() {
  const current = material.value;
  if (!current) return;
  try {
    await globalThis.navigator.clipboard.writeText(current.command);
    if (alive && material.value === current) notice.value = t('copied');
  } catch {
    if (alive && material.value === current) notice.value = t('copyFailed');
  }
}
watch(
  () => props.canWrite,
  (value) => {
    ++epoch;
    ++listVersion;
    clear();
    tokens.value = [];
    pending.value = undefined;
    busy.value = false;
    loading.value = false;
    denied.value = false;
    if (value) void load();
  },
  { immediate: true },
);
watch(
  () => props.refreshKey,
  () => {
    void load();
  },
);
onBeforeUnmount(() => {
  alive = false;
  ++epoch;
  ++listVersion;
  clear();
});
</script>
<template>
  <section v-if="canWrite" :aria-label="t('materials')">
    <h2>{{ t('materials') }}</h2>
    <form @submit.prevent="create">
      <label
        >{{ t('name')
        }}<ElInput
          v-model="name"
          maxlength="64"
          :disabled="locked || !enabled"
          :aria-label="t('name')"
      /></label>
      <label
        >{{ t('ttl')
        }}<ElInputNumber
          v-model="ttl"
          :min="5"
          :max="1440"
          :precision="0"
          :disabled="locked || !enabled"
          :aria-label="t('ttl')"
      /></label>
      <ElButton
        native-type="submit"
        :disabled="locked || !enabled || !!material"
        >{{ t('create') }}</ElButton
      >
      <ElButton :disabled="locked || !enabled" @click="load">{{
        t('refresh')
      }}</ElButton>
    </form>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <section v-if="material" :aria-label="t('once')">
      <p>{{ t('once') }}</p>
      <dl>
        <dt>{{ t('token') }}</dt>
        <dd>
          <code>{{ material.token }}</code>
        </dd>
        <dt>{{ t('command') }}</dt>
        <dd>
          <code>{{ material.command }}</code>
        </dd>
        <dt>{{ t('ca') }}</dt>
        <dd>
          <code>{{ material.caSha256 }}</code>
        </dd>
      </dl>
      <ElButton @click="copy">{{ t('copy') }}</ElButton>
      <ElButton @click="clear">{{ t('clear') }}</ElButton>
    </section>
    <h3>{{ t('unused') }}</h3>
    <p>{{ t('progress') }}</p>
    <p v-if="!tokens.length && !loading && !error">{{ t('empty') }}</p>
    <ul>
      <li v-for="token in tokens" :key="token.id">
        {{ token.name || token.id }} · {{ t(nodeProgress()) }} ·
        {{ t('expires') }}: {{ token.expiresAt }}
        <ElButton :disabled="locked || !enabled" @click="pending = token">{{
          t('revoke')
        }}</ElButton>
      </li>
    </ul>
    <ActionConfirm
      :message="pending ? t('confirmRevoke') : undefined"
      :busy="busy"
      @confirm="revoke"
      @cancel="!busy && (pending = undefined)"
    />
  </section>
</template>
