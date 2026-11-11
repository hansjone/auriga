<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, shallowRef } from 'vue';
import { ElButton } from 'element-plus';
import { isApiError } from '@auriga/shared';
import { securityApi, type SecurityEntries } from '../../admin/securityApi';
import { adminApi } from '../../admin/api';
import { useAdminI18n } from '../../admin/messages';
import '../../admin/management.css';
const { t } = useAdminI18n();
const entries = shallowRef<SecurityEntries>();
const loading = ref(false),
  error = ref('');
let alive = true;
onBeforeUnmount(() => {
  alive = false;
});
async function load() {
  if (loading.value) return;
  loading.value = true;
  error.value = '';
  entries.value = undefined;
  try {
    const membership = await adminApi.membership();
    if (!alive) return;
    if (!membership.permissions.includes('org.policies.read')) {
      error.value = t('forbidden');
      return;
    }
    const result = await securityApi.entries();
    if (alive) entries.value = result;
  } catch (failure) {
    if (alive)
      error.value = t(
        isApiError(failure) && failure.status === 403
          ? 'forbidden'
          : 'unavailable',
      );
  } finally {
    if (alive) loading.value = false;
  }
}
onMounted(load);
</script>
<template>
  <section class="management">
    <div class="head">
      <h1>{{ t('securityEntries') }}</h1>
      <ElButton :loading="loading" @click="load">{{ t('refresh') }}</ElButton>
    </div>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
    <div v-if="entries" class="details">
      <h2>{{ t('securityConfigured') }}</h2>
      <p class="muted">{{ t('securityEntryHint') }}</p>
      <dl>
        <dt>{{ t('securityPortal') }}</dt>
        <dd>{{ entries.portalOrigin }}</dd>
        <dt>{{ t('securityAdmin') }}</dt>
        <dd>{{ entries.adminOrigin }}</dd>
        <dt>{{ t('securityTransport') }}</dt>
        <dd>
          {{
            entries.portalOrigin.startsWith('https:')
              ? t('securityHttps')
              : t('securityHttp')
          }}
          /
          {{
            entries.adminOrigin.startsWith('https:')
              ? t('securityHttps')
              : t('securityHttp')
          }}
        </dd>
        <dt>{{ t('securityInstances') }}</dt>
        <dd>
          {{
            entries.mode === 'ip' ? t('securityPath') : t('securitySubdomain')
          }}
          {{ entries.instanceParentDomain }}
        </dd>
        <dt>{{ t('securityRelay') }}</dt>
        <dd>{{ entries.relayPublicUrl ?? t('securityRelayHint') }}</dd>
      </dl>
    </div>
  </section>
</template>
