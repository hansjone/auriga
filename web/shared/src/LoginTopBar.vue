<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import BrandMark from './BrandMark.vue';
const props = withDefaults(defineProps<{ fallback?: string }>(), {
  fallback: '/',
});
const { locale } = useI18n();
function language() {
  locale.value = locale.value === 'en' ? 'zh-CN' : 'en';
  document.documentElement.lang = locale.value;
}
function back() {
  let sameOrigin = false;
  try {
    sameOrigin =
      !!document.referrer &&
      new URL(document.referrer).origin === globalThis.location.origin;
  } catch {
    sameOrigin = false;
  }
  if (sameOrigin && globalThis.history.length > 1) globalThis.history.back();
  else globalThis.location.assign(props.fallback);
}
</script>
<template>
  <header class="auriga-login-topbar">
    <a class="brand" href="/" aria-label="Auriga DSH"
      ><BrandMark :size="36" /><span>Auriga DSH</span></a
    >
    <nav>
      <button type="button" class="language" @click="language">
        {{ locale === 'en' ? '中文' : 'EN' }}</button
      ><button type="button" class="back" @click="back">
        {{ locale === 'en' ? 'Back' : '返回' }}
      </button>
    </nav>
  </header>
</template>
<style scoped>
.auriga-login-topbar {
  position: fixed;
  inset: 0 0 auto;
  z-index: 10;
  box-sizing: border-box;
  width: 100%;
  max-width: 1120px;
  margin: 0 auto;
  padding: 22px 32px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: #e8f0f7;
}
.brand {
  display: flex;
  gap: 11px;
  align-items: center;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.03em;
  color: inherit;
  text-decoration: none;
}
nav {
  display: flex;
  align-items: center;
  gap: 20px;
}
button {
  font: inherit;
  cursor: pointer;
}
.language {
  border: 0;
  background: transparent;
  color: #a9bfd0;
  font-size: 14px;
}
.language:hover {
  color: #e8f0f7;
}
.back {
  color: #e8f0f7;
  background: rgba(14, 30, 46, 0.35);
  border: 1px solid rgba(150, 182, 208, 0.28);
  border-radius: 8px;
  padding: 8px 18px;
  font-size: 14px;
  font-weight: 500;
}
a:focus-visible,
button:focus-visible {
  outline: 2px solid #8fb9d6;
  outline-offset: 4px;
}
@media (max-width: 600px) {
  .auriga-login-topbar {
    padding: 20px;
  }
  .brand {
    gap: 8px;
    font-size: 18px;
  }
  nav {
    gap: 14px;
  }
  .back {
    padding: 8px 16px;
  }
}
@media (max-height: 550px) {
  .auriga-login-topbar {
    padding-block: 12px;
  }
}
</style>
