// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  clearSession,
  createUser,
  hasRBACLicenseSourceConfigured,
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  waitForDAGAvailable,
  writeLocalDAG,
} from './helpers/e2e';

test('redirects unauthenticated users to login and allows admin sign-in', async ({
  page,
}) => {
  const stack = await loadStack();

  await page.goto('/system-status');
  await expect(page).toHaveURL(/\/login$/);

  await page.getByLabel('Username').fill(stack.auth.adminUsername);
  await page.getByLabel('Password').fill(stack.auth.adminPassword);
  await page.getByRole('button', { name: 'Sign In' }).click();

  await expect(page).toHaveURL(/\/system-status$/);
  await expect(page.getByRole('heading', { name: 'System Status' })).toBeVisible();
});

test('enforces viewer route and execute restrictions', async ({ page, request }) => {
  test.skip(
    !hasRBACLicenseSourceConfigured(),
    'requires a Dagu Pro license source for RBAC user management'
  );

  const stack = await loadStack();
  const adminToken = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );

  const viewerUsername = uniqueName('viewer');
  const viewerPassword = 'viewer-pass-123';
  await createUser(request, adminToken, {
    username: viewerUsername,
    password: viewerPassword,
    role: 'viewer',
  });

  const dagName = uniqueName('e2e-viewer-execute');
  const fileName = await writeLocalDAG(
    dagName,
    `
name: ${dagName}
worker_selector:
  role: e2e
steps:
  - name: echo-step
    run: echo "viewer execute denied"
`
  );
  await waitForDAGAvailable(request, adminToken, fileName);

  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await clearSession(page);
  await loginViaUI(page, viewerUsername, viewerPassword);

  await expect(page.locator('aside')).not.toContainText('System Status');
  await expect(page.locator('aside')).not.toContainText('Remote Nodes');

  await page.goto('/system-status');
  await expect(page).not.toHaveURL(/\/system-status$/);

  await page.goto('/remote-nodes');
  await expect(page).not.toHaveURL(/\/remote-nodes$/);

  const viewerToken = await loginViaAPI(request, viewerUsername, viewerPassword);
  const startResponse = await request.post(
    `/api/v1/dags/${encodeURIComponent(fileName)}/start?remoteNode=local`,
    {
      headers: {
        Authorization: `Bearer ${viewerToken}`,
        'Content-Type': 'application/json',
      },
      data: {},
    }
  );

  expect(startResponse.status()).toBe(403);
});
