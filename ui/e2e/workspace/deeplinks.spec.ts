// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext, Page } from '@playwright/test';
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
  expectAt,
  signIn,
  test as base,
  type FixtureDAG,
} from './fixtures';

// Old and new workflow URLs in a browser: each link lands on the URL and the
// zone that the legacy table (state/legacyLinks.ts, rules R1-R11) gives, and
// every redirect keeps remoteNode and workspace. The workspace is one tab:
// the old Executions route redirects to the workflow route, and a link to a
// run opens the Run panel.
// parity:test-details-content-tabs parity:test-e2e-run-page-links
// parity:shell-remote-node

// The workspace label lets the page show the workflow while ?workspace names
// that workspace.
const WORKSPACE = 'e2elinks';

function labelledSpec(marker: string): string {
  return `
labels:
  - workspace=${WORKSPACE}
steps:
  - name: hello
    run: |
      sleep 1
      echo ${marker}
`;
}

// The child DAG is in the same file, so its sub-run belongs to the parent run.
const PARENT_WITH_CHILD = `
labels:
  - workspace=${WORKSPACE}
steps:
  - name: call-child
    action: dag.run
    with:
      dag: deeplink-child
---
name: deeplink-child
steps:
  - name: inner
    run: echo deeplink-child-output
`;

