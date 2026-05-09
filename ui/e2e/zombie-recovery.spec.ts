// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  getLatestLocalRunStatusRecord,
  killProcess,
  loadStack,
  loginViaAPI,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

test('marks a local run failed after its runner process is hard-killed', async ({ request }) => {
  test.slow();

  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );

  const dagName = uniqueName('e2e-zombie-recovery');
  const pidFile = `${stack.stateDir}/zombie-pids/${dagName}.pid`;
  const fileName = await writeLocalDAG(
    dagName,
    `
name: ${dagName}
steps:
  - name: zombie-step
    run: |
      mkdir -p "${stack.stateDir}/zombie-pids"
      echo $$ > "${pidFile}"
      exec sleep 300
`
  );

  await waitForDAGAvailable(request, token, fileName);
  const dagRunId = await startDAG(request, token, fileName);
  await waitForRunStatus(request, token, dagName, dagRunId, ['running'], 'local', 30_000);

  let runnerPid = 0;
  await expect
    .poll(
      async () => {
        const status = await getLatestLocalRunStatusRecord(dagName, dagRunId);
        runnerPid = status?.pid ?? 0;
        return runnerPid;
      },
      {
        timeout: 15_000,
      }
    )
    .toBeGreaterThan(0);

  await killProcess(runnerPid, 'KILL');

  const failedRun = await waitForRunStatus(
    request,
    token,
    dagName,
    dagRunId,
    ['failed'],
    'local',
    30_000
  );
  expect(failedRun.status).toBe('failed');
});
