<script setup lang="ts">
import { BrandMark } from '@auriga/shared';
import { useI18n } from 'vue-i18n';
import DesktopDownload from '../components/DesktopDownload.vue';
const { locale } = useI18n();
const copy = {
  'zh-CN': {
    login: '登录',
    title: '团队的工作，在此汇合。',
    sub: '团队共用的 DSH 云端工作台。连接云端与桌面，共享模型中转站。',
    preview: '工作台界面示意',
    workspace: '团队工作台',
    cloud: '云端实例',
    ready: '就绪',
    open: '进入工作台',
    model: '团队模型中转站',
    shared: '团队统一配置',
    desktop: '桌面与云端',
    connected: '同一账号，随处接续',
    cards: [
      [
        '一个团队，一处工作台',
        '从团队账号进入自己的云端实例，工作环境触手可及。',
      ],
      ['云端与桌面互通', '在 Windows 桌面或浏览器中使用 DSH，按场景选择。'],
      ['共用模型中转站', '由团队统一配置模型服务，成员专注手头的工作。'],
    ],
  },
  en: {
    login: 'Sign in',
    title: 'Where your team’s work meets.',
    sub: 'A shared DSH cloud workspace. Connect cloud and desktop with a team model relay.',
    preview: 'Workspace illustration',
    workspace: 'Team workspace',
    cloud: 'Cloud instance',
    ready: 'Ready',
    open: 'Open workspace',
    model: 'Team model relay',
    shared: 'Managed by your team',
    desktop: 'Desktop and cloud',
    connected: 'One account, connected work',
    cards: [
      [
        'One team, one workspace',
        'Sign in with your team account to access your cloud instance.',
      ],
      ['Cloud meets desktop', 'Use DSH on Windows or in your browser.'],
      [
        'A shared model relay',
        'Your team configures model services so you can focus on your work.',
      ],
    ],
  },
};
</script>

<template>
  <div class="landing">
    <header class="landing-nav">
      <a class="landing-brand" href="/" aria-label="Auriga DSH 首页"
        ><BrandMark :size="36" /><span>Auriga DSH</span></a
      >
      <nav>
        <button
          class="language"
          @click="locale = locale === 'en' ? 'zh-CN' : 'en'"
        >
          {{ locale === 'en' ? '中文' : 'EN' }}</button
        ><a class="sign-in" href="/login">{{
          copy[locale === 'en' ? 'en' : 'zh-CN'].login
        }}</a>
      </nav>
    </header>
    <div class="landing-body" :lang="locale === 'en' ? 'en' : 'zh-CN'">
      <section class="hero">
        <h1>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].title }}</h1>
        <p>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].sub }}</p>
        <DesktopDownload />
      </section>
      <figure
        class="product-preview"
        :aria-label="copy[locale === 'en' ? 'en' : 'zh-CN'].preview"
      >
        <div class="preview-bar">
          <span class="preview-dots" aria-hidden="true">● ● ●</span
          ><span>Auriga DSH</span
          ><span>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].preview }}</span>
        </div>
        <div class="preview-layout">
          <aside>
            <BrandMark :size="34" /><strong>DSH</strong
            ><span class="selected">{{
              copy[locale === 'en' ? 'en' : 'zh-CN'].workspace
            }}</span
            ><span>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].model }}</span>
          </aside>
          <div class="preview-main">
            <h2>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].workspace }}</h2>
            <div class="preview-instance">
              <div>
                <span class="ready">{{
                  copy[locale === 'en' ? 'en' : 'zh-CN'].ready
                }}</span>
                <h3>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].cloud }}</h3>
                <p>{{ copy[locale === 'en' ? 'en' : 'zh-CN'].connected }}</p>
              </div>
              <span class="preview-open">{{
                copy[locale === 'en' ? 'en' : 'zh-CN'].open
              }}</span>
            </div>
            <div class="preview-services">
              <div>
                {{ copy[locale === 'en' ? 'en' : 'zh-CN'].model
                }}<small>{{
                  copy[locale === 'en' ? 'en' : 'zh-CN'].shared
                }}</small>
              </div>
              <div>
                {{ copy[locale === 'en' ? 'en' : 'zh-CN'].desktop
                }}<small>Windows / Web</small>
              </div>
            </div>
          </div>
        </div>
      </figure>
      <section class="capabilities">
        <article
          v-for="card in copy[locale === 'en' ? 'en' : 'zh-CN'].cards"
          :key="card[0]"
        >
          <h2>{{ card[0] }}</h2>
          <p>{{ card[1] }}</p>
        </article>
      </section>
      <footer><BrandMark :size="20" /> Auriga DSH</footer>
    </div>
  </div>
