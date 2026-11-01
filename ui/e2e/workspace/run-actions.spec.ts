// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { writeFile } from 'node:fs/promises';
import path from 'node:path';
import type {
  APIRequestContext,
  Locator,
  Page,
  Response,
} from '@playwright/test';
import {
  enqueueDAG,
  getStepStdout,
  listDAGRuns,
  loadStack,
  loginViaAPI,
  startDAG,
  openStepForm,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { test as base, expect, signIn } from './fixtures';

// parity:test-e2e-run-actions - the start and run controls of the workspace
// bar, the node toolbar and the run chip against the e2e stack.

// NodeStatus in ui/src/api/v1/schema.ts.
const NODE_SUCCEEDED = 4;
const NODE_SKIPPED = 5;

type InlineDAG = { dagName: string; fileName: string; token: string };

const test = base.extend<{
  /**
   * Writes a DAG with writeLocalDAG. The spec gets the DAG name. The file is
   * deleted after the test.
   */
  inlineDAG: (
    prefix: string,
    spec: (dagName: string) => string
  ) => Promise<InlineDAG>;
}>({
  inlineDAG: async ({ request }, use) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const created: string[] = [];
    await use(async (prefix, spec) => {
      const dagName = uniqueName(prefix);
      const fileName = await writeLocalDAG(dagName, spec(dagName));
      created.push(fileName);
      await waitForDAGAvailable(request, token, fileName);
      return { dagName, fileName, token };
    });
    for (const fileName of created) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  },
});

/** A step that waits for this file holds its run until the test writes it. */
function releaseFile(stateDir: string, dagName: string): string {
  return path.join(stateDir, `${dagName}.release`);
}

function holdScript(release: string): string {
  return `while [ ! -f "${release}" ]; do sleep 0.2; done`;
}

function dagPath(fileName: string): string {
  return `/dags/${encodeURIComponent(fileName)}`;
}

function runChip(page: Page): Locator {
  return page.getByTestId('ww-run-chip');
}

/** The status icon of the run chip; its accessible name is the status text. */
function chipStatus(page: Page, label: string): Locator {
  return runChip(page).getByRole('img', { name: label, exact: true });
}

/**
 * The run ID of the selected run. The chip of the bar has no run ID: the bar
 * of the Runs panel carries it as a tooltip, whether the panel is open or not.
 * The box is hidden while the panel is a tab: check it is attached.
 */
function chipRun(page: Page, runId: string): Locator {
  return page.getByTestId('ww-dock').getByTitle(runId, { exact: true });
}

/** The warning dot of the chip: the run used another definition. */
function specDiffersDot(page: Page): Locator {
  return runChip(page).getByTestId('ww-run-stale-dot');
}

function runActions(page: Page): Locator {
  return page.getByTestId('ww-run-actions');
}

/** 'Run…': the start-only DAGActions of the bar. */
function runButton(page: Page): Locator {
  return page.getByTestId('ww-run-split').locator('[data-ww-action="start"]');
}

function canvasNode(page: Page, key: string): Locator {
  return page.getByTestId('ww-canvas').getByTestId(`ve-canvas-node-${key}`);
}

async function openRunMenu(page: Page): Promise<Locator> {
  await page
    .getByTestId('ww-run-split')
    .getByRole('button', { name: 'More run options' })
    .click();
  const menu = page.getByRole('menu');
  await expect(menu).toBeVisible();
  return menu;
}

async function openWorkspace(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  target: string
): Promise<void> {
  await gotoWorkspace(page, target);
  await expect(page.getByTestId('workflow-workspace')).toBeVisible({
    timeout: 30_000,
  });
  await expect(canvasNode(page, 'steps[0]')).toBeVisible({ timeout: 30_000 });
}

/**
 * Opens the workflow on a run (the latest run without an ID). The editor and
 * the run are one tab: the run chip and the run actions show on it.
 */
async function openRun(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  fileName: string,
  runId?: string
): Promise<void> {
  const query = runId ? `?run=${encodeURIComponent(runId)}` : '';
  await gotoWorkspace(page, `${dagPath(fileName)}${query}`);
  await expect(page.getByTestId('workflow-workspace')).toBeVisible({
    timeout: 30_000,
  });
  await expect(runChip(page)).toBeVisible({ timeout: 30_000 });
}

/** Resolves when the workspace has selected the run: ?run names it. */
async function expectOnRun(
  page: Page,
  fileName: string,
  runId: string
): Promise<void> {
  await expect
    .poll(() => {
      const url = new URL(page.url());
      return [url.pathname, url.searchParams.get('run')];
    })
    .toEqual([dagPath(fileName), runId]);
}

function waitForPost(page: Page, pathSuffix: string): Promise<Response> {
  return page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname.endsWith(pathSuffix)
  );
}

/** A run start: the saved file starts at .../start, an unsaved draft at /dag-runs. */
function waitForRunStart(page: Page, fileName: string): Promise<Response> {
  return page.waitForResponse((response) => {
    if (response.request().method() !== 'POST') return false;
    const pathname = new URL(response.url()).pathname;
    return (
      pathname.endsWith(`${dagPath(fileName)}/start`) ||
      pathname.endsWith('/api/v1/dag-runs')
    );
  });
}

/** The nodes of a run as the server has them. */
async function serverNodes(
  request: APIRequestContext,
  token: string,
  dagName: string,
  runId: string
): Promise<Map<string, { status: number; finishedAt?: string }>> {
  const response = await request.get(
    `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runId)}?remoteNode=local`,
    { headers: { Authorization: `Bearer ${token}` } }
  );
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as {
    dagRunDetails: {
      nodes?: {
        step: { name: string };
        status: number;
        finishedAt?: string;
      }[];
    };
  };
  return new Map(
    (body.dagRunDetails.nodes ?? []).map((node) => [
      node.step.name,
      { status: node.status, finishedAt: node.finishedAt },
    ])
  );
}

async function runIdOf(response: Response): Promise<string> {
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = (await response.json()) as { dagRunId?: string };
  expect(body.dagRunId).toBeTruthy();
  return body.dagRunId as string;
}

