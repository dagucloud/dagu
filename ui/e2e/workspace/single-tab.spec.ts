// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext, Locator, Page } from '@playwright/test';
import {
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
import { expect, signIn, test } from './fixtures';

// parity:test-e2e-gaps (the one tab: the editable graph with the run overlay,
// the Run panel below it, the run timeline and keys, Copy to editor and the
// read-only run definition)

// NodeStatus.Success in ui/src/api/v1/schema.ts.
const STATUS_SUCCESS = '4';

// The dock remembers its state in these keys: each test starts with a
// collapsed panel at its default height.
const DOCK_KEYS = [
  'dagu.workspace.dockHeight',
  'dagu.workspace.dockOpen',
  'dagu.workspace.dockOpen.v2',
];

// The sleep gives each run its own start second, so the order is fixed.
function versionSpec(version: string): string {
  return `
steps:
  - name: hello
    run: |
      sleep 1
      echo ${version}
`;
}

type History = {
  dagName: string;
  fileName: string;
  /** Oldest first. The first run used version-one, the others version-two. */
  runs: [string, string, string];
};

function apiHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
    'Content-Type': 'application/json',
  };
}

function specURL(fileName: string): string {
  return `/api/v1/dags/${encodeURIComponent(fileName)}/spec?remoteNode=local`;
}

async function adminToken(request: APIRequestContext): Promise<string> {
  const stack = await loadStack();
  return loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
}

async function readSpec(
  request: APIRequestContext,
  token: string,
  fileName: string
): Promise<string> {
  const response = await request.get(specURL(fileName), {
    headers: apiHeaders(token),
  });
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as { spec: string }).spec;
}

async function runToEnd(
  request: APIRequestContext,
  token: string,
  history: { dagName: string; fileName: string }
): Promise<string> {
  const runId = await startDAG(request, token, history.fileName);
  await waitForRunStatus(request, token, history.dagName, runId, ['succeeded']);
  return runId;
}

/**
 * Three finished runs: one of version-one, then two of version-two. The
 * caller deletes the DAG.
 */
async function createHistory(
  request: APIRequestContext,
  token: string,
  created: string[]
): Promise<History> {
  const dagName = uniqueName('e2e-ws-tab');
  const fileName = await writeLocalDAG(dagName, versionSpec('version-one'));
  created.push(fileName);
  await waitForDAGAvailable(request, token, fileName);
  const dag = { dagName, fileName };

  const oldest = await runToEnd(request, token, dag);
  const response = await request.put(specURL(fileName), {
    headers: apiHeaders(token),
    data: { spec: versionSpec('version-two').trimStart() },
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const middle = await runToEnd(request, token, dag);
  const newest = await runToEnd(request, token, dag);
  return { ...dag, runs: [oldest, middle, newest] };
}

function query(page: Page, key: string): string | null {
  return new URL(page.url()).searchParams.get(key);
}

function saveButton(page: Page): Locator {
  return page.locator('#save-config');
}

function dockOf(page: Page): Locator {
  return page.getByTestId('ww-dock');
}

/** The toggle of the Run panel: named Runs, aria-expanded tells its state. */
function dockToggle(page: Page): Locator {
  return dockOf(page)
    .getByRole('button', { name: 'Activity', exact: true })
    .first();
}

/** Opens the Run panel with its toggle, when it is collapsed. */
async function openDock(page: Page): Promise<void> {
  if ((await dockOf(page).getAttribute('data-state')) !== 'open') {
    await dockToggle(page).click();
  }
  await expect(dockOf(page)).toHaveAttribute('data-state', 'open');
}

/** The status dot that the run overlay puts on a step of the canvas. */
function overlayStatus(page: Page, key: string): Locator {
  return page
    .getByTestId('ww-canvas')
    .getByTestId(`ve-canvas-node-${key}`)
    .getByTestId('ve-canvas-node-status');
}

/** Run IDs of the timeline bars from left (oldest) to right (newest). */
async function timelineRuns(page: Page): Promise<string[]> {
  return page
    .getByTestId('ww-run-timeline-track')
    .locator('[role="option"]')
    .evaluateAll((bars) =>
      bars.map((bar) => bar.getAttribute('data-run-id') ?? '')
    );
}

/** Opens the workflow page and waits for the first step of the canvas. */
async function openWorkflow(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  target: string
): Promise<void> {
  await gotoWorkspace(page, target);
  await expect(page.getByTestId('workflow-workspace')).toBeVisible({
    timeout: 30_000,
  });
  await expect(
    page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
  ).toBeVisible({ timeout: 30_000 });
}

/** Types a description on the first step: the editor holds a draft. */
async function editFirstStep(page: Page, description: string): Promise<void> {
  const firstNode = page
    .getByTestId('ww-canvas')
    .getByTestId('ve-canvas-node-steps[0]');
  await firstNode.dblclick();
  await openStepForm(page);
  const inspector = page.getByTestId('ve-inspector');
  await expect(inspector).toBeVisible({ timeout: 30_000 });
  const field = inspector.getByLabel('Description', { exact: true });
  await field.fill(description);
  // The field sends its draft on blur.
  await field.blur();
  await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });
}

