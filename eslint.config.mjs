import js from '@eslint/js';
import ts from 'typescript-eslint';
import vue from 'eslint-plugin-vue';
import prettier from 'eslint-config-prettier';
export default ts.config(
  {
    ignores: [
      '**/dist/**',
      '**/node_modules/**',
      '**/generated/**',
      '**/coverage/**',
    ],
  },
  js.configs.recommended,
  ...ts.configs.recommended,
  ...vue.configs['flat/recommended'],
  {
    files: ['**/*.vue'],
    languageOptions: { parserOptions: { parser: ts.parser } },
  },
  {
    languageOptions: {
      globals: {
        process: 'readonly',
        URL: 'readonly',
        setTimeout: 'readonly',
        console: 'readonly',
        document: 'readonly',
        customElements: 'readonly',
        RequestInit: 'readonly',
        Response: 'readonly',
      },
    },
    rules: { 'vue/multi-word-component-names': 'off' },
  },
  prettier,
);