test.describe('workspace run actions', () => {
  // The run chip and the run actions of the bar show from the md breakpoint.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // parity:actions-start parity:start-modal-params parity:run-progress-stack
  test('starts from Run… with a param and follows the run in the chip', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const stack = await loadStack();
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-start',
      (name) => `
name: ${name}
params:
  - GREETING: hello
steps:
  - name: hold
    run: |
      ${holdScript(releaseFile(stack.stateDir, name))}
      echo "greeting=\${params.GREETING}"
`
    );
    const release = releaseFile(stack.stateDir, dagName);
    const runId = uniqueName('ws-start-run');

    try {
      await openWorkspace(page, gotoWorkspace, dagPath(fileName));
      // A workflow that never ran: the bar has no run chip, and no run is
      // selected.
      await expect(runChip(page)).toHaveCount(0);
      expect(new URL(page.url()).searchParams.get('run')).toBeNull();

      await runButton(page).click();
      const dialog = page.getByRole('dialog', { name: 'Start workflow' });
      await expect(dialog).toBeVisible();
      await dialog
        .getByLabel('GREETING', { exact: true })
        .fill('from-run-dialog');
      await dialog.getByLabel('Run ID (optional)').fill(runId);
      const started = waitForPost(page, `${dagPath(fileName)}/start`);
      await dialog.getByRole('button', { name: 'Start', exact: true }).click();
      expect(await runIdOf(await started)).toBe(runId);
      await expect(dialog).toBeHidden();

      // Start selects the new run in the same tab.
      await expectOnRun(page, fileName, runId);
      await expect(
        page.getByRole('button', { name: `Open run progress for ${runId}` })
      ).toBeVisible();
      await expect(chipRun(page, runId)).toBeAttached({ timeout: 30_000 });
      await expect(chipStatus(page, 'Running')).toBeVisible({
        timeout: 30_000,
      });
    } finally {
      await writeFile(release, '');
    }

    await expect(chipStatus(page, 'Succeeded')).toBeVisible({
      timeout: 60_000,
    });
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await expect
      .poll(
        () =>
          getStepStdout(request, token, dagName, runId, 'hold', {
            retryNotFound: true,
          }),
        { timeout: 15_000 }
      )
      .toContain('greeting=from-run-dialog');
  });

  // The shared queue has one active slot. A first run holds it, so the run
  // enqueued from the dialog stays queued until it is dequeued.
  // parity:start-modal-enqueue parity:run-action-dequeue
  test('enqueues from Run… and dequeues the queued run', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const stack = await loadStack();
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-queue',
      (name) => `
name: ${name}
queue: ${stack.queues.shared}
worker_selector:
  role: e2e
steps:
  - name: hold
    run: |
      ${holdScript(releaseFile(stack.stateDir, name))}
      echo released
`
    );
    const release = releaseFile(stack.stateDir, dagName);
    const queuedId = uniqueName('ws-queued-run');
    let firstId = '';

    try {
      firstId = await enqueueDAG(request, token, fileName);
      await waitForRunStatus(
        request,
        token,
        dagName,
        firstId,
        ['running'],
        'local',
        60_000
      );

      await openWorkspace(page, gotoWorkspace, dagPath(fileName));
      await runButton(page).click();
      const dialog = page.getByRole('dialog', { name: 'Start workflow' });
      await expect(dialog).toBeVisible();
      const enqueue = dialog.getByRole('checkbox', { name: 'Enqueue' });
      if ((await enqueue.getAttribute('data-state')) !== 'checked') {
        await enqueue.click();
      }
      await dialog.getByLabel('Run ID (optional)').fill(queuedId);
      const enqueued = waitForPost(page, `${dagPath(fileName)}/enqueue`);
      await dialog
        .getByRole('button', { name: 'Enqueue', exact: true })
        .click();
      expect(await runIdOf(await enqueued)).toBe(queuedId);
      await expect(dialog).toBeHidden();
      await waitForRunStatus(
        request,
        token,
        dagName,
        queuedId,
        ['queued'],
        'local',
        30_000
      );

      await openRun(page, gotoWorkspace, fileName, queuedId);
      await expect(chipRun(page, queuedId)).toBeAttached({ timeout: 30_000 });
      await expect(chipStatus(page, 'Queued')).toBeVisible();
      await runActions(page)
        .getByRole('button', { name: 'Dequeue', exact: true })
        .click();
      const confirm = page.getByRole('dialog', {
        name: 'Remove this run from the queue?',
      });
      await expect(confirm).toBeVisible();
      await confirm
        .getByRole('button', { name: 'Dequeue', exact: true })
        .click();

      // The server aborts the dequeued run, then hides it, since it never
      // started: the workspace names the latest run in ?run again.
      await expect
        .poll(() => new URL(page.url()).searchParams.get('run'), {
          timeout: 30_000,
        })
        .toBe(firstId);
      await expect(chipRun(page, queuedId)).toHaveCount(0);
      // A dequeued run that is gone is expected: no warning and no 404
      // banner, and the latest run (the held one) is shown.
      await expect(chipRun(page, firstId)).toBeAttached({ timeout: 30_000 });
      await expect(
        page.getByText(
          'The run in the link was not found. The latest run is shown.'
        )
      ).toHaveCount(0);
      await expect(
        page.getByRole('alert').filter({ hasText: /not found/i })
      ).toHaveCount(0);
      await expect
        .poll(async () =>
          (await listDAGRuns(request, token, dagName)).map((r) => r.dagRunId)
        )
        .not.toContain(queuedId);
    } finally {
      await writeFile(release, '');
    }

    if (firstId) {
      await waitForRunStatus(request, token, dagName, firstId, ['succeeded']);
    }
  });

  // parity:run-dag-button parity:dup-13
  test('Start starts the saved definition', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-run-now',
      (name) => `
name: ${name}
steps:
  - name: say
    run: echo "saved definition"
`
    );

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    // The DAG has no other run, so this is the spec of the new run.
    const runSpecPath = new RegExp(
      `/dag-runs/${encodeURIComponent(dagName)}/[^/]+/spec$`
    );
    const runSpec = page.waitForResponse(
      (response) =>
        response.request().method() === 'GET' &&
        runSpecPath.test(new URL(response.url()).pathname)
    );
    await runButton(page).click();
    const dialog = page.getByRole('dialog', { name: 'Start workflow' });
    await expect(dialog).toBeVisible();
    const started = waitForPost(page, `${dagPath(fileName)}/start`);
    await dialog.getByRole('button', { name: 'Start', exact: true }).click();
    const runId = await runIdOf(await started);

    await expectOnRun(page, fileName, runId);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await expect(chipRun(page, runId)).toBeAttached({ timeout: 30_000 });
    await expect(chipStatus(page, 'Succeeded')).toBeVisible({
      timeout: 30_000,
    });
    // The run used the saved file, so the chip shows no spec-differs dot.
    // The dot needs the run spec; without it the check proves nothing.
    expect((await runSpec).ok()).toBeTruthy();
    await expect(specDiffersDot(page)).toHaveCount(0);
    expect(
      await getStepStdout(request, token, dagName, runId, 'say', {
        retryNotFound: true,
      })
    ).toContain('saved definition');
  });

  // parity:run-draft-button
  test('runs the unsaved draft after a canvas edit', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-draft',
      (name) => `
name: ${name}
steps:
  - name: say
    run: echo "draft definition"
`
    );

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    await canvasNode(page, 'steps[0]').dblclick();
    await openStepForm(page);
    const inspector = page.getByTestId('ve-inspector');
    await expect(inspector).toBeVisible({ timeout: 30_000 });
    const description = inspector.getByLabel('Description', { exact: true });
    await description.fill('Changed in the browser, not saved');
    // The field sends its draft on blur.
    await description.blur();
    await expect(page.locator('#save-config')).toBeEnabled({
      timeout: 30_000,
    });

    // The menu holds the draft only: Start runs the saved file.
    const menu = await openRunMenu(page);
    await expect(menu.getByRole('menuitem')).toHaveCount(1);
    const started = waitForPost(page, '/api/v1/dag-runs');
    await menu.getByRole('menuitem', { name: 'Run unsaved draft' }).click();
    const runId = await runIdOf(await started);

    await expectOnRun(page, fileName, runId);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await expect(chipRun(page, runId)).toBeAttached({ timeout: 30_000 });
    await expect(specDiffersDot(page)).toBeVisible({ timeout: 30_000 });
  });

  // parity:actions-stop
  test('Stop on a running run gives Aborted', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-stop',
      (name) => `
name: ${name}
steps:
  - name: hold
    run: sleep 120
`
    );
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(
      request,
      token,
      dagName,
      runId,
      ['running'],
      'local',
      30_000
    );

    await openRun(page, gotoWorkspace, fileName);
    await expect(chipRun(page, runId)).toBeAttached({ timeout: 30_000 });
    await expect(chipStatus(page, 'Running')).toBeVisible();
    await runActions(page)
      .getByRole('button', { name: 'Stop', exact: true })
      .click();
    const confirm = page.getByRole('dialog', { name: 'Stop this run?' });
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Stop', exact: true }).click();

    await waitForRunStatus(
      request,
      token,
      dagName,
      runId,
      ['aborted'],
      'local',
      30_000
    );
    await expect(chipStatus(page, 'Aborted')).toBeVisible({
      timeout: 30_000,
    });
  });

  // parity:actions-retry
  test('Retry on a failed run runs a new attempt', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const stack = await loadStack();
    const flagDir = path.join(stack.stateDir, 'retry-flags');
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-retry',
      (name) => `
name: ${name}
steps:
  - name: retry-step
    run: |
      mkdir -p "${flagDir}"
      if [ -f "${path.join(flagDir, `${name}.flag`)}" ]; then
        echo "retry succeeded"
        exit 0
      fi
      touch "${path.join(flagDir, `${name}.flag`)}"
      echo "retry failed"
      exit 1
`
    );
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(
      request,
      token,
      dagName,
      runId,
      ['failed'],
      'local',
      30_000
    );

    await openRun(page, gotoWorkspace, fileName);
    await expect(chipRun(page, runId)).toBeAttached({ timeout: 30_000 });
    await expect(chipStatus(page, 'Failed')).toBeVisible();
    await runActions(page)
      .getByRole('button', { name: 'Retry', exact: true })
      .click();
    const confirm = page.getByRole('dialog', { name: 'Retry this run?' });
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Retry', exact: true }).click();

    await waitForRunStatus(
      request,
      token,
      dagName,
      runId,
      ['succeeded'],
      'local',
      30_000
    );
    await expect(chipStatus(page, 'Succeeded')).toBeVisible({
      timeout: 30_000,
    });
    await expect
      .poll(
        () =>
          getStepStdout(request, token, dagName, runId, 'retry-step', {
            retryNotFound: true,
          }),
        { timeout: 15_000 }
      )
      .toContain('retry succeeded');
  });

  // parity:retry-modal-reschedule
  test('Reschedule of a finished run makes a new run', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-reschedule',
      (name) => `
name: ${name}
steps:
  - name: say
    run: echo "reschedule complete"
`
    );
    const firstId = await startDAG(request, token, fileName);
    await waitForRunStatus(
      request,
      token,
      dagName,
      firstId,
      ['succeeded'],
      'local',
      30_000
    );

    const newRunId = uniqueName('ws-rescheduled-run');
    await openRun(page, gotoWorkspace, fileName);
    await expect(chipRun(page, firstId)).toBeAttached({ timeout: 30_000 });
    await runActions(page)
      .getByRole('button', { name: 'Retry', exact: true })
      .click();
    await expect(
      page.getByRole('dialog', { name: 'Retry this run?' })
    ).toBeVisible();
    // The checkbox changes the title of the dialog.
    const confirm = page.getByRole('dialog');
    await confirm.getByLabel('Reschedule as a new run').click();
    await expect(
      page.getByRole('dialog', { name: 'Reschedule as a new run?' })
    ).toBeVisible();
    await confirm.getByLabel('New run ID (optional)').fill(newRunId);
    await confirm
      .getByRole('button', { name: 'Reschedule', exact: true })
      .click();

    await expect
      .poll(
        async () =>
          (await listDAGRuns(request, token, dagName)).map((r) => r.dagRunId),
        { timeout: 30_000 }
      )
      .toContain(newRunId);
    await waitForRunStatus(
      request,
      token,
      dagName,
      newRunId,
      ['succeeded'],
      'local',
      30_000
    );
    await expect(chipRun(page, newRunId)).toBeAttached({ timeout: 30_000 });
    // The first run is unchanged.
    await waitForRunStatus(request, token, dagName, firstId, ['succeeded']);
  });

  // parity:canvas-node-toolbar
  test('Run step from the node toolbar asks, then starts', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-run-step',
      (name) => `
name: ${name}
steps:
  - name: first
    run: echo "first step"
  - name: second
    run: echo "second step"
`
    );

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    const node = canvasNode(page, 'steps[1]');
    // The toolbar shows while the pointer is over the node.
    await node.hover();
    await node.getByRole('button', { name: 'Run step', exact: true }).click();

    const confirm = page.getByRole('dialog', { name: 'Run step' });
    await expect(confirm).toBeVisible();
    await expect(confirm).toContainText('Run only the step second now?');
    // No previous step: Cancel and Run only.
    await expect(
      confirm.getByRole('button', { name: 'Step only', exact: true })
    ).toHaveCount(0);
    const started = waitForPost(page, `${dagPath(fileName)}/start`);
    await confirm.getByRole('button', { name: 'Run', exact: true }).click();
    const runId = await runIdOf(await started);

    // The URL names the shown run and does not follow a newer one by itself:
    // a start in the tab replaces ?run with the new run.
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    expect(new URL(page.url()).pathname).toBe(dagPath(fileName));
    await expect
      .poll(() => new URL(page.url()).searchParams.get('run'))
      .toBe(runId);
    await expect(chipRun(page, runId)).toBeAttached({ timeout: 30_000 });
    expect(
      await getStepStdout(request, token, dagName, runId, 'second', {
        retryNotFound: true,
      })
    ).toContain('second step');
  });

  // The dialog has Cancel, Step only and Run. Run runs the steps that the
  // step needs, then the step, and reuses no output.
  // parity:canvas-node-toolbar
  test('Run step runs the previous steps first and reuses no output', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-run-chain',
      (name) => `
name: ${name}
steps:
  - name: first
    run: echo "first step"
  - name: second
    depends: [first]
    run: echo "second step"
  - name: third
    depends: [second]
    run: echo "third step"
`
    );

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    const node = canvasNode(page, 'steps[2]');
    await node.hover();
    await node.getByRole('button', { name: 'Run step', exact: true }).click();

    const confirm = page.getByRole('dialog', { name: 'Run step' });
    // The previous steps are listed. No run holds their outputs, so the
    // default is Run, and there is nothing to change.
    await expect(confirm.getByTestId('run-step-item')).toHaveText([
      /first/,
      /second/,
    ]);
    await expect(
      confirm.getByRole('button', { name: 'Cancel', exact: true })
    ).toBeVisible();
    await expect(
      confirm.getByRole('button', { name: 'Step only', exact: true })
    ).toBeVisible();
    await expect(
      confirm.getByRole('button', { name: 'Changed steps', exact: true })
    ).toHaveCount(0);
    await expect(confirm.getByTestId('step-run-run')).toBeFocused();
    const started = waitForPost(page, `${dagPath(fileName)}/start`);
    await confirm.getByRole('button', { name: 'Run', exact: true }).click();
    const response = await started;
    const runId = await runIdOf(response);
    expect(response.request().postDataJSON()).toEqual({
      steps: ['first', 'second', 'third'],
      labels: ['dagu.source=editor'],
    });

    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    expect(
      await getStepStdout(request, token, dagName, runId, 'first', {
        retryNotFound: true,
      })
    ).toContain('first step');
  });

  // Run upstream steps and Run step stay on the editor: the node view
  // stays open on the step, and its columns show the run.
  test('Run upstream steps and Run step stay in the node view', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-run-view',
      (name) => `
name: ${name}
steps:
  - name: first
    run: echo "value-one"
    output: ONE
  - name: second
    depends: [first]
    run: echo "value-two"
    output: TWO
  - name: third
    depends: [second]
    run: echo "value-three"
    output: THREE
`
    );

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    await canvasNode(page, 'steps[2]').dblclick();
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    const input = ndv.getByRole('region', { name: 'Input' });

    // Run upstream steps: no navigation, the rows of the INPUT column follow.
    await input.getByRole('button', { name: 'Run upstream steps' }).click();
    let confirm = page.getByRole('dialog', { name: 'Run upstream steps' });
    const started = waitForPost(page, `${dagPath(fileName)}/start`);
    await confirm.getByRole('button', { name: 'Run', exact: true }).click();
    const response = await started;
    const upstreamRun = await runIdOf(response);
    expect(response.request().postDataJSON()).toEqual({
      steps: ['first', 'second'],
      labels: ['dagu.source=editor'],
    });
    await expect(input.getByTestId('upstream-state-first')).toHaveAttribute(
      'data-state',
      'success',
      { timeout: 60_000 }
    );
    await expect(input.getByTestId('upstream-state-second')).toHaveAttribute(
      'data-state',
      'success',
      { timeout: 60_000 }
    );
    await expect(input).toContainText('value-one');
    await expect(input).toContainText('value-two');
    await expect(input.getByTestId('upstream-last-run')).toBeVisible();
    expect(new URL(page.url()).pathname).toBe(dagPath(fileName));
    await expect(ndv).toBeVisible();
    await waitForRunStatus(request, token, dagName, upstreamRun, ['succeeded']);

    // Run step opens the dialog of Run step. Step only runs the step on
    // the outputs of the latest run.
    await ndv.getByRole('button', { name: 'Run step' }).click();
    confirm = page.getByRole('dialog', { name: 'Run step' });
    const executed = waitForPost(page, `${dagPath(fileName)}/start`);
    await confirm
      .getByRole('button', { name: 'Step only', exact: true })
      .click();
    const executeResponse = await executed;
    await runIdOf(executeResponse);
    const body = executeResponse.request().postDataJSON() as {
      steps: string[];
      outputsFromRunId?: string;
    };
    expect(body.steps).toEqual(['third']);
    expect(body.outputsFromRunId).toBeTruthy();
    const output = ndv.getByRole('region', { name: 'Output', exact: true });
    await expect(output).toContainText('value-three', { timeout: 60_000 });
    expect(new URL(page.url()).pathname).toBe(dagPath(fileName));
    await expect(ndv).toBeVisible();
  });

  // The step reads the output of a previous step. Run includes that step
  // and follows the run in the node view; Step only runs the step alone, on
  // the outputs of the latest run.
  test('Run step runs the steps that the step reads from with Run', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-run-reads',
      (name) => `
name: ${name}
steps:
  - id: first
    name: first
    run: echo "value-one"
    output: ONE
  - id: second
    name: second
    depends: [first]
    run: echo "got \${ONE}"
`
    );

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    await canvasNode(page, 'steps[1]').dblclick();
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });

    const startBody = async (
      button: 'Run' | 'Step only',
      check: (confirm: Locator) => Promise<void>
    ): Promise<{
      runId: string;
      body: { steps: string[]; outputsFromRunId?: string };
    }> => {
      await ndv.getByRole('button', { name: 'Run step' }).click();
      const confirm = page.getByRole('dialog', { name: 'Run step' });
      // The step reads the output of first: first is a previous step.
      await expect(confirm.getByTestId('run-step-item')).toHaveText([/first/]);
      await check(confirm);
      const started = waitForPost(page, `${dagPath(fileName)}/start`);
      await confirm.getByRole('button', { name: button, exact: true }).click();
      const response = await started;
      const runId = await runIdOf(response);
      await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
      return { runId, body: response.request().postDataJSON() };
    };

    // Run: no run holds the output that the step reads, so Step only is not
    // offered. The step that is read runs first, and no output is reused. The
    // INPUT column follows the run in place.
    const input = ndv.getByRole('region', { name: 'Input' });
    const first = await startBody('Run', async (confirm) => {
      await expect(confirm).toContainText(
        'No earlier run holds the outputs that this step reads.'
      );
      await expect(
        confirm.getByRole('button', { name: 'Step only', exact: true })
      ).toHaveCount(0);
      await expect(confirm.getByTestId('step-run-run')).toBeFocused();
    });
    expect(first.body.steps).toEqual(['first', 'second']);
    expect(first.body.outputsFromRunId).toBeUndefined();
    await expect(input.getByTestId('upstream-state-first')).toHaveAttribute(
      'data-state',
      'success',
      { timeout: 60_000 }
    );
    await expect(input).toContainText('value-one');
    expect(new URL(page.url()).pathname).toBe(dagPath(fileName));
    await expect(ndv).toBeVisible();

    // Step only: the step runs alone, on the outputs of the latest run. The
    // dialog names that run and makes Step only the default.
    const second = await startBody('Step only', async (confirm) => {
      // The picker shows the first 8 characters; the title has the whole ID.
      await expect(confirm.getByTestId('run-source-picker')).toContainText(
        first.runId.slice(0, 8)
      );
      await expect(confirm.getByTestId('run-source-link')).toHaveAttribute(
        'title',
        first.runId
      );
      await expect(confirm.getByTestId('step-run-only')).toBeFocused();
    });
    expect(second.body.steps).toEqual(['second']);
    expect(second.body.outputsFromRunId).toBe(first.runId);
    expect(new URL(page.url()).pathname).toBe(dagPath(fileName));
    await expect(ndv).toBeVisible();
  });

  // The owner's goal: editing and running a step must not run the costly
  // steps before it again. Run A runs the three steps. After an edit of the
  // second step, Run step on the third offers Changed steps as the default.
  // It runs the second and the third and reuses the first.
  test('Run step of an unsaved draft reuses the unchanged steps and leaves no trace', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-reuse',
      (name) => `
name: ${name}
steps:
  - id: slow
    name: slow
    run: echo "slow-$(date +%s%N)"
    output: SLOW
  - id: middle
    name: middle
    depends: [slow]
    run: echo "middle-$(date +%s%N) from \${SLOW}"
    output: MIDDLE
  - id: use
    name: use
    depends: [middle]
    run: echo "use-$(date +%s%N) from \${MIDDLE}"
`
    );
    const runA = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runA, ['succeeded']);
    const middleA = await getStepStdout(
      request,
      token,
      dagName,
      runA,
      'middle'
    );
    expect(middleA).toContain('middle-');

    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    const ndv = page.getByTestId('ndv');

    // Edit the second step: a description in the form is a change that the
    // run of the step has to take into account. It stays unsaved.
    await canvasNode(page, 'steps[1]').dblclick();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await openStepForm(page);
    const description = page
      .getByTestId('ve-inspector')
      .getByLabel('Description', { exact: true });
    await description.fill('Changed after run A');
    await description.blur();
    await expect(page.locator('#save-config')).toBeEnabled({
      timeout: 30_000,
    });
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(ndv).toBeHidden();

    // The INPUT column of the third step shows run A as its source and marks
    // the second step as changed since that run.
    await canvasNode(page, 'steps[2]').dblclick();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    const input = ndv.getByRole('region', { name: 'Input', exact: true });
    await expect(input.getByTestId('upstream-input-run')).toHaveAttribute(
      'data-run-id',
      runA,
      { timeout: 30_000 }
    );
    await expect(input.getByTestId('upstream-mark-middle')).toHaveAttribute(
      'data-mark',
      'stale'
    );

    // The dialog makes Changed steps the default and marks each step.
    await ndv.getByRole('button', { name: 'Run step' }).click();
    const confirm = page.getByRole('dialog', { name: 'Run step' });
    await expect(confirm.getByTestId('run-source-picker')).toContainText(
      runA.slice(0, 8)
    );
    await expect(confirm.getByTestId('run-source-link')).toHaveAttribute(
      'title',
      runA
    );
    // Run A holds the first step and it did not change: it is reused from
    // that run, which the row names. The second step changed since run A.
    await expect(confirm.getByTestId('run-step-item')).toHaveText([
      /slow.*reused.*run /,
      /middle.*changed since run/,
    ]);
    await expect(confirm.getByTestId('step-run-changed')).toBeFocused();
    await expect(
      confirm.getByRole('button', { name: 'Step only', exact: true })
    ).toBeVisible();
    await expect(
      confirm.getByRole('button', { name: 'Run', exact: true })
    ).toBeVisible();

    const started = waitForRunStart(page, fileName);
    await confirm
      .getByRole('button', { name: 'Changed steps', exact: true })
      .click();
    const response = await started;
    const runB = await runIdOf(response);
    expect(runB).not.toBe(runA);
    const body = response.request().postDataJSON() as {
      steps: string[];
      outputsFromRunId?: string;
      outputsFromRuns?: Record<string, string>;
      outputs?: unknown;
      ephemeral?: boolean;
      labels: string[];
    };
    expect(body.steps).toEqual(['middle', 'use']);
    expect(body.outputsFromRunId).toBe(runA);
    // The editor holds unsaved text: the step run is a test run. The browser
    // names the run of the inputs and never sends a value.
    expect(response.request().url()).toContain('/api/v1/dag-runs');
    expect(body.ephemeral).toBe(true);
    expect('outputs' in body).toBe(false);
    expect(body.labels).toEqual([
      'dagu.source=editor',
      `dagu.outputs-from=${runA}`,
    ]);
    await waitForRunStatus(request, token, dagName, runB, ['succeeded']);

    // The first step is reused: skipped, with the finish time of run A. The
    // second and the third ran again.
    const nodes = await serverNodes(request, token, dagName, runB);
    expect(nodes.get('slow')?.status).toBe(NODE_SKIPPED);
    expect(nodes.get('slow')?.finishedAt).toMatch(/^\d{4}-/);
    expect(nodes.get('slow')?.finishedAt).not.toMatch(/^0001/);
    expect(nodes.get('middle')?.status).toBe(NODE_SUCCEEDED);
    expect(nodes.get('use')?.status).toBe(NODE_SUCCEEDED);
    const middleB = await getStepStdout(
      request,
      token,
      dagName,
      runB,
      'middle',
      {
        retryNotFound: true,
      }
    );
    expect(middleB).toContain('middle-');
    expect(middleB).not.toBe(middleA);

    // The step view stays open, and its INPUT column follows the new run.
    await expect(ndv).toBeVisible();
    await expect(input.getByTestId('upstream-input-run')).toHaveAttribute(
      'data-run-id',
      runB,
      { timeout: 60_000 }
    );

    // The test run leaves no trace in the history: it holds run A only. The
    // facts line of the OUTPUT column says so, and the timeline draws it.
    await expect(ndv.getByTestId('ww-step-test-run')).toHaveText(
      'Test run, not kept'
    );
    const history = await listDAGRuns(request, token, dagName);
    expect(history.map((run) => run.dagRunId)).toEqual([runA]);
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(ndv).toBeHidden();
    // A test run is selectable while the server keeps it: ?run names it, the
    // run chip says "Test run" and the timeline draws a hatched bar.
    await expectOnRun(page, fileName, runB);
    await expect(page.getByTestId('ww-run-test-chip')).toHaveText('Test run');
    const dock = page.getByTestId('ww-dock');
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'open');
    await expect(page.getByTestId(`ww-run-bar-${runB}`)).toHaveAttribute(
      'data-test-run',
      'true',
      { timeout: 30_000 }
    );
    await expect(page.getByTestId(`ww-run-bar-${runA}`)).not.toHaveAttribute(
      'data-test-run',
      'true'
    );
  });

  // The browser names the run that each reused step comes from and never
  // sends a value: the server reads the outputs from the runs. The user
  // chooses the main run in the picker; the cache gives the other steps with
  // the run that it read them from. A run that is gone cannot give its steps.
  test('Step only names the runs of its inputs, sends no value, and a deleted run makes its steps missing', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(300_000);
    const spec = (slowText: string) => (name: string) => `
name: ${name}
params:
  - FAIL=0
steps:
  - id: slow
    name: slow
    run: echo "${slowText}-$(date +%s%N)"
    output: SLOW
  - id: middle
    name: middle
    depends: [slow]
    run: |
      if [ "$FAIL" = "1" ]; then exit 1; fi
      echo "middle from \${SLOW}"
    output: MIDDLE
  - id: use
    name: use
    depends: [middle]
    run: echo "use \${MIDDLE} and \${SLOW}"
`;
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-cache',
      spec('slow')
    );
    // Run A has the first definition. The first step is then changed and
    // saved: run B has the new definition, and run A is stale for it.
    const runA = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runA, ['succeeded']);
    await writeLocalDAG(dagName, spec('slow2')(dagName));
    await expect
      .poll(
        async () => {
          const response = await request.get(
            `/api/v1/dags/${encodeURIComponent(fileName)}/spec?remoteNode=local`,
            { headers: { Authorization: `Bearer ${token}` } }
          );
          return response.ok() ? await response.text() : '';
        },
        { timeout: 30_000 }
      )
      .toContain('slow2');
    const runB = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runB, ['succeeded']);
    // Run C failed at the second step: it cannot give the inputs of the third.
    const runC = await startDAG(request, token, fileName, {
      params: 'FAIL=1',
    });
    await waitForRunStatus(request, token, dagName, runC, ['failed']);

    // Reading the INPUT column of the last step fills the cache from the
    // source that the column shows. Each entry names its run.
    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    const ndv = page.getByTestId('ndv');
    const input = ndv.getByRole('region', { name: 'Input', exact: true });
    await canvasNode(page, 'steps[2]').dblclick();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    const picker = input.getByTestId('run-source-picker');
    await expect(picker).toContainText(runB.slice(0, 8), { timeout: 30_000 });
    const cache = () =>
      page.evaluate(() => {
        const key = Object.keys(localStorage).find((k) =>
          k.startsWith('dagu.output-cache')
        );
        if (!key) return {};
        const entries = JSON.parse(localStorage[key]).entries as Record<
          string,
          { runId: string }
        >;
        return Object.fromEntries(
          Object.entries(entries).map(([step, entry]) => [step, entry.runId])
        );
      });
    await expect
      .poll(cache, { timeout: 30_000 })
      .toEqual({ middle: runB, slow: runB });
    // No value can be typed: there is no editor and no pin.
    await expect(input.locator('[data-testid^="pin-"]')).toHaveCount(0);
    await expect(input.getByRole('textbox')).toHaveCount(0);

    // The step run dialog: the picker lists the runs, newest first, the
    // automatic one first; a run without the outputs that the step reads is
    // disabled with its reason.
    await ndv.getByRole('button', { name: 'Run step' }).click();
    const confirm = page.getByRole('dialog', { name: 'Run step' });
    await expect(confirm.getByTestId('step-run-only')).toBeFocused({
      timeout: 30_000,
    });
    await confirm.getByTestId('run-source-picker').click();
    const options = page.getByTestId('run-source-menu');
    await expect(
      options.getByTestId(`run-source-option-run:${runB}`)
    ).toContainText('Automatic');
    await expect(
      options.getByTestId(`run-source-option-run:${runC}`)
    ).toHaveAttribute('data-disabled', '');
    await expect(
      options.getByTestId(`run-source-option-run:${runC}`)
    ).toContainText('missing: middle');
    // Choose run A: the steps that it holds and that did not change come from
    // it; the first and the second step changed since, so the cache names the
    // run that it read them from (run B).
    await options.getByTestId(`run-source-option-run:${runA}`).click();
    await expect(confirm.getByTestId('run-source-picker')).toContainText(
      runA.slice(0, 8)
    );
    await expect(confirm.getByTestId('run-step-item')).toHaveText([
      /slow.*cached.*run /,
      /middle.*cached.*run /,
    ]);
    const started = waitForRunStart(page, fileName);
    await confirm.getByTestId('step-run-only').click();
    const response = await started;
    const runD = await runIdOf(response);
    const sent = response.request().postDataJSON() as {
      steps: string[];
      outputsFromRunId?: string;
      outputsFromRuns?: Record<string, string>;
      outputs?: unknown;
      outputsSources?: unknown;
      ephemeral?: boolean;
    };
    expect(sent.steps).toEqual(['use']);
    expect(sent.outputsFromRunId).toBe(runA);
    expect(sent.outputsFromRuns).toEqual({ middle: runB, slow: runB });
    expect('outputs' in sent).toBe(false);
    expect('outputsSources' in sent).toBe(false);
    expect(sent.ephemeral).toBeUndefined();
    await waitForRunStatus(request, token, dagName, runD, ['succeeded']);
    await expectOnRun(page, fileName, runD);

    // The server read the values from run B, and recorded the run of each step.
    const useD = await getStepStdout(request, token, dagName, runD, 'use', {
      retryNotFound: true,
    });
    expect(useD).toContain('slow2-');
    const recorded = await reusedInputsOf(request, token, dagName, runD);
    expect(recorded.map((r) => [r.step, r.runId, r.via])).toEqual([
      ['slow', runB, 'run'],
      ['middle', runB, 'run'],
    ]);
    expect(recorded.every((r) => r.startedAt && r.finishedAt)).toBe(true);

    // The record shows after a reload: the cards of the reused steps carry a
    // chip with the run, a dashed outline and the status Reused.
    const checkCards = async () => {
      for (const key of ['steps[0]', 'steps[1]']) {
        const card = canvasNode(page, key);
        const mark = card.getByTestId('ve-node-reused');
        await expect(mark).toHaveAttribute('data-source', 'run', {
          timeout: 30_000,
        });
        await expect(mark.getByTestId('input-mark-chip-text')).toHaveText(
          `from run ${runB.slice(0, 8)}`
        );
        await expect(card.getByTestId('ve-node-reuse-outline')).toHaveCount(1);
        await expect(card.getByTestId('status-dot')).toHaveAttribute(
          'aria-label',
          'Reused'
        );
      }
      await expect(
        canvasNode(page, 'steps[2]').getByTestId('ve-node-reused')
      ).toHaveCount(0);
    };
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(ndv).toBeHidden();
    await checkCards();
    await page.reload();
    await expect(page.getByTestId('workflow-workspace')).toBeVisible({
      timeout: 30_000,
    });
    await checkCards();

    // The step list shows the original duration of a reused step; the
    // Timeline tab of the run view has a hatched ghost row for each one.
    const dock = page.getByTestId('ww-dock');
    if ((await dock.getAttribute('data-state')) !== 'open') {
      await dock
        .getByRole('button', { name: 'Activity', exact: true })
        .first()
        .click();
    }
    await expect(
      dock.locator('li[data-step="slow"]').getByTestId('run-step-reused')
    ).toContainText('Reused', { timeout: 30_000 });
    await dock.locator('li[data-summary]').click();
    await dock.getByRole('tab', { name: 'Timeline', exact: true }).click();
    for (const step of ['slow', 'middle']) {
      await expect(
        dock.getByTestId(`timeline-bar-step:${step}`)
      ).toHaveAttribute('data-ghost', 'true', { timeout: 30_000 });
    }
    await expect(dock.getByTestId('timeline-bar-step:use')).not.toHaveAttribute(
      'data-ghost',
      'true'
    );
    await expect(
      dock.getByTestId('timeline-ghost-label').first()
    ).toContainText(`run ${runB.slice(0, 8)}`);
    // The reused steps stand before the start of the run, which has a mark.
    await expect(dock.getByTestId('timeline-run-start')).toContainText(
      'Run starts'
    );
    // The dots of the reused steps are hatched green; a step that ran is solid.
    for (const step of ['slow', 'middle']) {
      await expect(
        dock.locator(`li[data-step="${step}"]`).getByTestId('status-dot')
      ).toHaveAttribute('data-reused', 'success');
    }
    await expect(
      dock.locator('li[data-step="use"]').getByTestId('status-dot')
    ).not.toHaveAttribute('data-reused');

    // The Log of the run: the lines of the reused steps stand among the
    // lines of the run, with "cached" in place of the time. The order toggle
    // puts the newest line at the top.
    await dock.getByRole('tab', { name: 'Log', exact: true }).click();
    const cached = dock.getByTestId('cached-logs');
    await expect(cached.getByTestId('cached-log-line')).toHaveCount(2, {
      timeout: 30_000,
    });
    await expect(cached).toContainText('slow');
    await expect(cached).toContainText('middle');
    const order = dock.getByTestId('log-order');
    await expect(order).toHaveText('Old to new');
    await order.click();
    await expect(order).toHaveText('New to old');
    await expect(cached).toHaveAttribute('data-order', 'newToOld');
    await order.click();
    await expect(order).toHaveText('Old to new');

    // Run B is deleted: the cache entries name a run that is gone. The steps
    // count as missing and the dialog says so; no value is sent.
    const deleted = await request.delete(
      `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runB)}?remoteNode=local`,
      { headers: { Authorization: `Bearer ${token}` } }
    );
    expect(deleted.status()).toBe(204);
    await openWorkspace(page, gotoWorkspace, dagPath(fileName));
    await canvasNode(page, 'steps[2]').dblclick();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await ndv.getByRole('button', { name: 'Run step' }).click();
    const again = page.getByRole('dialog', { name: 'Run step' });
    await expect(again.getByTestId('run-step-gone')).toHaveCount(2, {
      timeout: 30_000,
    });
    await expect(again.getByTestId('run-step-gone').first()).toHaveText(
      'source run no longer available'
    );
    // The cards of run D now say that run B is gone.
    const startedAgain = waitForRunStart(page, fileName);
    await again.getByTestId('step-run-changed').click();
    const redo = await startedAgain;
    const runE = await runIdOf(redo);
    const redoBody = redo.request().postDataJSON() as Record<string, unknown>;
    expect(redoBody.steps).toEqual(['slow', 'middle', 'use']);
    expect('outputs' in redoBody).toBe(false);
    expect('outputsFromRuns' in redoBody).toBe(false);
    await waitForRunStatus(request, token, dagName, runE, ['succeeded']);

    await openRun(page, gotoWorkspace, fileName, runD);
    const mark = canvasNode(page, 'steps[0]').getByTestId('ve-node-reused');
    await mark.hover();
    await expect(page.getByTestId('input-mark-gone').first()).toContainText(
      '(no longer available)',
      { timeout: 30_000 }
    );
  });

  // A step run reuses the previous steps of its step. The graph outlines them
  // with a dashed line: green when the source run gave the outputs, red when
  // it did not. A step after the run step, or on another branch, has none.
  // The Upstream data column lists the previous steps as links.
  test('outlines the reused previous steps in green or red and links the previous steps', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(240_000);
    const { dagName, fileName, token } = await inlineDAG(
      'e2e-ws-outline',
      (name) => `
name: ${name}
params:
  - FAIL=0
steps:
  - id: first
    name: first
    run: echo first
    output: FIRST
  - id: second
    name: second
    depends: [first]
    run: |
      if [ "$FAIL" = "1" ]; then exit 1; fi
      echo second
    output: SECOND
  - id: side
    name: side
    depends: [first]
    run: echo side
  - id: third
    name: third
    depends: [second]
    run: echo third
  - id: after
    name: after
    depends: [third]
    run: echo after
`
    );
    // Run A: the second step fails. The first and the side step succeed; the
    // third and the last step never start.
    const runA = await startDAG(request, token, fileName, { params: 'FAIL=1' });
    await waitForRunStatus(request, token, dagName, runA, ['failed']);
    // Run B runs the third step alone, with the outputs of run A.
    const runB = await startDAG(request, token, fileName, {
      steps: ['third'],
      outputsFromRunId: runA,
      labels: ['dagu.source=editor', `dagu.outputs-from=${runA}`],
    });
    await waitForRunStatus(request, token, dagName, runB, ['succeeded']);

    await openRun(page, gotoWorkspace, fileName, runB);
    // steps[0] first, [1] second, [2] side, [3] third, [4] after.
    const short = runA.slice(0, 8);
    const expectOutlines = async () => {
      const first = canvasNode(page, 'steps[0]');
      await expect(first.getByTestId('ve-node-reuse-outline')).toHaveCount(1, {
        timeout: 30_000,
      });
      await expect(
        first.getByTestId('ve-node-reuse-outline').locator('rect')
      ).toHaveClass(/stroke-success/);
      await expect(first).toHaveAttribute(
        'title',
        new RegExp(`Reused from run ${short}: succeeded`)
      );
      const second = canvasNode(page, 'steps[1]');
      await expect(
        second.getByTestId('ve-node-reuse-outline').locator('rect')
      ).toHaveClass(/stroke-destructive/);
      await expect(second).toHaveAttribute(
        'title',
        new RegExp(`Reused from run ${short}: failed`)
      );
      // The side branch and the last step are not previous steps of the run
      // step: they are skipped, with no outputs, no outline and no chip. The
      // run step itself has none.
      for (const key of ['steps[2]', 'steps[3]', 'steps[4]']) {
        const card = canvasNode(page, key);
        await expect(card.getByTestId('ve-node-reuse-outline')).toHaveCount(0);
        await expect(card.getByTestId('ve-node-reused')).toHaveCount(0);
      }
    };
    await expectOutlines();
    // The same outlines on the simplified cards.
    await page.getByRole('button', { name: 'Simplified view' }).click();
    await expectOutlines();
    await page.getByRole('button', { name: 'Simplified view' }).click();

    // The previous steps of the run step are links in the Upstream data column.
    // The dock covers the fourth card: open the view from the link.
    await gotoWorkspace(
      page,
      `${dagPath(fileName)}?run=${encodeURIComponent(runB)}&step=third&ndv=expanded`
    );
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    const input = ndv.getByRole('region', { name: 'Input', exact: true });
    // The direct dependency is open; the step before it is closed.
    await expect(
      input.getByRole('button', { name: 'Open step second' })
    ).toBeVisible({ timeout: 30_000 });
    const chevron = input.getByRole('button', { name: 'Outputs of first' });
    await expect(chevron).toHaveAttribute('aria-expanded', 'false');
    await chevron.click();
    await expect(chevron).toHaveAttribute('aria-expanded', 'true');
    await input.getByRole('button', { name: 'Open step first' }).click();
    await expect
      .poll(() => {
        const url = new URL(page.url());
        return [
          url.searchParams.get('step'),
          url.searchParams.get('ndv'),
          url.searchParams.get('run'),
        ];
      })
      .toEqual(['first', 'expanded', runB]);
  });
});

/** The reused inputs that the server recorded for a run. */
async function reusedInputsOf(
  request: APIRequestContext,
  token: string,
  dagName: string,
  runId: string
): Promise<
  {
    step: string;
    runId: string;
    via: string;
    startedAt?: string;
    finishedAt?: string;
  }[]
> {
  const response = await request.get(
    `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runId)}?remoteNode=local`,
    { headers: { Authorization: `Bearer ${token}` } }
  );
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as {
    dagRunDetails: {
      reusedInputs?: {
        step: string;
        runId: string;
        via: string;
        startedAt?: string;
        finishedAt?: string;
      }[];
    };
  };
  return body.dagRunDetails.reusedInputs ?? [];
}
