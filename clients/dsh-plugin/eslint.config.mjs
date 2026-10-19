import js from '@eslint/js';

/** Node-oriented flat config for the standalone plugin package. */
export default [
  { ignores: ['node_modules/**', 'src/generated/**'] },
  js.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: 'module',
      globals: {
        AbortController: 'readonly',
        Buffer: 'readonly',
        RequestInit: 'readonly',
        Response: 'readonly',
        TextEncoder: 'readonly',
        URL: 'readonly',
        URLSearchParams: 'readonly',
        clearTimeout: 'readonly',
        console: 'readonly',
        fetch: 'readonly',
        process: 'readonly',
        setTimeout: 'readonly',
      },
    },
    rules: {
      'no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
    },
  },
  {
    files: ['src/client.js'],
    languageOptions: {
      globals: {
        window: 'readonly',
        document: 'readonly',
        customElements: 'readonly',
        fetch: 'readonly',
        setTimeout: 'readonly',
        console: 'readonly',
        URL: 'readonly',
      },
    },
  },
];
