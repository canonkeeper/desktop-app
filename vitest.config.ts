import { availableParallelism } from 'node:os';
import { configDefaults, defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { editionAlias } from './vite.config';

export default defineConfig({
  plugins: [react()],
  resolve: { alias: editionAlias() },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    css: true,
    pool: 'threads',
    // Every core, at most 8: a jsdom worker holds about 200 MB and more workers barely help.
    maxWorkers: Math.min(availableParallelism(), 8),
    exclude: [...configDefaults.exclude, '**/.worktrees/**', '**/.local-test/**', '**/.local-production/**', 'e2e/**', 'scripts/**/*.test.mjs'],
  },
});
