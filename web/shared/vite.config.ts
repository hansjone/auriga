import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';
export default defineConfig({
  plugins: [vue()],
  test: { environment: 'jsdom', clearMocks: true },
  build: {
    lib: { entry: 'src/index.ts', formats: ['es'], fileName: 'shared' },
    rollupOptions: { external: ['vue', 'vue-i18n'] },
  },
});
