// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { adminToken, expect, expectAt, signIn, test } from './fixtures';

// The History tab of the Run panel: the steps by run without a step, and the
// runs of one step with a step. Both read the runs of the run timeline.

// The second step fails when FAIL is 1; its log says how it went.
const SPEC = `
params:
  - FAIL=0
steps:
  - name: prepare
    run: echo prepare
  - name: check
    depends: [prepare]
    run: |
      if [ "$FAIL" = "1" ]; then echo "check failed" >&2; exit 1; fi
      echo "check passed"
`;

// The canvas key of the step 'check': the second step of the file.
const CHECK_NODE = '[data-flow-node="steps[1]"]';

test.describe('workspace step history', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1024,
    'the dock shows its tabs inline beside the canvas from 1024px'
  );

  const created: string[] = [];

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test.afterEach(async ({ request }) => {
    const token = await adminToken(request);
    for (const fileName of created.splice(0)) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  });

  test('shows the steps by run and the runs of one step', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const dagName = uniqueName('e2e-ws-history');
    const fileName = await writeLocalDAG(dagName, SPEC);
    created.push(fileName);
    await waitForDAGAvailable(request, token, fileName);
    const passed = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, passed, ['succeeded']);
    const failed = await startDAG(request, token, fileName, {
      params: 'FAIL=1',
    });
    await waitForRunStatus(request, token, dagName, failed, ['failed']);
    const base = `/dags/${encodeURIComponent(fileName)}`;

    await gotoWorkspace(page, `${base}?dock=history`);
    const dock = page.getByTestId('ww-dock');
    await expect(dock).toHaveAttribute('data-state', 'open', {
      timeout: 30_000,
    });
    await expect(
      dock.getByRole('tab', { name: 'History', exact: true })
    ).toHaveAttribute('aria-selected', 'true');

    // The grid: a row for each step, a cell for each run, named by its state.
    const grid = dock.getByRole('grid', { name: 'Steps by run' });
    await expect(grid).toBeVisible({ timeout: 30_000 });
    await expect(grid.getByRole('rowheader')).toHaveText(['prepare', 'check']);
    const passedCell = dock.getByTestId(`ww-history-cell-check-${passed}`);
    const failedCell = dock.getByTestId(`ww-history-cell-check-${failed}`);
    await expect(passedCell).toHaveAttribute('data-state', 'ran');
    await expect(passedCell).toHaveAccessibleName(/^check, Run .*, Succeeded/);
    await expect(failedCell).toHaveAccessibleName(/^check, Run .*, Failed/);

    // A hover lights the node of the step on the canvas and selects nothing.
    const checkNode = page.getByTestId('ww-canvas').locator(CHECK_NODE);
    await passedCell.hover();
    await expect(checkNode).toHaveAttribute('data-spotlit', '');
    await dock.getByRole('tab', { name: 'History', exact: true }).hover();
    await expect(checkNode).not.toHaveAttribute('data-spotlit', '');
    await expectAt(page, base, { step: null, dock: 'history' });

    // A cell selects its run and its step: the tab shows the runs of the step.
    await failedCell.click();
    await expectAt(page, base, {
      run: failed,
      step: 'check',
      dock: 'history',
    });
    const history = dock.getByRole('region', { name: 'History of check' });
    await expect(history.getByTestId('ww-step-history-summary')).toContainText(
      '50% succeeded',
      { timeout: 30_000 }
    );
    await expect(
      history.getByTestId(`ww-step-history-bar-${failed}`)
    ).toHaveAttribute('aria-selected', 'true');

    // Log selects the run and opens the log of the step in it.
    await history
      .getByRole('button', {
        name: `Open the log of run ${passed.slice(0, 8)}`,
      })
      .click();
    await expectAt(page, base, { run: passed, step: 'check', dock: 'log' });
    await expect(
      dock.getByRole('tab', { name: 'Log', exact: true })
    ).toHaveAttribute('aria-selected', 'true');
    const log = dock.getByTestId('ww-step-run');
    await expect(log).toHaveAttribute('data-step', 'check');
    await expect(log).toContainText('check passed', { timeout: 30_000 });
  });
});
