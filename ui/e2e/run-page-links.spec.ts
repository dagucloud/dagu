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

// A run is selected on its workflow page with ?run, and the URL keeps the
// open tab and step. The links of the removed run page redirect there.
test('opens runs, steps, and tabs from their old links', async ({
  page,
  request,
}) => {
  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  const dagName = uniqueName('run-links');
  const fileName = await writeLocalDAG(
    dagName,
    `
name: ${dagName}
steps:
  - name: prepare
    run: echo prepared
  - name: publish
    run: echo published
    depends: prepare
`
  );
  await waitForDAGAvailable(request, token, fileName);
  const runId = await startDAG(request, token, fileName);
  await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);

  const here = () => {
    const url = new URL(page.url());
    return {
      path: url.pathname,
      run: url.searchParams.get('run'),
      step: url.searchParams.get('step'),
      dock: url.searchParams.get('dock'),
    };
  };

  await page.goto(
    `/dags/${encodeURIComponent(fileName)}?dagRunId=${runId}&dagRunName=${dagName}`
  );
  await expect
    .poll(() => here())
    .toMatchObject({ path: `/dags/${dagName}`, run: runId });

  // A step view has no Timeline tab (the timeline is the view of the run): the
  // old link with a step and the timeline tab opens the Log of the step.
  await page.goto(`/dag-runs/${dagName}/${runId}?step=publish&tab=timeline`);
  await expect.poll(() => here().path).toBe(`/dags/${dagName}`);
  expect(here().run).toBe(runId);
  await expect(
    page.getByTestId('ww-dock').getByRole('tab', { name: 'Log', exact: true })
  ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });

  await page.goto(`/dag-runs/${dagName}/${runId}?tab=timeline`);
  await expect.poll(() => here().path).toBe(`/dags/${dagName}`);
  expect(here().run).toBe(runId);
  await expect(page.getByTestId('ww-pane')).toBeVisible();
  const dock = page.getByTestId('ww-dock');
  await expect(
    dock.getByRole('tab', { name: 'Timeline', exact: true })
  ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
  expect(here().dock).toBe('timeline');

  await page.reload();
  await expect(
    dock.getByRole('tab', { name: 'Timeline', exact: true })
  ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
  expect(here().run).toBe(runId);
});
