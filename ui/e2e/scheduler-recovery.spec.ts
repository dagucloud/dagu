// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  listDAGRuns,
  loadStack,
  loginViaAPI,
  startService,
  stopService,
  waitForSchedulerDAGRegistered,
  uniqueName,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

test('runs an overdue scheduled DAG after the scheduler is restarted', async ({ request }) => {
  test.slow();

  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );

  try {
    const dagName = uniqueName('e2e-schedule-recovery');
    const scheduledDate = nextMinuteBoundary(30_000);
    const scheduledAt = formatOneOffScheduleAt(scheduledDate);
    const fileName = await writeLocalDAG(
      dagName,
      `
name: ${dagName}
schedule:
  start:
    - at: "${scheduledAt}"
worker_selector:
  role: e2e
steps:
  - name: scheduled-step
    run: echo "scheduled recovery ok"
`
    );

    await waitForSchedulerDAGRegistered(dagName, 30_000);

    await stopService('scheduler');

    const waitUntilDueMs = Math.max(scheduledDate.getTime() - Date.now() + 2_000, 0);
    await new Promise((resolve) => setTimeout(resolve, waitUntilDueMs));
    expect((await listDAGRuns(request, token, dagName)).length).toBe(0);

    await startService('scheduler');

    let scheduledRunId = '';
    await expect
      .poll(
        async () => {
          const runs = await listDAGRuns(request, token, dagName);
          scheduledRunId = runs[0]?.dagRunId ?? '';
          return runs.length;
        },
        {
          timeout: 30_000,
        }
      )
      .toBe(1);

    await waitForRunStatus(
      request,
      token,
      dagName,
      scheduledRunId,
      ['succeeded'],
      'local',
      30_000
    );
  } finally {
    await startService('scheduler');
  }
});

function nextMinuteBoundary(minLeadMs: number): Date {
  const boundary = new Date();
  boundary.setSeconds(0, 0);
  boundary.setMinutes(boundary.getMinutes() + 1);

  while (boundary.getTime() - Date.now() < minLeadMs) {
    boundary.setMinutes(boundary.getMinutes() + 1);
  }

  return boundary;
}

function formatOneOffScheduleAt(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hours = String(date.getHours()).padStart(2, '0');
  const minutes = String(date.getMinutes()).padStart(2, '0');
  const offsetMinutes = -date.getTimezoneOffset();
  const sign = offsetMinutes >= 0 ? '+' : '-';
  const absOffsetMinutes = Math.abs(offsetMinutes);
  const offsetHours = String(Math.floor(absOffsetMinutes / 60)).padStart(2, '0');
  const offsetRemainderMinutes = String(absOffsetMinutes % 60).padStart(2, '0');

  return `${year}-${month}-${day}T${hours}:${minutes}:00${sign}${offsetHours}:${offsetRemainderMinutes}`;
}
