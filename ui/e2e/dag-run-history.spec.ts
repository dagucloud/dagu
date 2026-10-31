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

// A row of a run list shows the run in a peek over the list, and Open
// workflow opens the run on its workflow page.
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
    const peek = page.getByRole('dialog', { name: dagName });

    if (view === 'activity') {
      await page.goto('/dashboard');
      await page.getByLabel('Filter workflows').fill(dagName);
      await openNewestRunFromLane(page, dagName);
    } else {
      await page.goto(`/dag-runs?name=${dagName}`);
      // A click away from the links of the run row shows the peek.
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
      await expect(peek).toBeVisible();
      expect(new URL(page.url()).searchParams.get('peekRun')).toBe(runId);
      await peek.getByRole('link', { name: 'Open workflow' }).click();
    }

    await expect
      .poll(() => here())
      .toEqual({ path: `/dags/${dagName}`, run: runId });
    // The run opens in the Run panel of the workflow page, not in a side panel.
    await expect(page.getByTestId('ww-pane')).toBeVisible();
    await expect(page.locator('.dagRun-modal-content')).toHaveCount(0);

    if (view === 'list') {
      // Back returns to the open peek; the list takes clicks once it closes.
      await page.goBack();
      await expect(page).toHaveURL(/\/dag-runs\?/);
      await expect(peek).toBeVisible();
      await peek.press('Escape');
      await expect(peek).toHaveCount(0);
      const [popup] = await Promise.all([
        page.context().waitForEvent('page'),
        page
          .getByRole('row')
          .filter({ hasText: dagName })
          .first()
          .click({ modifiers: ['ControlOrMeta'] }),
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
    }
  });
}
