// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test, type Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  writeLocalDAG,
} from './helpers/e2e';

function dagDefinitionsEntry(page: Page, dagName: string) {
  return page
    .locator('.card-obsidian:visible, tr:visible')
    .filter({ hasText: dagName })
    .first();
}

test.describe('DAG CRUD operations', () => {
  test.beforeEach(async ({ page }) => {
    const stack = await loadStack();
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await useDefaultWorkspaceScope(page);
  });

  test('creates a new DAG from the UI', async ({ page }) => {
    const dagName = uniqueName('e2e-create');

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
  });

  test('renames a DAG from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-rename');
    const newName = uniqueName('e2e-renamed');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: echo
    run: echo "rename test"
`
    );
    await waitForDAGAvailable(request, token, fileName);

    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();
    await page.getByRole('button', { name: 'Rename', exact: true }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('DAG Name').clear();
    await dialog.getByLabel('DAG Name').fill(newName);
    await dialog.getByRole('button', { name: 'Rename' }).click();

    await expect(page).toHaveURL(new RegExp(`/dags/${newName}$`));
  });

  test('deletes a DAG from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-delete');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: echo
    run: echo "delete test"
`
    );
    await waitForDAGAvailable(request, token, fileName);

    await page.goto(`/dags/${encodeURIComponent(fileName)}/spec`);
    await expect(
      page.getByRole('heading', { level: 1, name: dagName, exact: true })
    ).toBeVisible();

    page.once('dialog', (d) => d.accept());
    await page.getByRole('button', { name: 'Delete', exact: true }).click();

    await expect(page).toHaveURL(/\/dags$/);

    // Verify DAG is gone via API
    const response = await request.get(
      `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
      {
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
      }
    );
    expect(response.ok()).toBeFalsy();
  });

  test('suspends and resumes a DAG schedule', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-suspend');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
schedule: "0 0 * * *"
steps:
  - name: echo
    run: echo "suspend test"
`
    );
    await waitForDAGAvailable(request, token, fileName);

    await page.goto('/dags/');
    const dagEntry = dagDefinitionsEntry(page, dagName);
    await expect(dagEntry).toBeVisible();
    const scheduleToggle = dagEntry.getByRole('switch').first();
    await expect(scheduleToggle).toBeVisible();

    // DAG starts active — click switch to disable schedule
    await scheduleToggle.click();
    const disableDialog = page.getByRole('dialog');
    await expect(disableDialog).toBeVisible();
    await expect(disableDialog).toContainText('Disable Schedule');
    await disableDialog.getByRole('button', { name: 'Disable' }).click();
    await expect(disableDialog).toBeHidden();

    // Verify suspended via the canonical DAG details route the UI uses
    await expect
      .poll(
        async () => {
          const resp = await request.get(
            `/api/v1/dags/${encodeURIComponent(dagName)}?remoteNode=local`,
            {
              headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
              },
            }
          );
          const body = (await resp.json()) as { suspended: boolean };
          return body.suspended;
        },
        { timeout: 15_000 }
      )
      .toBeTruthy();

    // Click switch again to re-enable schedule
    await scheduleToggle.click();
    const enableDialog = page.getByRole('dialog');
    await expect(enableDialog).toBeVisible();
    await expect(enableDialog).toContainText('Enable Schedule');
    await enableDialog.getByRole('button', { name: 'Enable' }).click();
    await expect(enableDialog).toBeHidden();

    // Verify resumed via the canonical DAG details route the UI uses
    await expect
      .poll(
        async () => {
          const resp = await request.get(
            `/api/v1/dags/${encodeURIComponent(dagName)}?remoteNode=local`,
            {
              headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
              },
            }
          );
          const body = (await resp.json()) as { suspended: boolean };
          return body.suspended;
        },
        { timeout: 15_000 }
      )
      .toBeFalsy();
  });

  test('rejects DAG creation with invalid name', async ({ page }) => {
    await page.goto('/dags/');
    await page.getByRole('button', { name: 'Create new DAG' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('DAG Name').fill('invalid name with spaces!');
    await dialog.getByRole('button', { name: 'Create' }).click();

    // Dialog stays open — validation prevents creation
    await expect(dialog).toBeVisible();
    await expect(page).not.toHaveURL(/\/spec$/);
  });
});
