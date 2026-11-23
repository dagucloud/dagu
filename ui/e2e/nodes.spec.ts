// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import { loadStack, loginViaAPI, loginViaUI, uniqueName } from './helpers/e2e';

test.describe('Nodes', () => {
  test('lists imported nodes, narrows them by name and label, and shows one', async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    // Other specs share the server, so every name here is unique.
    const id = uniqueName('e2e');
    const web = `${id}-web`;
    const db = `${id}-db`;
    const imported = await request.post('/api/v1/inventory-imports', {
      headers: { Authorization: `Bearer ${token}` },
      data: [
        {
          name: `${id} inventory`,
          defaults: { user: 'deploy', key: { secret: 'ssh/e2e-fleet' } },
          nodes: [
            { name: web, host: '10.0.1.11', labels: ['role=web'] },
            {
              name: db,
              host: 'db.internal',
              port: 2222,
              labels: ['role=db'],
              disabled: true,
            },
          ],
        },
      ],
    });
    expect(imported.ok(), await imported.text()).toBeTruthy();

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await page.goto('/nodes');
    await expect(
      page.getByRole('heading', { level: 1, name: 'Nodes' })
    ).toBeVisible();

    await page.getByRole('textbox', { name: 'Search by name...' }).fill(id);
    await expect(
      page.getByRole('cell', { name: web, exact: true })
    ).toBeVisible();
    await expect(
      page.getByRole('cell', { name: new RegExp(`^${db}`) })
    ).toBeVisible();

    const labels = page.getByRole('combobox', { name: 'Filter by labels...' });
    await labels.fill('role=db');
    await labels.press('Enter');
    await expect(
      page.getByRole('cell', { name: web, exact: true })
    ).toHaveCount(0);

    await page.getByRole('cell', { name: new RegExp(`^${db}`) }).click();
    const drawer = page.getByRole('dialog');
    await expect(drawer).toContainText('db.internal');
    await expect(drawer).toContainText('2222');
    await expect(drawer).toContainText('ssh/e2e-fleet');
    await expect(drawer).toContainText('Disabled');
  });

  test('tests a checked node and shows its result as the stream delivers it', async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const id = uniqueName('e2e');
    const web = `${id}-web`;
    // The key's secret does not exist, so the test fails at once, before it
    // connects.
    const imported = await request.post('/api/v1/inventory-imports', {
      headers: { Authorization: `Bearer ${token}` },
      data: [
        {
          name: `${id} inventory`,
          nodes: [
            {
              name: web,
              host: '127.0.0.1',
              user: 'deploy',
              key: { secret: `ssh/${id}-missing` },
            },
          ],
        },
      ],
    });
    expect(imported.ok(), await imported.text()).toBeTruthy();

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await page.goto('/nodes');
    await page.getByRole('textbox', { name: 'Search by name...' }).fill(id);
    await page.getByRole('checkbox', { name: `Check ${web}` }).check();
    await page.getByRole('button', { name: 'Test 1 checked' }).click();

    const panel = page.getByRole('region', { name: 'Connection test' });
    await expect(panel).toContainText('0 passed, 1 failed');
    await expect(panel).toContainText(`ssh/${id}-missing`);
    await expect(
      page.getByRole('row', { name: new RegExp(web) })
    ).toContainText('Failed');
  });
});
