import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { resolve } from 'node:path';

export default defineConfig({
  root: resolve(import.meta.dirname),
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5178,
    strictPort: true,
  },
  resolve: {
    alias: {
      '@settings': resolve(import.meta.dirname, '../src/settings'),
    },
  },
});
