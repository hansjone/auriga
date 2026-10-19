import { createI18n } from 'vue-i18n';
export const messages = {
  'zh-CN': {
    portal: '????',
    admin: '???',
    panel: '????',
    welcome: 'Auriga?????????',
    placeholder: '????',
    language: 'English',
    theme: '????',
  },
  en: {
    portal: 'Workspace',
    admin: 'Administration',
    panel: 'Team panel',
    welcome: 'Auriga. A place to work together.',
    placeholder: 'Coming soon',
    language: '??',
    theme: 'Switch theme',
  },
};
export type Locale = keyof typeof messages;
export const createAurigaI18n = (locale: Locale = 'zh-CN') =>
  createI18n({ legacy: false, locale, fallbackLocale: 'en', messages });