/** Saves the draft through the review dialog, and returns the dialog. */
async function saveThroughReview(page: Page): Promise<void> {
  await page.keyboard.press('ControlOrMeta+s');
  const review = page.getByRole('dialog', { name: 'Review changes' });
  await expect(review).toBeVisible({ timeout: 30_000 });
  const saved = page.waitForResponse(
    (response) =>
      response.request().method() === 'PUT' &&
      new URL(response.url()).pathname.endsWith('/spec')
  );
  await review.getByRole('button', { name: 'Save', exact: true }).click();
  expect((await saved).ok()).toBeTruthy();
  await expect(review).toBeHidden({ timeout: 30_000 });
  await expect(saveButton(page)).toBeDisabled({ timeout: 30_000 });
}

test.describe('workspace single tab', () => {
  // The Run panel with its timeline needs the desktop layout.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'desktop layout only'
  );

  const created: string[] = [];

  test.beforeEach(async ({ page }) => {
    await page.addInitScript((keys) => {
      for (const key of keys) window.localStorage.removeItem(key);
    }, DOCK_KEYS);
    await signIn(page);
  });

  test.afterEach(async ({ request }) => {
    const token = await adminToken(request);
    for (const fileName of created.splice(0)) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: apiHeaders(token) }
      );
    }
  });

  // parity:chg-single-tab parity:chg-live-graph-overlay
  // parity:chg-dock-collapsed-default
  test('opens on the latest run, with the dock collapsed and one pane', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName, runs } = await createHistory(request, token, created);
    const newest = runs[2];

    await openWorkflow(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}`
    );

    // One tab: one pane.
    await expect(page.getByTestId('ww-pane')).toHaveCount(1);

    // The page names the latest run in ?run (history replace): the latest
    // run is overlaid on the editable graph, and the run chip says so.
    await expect.poll(() => query(page, 'run')).toBe(newest);
    await expect(overlayStatus(page, 'steps[0]')).toHaveAttribute(
      'data-status',
      STATUS_SUCCESS,
      { timeout: 30_000 }
    );
    const chip = page.getByTestId('ww-run-chip');
    await expect(chip).toBeVisible({ timeout: 30_000 });
    await expect(chip).toHaveAttribute('aria-label', 'Last run');
    await expect(
      chip.getByRole('button', { name: 'Latest run' })
    ).toBeDisabled();
    // The graph is the editable one: the editor has no unsaved change.
    await expect(saveButton(page)).toBeDisabled();

    // The dock is collapsed at load. Its toggle opens it on the timeline of
    // the latest runs, with the newest selected. The redirect did not open it.
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');
    await expect(dockToggle(page)).toHaveAttribute('aria-expanded', 'false');
    await dockToggle(page).click();
    await expect(dockOf(page)).toHaveAttribute('data-state', 'open');
    await expect(dockToggle(page)).toHaveAttribute('aria-expanded', 'true');
    await expect(page.getByTestId(`ww-run-bar-${newest}`)).toHaveAttribute(
      'aria-selected',
      'true',
      { timeout: 30_000 }
    );
    expect(query(page, 'run')).toBe(newest);
  });

  // parity:chg-canvas-click-no-dock
  test('does not open the dock when the canvas is clicked', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName } = await createHistory(request, token, created);

    await openWorkflow(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}`
    );
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');

    // A click on a step selects it, and the dock stays as it is.
    await page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]')
      .click();
    await expect.poll(() => query(page, 'step')).toBe('hello');
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');

    // A click on the empty canvas clears the step; the dock is still shut.
    await page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-frame')
      // Below the floating toolbar row, left of the graph.
      .click({ position: { x: 24, y: 160 } });
    await expect.poll(() => query(page, 'step')).toBeNull();
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');
    await page.waitForTimeout(500);
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');

    // 'l' opens the log, and only then the dock shows.
    await page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]')
      .click();
    await expect.poll(() => query(page, 'step')).toBe('hello');
    await page.keyboard.press('l');
    await expect(dockOf(page)).toHaveAttribute('data-state', 'open');
  });

  // parity:chg-run-start-selects parity:history-selection-url
  test('picks a past run, edits and saves with ?run kept, and Run selects the new run', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(240_000);
    const token = await adminToken(request);
    const { dagName, fileName, runs } = await createHistory(
      request,
      token,
      created
    );
    const [oldest, , newest] = runs;
    const base = `/dags/${encodeURIComponent(fileName)}`;

    await openWorkflow(page, gotoWorkspace, base);
    await expect(overlayStatus(page, 'steps[0]')).toHaveAttribute(
      'data-status',
      STATUS_SUCCESS,
      { timeout: 30_000 }
    );

    // An older run on the timeline writes ?run, and the chip leaves "Latest".
    await openDock(page);
    const oldestBar = page.getByTestId(`ww-run-bar-${oldest}`);
    await expect(oldestBar).toBeVisible({ timeout: 30_000 });
    await oldestBar.click();
    await expect.poll(() => query(page, 'run')).toBe(oldest);
    await expect(oldestBar).toHaveAttribute('aria-selected', 'true');
    const chip = page.getByTestId('ww-run-chip');
    await expect(chip).toHaveAttribute('aria-label', 'Selected run');
    await expect(
      chip.getByRole('button', { name: 'Latest run' })
    ).toBeEnabled();
    // The overlay shows the selected run on the same editable graph.
    await expect(overlayStatus(page, 'steps[0]')).toHaveAttribute(
      'data-status',
      STATUS_SUCCESS
    );

    // The panel gives its room back: the canvas keeps its view while the
    // panel is open, and the step must be in reach of the pointer.
    await dockToggle(page).click();
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');

    // The editor works while the run shows: the draft keeps the overlay.
    const description = 'Edited while a past run shows';
    await editFirstStep(page, description);
    await page
      .getByTestId('ndv')
      .getByRole('button', { name: 'Close', exact: true })
      .click();
    await expect(overlayStatus(page, 'steps[0]')).toBeVisible();
    expect(query(page, 'run')).toBe(oldest);

    // Save keeps the selected run.
    await saveThroughReview(page);
    expect(query(page, 'run')).toBe(oldest);
    await expect(oldestBar).toHaveAttribute('aria-selected', 'true');
    expect(await readSpec(request, token, fileName)).toContain(description);

    // Run selects the run that it creates, and the dock stays as it was.
    const dockState = await dockOf(page).getAttribute('data-state');
    const runsBefore = (await listDAGRuns(request, token, dagName)).map(
      (run) => run.dagRunId
    );
    await page
      .getByTestId('ww-run-split')
      .locator('[data-ww-action="start"]')
      .click();
    const startDialog = page.getByRole('dialog', { name: 'Start workflow' });
    await startDialog
      .getByRole('button', { name: 'Start', exact: true })
      .click();
    await expect(startDialog).toBeHidden({ timeout: 30_000 });
    await expect
      .poll(() => query(page, 'run'), { timeout: 30_000 })
      .not.toBe(oldest);
    const started = query(page, 'run');
    expect(started).toBeTruthy();
    expect(runsBefore).not.toContain(started);
    expect(started).not.toBe(newest);
    await expect(dockOf(page)).toHaveAttribute('data-state', dockState!);
    // A collapsed panel keeps its last render: it shows the new selection
    // when it opens.
    if (dockState === 'collapsed') await openDock(page);
    await expect(page.getByTestId(`ww-run-bar-${started}`)).toHaveAttribute(
      'aria-selected',
      'true',
      { timeout: 30_000 }
    );
  });

  // parity:chg-run-actions-flat
  test('shows the definition of a stale run read-only from its dot, then goes back', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName, runs } = await createHistory(request, token, created);
    const [oldest, , newest] = runs;
    const base = `/dags/${encodeURIComponent(fileName)}`;

    // The newest run used the saved definition: no dot. The oldest run used
    // version-one, so its chip has the stale dot.
    await openWorkflow(page, gotoWorkspace, base);
    await expect(page.getByTestId('ww-run-chip')).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId('ww-run-stale-dot')).toHaveCount(0);
    await openWorkflow(
      page,
      gotoWorkspace,
      `${base}?run=${encodeURIComponent(oldest)}`
    );
    const dot = page.getByTestId('ww-run-stale-dot');
    await expect(dot).toBeVisible({ timeout: 30_000 });
    await expect(dot).toHaveAttribute('aria-pressed', 'false');
    await expect(page.getByTestId('ww-run-definition')).toHaveCount(0);

    // The dot shows the stored definition of the run, read-only, in place of
    // the editor. ?def=run is a history entry.
    await dot.click();
    await expect.poll(() => query(page, 'def')).toBe('run');
    expect(query(page, 'run')).toBe(oldest);
    const snapshot = page.getByTestId('ww-run-definition');
    await expect(snapshot).toBeVisible({ timeout: 30_000 });
    await expect(snapshot).toHaveAttribute('data-run-id', oldest);
    await expect(snapshot.getByTestId('ve-canvas-node-steps[0]')).toBeVisible({
      timeout: 30_000,
    });
    await expect(dot).toHaveAttribute('aria-pressed', 'true');
    // The snapshot bar names the read-only state. The editor stays mounted
    // under it, hidden and inert: its Save button cannot be reached.
    await expect(
      snapshot
        .getByTestId('ww-run-snapshot-bar')
        .getByRole('img', { name: `Read-only: definition of run ${oldest}` })
    ).toBeVisible();
    await expect(saveButton(page)).toBeHidden();

    // The back button returns to the current definition and keeps ?run.
    await snapshot.getByTestId('ww-run-snapshot-back').click();
    await expect.poll(() => query(page, 'def')).toBeNull();
    expect(query(page, 'run')).toBe(oldest);
    await expect(page.getByTestId('ww-run-definition')).toHaveCount(0);
    await expect(
      page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
    ).toBeVisible();

    // The dot toggles the snapshot, and the browser Back button leaves it.
    await dot.click();
    await expect(page.getByTestId('ww-run-definition')).toBeVisible();
    await page.goBack();
    await expect.poll(() => query(page, 'def')).toBeNull();
    await expect(page.getByTestId('ww-run-definition')).toHaveCount(0);
    expect(query(page, 'run')).toBe(oldest);
    expect(newest).not.toBe(oldest);
  });

  // parity:chg-classic-executions-link parity:history-selection-url
  test('redirects /executions?run to the workflow route with the dock open', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName, runs } = await createHistory(request, token, created);
    const [, middle] = runs;
    const base = `/dags/${encodeURIComponent(fileName)}`;

    await gotoWorkspace(
      page,
      `${base}/executions?run=${encodeURIComponent(middle)}&remoteNode=local`
    );
    await expect
      .poll(
        () => {
          const url = new URL(page.url());
          return [
            url.pathname,
            url.searchParams.get('run'),
            url.searchParams.get('remoteNode'),
          ];
        },
        { timeout: 30_000 }
      )
      .toEqual([base, middle, 'local']);
    // ?run opens the dock at load, with the run selected on the timeline.
    await expect(dockOf(page)).toHaveAttribute('data-state', 'open', {
      timeout: 30_000,
    });
    await expect(page.getByTestId(`ww-run-bar-${middle}`)).toHaveAttribute(
      'aria-selected',
      'true',
      { timeout: 30_000 }
    );
    await expect(page.getByTestId('ww-pane')).toHaveCount(1);

    // A bare /executions gives the workflow route, with the dock collapsed.
    await gotoWorkspace(page, `${base}/executions`);
    await expect.poll(() => new URL(page.url()).pathname).toBe(base);
    await expect(
      page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
    ).toBeVisible({ timeout: 30_000 });
    await expect(dockOf(page)).toHaveAttribute('data-state', 'collapsed');
  });

  // One bar for each run, never for each step. The runs start within seconds
  // of each other, so their bars stand side by side without overlap.
  test('draws one thin bar for each run on the time axis', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(240_000);
    const token = await adminToken(request);
    const dagName = uniqueName('e2e-ws-fill');
    const fileName = await writeLocalDAG(
      dagName,
      `
steps:
  - name: one
    run: echo one
  - name: two
    run: echo two
  - name: three
    run: echo three
`
    );
    created.push(fileName);
    await waitForDAGAvailable(request, token, fileName);
    const runIds: string[] = [];
    for (let batch = 0; batch < 3; batch += 1) {
      const started = await Promise.all(
        Array.from({ length: 4 }, () => startDAG(request, token, fileName))
      );
      for (const runId of started) {
        await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
        runIds.push(runId);
      }
    }

    await openWorkflow(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}`
    );
    await openDock(page);
    const track = page.getByTestId('ww-run-timeline-track');
    await expect(track).toBeVisible({ timeout: 30_000 });
    await expect
      .poll(() => timelineRuns(page), { timeout: 30_000 })
      .toHaveLength(12);
    // Three steps for each run, twelve bars: the bars are the runs.
    await expect(page.locator('[data-testid="ww-run-bar-fill"]')).toHaveCount(
      12
    );

    // The bars keep their order and never overlap: each one is 3px wide at
    // least, and the runs of one burst stand side by side.
    const geometry = async () =>
      track.evaluate((element) => {
        const bars = Array.from(
          element.querySelectorAll('[data-testid="ww-run-bar-fill"]')
        )
          .map((bar) => bar.getBoundingClientRect())
          .sort((a, b) => a.left - b.left);
        const frame = element.getBoundingClientRect();
        return {
          track: frame.width,
          count: bars.length,
          narrowest: Math.min(...bars.map((b) => b.width)),
          overlaps: bars.some(
            (bar, i) => i > 0 && bar.left < bars[i - 1]!.right - 0.01
          ),
          inside:
            bars[0]!.left >= frame.left - 0.5 &&
            bars[bars.length - 1]!.right <= frame.right + 0.5,
        };
      });
    // The extents block under the bars has a fixed height (four lanes), and
    // holds one extent for each run, whatever the overlaps.
    const extents = page.getByTestId('ww-run-extents');
    const blockHeight = async () => (await extents.boundingBox())?.height ?? 0;
    expect(await blockHeight()).toBe(26);
    await expect(extents.getByRole('button')).toHaveCount(12);
    const plotHeight = async () =>
      (await page.getByTestId('ww-run-timeline-plot').boundingBox())?.height;
    const plotAtWide = await plotHeight();
    // The gutter of the duration axis keeps its width of 40px, and the
    // canvas starts right of it.
    const yaxis = page.getByTestId('ww-run-timeline-yaxis');
    const gutterWidth = async () => (await yaxis.boundingBox())?.width;
    expect(await gutterWidth()).toBe(40);
    await expect(page.getByTestId('ww-run-timeline-ytick').first()).toHaveText(
      '0'
    );

    const wide = await geometry();
    expect(wide.count).toBe(12);
    expect(wide.narrowest).toBeGreaterThanOrEqual(3);
    expect(wide.overlaps).toBe(false);
    expect(wide.inside).toBe(true);

    // A narrower window gives a narrower axis, still without overlap.
    await page.setViewportSize({ width: 1024, height: 800 });
    await expect
      .poll(async () => (await geometry()).track < wide.track)
      .toBe(true);
    const narrow = await geometry();
    expect(narrow.overlaps).toBe(false);
    expect(narrow.inside).toBe(true);
    expect(await blockHeight()).toBe(26);
    expect(await plotHeight()).toBe(plotAtWide);
    expect(await gutterWidth()).toBe(40);

    // The count control limits the bars to the newest runs.
    await page.getByTestId('ww-run-timeline-count').selectOption('5');
    await expect.poll(() => timelineRuns(page)).toHaveLength(5);
    await page.getByTestId('ww-run-timeline-count').selectOption('30');
    await expect.poll(() => timelineRuns(page)).toHaveLength(12);
  });

  // parity:history-load parity:history-keyboard parity:chg-history-keys
  // parity:embed-workflow-history
  test('draws the runs as timeline bars and moves ?run with the keys', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName, runs } = await createHistory(request, token, created);
    const [oldest, middle, newest] = runs;

    await openWorkflow(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}`
    );
    await openDock(page);
    const track = page.getByTestId('ww-run-timeline-track');
    await expect(track).toBeVisible({ timeout: 30_000 });
    // Oldest on the left, the newest on the right.
    await expect
      .poll(() => timelineRuns(page), { timeout: 30_000 })
      .toEqual([oldest, middle, newest]);
    const bar = (runId: string) => page.getByTestId(`ww-run-bar-${runId}`);
    await expect(bar(newest)).toHaveAttribute('data-latest', 'true');
    await expect(bar(oldest)).not.toHaveAttribute('data-latest', 'true');
    await expect(bar(newest)).toHaveAttribute('data-tone', 'success');
    // The latest run is selected, and ?run names it.
    await expect(bar(newest)).toHaveAttribute('aria-selected', 'true');
    expect(query(page, 'run')).toBe(newest);
    // Its extent is highlighted like its bar: a lane high, and alone.
    const extent = (runId: string) =>
      page.getByTestId(`ww-run-extent-${runId}`);
    await expect(extent(newest)).toHaveAttribute('data-selected', 'true');
    await expect(extent(newest).getByTestId('ww-run-extent-fill')).toHaveCSS(
      'height',
      '5px'
    );
    await expect(
      page.getByTestId('ww-run-extents').locator('[data-selected="true"]')
    ).toHaveCount(1);
    // The timeline has no Latest button and no All runs link.
    await expect(
      page.getByTestId('ww-run-timeline').getByRole('button', {
        name: 'Latest',
      })
    ).toHaveCount(0);
    await expect(
      page.getByTestId('ww-run-timeline').getByText('All runs')
    ).toHaveCount(0);

    // A click selects a run; the key moves the selection to its neighbour.
    await bar(oldest).click();
    await expect.poll(() => query(page, 'run')).toBe(oldest);
    await expect(bar(oldest)).toHaveAttribute('aria-selected', 'true');
    await expect(extent(oldest)).toHaveAttribute('data-selected', 'true');
    await expect(extent(newest)).not.toHaveAttribute('data-selected', 'true');

    // Wait for the bar of each run, not only for the URL: a key that comes
    // before the render of the previous selection moves from a stale run.
    const moveTo = async (key: string, runId: string) => {
      await page.keyboard.press(key);
      await expect.poll(() => query(page, 'run')).toBe(runId);
      await expect(bar(runId)).toHaveAttribute('aria-selected', 'true');
    };
    await expect(bar(oldest)).toBeFocused();
    await moveTo('ArrowRight', middle);
    await moveTo('ArrowRight', newest);
    await moveTo('ArrowLeft', middle);
    await moveTo('Home', oldest);
    await moveTo('End', newest);
  });

  // parity:history-grid parity:history-selection-url
  // parity:spec-save-diff-dialog
  test('selects a run on the timeline and copies its definition to the editor', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName, runs } = await createHistory(request, token, created);
    const [oldest] = runs;
    const base = `/dags/${encodeURIComponent(fileName)}`;

    await openWorkflow(page, gotoWorkspace, base);
    // The switch of the List and Grid views is gone.
    await expect(
      page.getByRole('button', { name: 'Grid', exact: true })
    ).toHaveCount(0);

    await openDock(page);
    const oldestBar = page.getByTestId(`ww-run-bar-${oldest}`);
    await expect(oldestBar).toBeVisible({ timeout: 30_000 });
    await oldestBar.click();
    await expect.poll(() => query(page, 'run')).toBe(oldest);

    // The run used another definition: its flat icon puts the old text in
    // the editor, which stays on the same page.
    const copy = page
      .getByTestId('ww-run-actions')
      .getByRole('button', { name: 'Copy to editor' });
    await expect(copy).toBeEnabled({ timeout: 30_000 });
    await copy.click();
    await expect.poll(() => new URL(page.url()).pathname).toBe(base);
    expect(query(page, 'run')).toBe(oldest);
    await expect(saveButton(page)).toBeEnabled({ timeout: 30_000 });

    await page.keyboard.press('ControlOrMeta+s');
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });
    await expect(review).toContainText(/\d+ added, \d+ removed/);
    await expect(review).toContainText('version-one');
    await expect(review).toContainText('version-two');

    const saved = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith('/spec')
    );
    await review.getByRole('button', { name: 'Save', exact: true }).click();
    expect((await saved).ok()).toBeTruthy();
    await expect(review).toBeHidden({ timeout: 30_000 });
    await expect(saveButton(page)).toBeDisabled({ timeout: 30_000 });

    const spec = await readSpec(request, token, fileName);
    expect(spec).toContain('echo version-one');
    expect(spec).not.toContain('version-two');
  });

  // The editor stays mounted under the run definition view: the draft is
  // not lost when the user picks a run or looks at its definition.
  test('keeps an unsaved buffer across a run pick and the run definition', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { fileName, runs } = await createHistory(request, token, created);
    const [oldest] = runs;
    const base = `/dags/${encodeURIComponent(fileName)}`;
    const description = 'Kept across the run views';

    await openWorkflow(page, gotoWorkspace, base);
    await editFirstStep(page, description);
    const ndv = page.getByTestId('ndv');
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(ndv).toBeHidden();

    // Pick an older run: the draft stays, the overlay follows the run.
    await openDock(page);
    await page.getByTestId(`ww-run-bar-${oldest}`).click();
    await expect.poll(() => query(page, 'run')).toBe(oldest);
    await expect(saveButton(page)).toBeEnabled();

    // The definition of the run replaces the editor on request, and the
    // draft is still there when the user comes back.
    await page.getByTestId('ww-run-stale-dot').click();
    await expect(page.getByTestId('ww-run-definition')).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId('ww-run-snapshot-back').click();
    await expect(page.getByTestId('ww-run-definition')).toHaveCount(0);
    await expect(saveButton(page)).toBeEnabled();

    await page.keyboard.press('ControlOrMeta+s');
    const review = page.getByRole('dialog', { name: 'Review changes' });
    await expect(review).toBeVisible({ timeout: 30_000 });
    await expect(review).toContainText(description);
    await review.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(review).toBeHidden();
    await expect(saveButton(page)).toBeEnabled();
  });

  // A test run (the steps of an unsaved draft) is in no list but the timeline:
  // it is drawn hatched with a flask, can be selected while the server keeps
  // it, says so on the canvas and on the run chip, and a toggle hides it.
  test('draws a test run in the timeline, selects it, and hides it with the toggle', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName, runs } = await createHistory(
      request,
      token,
      created
    );
    const newest = runs[2];
    const response = await request.post('/api/v1/dag-runs?remoteNode=local', {
      headers: apiHeaders(token),
      data: {
        spec: versionSpec('draft').trimStart(),
        name: dagName,
        steps: ['hello'],
        ephemeral: true,
      },
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const testRun = ((await response.json()) as { dagRunId: string }).dagRunId;
    await waitForRunStatus(request, token, dagName, testRun, ['succeeded']);
    // No list of the server holds it by default.
    expect(
      (await listDAGRuns(request, token, dagName)).map((r) => r.dagRunId)
    ).not.toContain(testRun);

    const base = `/dags/${encodeURIComponent(fileName)}`;
    await openWorkflow(page, gotoWorkspace, base);
    await openDock(page);
    const bar = page.getByTestId(`ww-run-bar-${testRun}`);
    await expect(bar).toHaveAttribute('data-test-run', 'true', {
      timeout: 30_000,
    });
    await expect(bar.getByTestId('ww-run-bar-flask')).toBeVisible();
    // The bar is hatched and keeps its status colour.
    await expect(bar.getByTestId('ww-run-bar-fill')).toHaveCSS(
      'background-image',
      /repeating-linear-gradient/
    );
    // The latest run is still the newest one that is kept.
    await expect(page.getByTestId(`ww-run-bar-${newest}`)).toHaveAttribute(
      'data-latest',
      'true'
    );
    await bar.hover();
    await expect(
      page.getByTestId('ww-run-timeline-tooltip').first()
    ).toContainText('Test run, not kept');

    // Select it: ?run names it, the canvas says that it is not kept and when it
    // disappears, and the run chip says "Test run".
    await bar.click();
    await expect.poll(() => query(page, 'run')).toBe(testRun);
    await expect(page.locator('[data-notice-id="test-run"]')).toContainText(
      'Test run, not kept: it disappears at',
      {
        timeout: 30_000,
      }
    );
    await expect(page.getByTestId('ww-run-test-chip')).toHaveText('Test run');
    await expect(page.getByText('Latest', { exact: true })).toHaveCount(0);

    // A reload on ?run keeps it all.
    await page.reload();
    await expect(page.getByTestId('workflow-workspace')).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator('[data-notice-id="test-run"]')).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId('ww-run-test-chip')).toBeVisible();

    // The chip goes back to the latest run, which is no test run.
    await page.getByTestId('ww-run-test-chip').click();
    await expect.poll(() => query(page, 'run')).toBe(newest);
    await expect(page.locator('[data-notice-id="test-run"]')).toHaveCount(0);

    // The toggle hides the test runs, and the browser keeps the choice.
    await openDock(page);
    const toggle = page.getByTestId('ww-run-timeline-tests');
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await expect(page.getByTestId(`ww-run-bar-${testRun}`)).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId('workflow-workspace')).toBeVisible({
      timeout: 30_000,
    });
    await openDock(page);
    await expect(page.getByTestId('ww-run-timeline-tests')).toHaveAttribute(
      'aria-pressed',
      'false',
      { timeout: 30_000 }
    );
    await expect(page.getByTestId(`ww-run-bar-${testRun}`)).toHaveCount(0);
    await page.getByTestId('ww-run-timeline-tests').click();
    await expect(page.getByTestId(`ww-run-bar-${testRun}`)).toBeVisible();
  });

  // The arrows of the run chip and the keys [ and ] go through the runs that
  // the timeline shows: a test run is one of them while the toggle is on.
  test('walks through the test runs and the normal runs with the arrows', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName, runs } = await createHistory(
      request,
      token,
      created
    );
    const newest = runs[2];
    const response = await request.post('/api/v1/dag-runs?remoteNode=local', {
      headers: apiHeaders(token),
      data: {
        spec: versionSpec('draft').trimStart(),
        name: dagName,
        steps: ['hello'],
        ephemeral: true,
      },
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    const testRun = ((await response.json()) as { dagRunId: string }).dagRunId;
    await waitForRunStatus(request, token, dagName, testRun, ['succeeded']);

    await openWorkflow(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}`
    );
    await openDock(page);
    // The timeline order, oldest first: the three runs, then the test run.
    await expect
      .poll(() => timelineRuns(page), { timeout: 30_000 })
      .toEqual([...runs, testRun]);
    await expect.poll(() => query(page, 'run')).toBe(newest);

    const older = page.getByRole('button', { name: 'Older run' });
    const newer = page.getByRole('button', { name: 'Newer run' });
    const bar = (runId: string) => page.getByTestId(`ww-run-bar-${runId}`);
    const selected = async (runId: string) => {
      await expect.poll(() => query(page, 'run')).toBe(runId);
      await expect(bar(runId)).toHaveAttribute('aria-selected', 'true');
    };

    // normal -> test -> normal, with the chip arrows.
    await newer.click();
    await selected(testRun);
    await older.click();
    await selected(newest);
    await older.click();
    await selected(runs[1]);

    // ... and with the keys, from a page that holds no focus.
    await page.locator('body').click({ position: { x: 1, y: 1 } });
    await page.keyboard.press(']');
    await selected(newest);
    await page.keyboard.press(']');
    await selected(testRun);

    // At the end the arrow is disabled, with the reason.
    await expect(newer).toBeDisabled();
    await expect(newer).toHaveAttribute('title', 'No newer run in this view');

    // A hidden test run is not a step of the arrows.
    await page.getByTestId('ww-run-timeline-tests').click();
    await expect(bar(testRun)).toHaveCount(0);
    await older.click();
    await selected(newest);
    await expect(newer).toBeDisabled();
    await page.getByTestId('ww-run-timeline-tests').click();
    await expect(newer).toBeEnabled();
  });

  // A run that reuses steps of another run counts their time: its bar is as
  // tall as the total time, with a hatched foot, and its extent starts where
  // the cached time starts. A toggle counts the own time alone.
  test('counts the cached time of a step run, and picks a time range', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const dagName = uniqueName('e2e-ws-cached');
    const fileName = await writeLocalDAG(
      dagName,
      `
steps:
  - name: slow
    run: |
      sleep 4
      echo slow
  - name: use
    depends: [slow]
    run: echo use
`
    );
    created.push(fileName);
    await waitForDAGAvailable(request, token, fileName);
    const full = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, full, ['succeeded']);
    const reuse = await startDAG(request, token, fileName, {
      steps: ['use'],
      outputsFromRunId: full,
    });
    await waitForRunStatus(request, token, dagName, reuse, ['succeeded']);

    await openWorkflow(
      page,
      gotoWorkspace,
      `/dags/${encodeURIComponent(fileName)}`
    );
    await openDock(page);
    await expect
      .poll(() => timelineRuns(page), { timeout: 30_000 })
      .toEqual([full, reuse]);
    const bar = (runId: string) => page.getByTestId(`ww-run-bar-${runId}`);
    // The step run took far under 4 s alone: with the cached time it is a
    // tall bar with a hatched foot, and its extent has a hatched start.
    await expect(bar(reuse).getByTestId('ww-run-bar-cached')).toBeVisible();
    await expect(
      page
        .getByTestId(`ww-run-extent-${reuse}`)
        .getByTestId('ww-run-extent-cached')
    ).toBeAttached();
    await expect(bar(full).getByTestId('ww-run-bar-cached')).toHaveCount(0);
    await bar(reuse).hover();
    await expect(
      page.getByTestId('ww-run-timeline-tooltip-total').first()
    ).toContainText(/Total .* = own .* \+ cached/);

    // The toggle counts the own time alone, and the browser keeps the choice.
    const toggle = page.getByTestId('ww-run-timeline-cached');
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await expect(page.getByTestId('ww-run-bar-cached')).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId('workflow-workspace')).toBeVisible({
      timeout: 30_000,
    });
    await openDock(page);
    await expect(page.getByTestId('ww-run-timeline-cached')).toHaveAttribute(
      'aria-pressed',
      'false',
      { timeout: 30_000 }
    );
    await page.getByTestId('ww-run-timeline-cached').click();
    await expect(bar(reuse).getByTestId('ww-run-bar-cached')).toBeVisible();

    // The time range: the last hour holds both runs, and the browser keeps it.
    const select = page.getByTestId('ww-run-timeline-count');
    const slots = () =>
      page
        .getByTestId('ww-run-timeline-controls')
        .evaluate((row) => row.getBoundingClientRect().height);
    const before = await slots();
    await select.selectOption('range:1h');
    await expect.poll(() => timelineRuns(page)).toEqual([full, reuse]);
    await page.reload();
    await expect(page.getByTestId('workflow-workspace')).toBeVisible({
      timeout: 30_000,
    });
    await openDock(page);
    await expect(page.getByTestId('ww-run-timeline-count')).toHaveValue(
      'range:1h',
      { timeout: 30_000 }
    );
    expect(await slots()).toBe(before);
    // A custom range in the past holds no run, and says so.
    await page
      .getByTestId('ww-run-timeline-count')
      .selectOption('range:custom');
    await page
      .getByTestId('ww-run-timeline-range-from')
      .fill('2001-01-01T00:00');
    await page.getByTestId('ww-run-timeline-range-to').fill('2001-01-02T00:00');
    await expect(page.getByTestId('ww-run-timeline-empty')).toContainText(
      'No runs in this time range'
    );
    await page
      .getByTestId('ww-run-timeline-range-from')
      .fill('2001-01-03T00:00');
    await expect(page.getByTestId('ww-run-timeline-range-error')).toBeVisible();
    await page.getByTestId('ww-run-timeline-count').selectOption('30');
    await expect.poll(() => timelineRuns(page)).toEqual([full, reuse]);
  });
});
