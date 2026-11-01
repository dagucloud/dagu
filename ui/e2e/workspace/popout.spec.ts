// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import {
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

// The full-page logs of the workspace: the step log that the dock opens in a
// new tab, the run log with its ZIP download, and the log of a sub-run.
// parity:log-fullpage parity:test-details-content-tabs
// parity:shell-log-tabs parity:shell-stream-param

const BOTH_STREAMS = `
steps:
  - name: both
    run: |
      echo popout-stdout-marker
      echo popout-stderr-marker >&2
`;

const ARCHIVE = `
steps:
  - name: output
    run: echo archive-output
`;

// The child DAG is in the same file, so its sub-run belongs to the parent run.
const PARENT_WITH_CHILD = `
steps:
  - name: call-child
    action: dag.run
    with:
      dag: popout-child
---
name: popout-child
steps:
  - name: inner
    run: echo popout-child-marker
`;

// Same limit as ir.DAGNameMaxLen; the API validates the file name with it.
const MAX_FILE_NAME = 40;

const test = base.extend<{
  /** Writes the YAML under a unique name; the DAG is deleted after the test. */
  inlineDAG: (spec: string, prefix?: string) => Promise<FixtureDAG>;
}>({
  inlineDAG: async ({ request }, use) => {
    const token = await adminToken(request);
    const created: string[] = [];
    await use(async (spec, prefix = 'e2e-ws-popout') => {
      const dagName = uniqueName(prefix);
      // The API refuses a file name ('.yaml' included) longer than 40
      // characters; uniqueName adds about 16 characters to the prefix.
      expect(
        `${dagName}.yaml`.length,
        `file name of ${dagName}`
      ).toBeLessThanOrEqual(MAX_FILE_NAME);
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

async function runToEnd(
  request: APIRequestContext,
  token: string,
  dag: FixtureDAG
): Promise<string> {
  const runId = await startDAG(request, token, dag.fileName);
  await waitForRunStatus(request, token, dag.dagName, runId, ['succeeded']);
  return runId;
}

/** The sub-run that the step of a finished root run started. */
async function subRunOf(
  request: APIRequestContext,
  token: string,
  dagName: string,
  dagRunId: string,
  step: string
): Promise<string> {
  let subRunId = '';
  await expect
    .poll(
      async () => {
        const response = await request.get(
          `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(dagRunId)}?remoteNode=local`,
          { headers: { Authorization: `Bearer ${token}` } }
        );
        if (!response.ok()) return '';
        const body = (await response.json()) as {
          dagRunDetails: {
            nodes?: {
              step: { name: string };
              subRuns?: { dagRunId: string }[];
            }[];
          };
        };
        subRunId =
          body.dagRunDetails.nodes?.find((node) => node.step.name === step)
            ?.subRuns?.[0]?.dagRunId ?? '';
        return subRunId;
      },
      { timeout: 30_000 }
    )
    .not.toBe('');
  return subRunId;
}

test.describe('workspace pop-out logs', () => {
  // The dock tab strip and the log toolbars need the desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  test('opens the step log page on the stream of the dock', async ({
    page,
    context,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(BOTH_STREAMS);
    const runId = await runToEnd(request, token, dag);

    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(dag.fileName)}?step=both&dock=log&remoteNode=local`
    );
    const dock = page.getByTestId('ww-dock');
    await expect(
      dock.getByRole('tab', { name: 'Log', exact: true })
    ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
    // The Run panel starts collapsed, and a ?dock link opens it.
    await expect(dock).toHaveAttribute('data-state', 'open');

    const stepLog = dock.getByTestId('ww-step-log');
    await expect(stepLog).toContainText('popout-stdout-marker', {
      timeout: 30_000,
    });
    await stepLog.getByRole('button', { name: 'stderr', exact: true }).click();
    await expect(stepLog).toContainText('popout-stderr-marker', {
      timeout: 30_000,
    });
    await expect
      .poll(() => new URL(page.url()).searchParams.get('stream'))
      .toBe('stderr');

    // The dock has no new-tab link: the step facts link the log page, and
    // a Cmd/Ctrl+click on it opens the page in a new tab.
    const popout = await context.newPage();
    await popout.goto(
      `/dags/${encodeURIComponent(dag.fileName)}/log?remoteNode=local&dagRunId=${runId}&step=both&stream=stderr`
    );
    await popout.waitForLoadState('domcontentloaded');

    // The run may name its source file with or without '.yaml'.
    const url = new URL(popout.url());
    expect(url.pathname).toMatch(
      new RegExp(`^/dags/${dag.dagName}(\\.yaml)?/log$`)
    );
    expect(url.searchParams.get('step')).toBe('both');
    expect(url.searchParams.get('stream')).toBe('stderr');
    expect(url.searchParams.get('dagRunId')).toBe(runId);
    expect(url.searchParams.get('remoteNode')).toBe('local');

    const view = popout.getByTestId('ww-popout');
    await expect(view).toHaveAttribute('data-tab', 'log', {
      timeout: 30_000,
    });
    await expect(popout.getByTestId('workflow-workspace')).toHaveCount(0);
    await expect(
      view.getByRole('button', { name: 'stderr', exact: true })
    ).toHaveAttribute('aria-pressed', 'true');
    const output = view.getByRole('region', {
      name: 'Step output',
      exact: true,
    });
    await expect(output).toContainText('popout-stderr-marker', {
      timeout: 30_000,
    });
    await expect(output).not.toContainText('popout-stdout-marker');

    // A root run links back to the workspace on the same step log: the
    // workflow route, with ?run, ?step and ?dock, and no Executions path.
    const back = popout
      .getByTestId('ww-popout-bar')
      .getByRole('link', { name: 'Open in workflow' });
    await expect(back).toHaveAttribute(
      'href',
      new RegExp(`[?&]run=${runId}(&|$)`)
    );
    await expect(back).toHaveAttribute('href', /[?&]step=both(&|$)/);
    await expect(back).toHaveAttribute('href', /[?&]dock=log(&|$)/);
    await expect(back).not.toHaveAttribute('href', /\/executions/);
    await back.click();
    await expect
      .poll(() => new URL(popout.url()).pathname, { timeout: 30_000 })
      .toMatch(new RegExp(`^/dags/${dag.dagName}(\\.yaml)?$`));
    expect(new URL(popout.url()).searchParams.get('run')).toBe(runId);
    await expect(popout.getByTestId('ww-pane')).toBeVisible({
      timeout: 30_000,
    });
    await expect(popout.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'open'
    );
    await popout.close();
  });

  // parity:execution-log-viewer
  test('downloads the step logs of /dagRun-log as a ZIP', async ({
    page,
    context,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(ARCHIVE, 'e2e-ws-popout-zip');
    const runId = await runToEnd(request, token, dag);

    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(dag.fileName)}/dagRun-log?dagRunId=${runId}&remoteNode=local`
    );
    const popout = page.getByTestId('ww-popout');
    await expect(popout).toHaveAttribute('data-tab', 'dagRun-log', {
      timeout: 30_000,
    });
    const runURL = page.url();

    const navigation = context.waitForEvent(
      'request',
      (req) =>
        req.method() === 'POST' &&
        new URL(req.url()).pathname.endsWith('/steps/log/download')
    );
    const downloaded = page.waitForEvent('download');
    await popout
      .getByRole('button', { name: 'Download step logs (ZIP)' })
      .click();
    const download = await downloaded;
    expect((await navigation).isNavigationRequest()).toBe(true);
    expect(download.suggestedFilename()).toBe(
      `${dag.dagName}-${runId}-steps.zip`
    );
    const path = await download.path();
    expect(path).not.toBeNull();
    const bytes = await readFile(path!);
    expect(bytes.subarray(0, 4).toString('hex')).toBe('504b0304');
    expect(bytes.includes(Buffer.from('001-output/stdout.log'))).toBe(true);
    expect(bytes.subarray(-22, -18).toString('hex')).toBe('504b0506');
    await expect(page).toHaveURL(runURL);
    await expect(
      page.getByText('Download requested. Check your browser downloads.')
    ).toBeVisible();
  });

  // parity:header-subrun-breadcrumb parity:header-actions-visibility
  // parity:shell-subdag-run-load parity:run-header-breadcrumb
  test('shows a sub-run log with its breadcrumb and no workflow actions', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const parent = await inlineDAG(PARENT_WITH_CHILD, 'e2e-ws-pop-parent');
    const rootId = await runToEnd(request, token, parent);
    const subId = await subRunOf(
      request,
      token,
      parent.dagName,
      rootId,
      'call-child'
    );

    // Control: the pop-out of the root run shows its run actions and the
    // link back to the workflow, so their absence below means something.
    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(parent.fileName)}/log?dagRunId=${rootId}&step=call-child&remoteNode=local`
    );
    const rootBar = page.getByTestId('ww-popout-bar');
    await expect(
      rootBar.getByRole('heading', { level: 1, name: parent.dagName })
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      rootBar.getByRole('button', { name: 'Retry', exact: true })
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      rootBar.getByRole('link', { name: 'Open in workflow' })
    ).toBeVisible();

    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(parent.fileName)}/log?dagRunId=${rootId}&dagRunName=${parent.dagName}&subDAGRunId=${subId}&step=inner&remoteNode=local`
    );
    const popout = page.getByTestId('ww-popout');
    await expect(popout).toHaveAttribute('data-tab', 'log', {
      timeout: 30_000,
    });
    const bar = popout.getByTestId('ww-popout-bar');
    await expect(
      bar.getByRole('heading', { level: 1, name: 'popout-child', exact: true })
    ).toBeVisible({ timeout: 30_000 });
    const crumbs = bar.getByRole('navigation');
    await expect(crumbs).toContainText('Root:');
    await expect(
      crumbs.getByRole('link', { name: parent.dagName, exact: true })
    ).toHaveAttribute(
      'href',
      `/dags/${encodeURIComponent(parent.dagName)}?remoteNode=local&run=${rootId}`
    );
    await expect(
      popout.getByRole('region', { name: 'Step output', exact: true })
    ).toContainText('popout-child-marker', { timeout: 30_000 });

    // No workflow action and no run action: the sub-run belongs to its root.
    const main = page.getByRole('main');
    for (const name of ['Start', 'Rename', 'Delete', 'Stop', 'Retry']) {
      await expect(main.getByRole('button', { name, exact: true })).toHaveCount(
        0
      );
    }
    await expect(
      main.getByRole('menuitem', { name: 'Rename', exact: true })
    ).toHaveCount(0);
    await expect(
      bar.getByRole('link', { name: 'Open in workflow' })
    ).toHaveCount(0);
  });
});
