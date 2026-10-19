<script setup lang="ts">
/* eslint-disable no-undef -- WebAuthn DOM types */
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import type { StepInput, WebAuthnStep } from '../generated/steps';

const props = defineProps<{ step: WebAuthnStep }>();
const emit = defineEmits<{ submit: [input: StepInput] }>();
const { t } = useI18n();
const error = ref('');

onMounted(async () => {
  try {
    const creds = globalThis.navigator?.credentials;
    if (!creds) {
      error.value = t('errors.common.unavailable');
      return;
    }
    const publicKey = props.step
      .publicKey as unknown as PublicKeyCredentialRequestOptions &
      PublicKeyCredentialCreationOptions;
    const credential =
      props.step.ceremony === 'create'
        ? await creds.create({ publicKey })
        : await creds.get({ publicKey });
    if (!credential) {
      error.value = t('steps.webauthnCancel');
      return;
    }
    emit('submit', {
      type: 'webauthn',
      credential: credentialToJson(credential as PublicKeyCredential),
    });
  } catch {
    error.value = t('steps.webauthnCancel');
  }
});

function credentialToJson(cred: PublicKeyCredential): Record<string, unknown> {
  const anyCred = cred as PublicKeyCredential & {
    response: AuthenticatorAssertionResponse & AuthenticatorAttestationResponse;
  };
  return {
    id: cred.id,
    type: cred.type,
    rawId: bufferToBase64Url(cred.rawId),
    response: {
      clientDataJSON: bufferToBase64Url(anyCred.response.clientDataJSON),
      attestationObject: anyCred.response.attestationObject
        ? bufferToBase64Url(anyCred.response.attestationObject)
        : undefined,
      authenticatorData: anyCred.response.authenticatorData
        ? bufferToBase64Url(anyCred.response.authenticatorData)
        : undefined,
      signature: anyCred.response.signature
        ? bufferToBase64Url(anyCred.response.signature)
        : undefined,
      userHandle: anyCred.response.userHandle
        ? bufferToBase64Url(anyCred.response.userHandle)
        : undefined,
    },
  };
}

function bufferToBase64Url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let str = '';
  for (const b of bytes) str += String.fromCharCode(b);
  return globalThis
    .btoa(str)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}
</script>

<template>
  <div class="webauthn-step" data-testid="webauthn-step">
    <p>{{ t('steps.webauthnPrompt') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.webauthn-step {
  text-align: center;
  color: var(--auriga-muted);
}
.error {
  color: #d27a7a;
}
</style>
