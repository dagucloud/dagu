// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, type Page, test } from '@playwright/test';
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

// The Activity lanes select a lane's newest run on the first arrow key.
async function openNewestRunFromLane(page: Page, dagName: string) {
  const lane = page.getByRole('group', { name: new RegExp(`^${dagName}:`) });
  await lane.focus();
  await lane.press('ArrowRight');
  await lane.press('Enter');
}

// A run of a run list or of the Activity lanes opens its workflow page over
// the list, at the page's own URL; the list stays below.
for (const view of ['list', 'grouped', 'activity'] as const) {
  test(`opens a run on its workflow page from the ${view} view`, async ({
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
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: result
    run: echo history-output
    output: RESULT
`
    );
    await waitForDAGAvailable(request, token, fileName);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);

    const here = () => {
      const url = new URL(page.url());
      return { path: url.pathname, run: url.searchParams.get('run') };
    };
    const overlay = page.getByRole('dialog', { name: 'Workflow' });

    if (view === 'activity') {
      await page.goto('/dashboard');
      await page.getByLabel('Filter workflows').fill(dagName);
      await openNewestRunFromLane(page, dagName);
    } else {
      await page.goto(`/dag-runs?name=${dagName}`);
      if (view === 'grouped') {
        await page.getByRole('button', { name: 'Grouped view' }).click();
        await page.getByText(dagName, { exact: true }).click();
        await page
          .getByRole('link', { name: runId, exact: true })
          .locator('xpath=..')
          .getByText('Duration:')
          .click();
      } else {
        await page
          .getByRole('row')
          .filter({ hasText: dagName })
          .first()
          .getByText(runId, { exact: true })
          .click();
      }
    }
    await expect(overlay).toBeVisible();
    await expect
      .poll(() => here())
      .toEqual({ path: `/dags/${dagName}`, run: runId });
    // The run opens in the Run panel of the workflow page, not in a side panel.
    await expect(overlay.getByTestId('ww-pane')).toBeVisible();
    await expect(page.locator('.dagRun-modal-content')).toHaveCount(0);

    if (view === 'list') {
      const row = page.getByRole('row').filter({ hasText: dagName }).first();
      // Back returns to the list, which takes clicks once the overlay closes.
      await page.goBack();
      await expect(overlay).toHaveCount(0);
      await expect(page).toHaveURL(/\/dag-runs\?/);
      const [popup] = await Promise.all([
        page.context().waitForEvent('page'),
        row.click({ modifiers: ['ControlOrMeta'] }),
      ]);
      await expect
        .poll(() => {
          const url = new URL(popup.url());
          return { path: url.pathname, run: url.searchParams.get('run') };
        })
        .toEqual({ path: `/dags/${dagName}`, run: runId });
      await popup.close();
      // The list stays where it was.
      expect(new URL(page.url()).pathname).toBe('/dag-runs');

      // Open workflow keeps the workflow page as the page.
      await row.getByText(runId, { exact: true }).click();
      await expect(overlay).toBeVisible();
      await overlay.getByRole('button', { name: 'Open workflow' }).click();
      await expect(overlay).toHaveCount(0);
      await expect(page.getByTestId('ww-pane')).toBeVisible();
      expect(here()).toEqual({ path: `/dags/${dagName}`, run: runId });
    }
  });
}
