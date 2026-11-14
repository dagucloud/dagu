// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locator, Page } from '@playwright/test';
import {
  getStepStdout,
  showStepOutput,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import {
  adminToken,
  expect,
  signIn,
  test as base,
  type FixtureDAG,
} from './fixtures';

// parity:test-e2e-gaps (approve, push back and complete a human task from the
// step view of the workspace)

// An approval gate between two steps. A push-back runs the gate again with
// FEEDBACK in its environment. The server needs the required FEEDBACK on
// Approve too, so the Approve dialog asks for it.
const APPROVAL = `
steps:
  - name: build
    run: echo built
  - name: review
    depends: [build]
    run: |
      echo "feedback=\${FEEDBACK:-}"
    approval:
      prompt: Approve the release
      input: [FEEDBACK]
      required: [FEEDBACK]
  - name: publish
    depends: [review]
    run: echo published
`;

// The workflow of e2e/human-task-push-back.spec.ts.
const HUMAN_TASK = `
steps:
  - id: implement
    run: echo "attempt=$DAG_PUSHBACK_ITERATION feedback=$feedback"
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review the implementation
      push_back:
        rewind_to: implement
        form:
          type: object
          required: [feedback]
          properties:
            feedback:
              type: string
              title: Feedback
  - id: publish
    depends: review
    run: echo published
`;

const test = base.extend<{
  /** Writes the YAML under a unique name; the DAG is deleted after the test. */
  inlineDAG: (spec: string, prefix?: string) => Promise<FixtureDAG>;
}>({
  inlineDAG: async ({ request }, use) => {
    const token = await adminToken(request);
    const created: string[] = [];
    await use(async (spec, prefix = 'e2e-ws-actions') => {
      const dagName = uniqueName(prefix);
      const fileName = await writeLocalDAG(dagName, spec);
      created.push(fileName);
      await waitForDAGAvailable(request, token, fileName);
      return { dagName, fileName };
    });
    for (const fileName of created) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  },
});

/**
 * Opens the workflow on a run and opens the step view of a step with a double
 * click on its node: the step view beside the canvas, with the run facts of
 * the step. Returns the manual action block of the step.
 */
async function openStepView(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  fileName: string,
  runId: string,
  step: string,
  nodeKey: string
): Promise<Locator> {
  await gotoWorkspace(
    page,
    `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(runId)}`
  );
  const node = page
    .getByTestId('ww-canvas')
    .getByTestId(`ve-canvas-node-${nodeKey}`);
  await expect(node).toBeVisible({ timeout: 30_000 });
  await node.dblclick();
  const ndv = page.getByTestId('ndv');
  await expect(ndv).toBeVisible({ timeout: 30_000 });
  await expect
    .poll(() => new URL(page.url()).searchParams.get('step'))
    .toBe(step);
  // A narrow step view shows OUTPUT as a tab; a waiting step opens Actions.
  await showStepOutput(ndv);
  return ndv.getByTestId('ww-step-manual-actions');
}

/** Opens the Runs panel when it is collapsed. */
async function openDock(page: Page): Promise<Locator> {
  const dock = page.getByTestId('ww-dock');
  if ((await dock.getAttribute('data-state')) !== 'open') {
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
  }
  await expect(dock).toHaveAttribute('data-state', 'open');
  return dock;
}

test.describe('workspace manual actions', () => {
  // The docked step view and the dock tab strip need the desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // parity:approval-tab parity:run-tab-approval parity:step-review-modal
  // parity:api-manual-actions
  test('counts the waiting approval and approves it from the step view', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await inlineDAG(APPROVAL);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);

    // The Runs panel is collapsed at load. The bar of the editor tells that
    // an action waits; the Actions tab of the panel counts it.
    await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
    await expect(page.getByText('An action is waiting.')).toBeVisible({
      timeout: 30_000,
    });
    const dock = await openDock(page);
    const actionsTab = dock.getByRole('tab', {
      name: 'Actions, 1 waiting',
      exact: true,
    });
    await expect(actionsTab).toBeVisible({ timeout: 30_000 });
    await expect(actionsTab).toContainText('1');

    const actions = await openStepView(
      page,
      gotoWorkspace,
      fileName,
      runId,
      'review',
      'steps[1]'
    );
    await expect(actions).toContainText('Approve the release', {
      timeout: 30_000,
    });
    await actions.getByRole('button', { name: 'Approve', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Approve: review' });
    await expect(dialog).toBeVisible();
    const submit = dialog.getByRole('button', { name: 'Approve', exact: true });
    await expect(submit).toBeDisabled();
    await dialog.getByLabel(/FEEDBACK/).fill('Ship it');
    await submit.click();
    await expect(dialog).toBeHidden({ timeout: 30_000 });

    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    expect(
      await getStepStdout(request, token, dagName, runId, 'publish')
    ).toContain('published');
    // Nothing waits any more: the count goes away. The panel may have
    // yielded to the step view, so it opens again first.
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('ndv')).toBeHidden();
    const after = await openDock(page);
    await expect(
      after.getByRole('tab', { name: 'Actions, 1 waiting' })
    ).toHaveCount(0, { timeout: 30_000 });
  });

  // parity:test-human-tasks-approval
  test('pushes an approval back with feedback and the gate reopens', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await inlineDAG(APPROVAL);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);

    const actions = await openStepView(
      page,
      gotoWorkspace,
      fileName,
      runId,
      'review',
      'steps[1]'
    );
    await expect(actions).toContainText('Approve the release', {
      timeout: 30_000,
    });
    await actions.getByRole('button', { name: 'Retry', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Retry: review' });
    await expect(dialog).toBeVisible();
    await dialog.getByLabel(/FEEDBACK/).fill('Add tests');
    await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(dialog).toBeHidden({ timeout: 30_000 });

    // The gate runs again with the feedback, then waits again.
    await expect
      .poll(
        () =>
          getStepStdout(request, token, dagName, runId, 'review', {
            retryNotFound: true,
          }),
        { timeout: 60_000 }
      )
      .toContain('feedback=Add tests');
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);
    await expect(actions).toContainText('Iteration', { timeout: 30_000 });
    await expect(actions.getByText('Add tests').first()).toBeVisible();

    await actions.getByRole('button', { name: 'Approve', exact: true }).click();
    const approve = page.getByRole('dialog', { name: 'Approve: review' });
    await approve.getByLabel(/FEEDBACK/).fill('Tests added');
    await approve.getByRole('button', { name: 'Approve', exact: true }).click();
    await expect(approve).toBeHidden({ timeout: 30_000 });
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
  });

  // The flow of e2e/human-task-push-back.spec.ts, from the step view.
  // parity:human-task-card parity:run-tab-human-tasks
  test('sends a human task back with feedback and completes it', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await inlineDAG(HUMAN_TASK);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);

    const actions = await openStepView(
      page,
      gotoWorkspace,
      fileName,
      runId,
      'review',
      'steps[1]'
    );
    await expect(actions).toContainText('Review the implementation', {
      timeout: 30_000,
    });
    await actions.getByRole('button', { name: 'Request changes' }).click();
    await expect(
      actions.getByText(
        'implement and the steps that depend on it run again with your feedback. This task reopens afterward.'
      )
    ).toBeVisible();
    await actions.getByLabel(/Feedback/).fill('Add tests');
    await actions.getByRole('button', { name: 'Request changes' }).click();

    // The rewound step runs again with the feedback before the task reopens.
    await expect
      .poll(
        () =>
          getStepStdout(request, token, dagName, runId, 'implement', {
            retryNotFound: true,
          }),
        { timeout: 60_000 }
      )
      .toContain('attempt=1 feedback=Add tests');
    await waitForRunStatus(request, token, dagName, runId, ['waiting']);

    await expect(actions.getByText('Previous Push-backs')).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      actions.getByText('feedback="Add tests"').first()
    ).toBeVisible();
    await actions.getByRole('button', { name: 'Complete task' }).click();

    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    expect(
      await getStepStdout(request, token, dagName, runId, 'publish')
    ).toContain('published');
  });
});
