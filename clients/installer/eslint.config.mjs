import js from '@eslint/js';

/** Node-oriented flat config for the installer scripts. */
export default [
  { ignores: ['node_modules/**', 'target/**', 'locks/**'] },
  js.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: 'module',
      globals: Object.fromEntries(
        [
          'console',
          'process',
          'URL',
          'URLSearchParams',
          'Buffer',
          'setTimeout',
          'clearTimeout',
          'setInterval',
          'clearInterval',
          'setImmediate',
          'AbortController',
          'AbortSignal',
          'fetch',
          'FormData',
          'Blob',
          'Response',
          'ReadableStream',
          'TextDecoder',
          'TextEncoder',
          'Uint8Array',
          'globalThis',
          'structuredClone',
        ].map((name) => [name, 'readonly']),
      ),
    },
  },
];
