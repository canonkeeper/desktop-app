import js from '@eslint/js';
import pluginQuery from '@tanstack/eslint-plugin-query';
import pluginRouter from '@tanstack/eslint-plugin-router';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default [
  {
    ignores: [
      'dist/**',
      'dist-desktop/**',
      'dist-desktop-backend/**',
      // The desktop web build the backend embeds (npm run build:desktop:backend).
      'backend/internal/app/desktop/web/**',
      // The build of the desktop shell and the set of its installers (desktop/README.md).
      'desktop/src-tauri/target/**',
      'desktop/src-tauri/gen/**',
      'desktop/bundle/**',
      'coverage/**',
      'node_modules/**',
      '.vite/**',
      '.magicpath/**',
      '.serena/**',
      '.supergoal/**',
      '.local-test/**',
      '.lab/**',
      '**/.worktrees/**',
      '**/.claude/worktrees/**',
    ],
  },
  js.configs.recommended,
  ...pluginQuery.configs['flat/recommended'],
  ...pluginRouter.configs['flat/recommended'],
  ...tseslint.configs.recommended,
  {
    files: ['*.config.{js,ts}', 'scripts/**/*.mjs', 'mock-server/**/*.ts', 'e2e/voice-exam/**/*.mjs'],
    languageOptions: {
      globals: {
        ...globals.node,
        ...globals.es2022,
      },
    },
  },
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.es2022,
      },
      parserOptions: {
        ecmaFeatures: {
          jsx: true,
        },
      },
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      'no-console': 'error',
    },
  },
  {
    files: ['src/shared/api/generated/**'],
    rules: {
      '@typescript-eslint/no-empty-object-type': 'off',
    },
  },
];
