// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext } from '@playwright/test';
import {
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  writeLocalDAG,
} from '../helpers/e2e';
import { adminToken, expect, signIn, test } from './fixtures';

// Spec 083: a run with a continuous step has a Continuous tab in the Run
// panel. The tab does not depend on recorded samples: its first state, with
// no sample yet, is the empty state.

// The heartbeat waits for the gate, so it has recorded no sample while the
// gate runs. The run has a continuous step and a step that is not.
const GATED_HEARTBEAT = `
steps:
  - name: gate
    run: sleep 60
  - name: heartbeat
    depends: [gate]
    run: echo beat
    repeat_policy:
      repeat: while
      interval_sec: 1
`;

const PLAIN = `
steps:
  - name: hello
    run: echo hello
`;

function headers(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}` };
}

test.describe('workspace continuous tab', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1024,
    'the dock shows every tab inline from 1024px'
  );

  const created: string[] = [];
  const started: Array<{ dagName: string; runId: string }> = [];

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test.afterEach(async ({ request }) => {
    const token = await adminToken(request);
    for (const { dagName, runId } of started.splice(0)) {
      await request.post(
        `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(
          runId
        )}/stop?remoteNode=local`,
        { headers: headers(token) }
      );
    }
    for (const fileName of created.splice(0)) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: headers(token) }
      );
    }
  });

  async function startRun(
    request: APIRequestContext,
    spec: string
  ): Promise<{ dagName: string; fileName: string; runId: string }> {
    const token = await adminToken(request);
    const dagName = uniqueName('e2e-ws-cont');
    const fileName = await writeLocalDAG(dagName, spec);
    created.push(fileName);
    await waitForDAGAvailable(request, token, fileName);
    const runId = await startDAG(request, token, fileName);
    started.push({ dagName, runId });
    return { dagName, fileName, runId };
  }

  test('shows the tab and the empty state for a step with no sample', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { fileName, runId } = await startRun(request, GATED_HEARTBEAT);

    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(runId)}&dock=continuous`
    );
    const dock = page.getByTestId('ww-dock');
    const tab = dock.getByRole('tab', { name: 'Continuous', exact: true });
    await expect(tab).toBeVisible({ timeout: 30_000 });
    await expect(tab).toHaveAttribute('aria-selected', 'true');

    const panel = dock.getByTestId('continuous-panel');
    await expect(panel).toBeVisible({ timeout: 30_000 });
    await expect(panel).toHaveAttribute('data-step', 'heartbeat');
    await expect(panel.getByTestId('continuous-empty')).toHaveText(
      'No samples yet'
    );
    // The chart draws nothing for no bucket.
    await expect(panel.getByTestId('continuous-chart')).toHaveCount(0);
  });

  test('has no Continuous tab for a run without a continuous step', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { fileName, runId } = await startRun(request, PLAIN);

    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(runId)}&dock=log`
    );
    const dock = page.getByTestId('ww-dock');
    await expect(
      dock.getByRole('tab', { name: 'Log', exact: true })
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      dock.getByRole('tab', { name: 'Continuous', exact: true })
    ).toHaveCount(0);
  });
});
