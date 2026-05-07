// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test, type APIRequestContext } from '@playwright/test';
import { execFile as execFileCallback } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import { promisify } from 'node:util';
import {
  enqueueRunFromUI,
  getStepStdout,
  getWorkers,
  loadStack,
  loginViaAPI,
  loginViaUI,
  waitForQueueCounts,
  waitForWorkerSet,
} from './helpers/e2e';

const DAG_FILE = 'e2e-distributed-queue.yaml';
const DAG_NAME = 'e2e-distributed-queue';
const STEP_NAME = 'hold-and-report';
const EXPECTED_STEP_OUTPUT = 'e2e distributed worker ok';
const RELEASE_FILE_NAME = 'e2e-distributed-queue.release';
const RELEASE_GATE_NAME = 'e2e-distributed-queue.release.fifo';
const execFile = promisify(execFileCallback);

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

test('exercises the web UI against the real distributed shared-nothing worker stack', async ({
  page,
  request,
}) => {
  test.slow();

  const stack = await loadStack();
  const releaseFile = path.join(stack.stateDir, RELEASE_FILE_NAME);
  const releaseGate = path.join(stack.stateDir, RELEASE_GATE_NAME);
  let released = false;
  let completed = false;
  let releaseGateReady = false;

  const releaseRuns = async (): Promise<void> => {
    await fs.mkdir(path.dirname(releaseFile), { recursive: true });
    await fs.writeFile(releaseFile, 'release\n', 'utf8');
    await execFile(
      'sh',
      ['-c', 'exec 3<>"$1"; printf release >&3', 'release-gate', releaseGate],
      { timeout: 30_000 }
    );
    released = true;
  };

  try {
    await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );

    await waitForWorkerSet(request, token, stack.workers);
    await waitForQueueCounts(request, token, stack.queues.shared, {
      runningCount: 0,
      queuedCount: 0,
    });
    await fs.rm(releaseFile, { force: true });
    await fs.rm(releaseGate, { force: true });
    await execFile('mkfifo', [releaseGate]);
    releaseGateReady = true;

    await page.goto('/queues');
    const sharedQueueCard = page.getByRole('link', {
      name: new RegExp(escapeRegExp(stack.queues.shared), 'i'),
    });
    await expect(sharedQueueCard).toBeVisible();
    await expect(sharedQueueCard).toContainText('No activity');

    await page.goto('/system-status');
    await expect(
      page.getByRole('heading', { name: 'System Status' })
    ).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Workers' })).toBeVisible();
    for (const workerId of stack.workers) {
      await expect(page.getByText(workerId)).toBeVisible();
    }
    await expect(page.getByText('role=e2e')).toHaveCount(stack.workers.length);

    await page.goto(`/dags/${DAG_FILE}`);
    await expect(
      page.getByRole('heading', { level: 1, name: DAG_NAME, exact: true })
    ).toBeVisible();

    const firstRunId = await enqueueRunFromUI(page, DAG_FILE);
    const activeWorkerId = await waitForWorkerState(
      request,
      token,
      {
        busyPollers: 1,
        hasTask: true,
      },
      30_000
    );
    await waitForQueueCounts(
      request,
      token,
      stack.queues.shared,
      {
        runningCount: 1,
        queuedCount: 0,
      },
      30_000
    );

    const secondRunId = await enqueueRunFromUI(page, DAG_FILE);
    expect(secondRunId).not.toBe(firstRunId);
    await waitForQueueCounts(
      request,
      token,
      stack.queues.shared,
      {
        runningCount: 1,
        queuedCount: 1,
      },
      30_000
    );

    await page.goto(`/queues/${stack.queues.shared}`);
    await expect(
      page.getByRole('link', { name: stack.queues.shared, exact: true })
    ).toBeVisible();
    await expect(page.getByText('Running (1)')).toBeVisible();
    await expect(page.getByText('Queued (1)')).toBeVisible();
    await expect(
      page.getByRole('button', {
        name: new RegExp(`\\b${escapeRegExp(DAG_NAME)}\\b`, 'i'),
      })
    ).toHaveCount(2);

    await page.goto('/system-status');
    await page.getByText(activeWorkerId).click();
    await expect(page.getByText(DAG_NAME, { exact: true })).toBeVisible();

    await releaseRuns();

    await waitForWorkerState(
      request,
      token,
      {
        busyPollers: 0,
        hasTask: false,
      },
      120_000
    );
    await waitForQueueCounts(
      request,
      token,
      stack.queues.shared,
      {
        runningCount: 0,
        queuedCount: 0,
      },
      120_000
    );

    await page.goto(`/queues/${stack.queues.shared}`);
    await expect(
      page.getByText('No DAG runs are currently executing in this queue.')
    ).toBeVisible();
    await expect(
      page.getByText('No queued items in this queue.')
    ).toBeVisible();

    await expect
      .poll(
        () => getStepStdout(request, token, DAG_NAME, firstRunId, STEP_NAME),
        {
          timeout: 30_000,
        }
      )
      .toContain(EXPECTED_STEP_OUTPUT);
    await expect
      .poll(
        () => getStepStdout(request, token, DAG_NAME, secondRunId, STEP_NAME),
        {
          timeout: 30_000,
        }
      )
      .toContain(EXPECTED_STEP_OUTPUT);
    completed = true;
  } finally {
    if (!released && releaseGateReady) {
      try {
        await releaseRuns();
      } catch {
        // Preserve the original test failure while still running cleanup below.
      }
    }
    if (completed) {
      await fs.rm(releaseFile, { force: true });
      await fs.rm(releaseGate, { force: true });
    }
  }
});

async function waitForWorkerState(
  request: APIRequestContext,
  token: string,
  expected: { busyPollers: number; hasTask: boolean },
  timeout: number
): Promise<string> {
  let activeWorkerId = '';

  await expect
    .poll(
      async () => {
        const workers = await getWorkers(request, token);
        const activeWorker = workers.workers.find((worker) =>
          worker.runningTasks.some((task) => task.dagName === DAG_NAME)
        );
        const busyWorker =
          activeWorker ??
          workers.workers.find((worker) => worker.busyPollers > 0);
        activeWorkerId = activeWorker?.id ?? busyWorker?.id ?? '';

        return {
          busyPollers: busyWorker?.busyPollers ?? 0,
          hasTask: Boolean(activeWorker),
        };
      },
      {
        timeout,
      }
    )
    .toEqual(expected);

  return activeWorkerId;
}
