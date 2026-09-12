// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
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

  test('reads parallel output without losing the selected step or scroll position', async ({ page, request }, testInfo) => {
    const stack = await loadStack();
    const token = await loginViaAPI(request, stack.auth.adminUsername, stack.auth.adminPassword);
    const dagName = uniqueName('e2e-parallel-output');
    const releaseFirst = `${stack.stateDir}/${dagName}-first`;
    const releaseSecond = `${stack.stateDir}/${dagName}-second`;
    const fileName = await writeLocalDAG(dagName, `
name: ${dagName}
type: graph
steps:
  - id: first
    run: |
      i=0
      while [ "$i" -lt 150 ]; do
        echo "first line $i"
        i=$((i + 1))
      done
      while [ ! -f "${releaseFirst}" ]; do
        echo "first tick $i"
        i=$((i + 1))
        sleep 0.2
      done
      echo "first final"
  - id: second
    run: |
      echo "second started"
      while [ ! -f "${releaseSecond}" ]; do sleep 0.2; done
      echo "second final"
      echo "second error" >&2
      exit 1
`);
    await waitForDAGAvailable(request, token, fileName);
    const runId = uniqueName('parallel-run');
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto(`/dags/${encodeURIComponent(fileName)}`);
    await page.getByRole('button', { name: 'Start', exact: true }).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('DAG-Run ID (optional)').fill(runId);
    await dialog.getByRole('button', { name: 'Start', exact: true }).click();
    await expect
      .poll(() => new URL(page.url()).pathname)
      .toBe(`/dags/${encodeURIComponent(fileName)}`);

    const progressDialog = page.getByRole('dialog', { name: 'Run progress' });
    await expect(progressDialog).toBeVisible();
    const output = progressDialog.getByRole('region', { name: 'Run output', exact: true });
    const log = output.getByRole('region', { name: 'Step output', exact: true });
    try {
      await expect(output.getByText('2 running', { exact: true })).toBeVisible();
      await expect(log).toContainText('first tick');
      await expect.poll(() => log.evaluate((element) =>
        element.scrollHeight - element.scrollTop - element.clientHeight
      )).toBeLessThanOrEqual(4);
      await log.hover();
      await page.mouse.wheel(0, -200);
      await expect(output.getByText('New output available')).toBeVisible();
      const snapshot = await log.innerText();
      const scrollTop = await log.evaluate((element) => element.scrollTop);
      await writeFile(releaseFirst, '');
      await expect(output.getByRole('tab', { name: /first/ })).toContainText('succeeded');
      await expect(log).toHaveText(snapshot, { useInnerText: true });
      expect(await log.evaluate((element) => element.scrollTop)).toBe(scrollTop);
      await page.screenshot({ path: testInfo.outputPath('parallel-desktop.png'), fullPage: true });

      await output.getByRole('button', { name: 'Back to live' }).click();
      await expect(log).toContainText('second started');
      await output.getByRole('tab', { name: /first/ }).focus();
      await page.keyboard.press('ArrowDown');
      const secondTab = output.getByRole('tab', { name: /second/ });
      await expect(secondTab).toBeFocused();
      await writeFile(releaseSecond, '');
      await expect(log).toContainText('second final');
      await expect(output.getByRole('button', { name: '1 failed' })).toBeVisible();
      await expect(secondTab).toBeFocused();
      await output.getByRole('button', { name: 'stderr', exact: true }).click();
      await expect(log).toContainText('second error');

      await page.setViewportSize({ width: 390, height: 844 });
      await expect(output.getByLabel('Step', { exact: true })).toBeVisible();
      await output.getByLabel('Step', { exact: true }).selectOption('first');
      await output.getByRole('button', { name: 'stdout', exact: true }).click();
      await expect(log).toContainText('first final');
      await log.getByText('first final', { exact: true }).scrollIntoViewIfNeeded();
      await expect(log.getByText('first final', { exact: true })).toBeInViewport();
      expect(await output.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath('parallel-mobile.png'), fullPage: true });
      await progressDialog.getByRole('button', { name: 'View details' }).click();
      await expect(page).toHaveURL(
        new RegExp(`/dag-runs/${dagName}/${runId}\\?remoteNode=local`)
      );
    } finally {
      await Promise.all([writeFile(releaseFirst, ''), writeFile(releaseSecond, '')]);
    }
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
      .poll(
        () =>
          getStepStdout(
            request,
            token,
            dagName,
            dagRunId,
            'retry-step',
            { retryNotFound: true }
          ),
        {
          timeout: 15_000,
        }
      )
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
