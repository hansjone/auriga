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
  margin: 0 0 0.5rem;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  font-size: 0.75rem;
  color: var(--auriga-muted);
}
h1 {
  margin: 0 0 0.75rem;
  font-size: 1.25rem;
  font-weight: 600;
  letter-spacing: -0.02em;
  line-height: 1.3;
}
.muted {
  color: var(--auriga-muted);
  font-size: 0.875rem;
  line-height: 1.55;
}
.error {
  color: var(--auriga-danger);
  font-size: 0.875rem;
}
.primary {
  margin-top: 1rem;
  height: 2.5rem;
  padding: 0 1.15rem;
  border: 0;
  border-radius: 10px;
  background: var(--auriga-accent);
  color: #e8eef5;
  font: inherit;
  font-weight: 550;
  cursor: pointer;
}
.primary:hover {
  filter: brightness(1.06);
}
</style>
