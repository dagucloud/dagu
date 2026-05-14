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
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  writeLocalDAG,
} from './helpers/e2e';

test.describe('auth flows', () => {
  test.skip(
    !hasRBACLicenseSourceConfigured(),
    'requires a Dagu Pro license source for RBAC user management'
  );

  test('user changes own password', async ({ page, request }) => {
    const stack = await loadStack();
    const adminToken = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const username = uniqueName('pw-change');
    const oldPassword = 'old-pass-12345';
    const newPassword = 'new-pass-67890';

    await createUser(request, adminToken, {
      username,
      password: oldPassword,
      role: 'developer',
    });

    await loginViaUI(page, username, oldPassword);

    // Open user menu and start password change
    await page.locator('aside').getByRole('button').filter({ hasText: username }).click();
    await page.getByRole('menuitem', { name: 'Change Password' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('Current Password').fill(oldPassword);
    await dialog.getByLabel('New Password', { exact: true }).fill(newPassword);
    await dialog.getByLabel('Confirm New Password').fill(newPassword);
    await dialog.getByRole('button', { name: 'Change Password' }).click();

    await expect(dialog.getByText('Password changed successfully')).toBeVisible();

    // Clear session and login with new password
    await clearSession(page);
    await loginViaUI(page, username, newPassword);
    await expect(page).not.toHaveURL(/\/login$/);

    // Old password should fail
    const failResponse = await request.post('/api/v1/auth/login', {
      data: { username, password: oldPassword },
    });
    expect(failResponse.status()).toBe(401);
  });

  test('manager can create DAGs but cannot manage users', async ({ page, request }) => {
    const stack = await loadStack();
    const adminToken = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const managerUser = uniqueName('manager');
    const managerPass = 'manager-pass-123';
    await createUser(request, adminToken, {
      username: managerUser,
      password: managerPass,
      role: 'manager',
    });

    await loginViaUI(page, managerUser, managerPass);

    // Manager can create DAG via UI
    const dagName = uniqueName('e2e-mgr-dag');
    await useDefaultWorkspaceScope(page);
    await page.goto('/dags/');
    await page.getByRole('button', { name: 'Create new DAG' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('DAG Name').fill(dagName);
    await dialog.getByRole('button', { name: 'Create' }).click();
    await expect(page).toHaveURL(new RegExp(`/dags/${dagName}/spec$`));
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();

    // Manager cannot access user management
    await page.goto('/users');
    await expect(page).not.toHaveURL(/\/users$/);
  });

  test('operator cannot write DAGs but can execute them', async ({ page, request }) => {
    const stack = await loadStack();
    const adminToken = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const operatorUser = uniqueName('operator');
    const operatorPass = 'operator-pass-123';
    await createUser(request, adminToken, {
      username: operatorUser,
      password: operatorPass,
      role: 'operator',
    });

    const dagName = uniqueName('e2e-op-dag');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: echo
    run: echo "operator test"
`
    );
    await waitForDAGAvailable(request, adminToken, fileName);

    await loginViaUI(page, operatorUser, operatorPass);

    // Operator should not see Create button
    await useDefaultWorkspaceScope(page);
    await page.goto('/dags/');
    await expect(page.getByRole('button', { name: 'Create new DAG' })).toBeHidden();

    // Operator should not see Rename/Delete on spec tab
    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Rename', exact: true })
    ).toBeHidden();
    await expect(
      page.getByRole('button', { name: 'Delete', exact: true })
    ).toBeHidden();

    // Operator can still execute via API
    const operatorToken = await loginViaAPI(request, operatorUser, operatorPass);
    const startResponse = await request.post(
      `/api/v1/dags/${encodeURIComponent(fileName)}/start?remoteNode=local`,
      {
        headers: {
          Authorization: `Bearer ${operatorToken}`,
          'Content-Type': 'application/json',
        },
        data: {},
      }
    );
    expect(startResponse.ok()).toBeTruthy();
  });

  test('admin resets user password from management page', async ({ page, request }) => {
    const stack = await loadStack();
    const adminToken = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const username = uniqueName('reset-pw');
    const originalPass = 'original-pass-123';
    const resetPass = 'reset-pass-67890';

    await createUser(request, adminToken, {
      username,
      password: originalPass,
      role: 'viewer',
    });

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await page.goto('/users');

    // Find user row and open actions
    const row = page.getByRole('row').filter({ hasText: username });
    await row.getByRole('button').click();
    await page.getByRole('menuitem', { name: 'Reset Password' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('New Password').fill(resetPass);
    await dialog.getByLabel('Confirm Password').fill(resetPass);
    await dialog.getByRole('button', { name: 'Reset Password' }).click();

    await expect(dialog.getByText('Password reset successfully')).toBeVisible();

    // Verify login with new password via API
    const loginResponse = await request.post('/api/v1/auth/login', {
      data: { username, password: resetPass },
    });
    expect(loginResponse.ok()).toBeTruthy();

    // Old password should fail
    const oldLoginResponse = await request.post('/api/v1/auth/login', {
      data: { username, password: originalPass },
    });
    expect(oldLoginResponse.status()).toBe(401);
  });
});
