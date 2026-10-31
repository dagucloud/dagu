// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import { readFile, writeFile } from 'node:fs/promises';
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

  test('downloads step logs as an authenticated ZIP', async ({
    page,
    context,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-log-zip');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: output
    run: echo archive-output
`
    );
    await waitForDAGAvailable(request, token, fileName);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await page.goto(
      `/dags/${encodeURIComponent(fileName)}/dagRun-log?dagRunId=${runId}`
    );
    const runURL = page.url();
    const navigation = context.waitForEvent(
      'request',
      (req) =>
        req.method() === 'POST' &&
        new URL(req.url()).pathname.endsWith('/steps/log/download')
    );
    const downloaded = page.waitForEvent('download');
    await page
      .getByRole('button', { name: 'Download step logs (ZIP)' })
      .click();
    const download = await downloaded;
    expect((await navigation).isNavigationRequest()).toBe(true);
    expect(download.suggestedFilename()).toBe(`${dagName}-${runId}-steps.zip`);
    const path = await download.path();
    expect(path).not.toBeNull();
    const bytes = await readFile(path!);
    expect(bytes.subarray(0, 4).toString('hex')).toBe('504b0304');
    expect(bytes.includes(Buffer.from('001-output/stdout.log'))).toBe(true);
    expect(bytes.subarray(-22, -18).toString('hex')).toBe('504b0506');
    await expect(page).toHaveURL(runURL);
    await expect(
      page.getByText('Download requested. Check your browser downloads.')
    ).toBeVisible();

    // A rejected form submission must leave the run page usable.
    await context.route('**/steps/log/download?*', async (route) => {
      await route.continue({ postData: 'token=invalid-token' });
    });
    const rejected = context.waitForEvent('page');
    await page
      .getByRole('button', { name: 'Download step logs (ZIP)' })
      .click();
    const errorPage = await rejected;
    await errorPage.waitForLoadState('domcontentloaded');
    expect(await errorPage.locator('body').innerText()).toMatch(
      /unauthorized/i
    );
    await expect(page).toHaveURL(runURL);
    await errorPage.close();
  });

  test('pages a large log and downloads its complete contents', async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-log-pages');
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
steps:
  - name: output
    run: |
      awk 'BEGIN { for (i = 1; i <= 100000; i++) printf "line %06d\\n", i }'
`
    );
    await waitForDAGAvailable(request, token, fileName);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await page.emulateMedia({ reducedMotion: 'reduce' });

    const waitForLog = (params: Record<string, number>) =>
      page.waitForResponse((response) => {
        const url = new URL(response.url());
        return (
          url.pathname.endsWith(`/steps/output/log`) &&
          Object.entries(params).every(
            ([name, value]) => url.searchParams.get(name) === String(value)
          )
        );
      });
    const initialLog = waitForLog({ tail: 1000 });
    await page.goto(
      `/dags/${encodeURIComponent(fileName)}/log?dagRunId=${runId}&step=output`
    );
    expect((await (await initialLog).json()).lineCount).toBe(1000);
    const output = page.getByRole('region', {
      name: 'Step output',
      exact: true,
    });
    await expect(output).toContainText('line 100000');

    const customLog = waitForLog({ tail: 1234 });
    await page
      .getByLabel('Lines per page', { exact: true })
      .selectOption('custom');
    const customSize = page.getByLabel('Custom lines per page', {
      exact: true,
    });
    await customSize.fill('1234');
    await customSize.press('Enter');
    expect((await (await customLog).json()).lineCount).toBe(1234);

    await page.getByRole('button', { name: 'Page View', exact: true }).click();
    const pageInput = page.getByLabel('Page', { exact: true });
    await expect(pageInput).toBeEnabled();
    const fourthPage = waitForLog({ offset: 3703, limit: 1234 });
    await pageInput.fill('4');
    await pageInput.press('Enter');
    expect((await (await fourthPage).json()).lineCount).toBe(1234);
    await expect(output).toContainText('line 003703');
    await expect(output).toContainText('line 004936');

    // Leaving an unchanged custom size must preserve the current page.
    await customSize.focus();
    await customSize.blur();
    await expect(pageInput).toHaveValue('4');
    const search = page.getByPlaceholder('Search in loaded lines...');
    await search.fill('line 004936');
    await search.press('Enter');
    await expect(
      output.getByText('line 004936', { exact: true })
    ).toBeInViewport();
    await search.press('Escape');

    const largestPage = waitForLog({ offset: 1, limit: 10000 });
    await customSize.fill('100000');
    await customSize.press('Enter');
    expect((await (await largestPage).json()).lineCount).toBe(10000);
    await expect(customSize).toHaveValue('10000');
    await expect(pageInput).toHaveValue('1');
    await expect(output).toContainText('line 010000');

    const lastPage = waitForLog({ offset: 90001, limit: 10000 });
    const jump = page.getByLabel('Jump to line:', { exact: true });
    await jump.fill('100000');
    await jump.press('Enter');
    expect((await (await lastPage).json()).lineCount).toBe(10000);
    await expect(pageInput).toHaveValue('10');
    await expect(
      output.getByText('line 100000', { exact: true })
    ).toBeInViewport();

    await pageInput.fill('10.9');
    await pageInput.press('Enter');
    await expect(pageInput).toHaveValue('10');

    const downloaded = page.waitForEvent('download');
    await page.getByTitle('Download full log', { exact: true }).click();
    const download = await downloaded;
    const path = await download.path();
    expect(path).not.toBeNull();
    const contents = await readFile(path!, 'utf8');
    expect(contents.trimEnd().split('\n')).toHaveLength(100000);
    expect(contents).toMatch(/^line 000001\n/);
    expect(contents).toMatch(/line 100000\n$/);
  });

  test('opens the run of the progress card on the workflow page', async ({
    page,
    request,
  }) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-parallel-output');
    const releaseFirst = `${stack.stateDir}/${dagName}-first`;
    const releaseSecond = `${stack.stateDir}/${dagName}-second`;
    const fileName = await writeLocalDAG(
      dagName,
      `
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
`
    );
    await waitForDAGAvailable(request, token, fileName);
    const runId = uniqueName('parallel-run');
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto(`/dags/${encodeURIComponent(fileName)}`);
    await page
      .getByRole('button', { name: 'Start', exact: true })
      .first()
      .click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Run ID (optional)').fill(runId);
    await dialog.getByRole('button', { name: 'Start', exact: true }).click();
    // Start selects the new run in the one tab of the workflow.
    await expect
      .poll(() => {
        const url = new URL(page.url());
        return [url.pathname, url.searchParams.get('run')];
      })
      .toEqual([`/dags/${encodeURIComponent(fileName)}`, runId]);

    await expect(
      page.getByRole('dialog', { name: 'Run progress' })
    ).toHaveCount(0);
    const progressCard = page.getByRole('button', {
      name: `Open run progress for ${runId}`,
    });
    await expect(progressCard).toBeVisible();
    // A click on the card opens no dialog: it removes the card and shows the
    // run on the page of the workflow.
    await progressCard.click();
    await expect(progressCard).toHaveCount(0);
    await expect(
      page.getByRole('dialog', { name: 'Run progress' })
    ).toHaveCount(0);
    await expect
      .poll(() => {
        const url = new URL(page.url());
        return [
          url.pathname,
          url.searchParams.get('remoteNode'),
          url.searchParams.get('run'),
        ];
      })
      .toEqual([`/dags/${encodeURIComponent(fileName)}`, 'local', runId]);
    try {
      await expect(
        page.getByTestId('ww-run-chip').getByTestId('ww-run-progress')
      ).toBeVisible({ timeout: 30_000 });
    } finally {
      await Promise.all([
        writeFile(releaseFirst, ''),
        writeFile(releaseSecond, ''),
      ]);
    }
  });

  test('stops a running distributed DAG run from the UI', async ({
    page,
    request,
  }) => {
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
    await waitForRunStatus(
      request,
      token,
      dagName,
      dagRunId,
      ['running'],
      'local',
      30_000
    );

    // The old run page redirects to the workflow page with the run.
    await page.goto(`/dag-runs/${dagName}/${dagRunId}`);
    await page
      .getByTestId('ww-run-actions')
      .getByRole('button', { name: 'Stop', exact: true })
      .click();

    const dialog = page.getByRole('dialog', { name: 'Stop this run?' });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Stop', exact: true }).click();

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
    await waitForRunStatus(
      request,
      token,
      dagName,
      dagRunId,
      ['failed'],
      'local',
      30_000
    );

    await page.goto(`/dag-runs/${dagName}/${dagRunId}`);
    await page
      .getByRole('button', { name: 'Retry', exact: true })
      .first()
      .click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Retry' }).click();

    await waitForRunStatus(
      request,
      token,
      dagName,
      dagRunId,
      ['succeeded'],
      'local',
      30_000
    );
    await expect
      .poll(
        () =>
          getStepStdout(request, token, dagName, dagRunId, 'retry-step', {
            retryNotFound: true,
          }),
        {
          timeout: 15_000,
        }
      )
      .toContain('retry succeeded');
  });

  test('reschedules a completed queue-backed DAG run from the UI', async ({
    page,
    request,
  }) => {
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
    await page
      .getByRole('button', { name: 'Retry', exact: true })
      .first()
      .click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByLabel('Reschedule as a new run').click();
    await dialog.getByLabel('New run ID (optional)').fill(newRunId);
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
