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

// A row of the Workflows list shows the peek of the workflow over the list;
// Open workflow or Enter opens the workflow page. Ctrl or Cmd opens the page
// in a new tab, and on a narrow window a row opens the page at once.
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

  // A click away from the links and buttons of the row shows the peek.
  const rowBody = row.getByText('No schedule', { exact: true });
  const peek = page.getByRole('dialog', { name: dagName });
  const peekParam = () => new URL(page.url()).searchParams.get('peek');
  await rowBody.click();
  await expect(peek).toBeVisible();
  expect(peekParam()).toBe(dagName);
  expect(new URL(page.url()).pathname).toBe('/dags');
  const open = peek.getByRole('link', { name: 'Open workflow' });
  await expect(open).toHaveAttribute('href', pageHref);

  // Esc and Back close the peek.
  await peek.press('Escape');
  await expect(peek).toHaveCount(0);
  expect(peekParam()).toBeNull();
  await rowBody.click();
  await expect(peek).toBeVisible();
  await page.goBack();
  await expect(peek).toHaveCount(0);
  expect(peekParam()).toBeNull();

  // Open workflow opens the page; Back comes back to the open peek.
  await rowBody.click();
  await open.click();
  await expect
    .poll(() => new URL(page.url()).pathname)
    .toBe(`/dags/${dagName}`);
  await expect(page.getByTestId('ww-pane')).toBeVisible();
  await page.goBack();
  await expect(peek).toBeVisible();

  // Enter on the peek opens the page too.
  await peek.press('Enter');
  await expect
    .poll(() => new URL(page.url()).pathname)
    .toBe(`/dags/${dagName}`);
  await expect(page.getByTestId('ww-pane')).toBeVisible();

  // On a narrow window the list shows cards, and a card opens the page.
  await page.goBack();
  await expect(peek).toBeVisible();
  await peek.press('Escape');
  await expect(peek).toHaveCount(0);
  await page.setViewportSize({ width: 600, height: 900 });
  const card = page
    .locator('.card-obsidian:visible')
    .filter({ hasText: dagName })
    .first();
  await expect(card).toBeVisible();
  // The corner of the card is clear of its checkbox and its links.
  await card.click({ position: { x: 4, y: 4 } });
  await expect
    .poll(() => new URL(page.url()).pathname)
    .toBe(`/dags/${dagName}`);
  expect(peekParam()).toBeNull();
});
