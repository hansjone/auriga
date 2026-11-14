import config from '../installer/eslint.config.mjs';
export default [
  { ignores: ['**/target/**', '**/node_modules/**'] },
  ...config,
  {
    languageOptions: {
      globals: Object.fromEntries(
        [
          'structuredClone',
          'AbortController',
          'AbortSignal',
          'setInterval',
          'clearInterval',
          'clearTimeout',
          'globalThis',
          'URLSearchParams',
          'FormData',
          'Blob',
          'Response',
          'ReadableStream',
        ].map((name) => [name, 'readonly']),
      ),
    },
  },
  {
    files: ['**/public/*.js'],
    languageOptions: {
      globals: Object.fromEntries(
        [
          'location',
          'history',
          'document',
          'fetch',
          'crypto',
          'btoa',
          'setInterval',
          'Uint8Array',
          'HTMLButtonElement',
        ].map((name) => [name, 'readonly']),
      ),
    },
  },
];
