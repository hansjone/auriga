import config from '../installer/eslint.config.mjs';
export default [
  ...config,
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
