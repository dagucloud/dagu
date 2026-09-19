// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

for (const view of ['grouped', 'timeline'] as const) {
  test(`preserves the selected tab during ${view} history navigation`, async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName(`history-${view}`);
    // Distinct start seconds make the history order deterministic.
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: result
    run: |
      sleep 1
      echo history-output
    output: RESULT
`
    );
    await waitForDAGAvailable(request, token, fileName);
    const olderRun = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, olderRun, ['succeeded']);
    const newerRun = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, newerRun, ['succeeded']);
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);

    if (view === 'grouped') {
      await page.goto(`/dag-runs?name=${dagName}`);
      await page.getByRole('button', { name: 'Grouped view' }).click();
      await page.getByText(dagName, { exact: true }).click();
      await page.getByText(newerRun, { exact: true }).click();
    } else {
      await page.goto('/dashboard');
      await page.getByRole('combobox').filter({ hasText: 'All DAGs' }).click();
      await page.getByRole('option', { name: dagName, exact: true }).click();
      await page
        .locator('.vis-item-content')
        .filter({ hasText: dagName })
        .first()
        .click();
    }

    const modal = page.locator('.dagRun-modal-content');
    await expect(modal).toBeVisible();
    await expect(
      modal.getByText(new RegExp(`${olderRun}|${newerRun}`)).first()
    ).toBeVisible();
    const openedNewer = await modal
      .getByText(newerRun, { exact: true })
      .isVisible();
    const firstRun = openedNewer ? newerRun : olderRun;
    const nextRun = openedNewer ? olderRun : newerRun;
    const forwardKey = openedNewer ? 'ArrowDown' : 'ArrowUp';
    const backwardKey = openedNewer ? 'ArrowUp' : 'ArrowDown';

    await modal.getByRole('button', { name: 'Outputs', exact: true }).click();
    await expect(
      modal.getByText('history-output', { exact: true })
    ).toBeVisible();
    await page.keyboard.press(forwardKey);
    await expect(modal.getByText(nextRun, { exact: true })).toBeVisible();
    await expect(
      modal.getByText('history-output', { exact: true })
    ).toBeVisible();

    if (view === 'grouped') {
      await expect
        .poll(() => new URL(page.url()).searchParams.get('selectedRunTab'))
        .toBe('outputs');
      await expect
        .poll(() => new URL(page.url()).searchParams.get('name'))
        .toBe(dagName);
      await page.reload();
      await expect(modal.getByText(nextRun, { exact: true })).toBeVisible();
      await expect(
        modal.getByText('history-output', { exact: true })
      ).toBeVisible();
    }

    await page.keyboard.press(backwardKey);
    await expect(modal.getByText(firstRun, { exact: true })).toBeVisible();
    await expect(
      modal.getByText('history-output', { exact: true })
    ).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(modal).toHaveCount(0);

    if (view === 'grouped') {
      await page.getByText(dagName, { exact: true }).click();
      await page.getByText(newerRun, { exact: true }).click();
    } else {
      await page
        .locator('.vis-item-content')
        .filter({ hasText: dagName })
        .first()
        .click();
    }
    await expect(
      modal.getByRole('columnheader', { name: 'Step Name' })
    ).toBeVisible();
  });
}
