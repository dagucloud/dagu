// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { startDAG, waitForRunStatus } from '../helpers/e2e';
import { adminToken, expect, signIn, test } from './fixtures';

const DONE = ['succeeded', 'partially_succeeded'] as const;

// The workflow page on a phone shows one view at a time: the views bar at
// the bottom switches between the graph, the Activity panel and the YAML.
// The core flows work from it: pick a run, open the log of a step, read the
// YAML and go back to the graph.
test.describe('workspace on a phone', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) >= 768,
    'phone layout only'
  );

  test('switches the views and reads a run from the views bar', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    await signIn(page);
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const older = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, older, [...DONE]);
    const newer = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, newer, [...DONE]);

    await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
    const nav = page.getByTestId('ww-phone-nav');
    const node = page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]');
    await expect(node).toBeVisible({ timeout: 30_000 });

    // Activity takes the place of the graph.
    await nav.getByRole('button', { name: 'Activity', exact: true }).click();
    const dock = page.getByTestId('ww-dock');
    await expect(dock).toHaveAttribute('data-state', 'open');
    await expect(node).toBeHidden();

    // The run picker lists the runs newest first: the second is the older.
    await dock.getByTestId('ww-run-picker').click();
    await page.getByRole('menuitemradio').nth(1).click();
    await expect(page).toHaveURL(new RegExp(`[?&]run=${older}(&|$)`));

    // The step list opens over the tabs; a step opens its log.
    await dock.getByRole('button', { name: /^Run summary/ }).click();
    const steps = dock.getByRole('list', { name: 'Run steps' });
    await steps.getByRole('button', { name: 'prepare', exact: true }).click();
    await expect(page).toHaveURL(/[?&]step=prepare(&|$)/);
    await expect(steps).toBeHidden();
    await expect(dock.getByTestId('ww-step-log')).toBeVisible({
      timeout: 30_000,
    });

    // The YAML replaces the panel.
    await nav.getByRole('button', { name: 'YAML', exact: true }).click();
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
    await expect(page).toHaveURL(/[?&]ndv=docked(&|$)/);

    // And the graph comes back.
    await nav.getByRole('button', { name: 'Graph', exact: true }).click();
    await expect(page).not.toHaveURL(/[?&]ndv=/);
    await expect(node).toBeVisible();
  });

  // A link to a run opens the Activity panel on it, in place of the graph,
  // with the run named in the run picker.
  test('opens a run link on the Activity view', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    await signIn(page);
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [...DONE]);

    const base = `/dags/${encodeURIComponent(fileName)}`;
    await gotoWorkspace(page, `${base}/executions?run=${runId}`);
    await expect(page).toHaveURL(new RegExp(`[?&]run=${runId}(&|$)`));
    const dock = page.getByTestId('ww-dock');
    await expect(dock).toHaveAttribute('data-state', 'open', {
      timeout: 30_000,
    });
    await expect(dock.getByTestId('ww-run-picker')).toContainText(
      runId.slice(0, 8),
      { timeout: 30_000 }
    );
    await expect(page.getByTestId('ww-pane')).toBeHidden();
  });
});
