// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  getDAGRun,
  getStepStdout,
  listDAGRuns,
  loadStack,
  loginViaAPI,
  loginViaUI,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

test.describe('DAG run actions', () => {
  test.beforeEach(async ({ page }) => {
    const stack = await loadStack();
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  });

  test('stops a running distributed DAG run from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-stop-flow');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
worker_selector:
  role: e2e
steps:
  - name: hold
    run: sleep 30
`
    );

    await waitForDAGAvailable(request, token, fileName);
    const dagRunId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, dagRunId, ['running'], 'local', 30_000);

    await page.goto(`/dag-runs/${dagName}/${dagRunId}`);
    await page.getByRole('button', { name: 'Stop' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Stop' }).click();

    const stoppedRun = await waitForRunStatus(
      request,
      token,
      dagName,
      dagRunId,
      ['aborted', 'failed'],
      'local',
      30_000
    );
    expect(['aborted', 'failed']).toContain(stoppedRun.status);
  });

  test('retries a failed DAG run from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-retry-flow');
    const retryFlag = `${stack.stateDir}/retry-flags/${dagName}.flag`;
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
worker_selector:
  role: e2e
steps:
  - name: retry-step
    retry_policy:
      limit: 0
      interval_sec: 0
    run: |
      mkdir -p "${stack.stateDir}/retry-flags"
      if [ -f "${retryFlag}" ]; then
        echo "retry succeeded"
        exit 0
      fi
      touch "${retryFlag}"
      echo "retry failed"
      exit 1
`
    );

    await waitForDAGAvailable(request, token, fileName);
    const dagRunId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, dagRunId, ['failed'], 'local', 30_000);

    await page.goto(`/dag-runs/${dagName}/${dagRunId}`);
    await page.getByRole('button', { name: 'Retry', exact: true }).first().click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Retry' }).click();

    await waitForRunStatus(request, token, dagName, dagRunId, ['succeeded'], 'local', 30_000);
    await expect
      .poll(() => getStepStdout(request, token, dagName, dagRunId, 'retry-step'), {
        timeout: 15_000,
      })
      .toContain('retry succeeded');
  });

  test('reschedules a completed queue-backed DAG run from the UI', async ({ page, request }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    const dagName = uniqueName('e2e-reschedule-flow');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
queue: ${stack.queues.shared}
worker_selector:
  role: e2e
steps:
  - name: reschedule-step
    run: echo "reschedule complete"
`
    );

    await waitForDAGAvailable(request, token, fileName);
    const originalRunId = await startDAG(request, token, fileName);
    await waitForRunStatus(
      request,
      token,
      dagName,
      originalRunId,
      ['succeeded'],
      'local',
      30_000
    );

    const newRunId = uniqueName('rescheduled-run');
    await page.goto(`/dag-runs/${dagName}/${originalRunId}`);
    await page.getByRole('button', { name: 'Retry', exact: true }).first().click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('Reschedule with new DAG-run').click();
    await dialog.getByLabel('New DAG-Run ID (optional)').fill(newRunId);
    await dialog.getByRole('button', { name: 'Reschedule' }).click();

    await expect
      .poll(
        async () => {
          const runs = await listDAGRuns(request, token, dagName);
          return runs.some((run) => run.dagRunId === newRunId);
        },
        {
          timeout: 30_000,
        }
      )
      .toBeTruthy();

    const rescheduledRun = await waitForRunStatus(
      request,
      token,
      dagName,
      newRunId,
      ['succeeded'],
      'local',
      30_000
    );
    expect(rescheduledRun.dagRunId).toBe(newRunId);

    const latestRun = await getDAGRun(request, token, dagName, newRunId);
    expect(latestRun.dagRunId).toBe(newRunId);
  });
});
