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
    title: '团队的工作，在此汇合。',
    sub: '团队共用的 DSH 云端工作台。连接云端与桌面，共享模型中转站。',
  },
  en: {
    login: 'Sign in',
    home: 'Auriga DSH home',
    title: 'Where your team’s work meets.',
    sub: 'A shared DSH cloud workspace. Connect cloud and desktop with a team model relay.',
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
  background: #071421;
  color: #e8f0f7;
  overflow: clip;
}
.landing-nav {
  box-sizing: border-box;
  width: 100%;
  max-width: 1280px;
  margin: 0 auto;
  padding: 25px 40px;
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.landing-brand {
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
.language {
  border: 0;
  background: transparent;
  color: #a9bfd0;
  font: inherit;
  cursor: pointer;
}
.sign-in {
  color: #e8f0f7;
  text-decoration: none;
  border: 1px solid #365065;
  padding: 9px 23px;
  border-radius: 8px;
}
.hero {
  min-height: 0;
  display: grid;
  align-items: center;
  text-align: center;
  padding: 0 24px 8vh;
}
.hero-content {
  width: 100%;
  max-width: 1000px;
  margin: auto;
}
h1 {
  font-size: clamp(34px, 4.5vw, 60px);
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: -0.045em;
  text-wrap: balance;
  margin: 0 0 25px;
}
p {
  color: #a9bfd0;
  font-size: 17px;
  line-height: 1.8;
  margin: 0 auto 34px;
  max-width: 620px;
}
a:focus-visible,
button:focus-visible {
  outline: 2px solid #8fb9d6;
  outline-offset: 4px;
}
@media (max-width: 600px) {
  .landing-nav {
    padding: 20px;
  }
  .landing-brand {
    gap: 8px;
    font-size: 18px;
  }
  nav {
    gap: 14px;
  }
  .sign-in {
    padding: 8px 16px;
  }
  .hero {
    padding: 0 20px 10vh;
  }
  p {
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
  h1 {
    font-size: 30px;
    margin-bottom: 12px;
  }
  p {
    margin-bottom: 18px;
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
