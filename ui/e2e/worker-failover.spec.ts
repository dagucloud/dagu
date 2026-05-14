// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  enqueueDAG,
  getWorkers,
  listDAGRuns,
  loadStack,
  loginViaAPI,
  loginViaUI,
  startService,
  stopService,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  waitForWorkerSet,
  writeLocalDAG,
} from './helpers/e2e';

test('balances work across two workers and keeps the surviving worker serving after a crash', async ({
  page,
  request,
}) => {
  test.slow();

  const stack = await loadStack();
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  await waitForWorkerSet(request, token, stack.workers);

  const dagName = uniqueName('e2e-worker-balance');
  const fileName = await writeLocalDAG(
    dagName,
    `
name: ${dagName}
queue: ${stack.queues.balance}
worker_selector:
  role: e2e
steps:
  - name: long-step
    run: sleep 12
`
  );
  await waitForDAGAvailable(request, token, fileName);

  const firstRunId = await enqueueDAG(request, token, fileName);
  const secondRunId = await enqueueDAG(request, token, fileName);

  let activeWorkerIds: string[] = [];

  await expect
    .poll(
      async () => {
        const workers = await getWorkers(request, token);
        activeWorkerIds = workers.workers
          .filter((worker) =>
            worker.runningTasks.some((task) => task.dagName === dagName)
          )
          .map((worker) => worker.id)
          .sort();
        return activeWorkerIds.join(',');
      },
      {
        timeout: 45_000,
      }
    )
    .toBe(stack.workers.slice().sort().join(','));

  await page.goto('/system-status');
  await expect(page.getByText(activeWorkerIds[0])).toBeVisible();
  await expect(page.getByText(activeWorkerIds[1])).toBeVisible();

  const failedWorkerId = activeWorkerIds[0];
  const survivorWorkerId = activeWorkerIds[1];

  try {
    await expect
      .poll(
        async () => {
          const runs = await listDAGRuns(request, token, dagName);
          const firstRun = runs.find((run) => run.dagRunId === firstRunId);
          const secondRun = runs.find((run) => run.dagRunId === secondRunId);
          return firstRun?.status === 'succeeded' && secondRun?.status === 'succeeded';
        },
        {
          timeout: 30_000,
        }
      )
      .toBeTruthy();

    await stopService(failedWorkerId, 'KILL');
    await page.waitForTimeout(1_000);

    const recoveryDagName = uniqueName('e2e-worker-recovery');
    const recoveryFileName = await writeLocalDAG(
      recoveryDagName,
      `
name: ${recoveryDagName}
queue: ${stack.queues.balance}
worker_selector:
  role: e2e
steps:
  - name: survivor-step
    run: echo "survivor worker ok"
`
    );

    await waitForDAGAvailable(request, token, recoveryFileName);
    const recoveryRunId = await enqueueDAG(request, token, recoveryFileName);
    let recoveryWorkerId = '';
    await expect
      .poll(
        async () => {
          const runs = await listDAGRuns(request, token, recoveryDagName);
          const recoveryRun = runs.find((run) => run.dagRunId === recoveryRunId);
          recoveryWorkerId = recoveryRun?.workerId ?? '';
          return recoveryRun?.status === 'succeeded';
        },
        {
          timeout: 30_000,
        }
      )
      .toBeTruthy();

    expect(recoveryWorkerId).toBe(survivorWorkerId);
  } finally {
    await startService(failedWorkerId);
    await waitForWorkerSet(request, token, stack.workers, 30_000);
  }
});
