<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { isApiError, mapProblemMessage } from '@auriga/shared';
import { orgApi } from '../api';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();

const loading = ref(true);
const error = ref('');
const preview = ref<{
  valid: boolean;
  tenantName?: string;
  inviterName?: string;
} | null>(null);

onMounted(async () => {
  const code = String(route.params.code || '');
  try {
    preview.value = await orgApi.previewInvitation(code);
  } catch (e) {
    error.value = isApiError(e)
      ? mapProblemMessage(e.problem, t)
      : t('errors.unknown');
  } finally {
    loading.value = false;
  }
});

function start() {
  void router.push({
    path: '/login',
    query: {
      invitation: String(route.params.code || ''),
      returnTo: '/',
    },
  });
}
</script>

<template>
  <section class="invite-card auriga-glass">
    <p class="brand">{{ t('brand') }}</p>
    <h1>{{ t('invite.title') }}</h1>
    <p v-if="loading" class="muted">{{ t('login.loading') }}</p>
    <p v-else-if="error" class="error" role="alert">{{ error }}</p>
    <template v-else-if="preview?.valid">
      <p class="muted">
        {{
          t('invite.inviter', {
            name: preview.inviterName || '—',
            tenant: preview.tenantName || '—',
          })
        }}
      </p>
      <button type="button" class="primary" @click="start">
        {{ t('invite.start') }}
      </button>
    </template>
    <p v-else class="error">{{ t('invite.invalid') }}</p>
  </section>
</template>

<style scoped>
.invite-card {
  width: min(380px, 100%);
  text-align: center;
}
.brand {
  letter-spacing: 0.2em;
  text-transform: uppercase;
  font-size: 0.75rem;
}
h1 {
  font-size: 1.35rem;
  font-weight: 600;
}
.muted {
  color: var(--auriga-muted);
}
.error {
  color: #d27a7a;
}
.primary {
  margin-top: 1rem;
  height: 2.75rem;
  padding: 0 1.25rem;
  border: 0;
  border-radius: 14px;
  background: var(--auriga-accent);
  color: #dbe4ee;
  font: inherit;
  cursor: pointer;
}
</style>
