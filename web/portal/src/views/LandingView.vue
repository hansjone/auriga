<script setup lang="ts">
import { computed } from 'vue';
import { BrandMark } from '@auriga/shared';
import { useI18n } from 'vue-i18n';
import DesktopDownload from '../components/DesktopDownload.vue';
const { locale } = useI18n();
const messages = {
  'zh-CN': {
    login: '登录',
    home: 'Auriga DSH 首页',
    title: '一个团队，一套 AI 工作台。',
    sub: '云端与桌面的 DSH 共用同一身份、模型与资源，由团队统一管理。',
  },
  en: {
    login: 'Sign in',
    home: 'Auriga DSH home',
    title: 'One team, one AI workspace.',
    sub: 'Cloud and desktop DSH share one identity, models and resources, managed by your team.',
  },
};
const copy = computed(() => messages[locale.value === 'en' ? 'en' : 'zh-CN']);
</script>
<template>
  <div class="landing">
    <header class="landing-nav">
      <a class="landing-brand" href="/" :aria-label="copy.home"
        ><BrandMark :size="36" /><span>Auriga DSH</span></a
      >
      <nav>
        <button
          class="language"
          @click="locale = locale === 'en' ? 'zh-CN' : 'en'"
        >
          {{ locale === 'en' ? '中文' : 'EN' }}</button
        ><a class="sign-in" href="/login">{{ copy.login }}</a>
      </nav>
    </header>
    <section class="hero" :lang="locale === 'en' ? 'en' : 'zh-CN'">
      <div class="hero-content">
        <p class="brand-line">Auriga DSH</p>
        <h1>{{ copy.title }}</h1>
        <p>{{ copy.sub }}</p>
        <DesktopDownload />
      </div>
    </section>
  </div>
</template>
<style scoped>
.landing {
  width: 100%;
  height: 100vh;
  height: 100dvh;
  display: grid;
  grid-template-rows: auto 1fr;
  background:
    radial-gradient(72% 48% at 50% -8%, #12314a 0%, rgba(18, 49, 74, 0) 70%),
    linear-gradient(180deg, #071421 0%, #040b13 100%);
  color: #e8f0f7;
  overflow: clip;
}
.landing-nav {
  box-sizing: border-box;
  width: 100%;
  max-width: 1120px;
  margin: 0 auto;
  padding: 22px 32px;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.landing-brand {
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
.language {
  border: 0;
  background: transparent;
  color: #a9bfd0;
  font: inherit;
  font-size: 14px;
  cursor: pointer;
}
.language:hover {
  color: #e8f0f7;
}
.sign-in {
  color: #e8f0f7;
  text-decoration: none;
  border: 1px solid rgba(150, 182, 208, 0.28);
  background: rgba(14, 30, 46, 0.35);
  padding: 8px 18px;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 500;
}
.sign-in:hover {
  border-color: rgba(150, 182, 208, 0.45);
  background: rgba(20, 42, 62, 0.5);
}
.hero {
  min-height: 0;
  display: grid;
  align-items: center;
  text-align: center;
  padding: 0 24px 7vh;
}
.hero-content {
  width: 100%;
  max-width: 920px;
  margin: auto;
}
.brand-line {
  margin: 0 auto 14px;
  color: #9cc0dc;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}
h1 {
  font-size: clamp(32px, 4.2vw, 52px);
  font-weight: 600;
  line-height: 1.22;
  letter-spacing: -0.04em;
  text-wrap: balance;
  margin: 0 0 18px;
  color: #f0f5fa;
}
p:not(.brand-line) {
  color: #a9bfd0;
  font-size: 16px;
  line-height: 1.7;
  margin: 0 auto 28px;
  max-width: 560px;
}
a:focus-visible,
button:focus-visible {
  outline: 2px solid #8fb9d6;
  outline-offset: 4px;
}
@media (max-width: 600px) {
  .landing-nav {
    padding: 18px 20px;
  }
  .landing-brand {
    gap: 8px;
    font-size: 17px;
  }
  nav {
    gap: 12px;
  }
  .sign-in {
    padding: 7px 14px;
  }
  .hero {
    padding: 0 20px 8vh;
  }
  p:not(.brand-line) {
    font-size: 15px;
  }
}
@media (max-height: 550px) {
  .landing-nav {
    padding-block: 12px;
  }
  .hero {
    padding-bottom: 16px;
  }
  .brand-line {
    margin-bottom: 8px;
  }
  h1 {
    font-size: 28px;
    margin-bottom: 10px;
  }
  p:not(.brand-line) {
    margin-bottom: 16px;
    font-size: 14px;
  }
}
@media (max-height: 700px) {
  .hero :deep(.release-panel) {
    position: fixed;
    top: auto;
    bottom: 12px;
    max-height: calc(100dvh - 100px);
    overflow-y: auto;
  }
}
</style>
