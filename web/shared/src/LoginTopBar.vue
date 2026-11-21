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
  max-width: 1280px;
  margin: 0 auto;
  padding: 25px 40px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: #e8f0f7;
}
.brand {
  display: flex;
  gap: 12px;
  align-items: center;
  font-size: 21px;
  font-weight: 650;
  color: inherit;
  text-decoration: none;
}
nav {
  display: flex;
  align-items: center;
  gap: 26px;
}
button {
  font: inherit;
  cursor: pointer;
}
.language {
  border: 0;
  background: transparent;
  color: #a9bfd0;
}
.back {
  color: #e8f0f7;
  background: transparent;
  border: 1px solid #365065;
  padding: 9px 23px;
  border-radius: 8px;
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
