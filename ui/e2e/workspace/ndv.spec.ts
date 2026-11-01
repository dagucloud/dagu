// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readFileSync } from 'node:fs';
import path from 'node:path';
import type {
  APIRequestContext,
  Locator,
  Page,
  Response,
  Route,
} from '@playwright/test';
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

// parity:test-e2e-gaps (the step view of the canvas: NDV, step YAML, Set
// status, Retry step and the failed-run dock. The workflow is one tab: the
// canvas shows the selected run (the latest without ?run), and the Runs panel
// is at the bottom. The manual actions are in manual-actions.spec.ts)

// NodeStatus in ui/src/api/v1/schema.ts.
const NODE_FAILED = '2';
const NODE_SUCCEEDED = '4';

// Two steps whose stdout changes at each run, so a rerun shows.
const TWO_STEPS = `
steps:
  - name: first
    run: echo "first-$(date +%s%N)"
  - name: second
    depends: [first]
    run: echo "second-$(date +%s%N)"
`;

// The second step writes a marker to stderr, then fails the run.
const FAILING = `
steps:
  - name: ok
    run: echo fine
  - name: broken
    depends: [ok]
    run: |
      echo "broken-stderr-marker" >&2
      exit 3
`;

// The second step has no id and an out-of-order, long description; the first
// and the third are canonical, so that a change outside the second is a bug.
const LONG_WORDS = 'word '.repeat(30).trim();
const STEP_TOOLS = `
steps:
  - id: gather
    name: gather
    run: echo gather

  - run: echo second   # keep this
    name: second step
    depends: [gather]
    outputs:
      - name: out
    description: ${LONG_WORDS} end

  - id: third
    name: third
    run: echo third
`;

// The second step has no id, as the two others have: the editor offers the
// quick fix that adds one.
const NO_ID = `
steps:
  - name: first step
    id: first_step
    run: echo first
  - name: second step
    depends: [first_step]
    run: echo second
  - name: third step
    id: third_step
    depends: [first_step]
    run: echo third
`;

