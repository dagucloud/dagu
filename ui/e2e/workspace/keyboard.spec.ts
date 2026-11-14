// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext, Locator, Page } from '@playwright/test';
import {
  listDAGRuns,
  loadStack,
  loginViaAPI,
  startDAG,
  waitForRunStatus,
} from '../helpers/e2e';
import { expect, signIn, test } from './fixtures';

const STEP = 'prepare';
const MAX_TABS = 150;

/** Presses Tab until the target has the focus. Fails after MAX_TABS presses. */
async function tabTo(page: Page, target: Locator): Promise<void> {
  for (let i = 0; i < MAX_TABS; i++) {
    if (await target.evaluate((element) => element === document.activeElement))
      return;
    await page.keyboard.press('Tab');
  }
  await expect(target).toBeFocused();
}

/** Signs in and opens a DAG that has a run: Run step reuses its outputs. */
async function startWithRun(
  page: Page,
  request: APIRequestContext,
  fixtureDAG: (
    file: string,
    prefix?: string
  ) => Promise<{
    dagName: string;
    fileName: string;
  }>
) {
  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  const { dagName, fileName } = await fixtureDAG(
    'visual-params.yaml',
    'e2e-ws-keys'
  );
  const firstRun = await startDAG(request, token, fileName);
  await waitForRunStatus(
    request,
    token,
    dagName,
    firstRun,
    ['succeeded', 'partially_succeeded', 'failed'],
    'local',
    90_000
  );
  await signIn(page);
  return { token, dagName, fileName };
}

/** Confirms a run dialog with the keyboard and waits for the new run. */
async function confirmRunStep(
  page: Page,
  request: APIRequestContext,
  token: string,
  dagName: string
): Promise<void> {
  const runsBefore = new Set(
    (await listDAGRuns(request, token, dagName)).map((run) => run.dagRunId)
  );
  const confirm = page.getByRole('dialog', { name: 'Run step' });
  await expect(confirm).toBeVisible();
  // The dialog has Cancel, Step only and Run (Changed steps shows only when
  // an earlier step changed): tab to Run.
  await tabTo(page, confirm.getByRole('button', { name: 'Run' }));
  await page.keyboard.press('Enter');
  await expect(confirm).toBeHidden({ timeout: 30_000 });
  let started: string[] = [];
  await expect
    .poll(
      async () => {
        started = (await listDAGRuns(request, token, dagName))
          .map((run) => run.dagRunId)
          .filter((id) => !runsBefore.has(id));
        return started.length;
      },
      { timeout: 60_000 }
    )
    .toBe(1);
  // The one tab stays on its page: ?run names the new run, not the earlier
  // one, and the dock is not opened by the run.
  await expect
    .poll(() => new URL(page.url()).searchParams.get('run'), {
      timeout: 30_000,
    })
    .toBe(started[0]);
  await expect(page.getByTestId('ww-pane')).toHaveCount(1);
}

/** 'l' opens the dock on the log of the selected step. */
async function openStepLogWithL(page: Page): Promise<void> {
  // The step that the toolbar ran is selected: ?step names it.
  await expect(page).toHaveURL(/[?&]step=/);
  const dock = page.getByTestId('ww-dock');
  await expect(dock).toHaveAttribute('data-state', 'collapsed');
  await page.keyboard.press('l');
  await expect(dock).toHaveAttribute('data-state', 'open');
  await expect(dock.getByTestId('ww-step-log')).toBeVisible({
    timeout: 30_000,
  });
  await expect(page).toHaveURL(/[?&]dock=log(&|$)/);
}

test.describe('workspace keyboard', () => {
  // The node view needs the desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1024,
    'desktop layout only'
  );

  test('runs a step and reads its log without the mouse', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const { token, dagName, fileName } = await startWithRun(
      page,
      request,
      fixtureDAG
    );
    await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
    const canvas = page.getByRole('region', { name: 'Workflow canvas' });
    await expect(canvas).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
    ).toBeVisible({ timeout: 30_000 });

    // Ctrl+K: 'Go to step <name>' selects the step.
    await page.keyboard.press('ControlOrMeta+k');
    const palette = page.getByRole('dialog', { name: 'Command palette' });
    await expect(palette).toBeVisible();
    await page.keyboard.type(`Go to step ${STEP}`);
    const command = palette.getByRole('option', {
      name: new RegExp(`Go to step:? ${STEP}$`),
    });
    await expect(command).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Enter');
    await expect(palette).toBeHidden();
    await expect(page).toHaveURL(new RegExp(`[?&]step=${STEP}(&|$)`));

    // Enter on the canvas opens the node view of the selected step.
    await tabTo(page, canvas);
    await page.keyboard.press('Enter');
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect(page).toHaveURL(/[?&]ndv=expanded(&|$)/);

    // Esc closes the node view and the focus comes back, visible, to the
    // canvas.
    await page.keyboard.press('Escape');
    await expect(ndv).toBeHidden();
    await expect
      .poll(() =>
        canvas.evaluate((element) => {
          const active = document.activeElement;
          return (
            active instanceof HTMLElement &&
            element.contains(active) &&
            active.matches(':focus-visible')
          );
        })
      )
      .toBe(true);

    // Tab reaches the hover toolbar of the selected step: its Run step button
    // asks to confirm.
    const runButton = page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]')
      .getByRole('button', { name: 'Run step', exact: true });
    await tabTo(page, runButton);
    await page.keyboard.press('Enter');
    await confirmRunStep(page, request, token, dagName);

    // The run shows on the same page: 'l' opens the dock on the log of the
    // step.
    await openStepLogWithL(page);
  });

  // Run step in the node view stays on the editor: the node view stays
  // open, and Escape still closes it.
  test('keeps the node view and its keys after Run step', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const { token, dagName, fileName } = await startWithRun(
      page,
      request,
      fixtureDAG
    );
    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?step=${STEP}&ndv=expanded`
    );
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await tabTo(page, ndv.getByRole('button', { name: 'Run step' }));
    await page.keyboard.press('Enter');
    await confirmRunStep(page, request, token, dagName);
    await expect(page).not.toHaveURL(/\/executions/);
    await expect(ndv).toBeVisible();
    await expect(
      ndv.getByRole('region', { name: 'Output', exact: true })
    ).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(ndv).toBeHidden();
  });
});
