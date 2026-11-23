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

  test('creates an inventory, opens it as JSON, and deletes it', async ({
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
    // An inventory makes the page show its tabs rather than the empty state.
    const imported = await request.post('/api/v1/inventory-imports', {
      headers: { Authorization: `Bearer ${token}` },
      data: [{ name: `${id} existing`, nodes: [] }],
    });
    expect(imported.ok(), await imported.text()).toBeTruthy();
    const name = `${id} created`;

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await page.goto('/nodes');
    await page.getByRole('button', { name: 'Inventories' }).click();
    await page.getByRole('button', { name: 'New inventory' }).click();
    const form = page.getByRole('dialog');
    await form.getByLabel('Name').fill(name);
    await form.getByLabel('Default user').fill('deploy');
    await form.getByRole('button', { name: 'Create' }).click();
    await expect(form).toHaveCount(0);

    await page.getByRole('button', { name: `Edit JSON ${name}` }).click();
    const editor = page.getByRole('dialog');
    await expect(editor).toContainText('"deploy"');
    await editor.getByRole('button', { name: 'Cancel' }).click();

    await page.getByRole('button', { name: `Delete ${name}` }).click();
    await page
      .getByRole('dialog')
      .getByRole('button', { name: 'Delete' })
      .click();
    await expect(page.getByRole('cell', { name, exact: true })).toHaveCount(0);
  });

  test('tests a node before adding it, and adds it', async ({
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
    const inventory = `${id} inventory`;
    const imported = await request.post('/api/v1/inventory-imports', {
      headers: { Authorization: `Bearer ${token}` },
      data: [{ name: inventory, defaults: { user: 'deploy' }, nodes: [] }],
    });
    expect(imported.ok(), await imported.text()).toBeTruthy();
    const name = `${id}-web`;

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await page.goto('/nodes');
    await page.getByRole('button', { name: 'Add node' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('combobox', { name: 'Inventory' }).click();
    await page.getByRole('option', { name: inventory }).click();
    await dialog.getByLabel('Name').fill(name);
    await dialog.getByLabel('Host').fill('127.0.0.1');
    // The key's secret does not exist, so the test fails before it connects.
    await dialog.getByLabel('Key secret').fill(`ssh/${id}-missing`);
    await dialog.getByRole('button', { name: 'Test connection' }).click();
    await expect(dialog).toContainText(`ssh/${id}-missing`);
    await expect(
      dialog.getByRole('button', { name: 'Add node' })
    ).toBeDisabled();

    await dialog.getByRole('button', { name: 'Add without testing' }).click();
    await expect(dialog).toHaveCount(0);
    await page.getByRole('textbox', { name: 'Search by name...' }).fill(id);
    await expect(page.getByRole('cell', { name, exact: true })).toBeVisible();
  });

  test('imports an inventory, then disables a node from its drawer', async ({
    page,
  }) => {
    const stack = await loadStack();
    const id = uniqueName('e2e');
    const web = `${id}-web`;

    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await page.goto('/nodes');
    // With no inventory yet the page shows its empty state, which offers
    // Import without tabs.
    const inventoriesTab = page.getByRole('button', { name: 'Inventories' });
    if (await inventoriesTab.isVisible()) {
      await inventoriesTab.click();
    }
    await page.getByRole('button', { name: 'Import' }).click();
    const dialog = page.getByRole('dialog');
    await dialog
      .getByLabel('Inventories JSON')
      .fill(
        JSON.stringify([
          { name: `${id} inventory`, nodes: [{ name: web, host: '10.0.9.9' }] },
        ])
      );
    await dialog.getByRole('button', { name: 'Check' }).click();
    await expect(dialog).toContainText(`Would create: ${id} inventory`);
    await dialog.getByRole('button', { name: 'Import' }).click();
    await expect(dialog).toHaveCount(0);

    await page.getByRole('button', { name: 'Nodes', exact: true }).click();
    await page.getByRole('textbox', { name: 'Search by name...' }).fill(id);
    await page.getByRole('cell', { name: web, exact: true }).click();
    const drawer = page.getByRole('dialog');
    await drawer.getByRole('button', { name: 'Disable' }).click();
    await expect(drawer.getByRole('button', { name: 'Enable' })).toBeVisible();
    await expect(
      page.getByRole('row', { name: new RegExp(web) })
    ).toContainText('Disabled');
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
