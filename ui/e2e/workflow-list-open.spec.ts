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

// A row of the Workflows list opens the workflow page, in the same tab or, with
// Ctrl or Cmd, in a new tab. No right panel opens over the list.
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
  await expect(row.getByRole('link', { name: dagName })).toHaveAttribute(
    'href',
    new RegExp(`^/dags/${dagName}\\?remoteNode=local`)
  );

  const [popup] = await Promise.all([
    page.context().waitForEvent('page'),
    row.click({ modifiers: ['ControlOrMeta'] }),
  ]);
  await popup.waitForLoadState();
  expect(new URL(popup.url()).pathname).toBe(`/dags/${dagName}`);
  await popup.close();
  expect(new URL(page.url()).pathname).toBe('/dags');

  await row.click();
  await expect
    .poll(() => new URL(page.url()).pathname)
    .toBe(`/dags/${dagName}`);
  await expect(page.getByTestId('ww-pane')).toBeVisible();
  await expect(page.locator('.dag-modal-content')).toHaveCount(0);
});
