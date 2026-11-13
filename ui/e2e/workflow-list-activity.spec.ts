// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  loginViaUI,
  startDAG,
  uniqueName,
  useDefaultWorkspaceScope,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

// A workflow row shows the runs of the last 24 hours as thin bars, and its
// chevron unfolds the latest runs under the row. A bar and a run line open the
// workflow on that run.
test('shows the activity of a workflow in the Workflows list', async ({
  page,
  request,
}) => {
  test.setTimeout(120_000);
  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  const dagName = uniqueName('list-activity');
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
  const first = await startDAG(request, token, fileName);
  await waitForRunStatus(request, token, dagName, first, ['succeeded']);
  const second = await startDAG(request, token, fileName);
  await waitForRunStatus(request, token, dagName, second, ['succeeded']);

  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await useDefaultWorkspaceScope(page);
  await page.setViewportSize({ width: 1500, height: 900 });
  await page.goto('/dags/');
  await page.getByPlaceholder('Filter by workflow name...').fill(dagName);
  const row = page.getByRole('row').filter({ hasText: dagName }).first();
  await expect(row).toBeVisible({ timeout: 30_000 });

  const strip = row.getByTestId('dag-activity');
  await expect(strip).toHaveAttribute('data-runs', '2');
  await expect(strip.locator(`[data-run-id="${first}"]`)).toHaveCount(1);
  await expect(strip.locator(`[data-run-id="${second}"]`)).toHaveCount(1);

  await row.getByTestId('dag-activity-toggle').click();
  const runs = page.getByTestId('dag-recent-runs');
  await expect(runs.getByText(first)).toBeVisible();
  await expect(runs.getByText(second)).toBeVisible();
  expect(new URL(page.url()).pathname).toBe('/dags');

  await runs.getByText(first).click();
  await expect
    .poll(() => new URL(page.url()).pathname)
    .toBe(`/dags/${dagName}`);
  expect(new URL(page.url()).searchParams.get('run')).toBe(first);
});