</template>

<style scoped>
.landing {
  width: 100%;
  min-height: 100vh;
  background: #071421;
  color: #e8f0f7;
}
.landing-nav {
  max-width: 1200px;
  margin: auto;
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
.landing-body {
  max-width: 1080px;
  padding: 0 40px;
  margin: auto;
}
.hero {
  text-align: center;
  padding: 78px 0 60px;
}
h1 {
  font-size: clamp(34px, 4.5vw, 60px);
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: -0.045em;
  text-wrap: balance;
  margin: 0 0 25px;
}
.hero > p {
  color: #a9bfd0;
  font-size: 17px;
  line-height: 1.8;
  margin: 0 auto 34px;
  max-width: 620px;
}
.product-preview {
  margin: 0;
  border: 1px solid #304658;
  border-radius: 14px;
  overflow: hidden;
  background: #0c1d2c;
}
.preview-bar {
  padding: 15px 22px;
  border-bottom: 1px solid #253d50;
  display: flex;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  color: #91a8bc;
}
.preview-dots {
  letter-spacing: 4px;
  color: #456278;
}
.preview-layout {
  display: grid;
  grid-template-columns: 205px 1fr;
  min-height: 320px;
}
aside {
  border-right: 1px solid #253d50;
  padding: 30px 18px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 17px;
  font-size: 13px;
  color: #91a8bc;
}
aside strong {
  color: #e8f0f7;
  margin-bottom: 10px;
}
aside span {
  padding: 10px;
}
.selected {
  background: #19354b;
  border-radius: 6px;
  width: calc(100% - 20px);
  color: #e8f0f7;
}
.preview-main {
  padding: 30px;
  min-width: 0;
}
.preview-main h2 {
  font-size: 20px;
  margin: 0 0 23px;
  font-weight: 500;
}
.preview-instance {
  background: #122b3f;
  border: 1px solid #304b61;
  border-radius: 10px;
  padding: 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.ready {
  color: #a4c8b7;
  font-size: 12px;
}
h3 {
  margin: 10px 0;
  font-size: 17px;
  font-weight: 500;
}
.preview-instance p {
  margin: 0;
  color: #91a8bc;
  font-size: 13px;
}
.preview-open {
  background: #356586;
  padding: 12px 18px;
  border-radius: 6px;
  font-size: 13px;
  white-space: nowrap;
}
.preview-services {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 18px;
  margin-top: 18px;
  font-size: 13px;
}
.preview-services > div {
  padding: 16px;
  border: 1px solid #253d50;
  border-radius: 8px;
}
small {
  display: block;
  margin-top: 9px;
  color: #91a8bc;
}
.capabilities {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 40px;
  margin: 55px 0 65px;
}
.capabilities h2 {
  font-size: 17px;
  font-weight: 500;
}
.capabilities p {
  color: #91a8bc;
  font-size: 14px;
  line-height: 1.8;
}
footer {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 25px 0;
  border-top: 1px solid #253d50;
  color: #91a8bc;
  font-size: 13px;
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
  .landing-body {
    padding: 0 20px;
  }
  .hero {
    padding: 52px 0 38px;
  }
  .hero > p {
    font-size: 15px;
  }
  .preview-layout {
    grid-template-columns: 1fr;
    min-height: auto;
  }
  aside {
    display: none;
  }
  .preview-main {
    padding: 20px 14px;
  }
  .preview-bar {
    padding: 14px;
    font-size: 10px;
  }
  .preview-instance {
    padding: 16px;
    flex-wrap: wrap;
  }
  .preview-services {
    gap: 8px;
  }
  .preview-services > div {
    padding: 12px;
  }
  .capabilities {
    grid-template-columns: 1fr;
    gap: 12px;
    margin: 35px 0;
  }
}
</style>
