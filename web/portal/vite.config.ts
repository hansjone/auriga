import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';
export default defineConfig({
  plugins: [vue()],
  build: {
    rollupOptions: {
      input: {
        index: 'index.html',
        instanceLogin: 'instance-login.html',
      },
    },
  },
  test: { environment: 'jsdom', clearMocks: true },
});
