// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  writeLocalDAG,
} from './helpers/e2e';

// A row of the Workflows list opens the workflow page over the list, at the
// page's own URL; Open workflow keeps it as the page. Ctrl or Cmd opens the
// page in a new tab, and on a narrow window a row opens the page at once.
test('opens a workflow from its row in the Workflows list', async ({
  page,
  request,
}) => {
  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  const dagName = uniqueName('list-open');
  const fileName = await writeLocalDAG(
    dagName,
    `
name: ${dagName}
steps:
  - name: hello
    run: echo hello
`
  );
  await waitForDAGAvailable(request, token, fileName);
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await useDefaultWorkspaceScope(page);

  await page.goto('/dags/');
  await page.getByPlaceholder('Filter by workflow name...').fill(dagName);
  const row = page.getByRole('row').filter({ hasText: dagName }).first();
  await expect(row).toBeVisible({ timeout: 30_000 });
  const pageHref = new RegExp(`^/dags/${dagName}\\?remoteNode=local`);
  await expect(row.getByRole('link', { name: dagName })).toHaveAttribute(
    'href',
    pageHref
  );

  const [popup] = await Promise.all([
    page.context().waitForEvent('page'),
    row.click({ modifiers: ['ControlOrMeta'] }),
  ]);
  await popup.waitForLoadState();
  expect(new URL(popup.url()).pathname).toBe(`/dags/${dagName}`);
  await popup.close();
  expect(new URL(page.url()).pathname).toBe('/dags');

  // A click away from the links and buttons of the row opens the overlay.
  const rowBody = row.getByText('No schedule', { exact: true });
  const overlay = page.getByRole('dialog', { name: 'Workflow' });
  const path = () => new URL(page.url()).pathname;
  await rowBody.click();
  await expect(overlay).toBeVisible();
  await expect.poll(path).toBe(`/dags/${dagName}`);
  await expect(overlay.getByTestId('ww-pane')).toBeVisible();

  // Esc, Back, and Close return to the list.
  await overlay.press('Escape');
  await expect(overlay).toHaveCount(0);
  expect(path()).toBe('/dags');
  await rowBody.click();
  await expect(overlay).toBeVisible();
  await page.goBack();
  await expect(overlay).toHaveCount(0);
  expect(path()).toBe('/dags');
  await rowBody.click();
  await overlay.getByRole('button', { name: 'Close' }).click();
  await expect(overlay).toHaveCount(0);
  expect(path()).toBe('/dags');

  // Open workflow keeps the page; Back returns to the list.
  await rowBody.click();
  await overlay.getByRole('button', { name: 'Open workflow' }).click();
  await expect(overlay).toHaveCount(0);
  await expect(page.getByTestId('ww-pane')).toBeVisible();
  expect(path()).toBe(`/dags/${dagName}`);
  await page.goBack();
  await expect.poll(path).toBe('/dags');

  // On a narrow window the list shows cards, and a card opens the page.
  await page.setViewportSize({ width: 600, height: 900 });
  const card = page
    .locator('.card-obsidian:visible')
    .filter({ hasText: dagName })
    .first();
  await expect(card).toBeVisible();
  // The corner of the card is clear of its checkbox and its links.
  await card.click({ position: { x: 4, y: 4 } });
  await expect.poll(path).toBe(`/dags/${dagName}`);
  await expect(overlay).toHaveCount(0);
});
