import { defineConfig } from 'vitest/config';
import path from 'path';

export default defineConfig({
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/__tests__/**/*.{test,spec}.{ts,tsx}'],
    // monaco-editor declares only a `module` entry, which the test resolver
    // does not read. The alias lets a test mock it.
    alias: [
      {
        find: /^monaco-editor$/,
        replacement: 'monaco-editor/esm/vs/editor/editor.api.js',
      },
    ],
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
});
