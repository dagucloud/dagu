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

// The first Monday of March in the year the calendar page opens on.
function firstMondayOfMarch(): string {
  const date = new Date(Date.UTC(new Date().getFullYear(), 2, 1));
  while (date.getUTCDay() !== 1) {
    date.setUTCDate(date.getUTCDate() + 1);
  }
  return date.toISOString().slice(0, 10);
}

test.describe('Business calendars', () => {
  test.beforeEach(async ({ page }) => {
    const stack = await loadStack();
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    await useDefaultWorkspaceScope(page);
  });

  test('creates a calendar, previews a holiday, and keeps it while a DAG uses it', async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const calendarName = uniqueName('e2e-cal');
    const holiday = firstMondayOfMarch();

    await page.goto('/calendars');
    await page.getByRole('button', { name: 'New calendar' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Name').fill(calendarName);
    await dialog.getByRole('button', { name: 'Create' }).click();
    await expect(page).toHaveURL(
      new RegExp(`/calendars/${calendarName}\\?remoteNode=local$`)
    );
    await expect(
      page.getByRole('heading', { level: 1, name: calendarName, exact: true })
    ).toBeVisible();

    // The unsaved holiday shows in the year view before it is saved.
    const editor = page.locator('.monaco-editor textarea').first();
    await editor.focus();
    await page.keyboard.press('ControlOrMeta+A');
    await page.keyboard.insertText(`holidays:\n  - ${holiday}\n`);
    await expect(
      page.getByRole('button', { name: `Mon ${holiday}: Holiday`, exact: true })
    ).toBeVisible();
    await expect(
      page.getByText('1 date changes', { exact: false })
    ).toBeVisible();

    const saveButton = page.getByRole('button', { name: 'Save', exact: true });
    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith(`/calendars/${calendarName}`)
    );
    await saveButton.click();
    expect((await saved).ok()).toBeTruthy();
    await expect(saveButton).toBeDisabled();

    const dagName = uniqueName('e2e-cal-dag');
    const fileName = await writeLocalDAG(
      dagName,
      `calendar: ${calendarName}
schedule: "0 9 * * *"
steps:
  - run: echo scheduled
`
    );
    await waitForDAGAvailable(request, token, fileName);
    await page.reload();
    await expect(
      page.getByRole('link', { name: dagName, exact: true })
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Delete', exact: true })
    ).toBeDisabled();
  });
});
