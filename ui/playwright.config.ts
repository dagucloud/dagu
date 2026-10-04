// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';

const defaultServerPort = '32180';
const defaultRemoteServerPort = '32182';
const repoRoot = path.resolve(__dirname, '..');
const defaultStateDir = path.resolve(__dirname, 'test-results/e2e-stack');
const defaultBinPath = path.resolve(repoRoot, '.local/bin/dagu-e2e');

function resolveE2EEndpoint(): { baseURL: string; serverPort: string } {
  const explicitBaseURL = process.env.PLAYWRIGHT_BASE_URL;
  if (!explicitBaseURL) {
    const serverPort = process.env.DAGU_E2E_SERVER_PORT ?? defaultServerPort;
    return {
      baseURL: `http://127.0.0.1:${serverPort}`,
      serverPort,
    };
  }

  const url = new URL(explicitBaseURL);
  const defaultPort = url.protocol === 'https:' ? '443' : '80';
  const serverPort =
    process.env.DAGU_E2E_SERVER_PORT ?? (url.port || defaultPort);

  return {
    baseURL: url.toString().replace(/\/$/, ''),
    serverPort,
  };
}

const { baseURL, serverPort } = resolveE2EEndpoint();
const stateDir = process.env.DAGU_E2E_STATE_DIR ?? defaultStateDir;
const e2eBinPath = process.env.DAGU_E2E_BIN ?? defaultBinPath;
const fullyParallel = process.env.PLAYWRIGHT_FULLY_PARALLEL === 'true';

process.env.DAGU_E2E_STATE_DIR = stateDir;
process.env.DAGU_E2E_BIN = e2eBinPath;

export default defineConfig({
  testDir: './e2e',
  fullyParallel,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  timeout: 90_000,
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'playwright-report' }],
  ],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        browserName: 'chromium',
      },
    },
  ],
  webServer: {
    command: './scripts/e2e/start-stack.sh',
    cwd: repoRoot,
    url: `${baseURL}/api/v1/health`,
    timeout: 90_000,
    // Allow six sequential 10-second service shutdowns plus cleanup overhead.
    gracefulShutdown: { signal: 'SIGTERM', timeout: 90_000 },
    reuseExistingServer: false,
    stdout: 'pipe',
    stderr: 'pipe',
    env: {
      ...process.env,
      DAGU_E2E_STATE_DIR: stateDir,
      DAGU_E2E_BIN: e2eBinPath,
      DAGU_E2E_SERVER_PORT: serverPort,
      DAGU_E2E_COORDINATOR_PORT:
        process.env.DAGU_E2E_COORDINATOR_PORT ?? '32181',
      DAGU_E2E_REMOTE_SERVER_PORT:
        process.env.DAGU_E2E_REMOTE_SERVER_PORT ?? defaultRemoteServerPort,
    },
  },
});
