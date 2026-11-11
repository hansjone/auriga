<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue';
import { ElButton, ElTable, ElTableColumn } from 'element-plus';
import type { Instance } from '../../admin/api';
import { adminApi } from '../../admin/api';
import { runtimeApi, type RuntimeCatalog } from '../../admin/runtimeApi';
import { useAdminI18n } from '../../admin/messages';
import { orgError } from '../../admin/useOrgActions';
import '../../admin/management.css';
const { t, state } = useAdminI18n();
const catalog = shallowRef<RuntimeCatalog>();
const instances = shallowRef<Instance[]>([]);
const cursor = ref<string>();
const loading = ref(false);
const error = ref('');
const instanceError = ref('');
const canInstances = ref(false);
let alive = true;
async function load(next = false) {
  if (loading.value) return;
  loading.value = true;
  error.value = '';
  instanceError.value = '';
  if (!next) {
    catalog.value = undefined;
    instances.value = [];
    cursor.value = undefined;
  }
  try {
    const { permissions } = await adminApi.membership();
    if (!alive) return;
    canInstances.value = permissions.includes('workspace.instances.read');
    if (!permissions.includes('workspace.runtimes.read')) {
      catalog.value = undefined;
      error.value = t('forbidden');
    } else if (!next) {
      try {
        const result = await runtimeApi.runtimes();
        if (alive) catalog.value = result;
      } catch (e) {
        if (alive) error.value = orgError(e, t);
      }
    }
    if (!canInstances.value) {
      instances.value = [];
      cursor.value = undefined;
      instanceError.value = t('forbidden');
    } else {
      try {
        const result = await adminApi.instances(
          next ? cursor.value : undefined,
        );
        if (alive) {
          instances.value = result.items;
          cursor.value = result.nextCursor;
        }
      } catch (e) {
        if (alive) {
          instances.value = [];
          cursor.value = undefined;
          instanceError.value = orgError(e, t);
        }
      }
    }
  } catch (e) {
    if (alive) {
      catalog.value = undefined;
      instances.value = [];
      cursor.value = undefined;
      error.value = orgError(e, t);
    }
  } finally {
    if (alive) loading.value = false;
  }
}
onMounted(() => load());
onBeforeUnmount(() => {
  alive = false;
});
</script>
<template>
  <section class="management">
    <div class="head">
      <h1>{{ t('opsUpgrades') }}</h1>
      <ElButton :disabled="loading" @click="load()">{{
        t('refresh')
      }}</ElButton>
    </div>
    <p class="muted">{{ t('runtimeScope') }}</p>
    <p role="status">{{ t('runtimeMissing') }}</p>
    <p v-if="loading" role="status">{{ t('loading') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <section v-if="catalog" class="details">
      <h2>{{ t('runtimeCatalog') }}</h2>
      <p>{{ t('runtimeDefault') }}: {{ catalog.defaultVersion }}</p>
      <ElTable :data="catalog.items" :empty-text="t('empty')">
        <ElTableColumn prop="version" :label="t('version')" />
        <ElTableColumn prop="instances" :label="t('runtimeInstances')" />
        <ElTableColumn :label="t('runtimeImages')"
          ><template #default="{ row }"
            ><p v-for="artifact in row.artifacts" :key="artifact.artifactId">
              {{ artifact.os }} / {{ artifact.arch }} ·
              {{ artifact.image || artifact.digest }}
            </p></template
          ></ElTableColumn
        >
      </ElTable>
    </section>
    <section class="details">
      <h2>{{ t('instances') }}</h2>
      <p v-if="instanceError" role="alert" class="error">{{ instanceError }}</p>
      <ElTable v-if="canInstances" :data="instances" :empty-text="t('empty')">
        <ElTableColumn prop="name" :label="t('name')" /><ElTableColumn
          prop="dshVersion"
          :label="t('version')"
        />
        <ElTableColumn :label="t('generation')"
          ><template #default="{ row }"
            >{{ row.generation }} / {{ row.observedGeneration }}</template
          ></ElTableColumn
        >
        <ElTableColumn :label="t('status')"
          ><template #default="{ row }"
            >{{ state(row.phase)
            }}<span v-if="row.pendingRestart">
              · {{ t('pendingRestart') }}</span
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <ElButton v-if="cursor" :disabled="loading" @click="load(true)">{{
        t('next')
      }}</ElButton>
    </section>
  </section>
</template>
