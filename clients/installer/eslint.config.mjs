import js from '@eslint/js';

/** Node-oriented flat config for the installer scripts. */
export default [
  { ignores: ['node_modules/**', 'target/**', 'locks/**'] },
  js.configs.recommended,
  {
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: 'module',
      globals: {
        console: 'readonly',
        process: 'readonly',
        URL: 'readonly',
        Buffer: 'readonly',
        setTimeout: 'readonly',
      },
    },
  },
];