const test = base.extend<{
  /** Writes the YAML under a unique name; the DAG is deleted after the test. */
  inlineDAG: (spec: string, prefix?: string) => Promise<FixtureDAG>;
}>({
  inlineDAG: async ({ request }, use) => {
    const token = await adminToken(request);
    const created: string[] = [];
    await use(async (spec, prefix = 'e2e-ws-ndv') => {
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

/** The saved text of the DAG, as the server has it. */
async function readSpecOf(
  request: APIRequestContext,
  token: string,
  fileName: string
): Promise<string> {
  const response = await request.get(
    `/api/v1/dags/${encodeURIComponent(fileName)}/spec?remoteNode=local`,
    { headers: { Authorization: `Bearer ${token}` } }
  );
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as { spec: string }).spec;
}

/** The position and size of an element, in whole pixels. */
async function box(locator: Locator): Promise<string> {
  const b = await locator.boundingBox();
  return b ? [b.x, b.y, b.width, b.height].map(Math.round).join(',') : 'none';
}

/** The status of one step in a run, as the server has it. */
async function serverNodeStatus(
  request: APIRequestContext,
  token: string,
  dagName: string,
  dagRunId: string,
  step: string
): Promise<number | undefined> {
  const response = await request.get(
    `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(dagRunId)}?remoteNode=local`,
    { headers: { Authorization: `Bearer ${token}` } }
  );
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as {
    dagRunDetails: { nodes?: { step: { name: string }; status: number }[] };
  };
  return body.dagRunDetails.nodes?.find((node) => node.step.name === step)
    ?.status;
}

function canvasNode(page: Page, key: string): Locator {
  return page.getByTestId('ww-canvas').getByTestId(`ve-canvas-node-${key}`);
}

/** A node of the read-only canvas of the run definition (?def=run). */
function runNode(page: Page, key: string): Locator {
  return page
    .getByTestId('ww-run-definition')
    .getByTestId(`ve-canvas-node-${key}`);
}

/** The status that the selected run puts on a node of the canvas. */
function nodeStatus(page: Page, key: string): Locator {
  return canvasNode(page, key).getByTestId('ve-canvas-node-status');
}

/** The URL of the workflow, on one run when given. */
function workflowPath(fileName: string, runId?: string): string {
  const base = `/dags/${encodeURIComponent(fileName)}`;
  return runId ? `${base}?run=${encodeURIComponent(runId)}` : base;
}

function saveButton(page: Page): Locator {
  return page.locator('#save-config');
}

function query(page: Page, key: string): string | null {
  return new URL(page.url()).searchParams.get(key);
}

/** Opens the workspace editor and waits for the canvas to show the first step. */
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

/** Opens the node menu of the canvas with a right-click and picks a row. */
async function pickNodeMenu(
  page: Page,
  key: string,
  label: string
): Promise<void> {
  await canvasNode(page, key).click({ button: 'right' });
  const menu = page.getByTestId('ve-canvas-menu');
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: label, exact: true }).click();
}

/** Sets the step to Failed in the Update status dialog. */
async function setFailed(page: Page, key: string): Promise<void> {
  await pickNodeMenu(page, key, 'Change status');
  const dialog = page.getByRole('dialog', { name: 'Update status' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Failed' }).click();
  await expect(dialog).toBeHidden({ timeout: 30_000 });
}

test.describe('workspace node details view', () => {
  // The docked NDV, the node menu and the dock tab strip need the desktop
  // layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // parity:drawer-url-sync parity:run-graph-click-inspect
  test('opens the node view on a double-click with the outputs of the run', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [
      'succeeded',
      'partially_succeeded',
    ]);

    await openWorkspace(page, gotoWorkspace, workflowPath(fileName));
    // One tab: the editor shows the latest run on its canvas and in the bar,
    // and the Runs panel is collapsed at load.
    await expect(page.getByTestId('ww-run-chip')).toBeVisible();
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );
    await expect(nodeStatus(page, 'steps[0]')).toHaveAttribute(
      'data-status',
      NODE_SUCCEEDED,
      { timeout: 60_000 }
    );

    // A single click only selects the step. It does not open the Runs panel.
    await canvasNode(page, 'steps[0]').click();
    await expect.poll(() => query(page, 'step')).toBe('prepare');
    await expect(page.getByTestId('ndv')).toBeHidden();
    expect(query(page, 'ndv')).toBeNull();
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );

    await canvasNode(page, 'steps[0]').dblclick();
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect(ndv).toHaveAttribute('data-state', 'expanded');
    await expect.poll(() => query(page, 'step')).toBe('prepare');
    await expect.poll(() => query(page, 'ndv')).toBe('expanded');

    // The view sits below the workflow bar, so that Start and the run menu
    // stay clickable.
    const bar = await page.getByTestId('ww-bar').boundingBox();
    expect((await ndv.boundingBox())!.y).toBeGreaterThanOrEqual(
      bar!.y + bar!.height - 0.5
    );

    // INPUT | CENTER | OUTPUT. The output column is the right side of the
    // Runs panel for this step: the facts line and the tabs of the latest run.
    await expect(ndv.getByRole('button', { name: 'Run step' })).toBeVisible();
    await expect(ndv.getByRole('region', { name: 'Input' })).toBeVisible();
    const output = ndv.getByRole('region', { name: 'Output', exact: true });
    await expect(output).toBeVisible();
    await expect(output.getByTestId('ww-step-output')).toHaveAttribute(
      'data-run-id',
      runId
    );
    await expect(output.getByTestId('ww-step-facts-line')).toBeVisible();
    await expect(
      output.getByRole('tab', { name: 'Output', exact: true })
    ).toBeVisible();

    // Escape closes the view and keeps the selection. It comes before the
    // trial clicks below: their hover opens a tooltip that takes one Escape.
    await page.keyboard.press('Escape');
    await expect(ndv).toBeHidden();
    await expect.poll(() => query(page, 'ndv')).toBeNull();
    expect(query(page, 'step')).toBe('prepare');

    // Open again: the view leaves Start and the run menu clickable.
    await canvasNode(page, 'steps[0]').dblclick();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect(ndv).toHaveAttribute('data-state', 'expanded');
    await page
      .locator('[data-ww-action="start"]')
      .click({ trial: true, timeout: 5_000 });
  });

  // parity:drawer-url-sync
  test('shows the declared outputs of a step that never ran', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await openWorkspace(page, gotoWorkspace, workflowPath(fileName));
    // No run: the bar has no run chip.
    await expect(page.getByTestId('ww-run-chip')).toHaveCount(0);
    await canvasNode(page, 'steps[0]').dblclick();
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    const output = ndv.getByRole('region', { name: 'Output', exact: true });
    await expect(
      output.getByRole('region', { name: 'Declared outputs' })
    ).toContainText('token', { timeout: 30_000 });
  });

  // parity:drawer-url-sync parity:run-graph-click-inspect
  test('shows the run of the step in the node view and the Run panel', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [
      'succeeded',
      'partially_succeeded',
    ]);

    // No ?run: the latest run.
    await openWorkspace(page, gotoWorkspace, workflowPath(fileName));
    await expect(nodeStatus(page, 'steps[0]')).toHaveAttribute(
      'data-status',
      NODE_SUCCEEDED,
      { timeout: 60_000 }
    );

    // The run chip keeps its own place in the bar: none of its facts runs
    // under the Start button.
    const start = page.locator('[data-ww-action="start"]');
    const latest = page.getByTestId('ww-run-chip').getByTestId('ww-run-latest');
    const startBox = await start.boundingBox();
    const latestBox = await latest.boundingBox();
    expect(latestBox!.x + latestBox!.width).toBeLessThanOrEqual(startBox!.x);

    const ndv = page.getByTestId('ndv');
    const dock = page.getByTestId('ww-dock');
    // A click selects the step: the canvas and the step list of the Runs
    // panel follow. The panel stays collapsed and the node view stays closed.
    await canvasNode(page, 'steps[0]').click();
    await expect.poll(() => query(page, 'step')).toBe('prepare');
    expect(query(page, 'ndv')).toBeNull();
    await expect(ndv).toBeHidden();
    await expect(dock).toHaveAttribute('data-state', 'collapsed');

    // A double click opens the node view, with the facts of the run in the
    // OUTPUT column.
    await canvasNode(page, 'steps[0]').dblclick();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect(ndv).toHaveAttribute('data-state', 'expanded');
    await expect.poll(() => query(page, 'step')).toBe('prepare');
    await expect.poll(() => query(page, 'ndv')).toBe('expanded');
    await expect(page.getByTestId('step-side-drawer')).toHaveCount(0);

    // The YAML of the view is the editor of the step alone: coloured, from
    // line 1, no change bars while nothing changed.
    const yaml = ndv.getByTestId('ndv-center').locator('.monaco-editor');
    await expect(yaml).toBeVisible({ timeout: 30_000 });
    const yamlLines = yaml.locator('.view-lines');
    await expect(yamlLines).toContainText('DAGU_OUTPUT_FILE', {
      timeout: 30_000,
    });
    await expect(yamlLines).not.toContainText('GREETING');
    await expect(yamlLines).not.toContainText('flaky');
    await expect(yaml.locator('.line-numbers').first()).toHaveText('1');
    await expect(yamlLines.locator('[class*="mtk"]').first()).toBeVisible();
    // Many colours: keys, strings and the rest are not one plain text.
    await expect
      .poll(async () =>
        yamlLines
          .locator('span[class*="mtk"]')
          .evaluateAll(
            (spans) =>
              new Set(spans.map((span) => getComputedStyle(span).color)).size
          )
      )
      .toBeGreaterThan(1);
    await expect(yaml.locator('.dag-change-added')).toHaveCount(0);
    await expect(yaml.locator('.dag-change-modified')).toHaveCount(0);
    await expect(ndv.getByRole('button', { name: 'Copy YAML' })).toBeVisible();

    // The output column is a tab when the view is narrow: the facts line of
    // the step, then the tabs of the latest run.
    await showStepOutput(ndv);
    const stepOutput = ndv.locator('#ndv-output').getByTestId('ww-step-output');
    await expect(stepOutput).toHaveAttribute('data-run-id', runId);
    await expect(stepOutput.getByTestId('ww-step-facts-line')).toContainText(
      'Succeeded'
    );
    // The Output tab shows the output that the step wrote (token=abc123).
    await stepOutput.getByRole('tab', { name: 'Output', exact: true }).click();
    await expect(stepOutput).toContainText('abc123', { timeout: 30_000 });

    // Escape closes the node view and keeps the step; a second Escape clears
    // the step.
    await page.keyboard.press('Escape');
    await expect(ndv).toBeHidden();
    await expect.poll(() => query(page, 'ndv')).toBeNull();
    expect(query(page, 'step')).toBe('prepare');
    await page.keyboard.press('Escape');
    await expect.poll(() => query(page, 'step')).toBeNull();

    // A panel that the user opens before the view shows stays open when the
    // view closes: the expanded view covers the tab of the panel.
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'open');

    // The Open step command of the node toolbar opens the node view again.
    await canvasNode(page, 'steps[0]').click();
    await canvasNode(page, 'steps[0]')
      .locator('..')
      .getByRole('button', { name: 'Open step', exact: true })
      .click();
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect.poll(() => query(page, 'step')).toBe('prepare');
    await expect.poll(() => query(page, 'ndv')).toBe('expanded');

    await page.keyboard.press('Escape');
    await expect(ndv).toBeHidden();
    await expect(dock).toHaveAttribute('data-state', 'open');
  });

  // The YAML button of the toolbar opens the whole definition in the same
  // right panel on the editor and on the run definition (?def=run). The
  // run definition is read-only and takes no edit. The stale dot of the run
  // chip shows it, and the dot or the back button ends it.
  // parity:drawer-url-sync
  test('opens the whole-file YAML panel on the run definition, as the editor does', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [
      'succeeded',
      'partially_succeeded',
    ]);
    // The saved file changes after the run: the run used another definition.
    await writeLocalDAG(
      dagName,
      `${readFileSync(path.resolve(__dirname, '../fixtures/dags/visual-params.yaml'), 'utf8')}
  - name: added-after-run
    depends: [report]
    run: echo added
`
    );
    const rect = (locator: Locator) =>
      locator.evaluate((element) => {
        const r = element.getBoundingClientRect();
        return {
          x: Math.round(r.x),
          y: Math.round(r.y),
          width: Math.round(r.width),
          height: Math.round(r.height),
        };
      });
    // The box of a part of the panel, from the top right corner of the graph
    // zone that holds it: the zones of the two views differ in their size.
    const measure = async (zone: Locator, zoneBox: Locator) => {
      const ndv = zone.getByTestId('ndv');
      const origin = await zoneBox.evaluate((element) => {
        const r = element.getBoundingClientRect();
        return { right: r.right, top: r.top, bottom: r.bottom };
      });
      const rel = async (locator: Locator) => {
        const r = await rect(locator);
        return {
          fromRight: origin.right - (r.x + r.width),
          fromTop: r.y - origin.top,
          fromBottom: origin.bottom - (r.y + r.height),
          width: r.width,
          height: r.height,
        };
      };
      return {
        panel: await rel(ndv),
        header: await rel(ndv.locator('[data-panel-header]')),
        close: await rel(ndv.getByRole('button', { name: 'Close' })),
      };
    };

    // The editor first: the reference of the geometry.
    await openWorkspace(page, gotoWorkspace, workflowPath(fileName, runId));
    const toolbar = page.getByTestId('ve-toolbar');
    await toolbar.getByRole('button', { name: 'YAML', exact: true }).click();
    const editorNdv = page.getByTestId('ww-pane').getByTestId('ndv');
    await expect(editorNdv).toHaveAttribute('data-state', 'docked', {
      timeout: 30_000,
    });
    await expect(editorNdv.locator('.monaco-editor')).toBeVisible({
      timeout: 30_000,
    });
    expect(query(page, 'ndv')).toBe('docked');
    const editorBox = await measure(
      page.getByTestId('ww-pane'),
      page.getByTestId('ww-canvas')
    );
    const editorButton = await rect(
      toolbar.getByRole('button', { name: 'YAML', exact: true })
    );

    // The stale dot opens the definition of the run in place of the editor.
    await page.goto(workflowPath(fileName, runId));
    const dot = page.getByTestId('ww-run-stale-dot');
    await expect(dot).toBeVisible({ timeout: 30_000 });
    await expect(dot).toHaveAttribute('aria-pressed', 'false');
    await dot.click();
    await expect.poll(() => query(page, 'def')).toBe('run');
    await expect(dot).toHaveAttribute('aria-pressed', 'true');
    const definition = page.getByTestId('ww-run-definition');
    await expect(definition).toBeVisible({ timeout: 30_000 });
    await expect(definition).toHaveAttribute('data-run-id', runId);
    const zone = definition.getByTestId('ww-run-zone');
    await expect(
      runNode(page, 'steps[0]'),
      'the nodes of the run, not the added step'
    ).toBeVisible({ timeout: 30_000 });
    await expect(runNode(page, 'steps[3]')).toHaveCount(0);
    const yamlButton = zone
      .getByTestId('ve-toolbar')
      .getByRole('button', { name: 'YAML', exact: true });
    await expect(yamlButton).toHaveAttribute('aria-pressed', 'false');
    await yamlButton.focus();
    await yamlButton.click();
    const ndv = zone.getByTestId('ndv');
    await expect(ndv).toHaveAttribute('data-state', 'docked', {
      timeout: 30_000,
    });
    await expect(ndv).toHaveAttribute('data-panel-shell', 'right');
    await expect(yamlButton).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(() => query(page, 'ndv')).toBe('docked');
    expect(query(page, 'step')).toBeNull();

    // The header: the YAML title, the run identity and the lock.
    const header = ndv.locator('[data-panel-header]');
    await expect(header.getByRole('heading', { name: 'YAML' })).toBeVisible();
    await expect(ndv.getByTestId('readonly-marker')).toBeVisible();
    await expect(ndv.getByTestId('yaml-run-ref')).toHaveText(runId.slice(0, 8));
    expect((await rect(header)).height, 'the header is 48 px high').toBe(48);

    // The editor: the whole file of the run, coloured, absolute lines,
    // read-only.
    const yaml = ndv.locator('.monaco-editor');
    await expect(yaml).toBeVisible({ timeout: 30_000 });
    const lines = yaml.locator('.view-lines');
    await expect(lines).toContainText('prepare', { timeout: 30_000 });
    await expect(lines).toContainText('DAGU_OUTPUT_FILE');
    await expect(lines).not.toContainText('added-after-run');
    await expect(yaml.locator('.line-numbers').first()).toHaveText('1');
    await expect
      .poll(async () =>
        lines
          .locator('span[class*="mtk"]')
          .evaluateAll(
            (spans) =>
              new Set(spans.map((span) => getComputedStyle(span).color)).size
          )
      )
      .toBeGreaterThan(1);
    await expect(yaml.locator('.dag-change-added')).toHaveCount(0);
    await expect(yaml.locator('.dag-change-modified')).toHaveCount(0);
    // No tools of the editor.
    await expect(ndv.getByTestId('yaml-format')).toHaveCount(0);
    await expect(ndv.getByTestId('yaml-fix-all')).toHaveCount(0);
    await expect(ndv.getByRole('button', { name: 'Open step' })).toHaveCount(0);
    await expect(zone.getByRole('button', { name: 'Save' })).toHaveCount(0);

    // A typed key changes nothing: the text of the run stays.
    const before = await lines.innerText();
    await lines.click();
    await page.keyboard.type('zzz-not-typed');
    await expect(lines).not.toContainText('zzz-not-typed');
    expect(await lines.innerText()).toBe(before);

    // The same box in both views.
    const runBox = await measure(zone, zone);
    // The panel is one box from the top of the zone to the bottom: its
    // header stands under the row of the canvas toolbars. Its height is the
    // one of the zone, which differs between the views.
    expect(runBox.panel).toMatchObject({
      fromRight: 0,
      fromTop: 0,
      fromBottom: 0,
      width: editorBox.panel.width,
    });
    const top = (box: typeof runBox.header) => ({
      fromRight: box.fromRight,
      fromTop: box.fromTop,
      width: box.width,
      height: box.height,
    });
    expect(top(runBox.header)).toEqual(top(editorBox.header));
    expect(top(runBox.close)).toEqual(top(editorBox.close));
    // The YAML button sits at the same place of the toolbar, with its look.
    const runButton = await rect(yamlButton);
    expect(runButton.width).toBe(editorButton.width);
    expect(runButton.height).toBe(editorButton.height);

    // The Copy overlay copies the definition of the run.
    await page
      .context()
      .grantPermissions(['clipboard-read', 'clipboard-write']);
    await ndv.getByRole('button', { name: 'Copy YAML' }).click();
    await expect(
      ndv.getByRole('button', { name: 'YAML copied' })
    ).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => navigator.clipboard.readText()))
      .toContain('prepare');

    // Escape closes the panel; the focus goes back to the YAML button.
    await header.click();
    await page.keyboard.press('Escape');
    await expect(ndv).toBeHidden();
    await expect.poll(() => query(page, 'ndv')).toBeNull();
    await expect(yamlButton).toBeFocused();
    await expect(yamlButton).toHaveAttribute('aria-pressed', 'false');

    // The back button ends the view of the run definition.
    await definition.getByTestId('ww-run-snapshot-back').click();
    await expect.poll(() => query(page, 'def')).toBeNull();
    await expect(definition).toHaveCount(0);
    await expect(dot).toHaveAttribute('aria-pressed', 'false');
  });

  // The YAML editor never unmounts when the frame changes its layout.
  // parity:chg-step-yaml-live
  test('keeps the typed step YAML across the form, and undoes it', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await openWorkspace(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}?step=prepare&ndv=expanded`
    );
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toHaveAttribute('data-state', 'expanded', {
      timeout: 30_000,
    });
    const editor = page.getByTestId('ndv-center').locator('.monaco-editor');
    await expect(editor).toBeVisible({ timeout: 30_000 });
    const lines = editor.locator('.view-lines');
    await expect(saveButton(page)).toBeDisabled();

    // A YAML comment at the end of the run line of the step.
    await editor
      .locator('.view-line')
      .filter({ hasText: 'DAGU_OUTPUT_FILE' })
      .click();
    await page.keyboard.press('End');
    await page.keyboard.type(' #ndvmarker');
    await expect(lines).toContainText('ndvmarker');
    await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });

    // The node view has no dock button: the form and the YAML take turns.
    await expect(
      ndv.getByRole('button', { name: 'Dock to the side', exact: true })
    ).toHaveCount(0);
    await ndv.getByRole('button', { name: 'Form', exact: true }).click();
    await ndv.getByRole('button', { name: 'YAML', exact: true }).click();
    await expect(ndv).toHaveAttribute('data-state', 'expanded');
    await expect(lines).toContainText('ndvmarker');

    // The undo history survived too. Monaco may keep the typing as more
    // than one undo step; an undo at the bottom of the stack does nothing.
    await editor
      .locator('.view-line')
      .filter({ hasText: 'DAGU_OUTPUT_FILE' })
      .click();
    await expect(async () => {
      await page.keyboard.press('ControlOrMeta+z');
      await expect(lines).not.toContainText('ndvmarker', { timeout: 1_000 });
    }).toPass({ timeout: 15_000 });
    await expect(saveButton(page)).toBeDisabled({ timeout: 30_000 });
  });

  // The save state follows the document, not the characters: a blank line
  // is no change. The numbers of the step view start at 1.
  test('keeps Save off for a blank line and numbers the step lines from 1', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await openWorkspace(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}?step=prepare&ndv=expanded`
    );
    const editor = page.getByTestId('ndv-center').locator('.monaco-editor');
    await expect(editor).toBeVisible({ timeout: 30_000 });
    await expect(saveButton(page)).toBeDisabled();
    await expect(editor.locator('.line-numbers').first()).toHaveText('1');

    await editor
      .locator('.view-line')
      .filter({ hasText: 'DAGU_OUTPUT_FILE' })
      .click();
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.press('Enter');
    await expect(editor.locator('.line-numbers').first()).toHaveText('1');
    // The two new lines are in reach of the cursor and change nothing.
    await page.keyboard.type('  ');
    await expect(saveButton(page)).toBeDisabled();
    await expect(editor.locator('.dag-change-added')).toHaveCount(0);

    // A real change turns Save on.
    await page.keyboard.type('# real');
    await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });
  });

  for (const [label, pinned] of [
    ['latest run', false],
    ['pinned run', true],
  ] as const) {
    // parity:graph-rightclick-status parity:steps-status-update-modal
    // parity:api-step-status
    test(`sets a step status from the node menu before and after the refresh (${label})`, async ({
      page,
      request,
      inlineDAG,
      gotoWorkspace,
    }) => {
      test.setTimeout(180_000);
      const token = await adminToken(request);
      const { dagName, fileName } = await inlineDAG(TWO_STEPS);
      const runId = await startDAG(request, token, fileName);
      await waitForRunStatus(request, token, dagName, runId, ['succeeded']);

      await openWorkspace(
        page,
        gotoWorkspace,
        workflowPath(fileName, pinned ? runId : undefined)
      );
      const first = nodeStatus(page, 'steps[0]');
      await expect(first).toHaveAttribute('data-status', NODE_SUCCEEDED, {
        timeout: 60_000,
      });

      // Optimistic: the server never sees this change, and the refresh of the
      // run waits until the ring has changed.
      // The latest run comes with the DAG; its own read is held too.
      const dagPath = `/dags/${encodeURIComponent(fileName)}`;
      const runPath = `/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(runId)}`;
      const isDetails = (url: URL) =>
        url.pathname.endsWith(dagPath) || url.pathname.endsWith(runPath);
      const isStatusPatch = (url: URL) =>
        url.pathname.endsWith('/steps/first/status');
      let holding = false;
      let release = () => {};
      const released = new Promise<void>((resolve) => {
        release = resolve;
      });
      const holdDetails = async (route: Route) => {
        if (holding && route.request().method() === 'GET') await released;
        await route.continue();
      };
      const fakePatch = async (route: Route) => {
        if (route.request().method() !== 'PATCH') {
          await route.continue();
          return;
        }
        holding = true;
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: '{}',
        });
      };
      await page.route(isDetails, holdDetails);
      await page.route(isStatusPatch, fakePatch);
      try {
        await setFailed(page, 'steps[0]');
        await expect(first).toHaveAttribute('data-status', NODE_FAILED);
        expect(
          await serverNodeStatus(request, token, dagName, runId, 'first')
        ).toBe(Number(NODE_SUCCEEDED));
      } finally {
        release();
        await page.unroute(isStatusPatch, fakePatch);
        await page.unroute(isDetails, holdDetails);
      }

      // The optimistic patch lives in the page only.
      await page.reload();
      await expect(first).toHaveAttribute('data-status', NODE_SUCCEEDED, {
        timeout: 60_000,
      });

      // The real change keeps the ring after the refresh and after a reload.
      // Responses arrive in order: a details read after the PATCH response is
      // the refresh (or a later read).
      let patchOk: boolean | null = null;
      let readsAfterPatch = 0;
      const onResponse = (response: Response) => {
        const url = new URL(response.url());
        const method = response.request().method();
        if (method === 'PATCH' && isStatusPatch(url)) {
          patchOk = response.ok();
        } else if (patchOk !== null && method === 'GET' && isDetails(url)) {
          readsAfterPatch += 1;
        }
      };
      page.on('response', onResponse);
      try {
        await setFailed(page, 'steps[0]');
        await expect.poll(() => patchOk, { timeout: 30_000 }).toBe(true);
        await expect(first).toHaveAttribute('data-status', NODE_FAILED);
        await expect
          .poll(() => readsAfterPatch, { timeout: 30_000 })
          .toBeGreaterThan(0);
        await expect(first).toHaveAttribute('data-status', NODE_FAILED);
      } finally {
        page.off('response', onResponse);
      }
      expect(
        await serverNodeStatus(request, token, dagName, runId, 'first')
      ).toBe(Number(NODE_FAILED));
      await page.reload();
      await expect(first).toHaveAttribute('data-status', NODE_FAILED, {
        timeout: 60_000,
      });
    });
  }

  // parity:run-step-retry parity:steps-actions-menu
  test('retries a step and its downstream steps from the node menu', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await inlineDAG(TWO_STEPS);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    const firstBefore = await getStepStdout(
      request,
      token,
      dagName,
      runId,
      'first'
    );
    const secondBefore = await getStepStdout(
      request,
      token,
      dagName,
      runId,
      'second'
    );

    await openWorkspace(page, gotoWorkspace, workflowPath(fileName, runId));
    await expect(nodeStatus(page, 'steps[0]')).toHaveAttribute(
      'data-status',
      NODE_SUCCEEDED,
      { timeout: 60_000 }
    );

    await pickNodeMenu(page, 'steps[0]', 'Retry step');
    const dialog = page.getByRole('dialog', { name: 'Retry this step?' });
    await expect(dialog).toBeVisible();
    const thisOnly = dialog.getByRole('radio', { name: /This step only/ });
    const downstream = dialog.getByRole('radio', {
      name: /This step and all downstream steps/,
    });
    await expect(thisOnly).toBeChecked();
    await downstream.check();
    await expect(downstream).toBeChecked();
    await dialog.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(dialog).toBeHidden({ timeout: 30_000 });

    // The step and its downstream step ran again in the same run. A reset
    // step has no log yet (''), so only a new marker line counts.
    const rerun = async (step: string, before: string) => {
      const stdout = await getStepStdout(request, token, dagName, runId, step, {
        retryNotFound: true,
      });
      return new RegExp(`${step}-\\S+`).test(stdout) && stdout !== before;
    };
    await expect
      .poll(() => rerun('first', firstBefore), { timeout: 60_000 })
      .toBe(true);
    await expect
      .poll(() => rerun('second', secondBefore), { timeout: 60_000 })
      .toBe(true);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
    await expect(nodeStatus(page, 'steps[1]')).toHaveAttribute(
      'data-status',
      NODE_SUCCEEDED,
      { timeout: 60_000 }
    );
  });

  // A link with ?run opens the Runs panel at load. The failed step of the
  // run chip opens the Log tab of that step, on its stderr.
  // parity:execution-log-viewer parity:run-log-viewer
  test('opens the Runs panel on ?run and the failed step log from the chip', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await inlineDAG(FAILING);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['failed']);

    await openWorkspace(page, gotoWorkspace, workflowPath(fileName, runId));
    const dock = page.getByTestId('ww-dock');
    await expect(dock).toHaveAttribute('data-state', 'open', {
      timeout: 30_000,
    });
    await expect(
      dock.getByRole('tab', { name: 'Log', exact: true })
    ).toHaveAttribute('aria-selected', 'true');
    // The link names no step: the run summary is current.
    expect(query(page, 'step')).toBeNull();

    await page
      .getByTestId('ww-run-chip')
      .getByRole('button', { name: 'Failed at broken' })
      .click();
    await expect.poll(() => query(page, 'step')).toBe('broken');
    await expect(dock.locator('li[data-step="broken"]')).toHaveAttribute(
      'aria-current',
      'true'
    );
    const log = dock.getByTestId('ww-step-run');
    await expect(log).toHaveAttribute('data-step', 'broken');
    await expect(log).toHaveAttribute('data-variant', 'logs');
    await expect(log).toContainText('broken-stderr-marker', {
      timeout: 30_000,
    });
  });

  // A run that the user starts in this tab and that fails opens the Runs
  // panel by itself, on the Log of the failed step. The URL stays as it is.
  // parity:execution-log-viewer parity:run-log-viewer
  test('opens the Log tab on the failed step of a run started here', async ({
    page,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const { fileName } = await inlineDAG(FAILING);

    await openWorkspace(page, gotoWorkspace, workflowPath(fileName));
    const dock = page.getByTestId('ww-dock');
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
    await page.locator('[data-ww-action="start"]').click();
    const dialog = page.getByRole('dialog', { name: 'Start workflow' });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Start', exact: true }).click();

    await expect(dock).toHaveAttribute('data-state', 'open', {
      timeout: 60_000,
    });
    await expect(
      dock.getByRole('tab', { name: 'Log', exact: true })
    ).toHaveAttribute('aria-selected', 'true');
    await expect(dock.locator('li[data-step="broken"]')).toHaveAttribute(
      'aria-current',
      'true'
    );
    const log = dock.getByTestId('ww-step-run');
    await expect(log).toHaveAttribute('data-step', 'broken');
    await expect(log).toHaveAttribute('data-variant', 'logs');
    await expect(log).toContainText('broken-stderr-marker', {
      timeout: 30_000,
    });
    // The auto-open never writes ?step or ?dock.
    expect(query(page, 'step')).toBeNull();
    expect(query(page, 'dock')).toBeNull();
  });

  // The wand and the format of the step YAML act on the step only, like the
  // ones of the whole file act on the file, and one undo takes each back.
  test('applies the quick fixes and the format to the step YAML only', async ({
    page,
    request,
    inlineDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { fileName } = await inlineDAG(STEP_TOOLS, 'e2e-ws-steptools');
    const before = readSpecOf(request, token, fileName);

    await openWorkspace(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}?step=${encodeURIComponent('second step')}&ndv=expanded`
    );
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toHaveAttribute('data-state', 'expanded', {
      timeout: 30_000,
    });
    const editor = page.getByTestId('ndv-center').locator('.monaco-editor');
    await expect(editor).toBeVisible({ timeout: 30_000 });
    const lines = editor.locator('.view-lines');
    await expect(lines).toContainText('second step', { timeout: 30_000 });
    // The YAML of one step has the tools of the step, not those of the file.
    await expect(ndv.getByTestId('yaml-fix-all')).toHaveCount(0);
    await expect(ndv.getByTestId('yaml-format')).toHaveCount(0);
    const wand = ndv.getByTestId('step-yaml-fix');
    const format = ndv.getByTestId('step-yaml-format');
    await expect(wand).toHaveAttribute(
      'aria-label',
      'Apply quick fixes to this step'
    );
    await expect(format).toHaveAttribute('aria-label', 'Format this step');
    await expect(wand).toBeEnabled({ timeout: 30_000 });
    await expect(format).toBeEnabled();
    const enabledBox = [await box(wand), await box(format)];
    const beforeText = (await lines.innerText()).replace(/\u00a0/g, ' ');

    // The wand adds the id of this step, and only this step.
    await wand.click();
    await expect(lines).toContainText('id: second_step', { timeout: 30_000 });
    await expect(wand).toBeDisabled({ timeout: 30_000 });
    await expect(wand).toHaveAttribute('title', 'No quick fix');
    expect([await box(wand), await box(format)]).toEqual(enabledBox);

    // The format puts the keys in order and folds the long description.
    await format.click();
    await expect(lines).toContainText('# keep this');
    // Monaco writes the spaces of a line as no-break spaces.
    const text = (await lines.innerText()).replace(/\u00a0/g, ' ');
    const rows = text.split('\n').filter((line) => line.trim() !== '');
    expect(
      rows.every((line) => line.length <= 100),
      text
    ).toBe(true);
    expect(text.indexOf('id: second_step')).toBeLessThan(
      text.indexOf('name: second step')
    );
    expect(text.indexOf('name: second step')).toBeLessThan(
      text.indexOf('run: echo second')
    );
    await expect(saveButton(page)).toBeEnabled();

    // One undo takes back the format, one more the id.
    await editor
      .locator('.view-line')
      .filter({ hasText: 'echo second' })
      // The lines are as wide as the long description: aim at their start.
      .click({ position: { x: 30, y: 5 } });
    const shown = async () => (await lines.innerText()).replace(/\u00a0/g, ' ');
    await page.keyboard.press('ControlOrMeta+z');
    await expect.poll(shown).toContain('echo second   # keep this');
    expect(await shown()).toContain('id: second_step');
    await page.keyboard.press('ControlOrMeta+z');
    await expect.poll(shown).not.toContain('id: second_step');
    expect(await shown()).toBe(beforeText);
    await expect(wand).toBeEnabled();

    // Again, then save: the rest of the file is the same, byte for byte.
    await wand.click();
    await expect(wand).toBeDisabled({ timeout: 30_000 });
    await format.click();
    await expect(saveButton(page)).toBeEnabled();
    // The expanded view covers the Save button: the shortcut saves.
    await page.keyboard.press('ControlOrMeta+s');
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });
    // The diff has coloured lines for what changed.
    await expect(
      review.locator('td[class*="-diff-added"]').first()
    ).toBeVisible();
    await review.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(review).toBeHidden({ timeout: 30_000 });
    const saved = await readSpecOf(request, token, fileName);
    const original = await before;
    const firstStep =
      '  - id: gather\n    name: gather\n    run: echo gather\n';
    const lastStep = '  - id: third\n    name: third\n    run: echo third\n';
    expect(original).toContain(firstStep);
    expect(
      saved.startsWith(original.slice(0, original.indexOf('  - run:')))
    ).toBe(true);
    expect(saved).toContain(firstStep);
    expect(saved.endsWith(lastStep)).toBe(true);
    expect(saved).toContain('  - id: second_step\n    name: second step\n');
    expect(saved).toContain('# keep this');
    expect(saved).not.toContain('id: third_step');
  });
  // The code actions of the quick fixes are in every editor of the page: the
  // YAML of one step in its view, and the whole file in its panel. Hover on the
  // mark shows Quick Fix, and the quick fix adds the id line.
  for (const [label, search, scope] of [
    ['the step view', 'step=second+step&ndv=expanded', 'ndv-center'],
    ['the whole-file panel', 'ndv=docked', 'ndv'],
  ] as const) {
    test(`offers the quick fix of a step without id in ${label}`, async ({
      page,
      inlineDAG,
      gotoWorkspace,
    }) => {
      test.setTimeout(150_000);
      const { fileName } = await inlineDAG(NO_ID);
      await gotoWorkspace(page, `${workflowPath(fileName)}?${search}`);
      const ndv = page.getByTestId('ndv');
      await expect(ndv).toBeVisible({ timeout: 30_000 });
      if (scope === 'ndv-center') {
        await expect(
          ndv.getByRole('button', { name: 'YAML', pressed: true })
        ).toBeVisible({ timeout: 30_000 });
      }
      const editor = (scope === 'ndv-center' ? ndv.getByTestId(scope) : ndv)
        .locator('.monaco-editor')
        .first();
      await expect(editor).toBeVisible({ timeout: 30_000 });
      const mark = editor
        .locator('.squiggly-warning, .squiggly-error, .squiggly-info')
        .first();
      await expect(mark).toBeVisible({ timeout: 30_000 });

      // The hover opens when the editor is idle: the mouse moves in and out
      // until it does.
      const hover = page.locator('.monaco-hover:not(.hidden)').first();
      await expect(async () => {
        await page.mouse.move(0, 0);
        await mark.hover({ force: true });
        await expect(hover).toContainText('The step has no id', {
          timeout: 4_000,
        });
        await expect(hover).toContainText('Quick Fix', { timeout: 4_000 });
      }).toPass({ timeout: 45_000 });
      await expect(hover).not.toContainText('No quick fixes available');

      await hover.getByText('Quick Fix', { exact: false }).first().click();
      const actions = page.locator('.action-widget');
      await expect(actions).toContainText('Add id: second_step');
      await page.keyboard.press('Enter');
      await expect(editor.locator('.view-lines')).toContainText(
        'id: second_step',
        { timeout: 15_000 }
      );
    });
  }

  // A run that starts from the step view leaves the view as it was: the
  // same tab, no frame without the view, the same editor. On the definition
  // of the run (?def=run) the selected run changes, and the snapshot of the
  // new run must not unmount the view while it loads.
  for (const [label, snapshot, choice] of [
    ['Step only', false, 'Step only'],
    ['Run', false, 'Run'],
    ['Step only, on the run definition', true, 'Step only'],
    ['Run, on the run definition', true, 'Run'],
  ] as const) {
    test(`keeps the step view and its YAML after Run step: ${label}`, async ({
      page,
      request,
      inlineDAG,
      gotoWorkspace,
    }) => {
      test.setTimeout(180_000);
      const token = await adminToken(request);
      const { dagName, fileName } = await inlineDAG(TWO_STEPS);
      const runId = await startDAG(request, token, fileName);
      await waitForRunStatus(request, token, dagName, runId, ['succeeded']);

      await gotoWorkspace(
        page,
        `${workflowPath(fileName, runId)}${snapshot ? '&def=run' : ''}&step=second&ndv=expanded&dock=log`
      );
      const scopeSelector = snapshot
        ? '[data-testid="ww-run-zone"] '
        : '[data-testid="ww-canvas-graph"] ';
      const ndv = page.locator(`${scopeSelector}[data-testid="ndv"]`);
      await expect(ndv).toBeVisible({ timeout: 30_000 });
      // The step opens on its YAML.
      await expect(
        ndv.getByRole('button', { name: 'YAML', pressed: true })
      ).toBeVisible({ timeout: 30_000 });
      const editor = ndv.locator('.monaco-editor').first();
      await expect(editor).toBeVisible({ timeout: 30_000 });
      if (!snapshot) {
        await editor.locator('.view-lines').click();
        await page.keyboard.press('Control+End');
        await page.keyboard.type('\n# edited');
      }

      // One frame at a time: the view and its editor stay, in the YAML.
      await page.evaluate((selector) => {
        const mark = document.querySelector(
          `${selector}[data-testid="yaml-file-body"] .monaco-editor`
        );
        if (mark) (mark as unknown as { __mark: string }).__mark = 'first';
        const frames: {
          view: boolean;
          mark: string;
          yaml: string | null;
        }[] = [];
        (window as unknown as { __frames: typeof frames }).__frames = frames;
        const tick = () => {
          const found = document.querySelector(
            `${selector}[data-testid="yaml-file-body"] .monaco-editor`
          ) as unknown as { __mark?: string } | null;
          const yaml = Array.from(
            document.querySelectorAll(
              `${selector}[role=group][aria-label=Editor] button`
            )
          ).find((button) => button.textContent === 'YAML');
          frames.push({
            view:
              document.querySelector(`${selector}[data-testid="ndv"]`) !== null,
            mark: found ? (found.__mark ?? 'new') : 'none',
            yaml: yaml ? yaml.getAttribute('aria-pressed') : null,
          });
          (window as unknown as { __raf: number }).__raf =
            requestAnimationFrame(tick);
        };
        tick();
      }, scopeSelector);

      await ndv
        .locator('header')
        .getByRole('button', { name: 'Run step' })
        .click();
      await page
        .getByRole('dialog')
        .getByRole('button', { name: choice, exact: true })
        .click();
      await expect
        .poll(() => query(page, 'run'), { timeout: 60_000 })
        .not.toBe(runId);
      await page.waitForTimeout(1500);

      const frames = await page.evaluate(() => {
        cancelAnimationFrame((window as unknown as { __raf: number }).__raf);
        return (
          window as unknown as {
            __frames: { view: boolean; mark: string; yaml: string | null }[];
          }
        ).__frames;
      });
      expect(frames.length).toBeGreaterThan(10);
      expect(frames.filter((frame) => !frame.view)).toEqual([]);
      expect([...new Set(frames.map((frame) => frame.mark))]).toEqual([
        'first',
      ]);
      expect([...new Set(frames.map((frame) => frame.yaml))]).toEqual(['true']);
      expect(query(page, 'step')).toBe('second');
      expect(query(page, 'ndv')).toBe('expanded');
    });
  }
});
