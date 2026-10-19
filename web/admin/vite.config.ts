import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

const sharedSrc = fileURLToPath(new URL('../shared/src', import.meta.url));

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: [
      {
        find: '@auriga/shared/theme.css',
        replacement: `${sharedSrc}/theme.css`,
      },
      {
        find: /^@auriga\/shared$/,
        replacement: `${sharedSrc}/index.ts`,
      },
    ],
  },
  test: {
    environment: 'jsdom',
    clearMocks: true,
    server: { deps: { inline: [/@auriga\/shared/] } },
  },
});
