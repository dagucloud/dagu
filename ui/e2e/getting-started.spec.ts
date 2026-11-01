// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test, type Page } from '@playwright/test';
import { loadStack, loginViaUI } from './helpers/e2e';
import type { LicenseStatus } from '../src/contexts/ConfigContext';

const community: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};

// The examples a first launch writes, none of them run yet.
const examples = [
  'example-01-basic-sequential',
  'example-02-parallel-execution',
  'example-03-scheduling',
  'example-04-nested-workflows',
  'example-05-container-workflow',
  'example-06-retry-on-failure',
].map((name) => ({
  fileName: name,
  dag: { name, schedule: [] },
  latestDAGRun: { dagRunId: '', name, status: 0, statusLabel: 'not_started' },
  suspended: false,
  pinned: false,
  errors: [],
}));

// Serves a fresh Community install inside this browser context; the shared
// stack is unchanged.
async function serveFreshInstall(page: Page) {
  await page.route(
    (url) => url.pathname === '/api/v1/dags',
    (route) =>
      route.request().method() === 'GET'
        ? route.fulfill({
            json: {
              dags: examples,
              errors: [],
              pagination: {
                totalRecords: examples.length,
                currentPage: 1,
                totalPages: 1,
                nextPage: 1,
                prevPage: 1,
                hasNextPage: false,
              },
            },
          })
        : route.fallback()
  );
  await page.route('**/api/v1/license/status*', (route) =>
    route.fulfill({ json: community })
  );
  await page.route(
    (url) => url.pathname === '/api/v1/notification-channels',
    (route) =>
      route.request().method() === 'GET'
        ? route.fulfill({ json: { channels: [] } })
        : route.fallback()
  );
}

test('guides a fresh install through its first steps', async ({
  page,
}, testInfo) => {
  const stack = await loadStack();
  await serveFreshInstall(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await page.goto('/');

  const checklist = page.getByRole('region', { name: 'Getting started' });
  await expect(checklist).toContainText('0 of 4 done');
  await expect(
    checklist.getByRole('link', { name: 'Open the retry example' })
  ).toHaveAttribute('href', '/dags/example-06-retry-on-failure');
  await expect(
    checklist.getByRole('link', { name: 'Connect for free' })
  ).toHaveAttribute('href', '/license#monitoring');
  await page.screenshot({
    path: testInfo.outputPath('getting-started-desktop.png'),
    fullPage: true,
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(checklist).toBeVisible();
  // Rows stack, so nothing in the checklist runs past the screen edge.
  expect(
    await checklist.evaluate(
      (element) =>
        element.getBoundingClientRect().right <= window.innerWidth &&
        element.scrollWidth <= element.clientWidth
    )
  ).toBe(true);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth
    )
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath('getting-started-mobile.png'),
    fullPage: true,
  });

  await checklist.getByRole('button', { name: 'Dismiss' }).click();
  await expect(checklist).toHaveCount(0);
  await page.reload();
  await expect(page.getByLabel('Filter workflows')).toBeVisible();
  await expect(page.getByRole('status', { name: 'Loading...' })).toHaveCount(0);
  await expect(checklist).toHaveCount(0);
});
