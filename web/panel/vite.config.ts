import { defineConfig } from 'vitest/config';
export default defineConfig({
  test: { environment: 'jsdom', css: true, clearMocks: true },
  build: {
    lib: {
      entry: 'src/index.ts',
      formats: ['es'],
      fileName: 'auriga-team-panel',
    },
  },
});
