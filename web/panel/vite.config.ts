import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';
export default defineConfig({
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  plugins: [vue({ customElement: true })],
  test: { environment: 'jsdom', css: true, clearMocks: true },
  build: {
    lib: {
      entry: 'src/index.ts',
      formats: ['es'],
      fileName: 'auriga-team-panel',
    },
  },
});