const test = base.extend<{
  /** Writes the YAML under a unique name; the DAG is deleted after the test. */
  inlineDAG: (spec: string, prefix?: string) => Promise<FixtureDAG>;
}>({
  inlineDAG: async ({ request }, use) => {
    const token = await adminToken(request);
    const created: string[] = [];
    await use(async (spec, prefix = 'e2e-ws-deep') => {
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

async function runToEnd(
  request: APIRequestContext,
  token: string,
  dag: FixtureDAG
): Promise<string> {
  const runId = await startDAG(request, token, dag.fileName);
  await waitForRunStatus(request, token, dag.dagName, runId, ['succeeded']);
  return runId;
}

/** The query of every link: the keys that each redirect must keep. */
const CARRY = `remoteNode=local&workspace=${WORKSPACE}`;

function withCarry(target: string): string {
  return `${target}${target.includes('?') ? '&' : '?'}${CARRY}`;
}

type Zone =
  | 'editor'
  | 'dock'
  | 'timeline'
  | 'overview'
  | 'webhook'
  | 'notifications'
  | 'incidents'
  | 'wiki';

/**
 * The pane, the Run panel, the YAML panel, the tab of the Workflow panel or
 * the launcher panel that the URL shows.
 */
async function expectZone(page: Page, zone: Zone): Promise<void> {
  if (zone === 'editor') {
    await expect(page.getByTestId('ww-pane')).toBeVisible({ timeout: 30_000 });
    return;
  }
  if (zone === 'dock' || zone === 'timeline') {
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'open',
      { timeout: 30_000 }
    );
    if (zone === 'timeline') {
      await expect(
        page
          .getByTestId('ww-dock')
          .getByRole('tab', { name: 'Timeline', exact: true })
      ).toHaveAttribute('aria-selected', 'true');
    }
    return;
  }
  if (zone === 'webhook') {
    await expect(page.getByTestId('ve-launcher-panel-webhook')).toBeVisible({
      timeout: 30_000,
    });
    return;
  }
  const tabs = page.getByTestId('ww-workflow-tabs');
  await expect(
    tabs.getByRole('tab', { name: ZONE_TABS[zone], exact: true })
  ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
  await expect(page.getByTestId(`ww-wtab-body-${zone}`)).toBeVisible();
  if (zone === 'overview') {
    await expect(page.getByTestId('ww-runtime-profile')).toBeVisible();
  }
}

function runPagePath(dagName: string, runId: string): string {
  return `/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runId)}`;
}

const ZONE_TABS = {
  overview: 'Overview',
  notifications: 'Notifications',
  incidents: 'Incidents',
  wiki: 'Wiki',
} as const;

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

test.describe('workspace deep links', () => {
  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // R2, R3, R5, R6, R9, R10 (the panel paths) and R11.
  // parity:shell-docs-redirect parity:page-legacy-docs-redirect
  test('opens each workflow tab path in its zone or panel', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const dag = await inlineDAG(labelledSpec('tab-paths'));
    const { fileName } = dag;
    // The timeline tab of the Run panel needs a run.
    await runToEnd(request, await adminToken(request), dag);
    const base = `/dags/${encodeURIComponent(fileName)}`;
    const rows: {
      rule: string;
      from: string;
      to: string;
      query?: Record<string, string>;
      zone: Zone;
    }[] = [
      { rule: 'R3', from: '/status', to: '', zone: 'editor' },
      // The old Spec tab was the YAML: the editor drops ?yaml. The panel
      // itself is in the YAML test of legacy-links.spec.ts.
      {
        rule: 'R5',
        from: '/spec',
        to: '',
        query: { yaml: null },
        zone: 'editor',
      },
      { rule: 'R11', from: '/unknown-tab', to: '', zone: 'editor' },
      {
        rule: 'R2',
        from: '/docs',
        to: '',
        query: { wtab: 'wiki', panel: null },
        zone: 'wiki',
      },
      // The History grid is the timeline of the Run panel.
      {
        rule: 'R9',
        from: '/history',
        to: '',
        query: { dock: 'timeline' },
        zone: 'timeline',
      },
      // The runtime profile of the old settings path is in the Overview tab.
      {
        rule: 'R6',
        from: '/settings',
        to: '',
        query: { panel: null, wtab: null },
        zone: 'overview',
      },
      {
        rule: 'R10',
        from: '/webhook',
        to: '',
        query: { panel: null },
        zone: 'webhook',
      },
      {
        rule: 'R10',
        from: '/notifications',
        to: '',
        query: { wtab: 'notifications', panel: null },
        zone: 'notifications',
      },
      {
        rule: 'R10',
        from: '/incidents',
        to: '',
        query: { wtab: 'incidents', panel: null },
        zone: 'incidents',
      },
      {
        rule: 'R10',
        from: '/wiki',
        to: '',
        query: { wtab: 'wiki', panel: null },
        zone: 'wiki',
      },
    ];

    for (const row of rows) {
      await test.step(`${row.rule} ${row.from}`, async () => {
        await gotoWorkspace(page, withCarry(`${base}${row.from}`));
        await expectAt(page, `${base}${row.to}`, {
          ...row.query,
          remoteNode: 'local',
          workspace: WORKSPACE,
        });
        await expectZone(page, row.zone);
      });
    }
  });

  // R4. parity:run-page-subrun-deeplink parity:shell-subdag-run-load
  test('opens /spec of a sub-run on the root run of that sub-run', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const parent = await inlineDAG(PARENT_WITH_CHILD, 'e2e-ws-deep-parent');
    const rootId = await runToEnd(request, token, parent);
    const subId = await subRunOf(
      request,
      token,
      parent.dagName,
      rootId,
      'call-child'
    );

    await gotoWorkspace(
      page,
      withCarry(
        `/dags/${encodeURIComponent(parent.fileName)}/spec?dagRunId=${rootId}&subDAGRunId=${subId}`
      )
    );
    // The workflow page has no address for a sub-run: the link opens its root run.
    await expectAt(page, `/dags/${encodeURIComponent(parent.fileName)}`, {
      run: rootId,
      subDAGRunId: null,
      remoteNode: 'local',
      workspace: WORKSPACE,
    });
    await expect(
      page
        .getByRole('heading', { level: 1, name: parent.dagName, exact: true })
        .first()
    ).toBeVisible({ timeout: 30_000 });
  });

  // R11: the old Executions route is the workflow route. It redirects, with
  // replace, to /dags/:f with every key; ?run opens the Run panel on the run
  // (parity:chg-classic-executions-link).
  test('redirects /executions to the workflow route', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(labelledSpec('classic-links'));
    const runId = await runToEnd(request, token, dag);
    const base = `/dags/${encodeURIComponent(dag.fileName)}`;
    const carried = { remoteNode: 'local', workspace: WORKSPACE };

    await test.step('/executions?run', async () => {
      await gotoWorkspace(page, withCarry(`${base}/executions?run=${runId}`));
      await expectAt(page, base, { ...carried, run: runId });
      await expectZone(page, 'editor');
      // ?run in the URL at load opens the Run panel on that run.
      await expectZone(page, 'dock');
      await expect(page.getByTestId(`ww-run-bar-${runId}`)).toHaveAttribute(
        'aria-selected',
        'true',
        { timeout: 30_000 }
      );
    });

    await test.step('/executions', async () => {
      await gotoWorkspace(page, withCarry(`${base}/executions`));
      // No run in the link: the page names the latest run.
      await expectAt(page, base, { ...carried, run: runId });
      await expectZone(page, 'editor');
    });
  });
});

test.describe('workspace deep links to runs', () => {
  // The run list, the NDV and the dock need the desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // R1, R5 and R6 with a run, R7 and R8.
  // parity:shell-run-redirect parity:overview-legacy-run-redirect
  // parity:history-selection-url
  test('opens the run links of the legacy table on the run', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(240_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(labelledSpec('run-links'));
    // Oldest first. The timeline of the Run panel shows the runs in this
    // order, so ?idx=2 names the third run.
    const runs: string[] = [];
    for (let i = 0; i < 4; i++) {
      runs.push(await runToEnd(request, token, dag));
    }
    const base = `/dags/${encodeURIComponent(dag.fileName)}`;
    const carried = {
      remoteNode: 'local',
      workspace: WORKSPACE,
    };
    // The bar of a run on the timeline of the Run panel; ?run opens the panel.
    const row = (runId: string) => page.getByTestId(`ww-run-bar-${runId}`);

    await test.step('R1 /status?dagRunId', async () => {
      await gotoWorkspace(
        page,
        withCarry(`${base}/status?dagRunId=${runs[3]}`)
      );
      await expectAt(page, base, { ...carried, run: runs[3] });
      await expect(
        page
          .getByRole('heading', { level: 1, name: dag.dagName, exact: true })
          .first()
      ).toBeVisible({ timeout: 30_000 });
    });

    await test.step('R5 /spec?dagRunId', async () => {
      await gotoWorkspace(
        page,
        withCarry(`${base}/spec?dagRunId=${runs[1]}&dagRunName=${dag.dagName}`)
      );
      // The old Spec tab was the YAML: the editor opens it and keeps ?run.
      await expectAt(page, base, {
        run: runs[1],
        yaml: null,
        dagRunId: null,
        dagRunName: null,
        ...carried,
      });
      await expectZone(page, 'editor');
      // The YAML panel and the Run panel take turns: the Run panel waits,
      // collapsed, while the YAML shows.
      await expect(page.getByTestId('ndv')).toHaveAttribute(
        'data-state',
        'docked',
        { timeout: 30_000 }
      );
      await expect(page.getByTestId('ww-dock')).toHaveAttribute(
        'data-state',
        'collapsed'
      );
    });

    await test.step('R6 /webhook?dagRunId', async () => {
      await gotoWorkspace(
        page,
        withCarry(`${base}/webhook?dagRunId=${runs[1]}`)
      );
      // A panel is not about one run: the link's run goes, and the page names
      // the latest run (the last of the four).
      await expectAt(page, base, {
        run: runs[3],
        dagRunId: null,
        panel: null,
        ...carried,
      });
      await expectZone(page, 'webhook');
    });

    await test.step('R7 /history?dagRunId&idx', async () => {
      await gotoWorkspace(
        page,
        withCarry(`${base}/history?dagRunId=${runs[0]}&idx=2`)
      );
      await expectAt(page, base, {
        run: runs[0],
        idx: null,
        dagRunId: null,
        ...carried,
      });
      await expectZone(page, 'dock');
      await expect(row(runs[0])).toHaveAttribute('aria-selected', 'true', {
        timeout: 30_000,
      });
    });

    await test.step('R8 /history?idx', async () => {
      await gotoWorkspace(page, withCarry(`${base}/history?idx=2`));
      await expectAt(page, base, {
        run: runs[2],
        idx: null,
        ...carried,
      });
      // The redirect waits for the run history and opens the Run panel with
      // ?dock; the run is selected on its timeline.
      await expectZone(page, 'dock');
      await expect(row(runs[2])).toHaveAttribute('aria-selected', 'true', {
        timeout: 30_000,
      });
    });
  });

  // The Run panel is always on the page: ?dock=log opens it on the step log.
  // parity:execution-log-viewer
  test('opens the dock log of ?step&dock=log', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(labelledSpec('dock-log-marker'));
    await runToEnd(request, token, dag);
    const base = `/dags/${encodeURIComponent(dag.fileName)}`;

    await gotoWorkspace(page, withCarry(`${base}?step=hello&dock=log`));
    const dock = page.getByTestId('ww-dock');
    await expect(
      dock.getByRole('tab', { name: 'Log', exact: true })
    ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
    await expect(dock).toHaveAttribute('data-state', 'open');
    const log = dock.getByTestId('ww-step-run');
    await expect(log).toHaveAttribute('data-step', 'hello');
    await expect(log).toHaveAttribute('data-variant', 'logs');
    await expect(log).toContainText('dock-log-marker', { timeout: 30_000 });

    await expectAt(page, base, {
      step: 'hello',
      dock: 'log',
      remoteNode: 'local',
      workspace: WORKSPACE,
    });
  });

  // R8 and D6: a link to a run opens the Run panel on it, also when the
  // redirect adds ?run after the history loads.
  test('opens the Run panel on /history?idx', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(labelledSpec('history-idx'));
    const runs: string[] = [];
    for (let i = 0; i < 2; i++) runs.push(await runToEnd(request, token, dag));
    const base = `/dags/${encodeURIComponent(dag.fileName)}`;

    await gotoWorkspace(page, withCarry(`${base}/history?idx=1`));
    await expectAt(page, base, {
      run: runs[1],
      idx: null,
      remoteNode: 'local',
      workspace: WORKSPACE,
    });
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'open',
      { timeout: 10_000 }
    );
  });

  // ?def=run shows the definition that the run used, read-only, in place of
  // the editor. The old Executions route keeps the key, and the back button
  // of the snapshot returns to the current definition.
  test('opens the read-only definition of ?run&def=run', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(labelledSpec('def-run-marker'));
    const runId = await runToEnd(request, token, dag);
    const base = `/dags/${encodeURIComponent(dag.fileName)}`;
    const carried = { remoteNode: 'local', workspace: WORKSPACE };

    await gotoWorkspace(
      page,
      withCarry(`${base}/executions?run=${runId}&def=run`)
    );
    await expectAt(page, base, { ...carried, run: runId, def: 'run' });
    const snapshot = page.getByTestId('ww-run-definition');
    await expect(snapshot).toBeVisible({ timeout: 30_000 });
    await expect(snapshot).toHaveAttribute('data-run-id', runId);
    await expect(snapshot.getByTestId('ve-canvas-node-steps[0]')).toBeVisible({
      timeout: 30_000,
    });

    await page.getByTestId('ww-run-snapshot-back').click();
    await expectAt(page, base, { ...carried, run: runId, def: null });
    await expect(page.getByTestId('ww-run-definition')).toHaveCount(0);
    await expect(page.getByTestId('ww-canvas')).toBeVisible();
  });

  // The node view of a step fills the zone: ?ndv=expanded. It holds the Run
  // panel collapsed, even when the same link has ?dock.
  // parity:drawer-url-sync
  test('opens the node view of ?step&ndv=expanded over the collapsed dock', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const dag = await inlineDAG(labelledSpec('ndv-marker'));
    await runToEnd(request, token, dag);
    const base = `/dags/${encodeURIComponent(dag.fileName)}`;

    await gotoWorkspace(
      page,
      withCarry(`${base}?step=hello&ndv=expanded&dock=log`)
    );
    const ndv = page.getByTestId('ww-pane').getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect(ndv).toHaveAttribute('data-state', 'expanded');
    await expectZone(page, 'editor');
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );
    await expectAt(page, base, {
      step: 'hello',
      ndv: 'expanded',
      remoteNode: 'local',
      workspace: WORKSPACE,
    });
  });

  // parity:test-e2e-run-page-links parity:runpage-header
  test('redirects the old run page to the workflow with the run', async ({
    page,
    request,
    fixtureDAG,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    // No workspace label: the old link names no workspace.
    const dag = await fixtureDAG('visual-params.yaml', 'e2e-ws-deep-show');
    const older = await startDAG(request, token, dag.fileName);
    await waitForRunStatus(request, token, dag.dagName, older, [
      'succeeded',
      'partially_succeeded',
    ]);
    const newer = await startDAG(request, token, dag.fileName);
    await waitForRunStatus(request, token, dag.dagName, newer, [
      'succeeded',
      'partially_succeeded',
    ]);
    // The source file name of the run may come with or without '.yaml'.
    const workflowPath = new RegExp(`^/dags/${dag.dagName}(\\.yaml)?$`);

    await test.step('opens the run in the workspace', async () => {
      await page.goto(runPagePath(dag.dagName, older));

      await expect
        .poll(() => new URL(page.url()).pathname, { timeout: 30_000 })
        .toMatch(workflowPath);
      const url = new URL(page.url());
      expect(url.searchParams.get('run')).toBe(older);
      expect(url.searchParams.get('remoteNode')).toBe('local');
      await expectZone(page, 'editor');
      await expectZone(page, 'dock');
      await expect(page.getByTestId(`ww-run-bar-${older}`)).toHaveAttribute(
        'aria-selected',
        'true',
        { timeout: 30_000 }
      );
    });
  });
});
