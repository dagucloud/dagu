// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locator, Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  openStepForm,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { adminToken, expect, signIn, test } from './fixtures';

// parity:run-graph-expand (the canvas fills the zone between the bar and
// the dock: 400px or more at 1280x800. The one tab always has the Run panel:
// collapsed to its bar at load, open with a run in the URL)

const DOCK_KEYS = [
  'dagu.workspace.dockHeight',
  'dagu.workspace.dockOpen',
  'dagu.workspace.dockOpen.v2',
];

function canvasFrame(page: Page): Locator {
  return page.getByTestId('ww-canvas').getByTestId('ve-canvas-frame');
}

async function box(locator: Locator) {
  const value = await locator.boundingBox();
  expect(value).not.toBeNull();
  return value!;
}

/** Opens the workspace editor on the DAG and waits for the first step. */
async function openEditor(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  fileName: string
): Promise<void> {
  await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
  await expect(page.getByTestId('workflow-workspace')).toBeVisible({
    timeout: 30_000,
  });
  await expect(
    page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
  ).toBeVisible({ timeout: 30_000 });
}

async function documentScrolls(page: Page) {
  return page.evaluate(() => ({
    vertical:
      document.documentElement.scrollHeight > window.innerHeight ||
      document.body.scrollHeight > window.innerHeight,
    horizontal:
      document.documentElement.scrollWidth > window.innerWidth ||
      document.body.scrollWidth > window.innerWidth,
  }));
}

test.describe('workspace layout', () => {
  // Each test sets its own viewport; one project is enough.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'runs once, in the desktop project'
  );

  test.beforeEach(async ({ page }) => {
    // The Run panel starts collapsed, at its default height when it opens.
    await page.addInitScript((keys) => {
      for (const key of keys) window.localStorage.removeItem(key);
    }, DOCK_KEYS);
    await signIn(page);
  });

  test('keeps the page still and the canvas at 400px at 1280x800', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [
      'succeeded',
      'partially_succeeded',
    ]);

    // One tab: the canvas with the run overlay, and the Run panel under it,
    // collapsed to its 32px bar.
    await openEditor(page, gotoWorkspace, fileName);
    const dock = page.getByTestId('ww-dock');
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
    expect((await box(dock)).height).toBeLessThanOrEqual(34);
    expect(await documentScrolls(page)).toEqual({
      vertical: false,
      horizontal: false,
    });
    expect((await box(canvasFrame(page))).height).toBeGreaterThanOrEqual(400);
    // The bar is one row.
    expect((await box(page.getByTestId('ww-bar'))).height).toBeLessThanOrEqual(
      44
    );

    // A run in a link opens the panel at load; the canvas stays the same
    // editable graph, with the run overlaid. (The page named this run in its
    // own URL, and a reload keeps the panel closed, so the link comes from a
    // fresh entry.)
    await page.goto('about:blank');
    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(runId)}`
    );
    await expect(
      page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
    ).toBeVisible({ timeout: 30_000 });
    await expect(dock).toHaveAttribute('data-state', 'open', {
      timeout: 30_000,
    });
    // The Run panel is open at about 40% of the window.
    const dockBox = await box(dock);
    expect(dockBox.height).toBeGreaterThanOrEqual(300);
    expect(dockBox.height).toBeLessThanOrEqual(800 * 0.5);
    expect(await documentScrolls(page)).toEqual({
      vertical: false,
      horizontal: false,
    });
    // The zone of the canvas keeps its floor of 280px, and the canvas is not
    // squeezed to nothing.
    expect(
      (await box(page.getByTestId('ww-zone-main'))).height
    ).toBeGreaterThanOrEqual(280);
    expect((await box(canvasFrame(page))).height).toBeGreaterThanOrEqual(200);
    // The timeline is above the two columns of the panel.
    const timeline = await box(page.getByTestId('ww-run-timeline'));
    const nav = await box(page.getByTestId('ww-run-steps-nav'));
    expect(timeline.y + timeline.height).toBeLessThanOrEqual(nav.y + 0.5);

    // Collapsed, the canvas takes 400px or more again.
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
    expect(await documentScrolls(page)).toEqual({
      vertical: false,
      horizontal: false,
    });
    expect((await box(canvasFrame(page))).height).toBeGreaterThanOrEqual(400);
  });

  // A control keeps its place in every state: Start does not move when the
  // draft becomes dirty (its caret keeps a slot, disabled), and the compact
  // run identity of a narrow bar keeps the way back to the latest run.
  test('keeps Start in place when the draft is dirty and the way back in a narrow bar', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const olderId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, olderId, [
      'succeeded',
      'partially_succeeded',
    ]);
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [
      'succeeded',
      'partially_succeeded',
    ]);

    // The bar is too narrow for the full chip: the compact identity has the
    // way back from an older run, and it needs no more than the 40px row.
    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(olderId)}`
    );
    const compact = page.getByTestId('ww-bar-compact-run');
    await expect(compact).toBeVisible({ timeout: 30_000 });
    expect((await box(page.getByTestId('ww-bar'))).height).toBeLessThanOrEqual(
      44
    );
    await compact
      .getByRole('button', { name: 'Back to latest', exact: true })
      .click();
    // The URL names the latest run.
    await expect(page).toHaveURL(
      new RegExp(`[?&]run=${encodeURIComponent(runId)}(&|$)`)
    );
    await expect(
      compact.getByRole('button', { name: 'Back to latest', exact: true })
    ).toHaveCount(0);

    await page.setViewportSize({ width: 1440, height: 900 });
    await openEditor(page, gotoWorkspace, fileName);
    const start = page
      .getByTestId('ww-run-split')
      .locator('[data-ww-action="start"]');
    const caret = page.getByRole('button', {
      name: 'More run options',
      exact: true,
    });
    await expect(caret).toBeVisible();
    const clean = await box(start);

    const firstNode = page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]');
    await firstNode.dblclick();
    await openStepForm(page);
    const field = page
      .getByTestId('ve-inspector')
      .getByLabel('Description', { exact: true });
    await field.fill('Changed in the browser, not saved');
    await field.blur();
    await expect(page.locator('#save-config')).toBeEnabled({
      timeout: 30_000,
    });
    await expect(caret).toBeEnabled();
    const dirty = await box(start);
    expect(dirty.x).toBeCloseTo(clean.x, 0);
    expect(dirty.width).toBeCloseTo(clean.width, 0);
  });

  // The canvas toolbar is in a row at the top of the graph zone. A panel that
  // opens starts under that row and never moves the toolbar. Save does not
  // move when the draft turns dirty, with the panel in the same state: the box
  // of the slot is the same in both states.
  test('keeps the Save slot in place when the draft is dirty', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);
    await page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]')
      .dblclick();
    await openStepForm(page);
    const slot = page.getByTestId('save-slot');
    const save = page.locator('#save-config');
    await expect(save).toBeDisabled();
    const clean = { slot: await box(slot), save: await box(save) };

    const field = page
      .getByTestId('ve-inspector')
      .getByLabel('Description', { exact: true });
    await field.fill('Changed in the browser, not saved');
    await field.blur();
    await expect(save).toBeEnabled({ timeout: 30_000 });
    const dirty = { slot: await box(slot), save: await box(save) };
    for (const part of ['slot', 'save'] as const) {
      for (const key of ['x', 'y', 'width', 'height'] as const) {
        expect(dirty[part][key], `${part}.${key}`).toBeCloseTo(
          clean[part][key],
          0
        );
      }
    }
  });

  // The toolbar of the canvas is in the row at the top of the zone. With no
  // panel, and with the node view (which covers the row), its right edge is
  // the right edge of the zone. A docked panel (step panel, YAML, Workflow)
  // has its title at the left of the row and its Close button at the right
  // end of the row: the toolbar then ends 40px from the right edge of the
  // zone, to the left of the button, whatever the panel, and Save keeps the
  // same x and y between panels, clean and dirty.
  test('ends the canvas toolbar before the Close button of a docked panel, at five widths', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(240_000);
    await page.setViewportSize({ width: 1920, height: 900 });
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);
    const toolbar = page.getByTestId('ve-toolbar');
    const slot = page.getByTestId('save-slot');
    const save = page.locator('#save-config');
    const row = page.getByTestId('ww-canvas-toolbar');
    const ndv = page.getByTestId('ndv');

    const parts = async () => ({
      row: await box(row),
      toolbar: await box(toolbar),
      slot: await box(slot),
      save: await box(save),
    });
    // A box after the layout settled: two reads in a row are equal.
    const settled = async () => {
      let last = JSON.stringify(await parts());
      for (let i = 0; i < 20; i += 1) {
        await page.waitForTimeout(150);
        const next = JSON.stringify(await parts());
        if (next === last)
          return JSON.parse(next) as Awaited<ReturnType<typeof parts>>;
        last = next;
      }
      throw new Error('the toolbar keeps moving');
    };
    // The row keeps 8px at its right end; a docked panel keeps 40px there for
    // its Close button: the toolbar moves left by the difference.
    const ROW_PADDING = 8;
    const CLOSE_SLOT = 40;
    const closeInRow = page.locator('[data-panel-close="row"] button');
    // The YAML dock adds its tools to the toolbar row: the toolbar grows to
    // the left and keeps its right edge. With `closeSlot`, a docked panel is
    // open: the right edge is 40px from the right edge of the zone, and the
    // Close button is in the row, at the right of the toolbar.
    const expectSame = async (
      reference: Awaited<ReturnType<typeof parts>>,
      where: string,
      { grows = false, closeSlot = false } = {}
    ) => {
      const now = await settled();
      const shift = closeSlot ? ROW_PADDING - CLOSE_SLOT : 0;
      for (const part of ['row', 'toolbar', 'slot', 'save'] as const) {
        for (const key of ['x', 'y', 'width', 'height'] as const) {
          if (grows && part === 'toolbar' && (key === 'x' || key === 'width')) {
            continue;
          }
          const moves = key === 'x' && part !== 'row';
          expect(now[part][key], `${where}: ${part}.${key}`).toBeCloseTo(
            reference[part][key] + (moves ? shift : 0),
            0
          );
        }
      }
      if (grows) {
        expect(
          now.toolbar.x + now.toolbar.width,
          `${where}: right edge`
        ).toBeCloseTo(reference.toolbar.x + reference.toolbar.width + shift, 0);
      }
      if (closeSlot) {
        const zone = await box(page.getByTestId('ww-canvas'));
        const close = await box(closeInRow);
        expect(
          now.toolbar.x + now.toolbar.width,
          `${where}: toolbar ends before the slot`
        ).toBeLessThanOrEqual(zone.x + zone.width - CLOSE_SLOT + 0.5);
        expect(close.x, `${where}: Close right of the toolbar`).toBeGreaterThan(
          now.toolbar.x + now.toolbar.width
        );
        expect(
          close.x + close.width,
          `${where}: Close in the zone`
        ).toBeLessThanOrEqual(zone.x + zone.width + 0.5);
        // On the same row as the toolbar.
        expect(
          close.y + close.height / 2,
          `${where}: Close on the row`
        ).toBeCloseTo(now.row.y + now.row.height / 2, 0);
      }
    };

    for (const width of [1920, 1440, 1280, 1152, 1024]) {
      await test.step(`window of ${width}px`, async () => {
        await page.setViewportSize({ width, height: 900 });
        await expect(slot).toBeVisible();
        const reference = await settled();
        // The toolbar sits in the row of the zone, which is 40px high and
        // starts at the top of the zone.
        const zone = await box(page.getByTestId('ww-canvas'));
        expect(reference.row.y).toBeCloseTo(zone.y, 0);
        expect(reference.row.height).toBeCloseTo(40, 0);
        expect(reference.toolbar.y).toBeGreaterThanOrEqual(zone.y - 0.5);
        expect(
          reference.toolbar.y + reference.toolbar.height
        ).toBeLessThanOrEqual(zone.y + 40.5);
        expect(
          reference.toolbar.x + reference.toolbar.width
        ).toBeLessThanOrEqual(zone.x + zone.width + 0.5);
        // The row floats over the graph: no background of its own, and the
        // canvas fills the zone from its top edge.
        const rowStyle = await row.evaluate((el) => {
          const style = getComputedStyle(el);
          return {
            background: style.backgroundColor,
            border: style.borderTopWidth,
            shadow: style.boxShadow,
            pointer: style.pointerEvents,
          };
        });
        expect(rowStyle.background).toBe('rgba(0, 0, 0, 0)');
        expect(rowStyle.border).toBe('0px');
        expect(rowStyle.shadow).toBe('none');
        expect(rowStyle.pointer).toBe('none');
        const canvas = await box(
          page.getByTestId('ww-canvas').getByTestId('ve-canvas')
        );
        // The frame of the canvas has a 1px border: the canvas is inside it.
        expect(canvas.y - zone.y).toBeLessThanOrEqual(1.5);
        expect(zone.height - canvas.height).toBeLessThanOrEqual(2.5);

        // The YAML panel.
        const yaml = toolbar.getByRole('button', { name: 'YAML', exact: true });
        await yaml.click();
        await expect(ndv).toHaveAttribute('data-state', 'docked');
        const panel = await box(ndv);
        // The panel is one box from the top of the zone: the row of toolbars
        // sits over its first 40px.
        expect(panel.y).toBeCloseTo(zone.y, 0);
        expect(reference.toolbar.y).toBeGreaterThanOrEqual(panel.y - 0.5);
        expect(
          reference.toolbar.y + reference.toolbar.height
        ).toBeLessThanOrEqual(panel.y + 40.5);
        await expectSame(reference, 'YAML panel', {
          grows: true,
          closeSlot: true,
        });
        await yaml.click();
        await expect(ndv).toBeHidden();
        await expectSame(reference, 'YAML panel closed');

        // The Workflow panel.
        const workflow = toolbar.getByRole('button', {
          name: 'Workflow',
          exact: true,
        });
        await workflow.click();
        await expect(page.getByTestId('ve-workflow')).toBeVisible();
        await expectSame(reference, 'Workflow panel', { closeSlot: true });
        await workflow.click();
        await expect(page.getByTestId('ve-workflow')).toBeHidden();
        await expectSame(reference, 'Workflow panel closed');

        // The step panel: its title is at the left of the row, its Close
        // button at the right end, and its own bar is hidden.
        await page
          .getByTestId('ww-canvas')
          .getByTestId('ve-canvas-node-steps[0]')
          .click();
        const stepPanel = page.getByTestId('ww-step-panel');
        await expect(stepPanel).toBeVisible();
        await expectSame(reference, 'step panel', { closeSlot: true });
        const stepBox = await box(stepPanel);
        const rowTitle = await box(stepPanel.getByTestId('panel-row-title'));
        expect(rowTitle.x).toBeCloseTo(stepBox.x, 0);
        expect(rowTitle.y).toBeCloseTo(zone.y, 0);
        await expect(stepPanel.locator('header')).toBeHidden();
        await closeInRow.click();
        await expect(stepPanel).toBeHidden();
        await expectSame(reference, 'step panel closed');

        // The node view.
        await page
          .getByTestId('ww-canvas')
          .getByTestId('ve-canvas-node-steps[0]')
          .dblclick();
        await expect(ndv).toHaveAttribute('data-state', 'expanded');
        // The expanded node view covers the whole zone, the row included.
        expect((await box(ndv)).y).toBeCloseTo(zone.y, 0);
        await expectSame(reference, 'node view');
        await ndv.getByRole('button', { name: 'Close', exact: true }).click();
        await expect(ndv).toBeHidden();
        await expectSame(reference, 'node view closed');
      });
    }

    // Dirty: the slot of Save keeps its box, with and without a panel.
    await page.setViewportSize({ width: 1440, height: 900 });
    const clean = await settled();
    await page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]')
      .dblclick();
    await openStepForm(page);
    const field = page
      .getByTestId('ve-inspector')
      .getByLabel('Description', { exact: true });
    await field.fill('Changed in the browser, not saved');
    await field.blur();
    await expect(save).toBeEnabled({ timeout: 30_000 });
    await expectSame(clean, 'dirty with the form open');
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    await expectSame(clean, 'dirty with no panel');
  });

  test('keeps the node view in the graph zone and docks it at 1280px and 900px', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);

    await page
      .getByTestId('ww-canvas')
      .getByTestId('ve-canvas-node-steps[0]')
      .dblclick();
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toBeVisible({ timeout: 30_000 });
    await expect(ndv).toHaveAttribute('data-state', 'expanded');

    // The expanded view fills the graph zone: not the window, and it leaves
    // the bar and the sidebar free.
    const zone = await box(page.getByTestId('ww-canvas'));
    const bar = await box(page.getByTestId('ww-bar'));
    let panel = await box(ndv);
    expect(panel.x).toBeGreaterThanOrEqual(zone.x - 0.5);
    expect(panel.y).toBeGreaterThanOrEqual(bar.y + bar.height - 0.5);
    expect(panel.x + panel.width).toBeLessThanOrEqual(
      zone.x + zone.width + 0.5
    );
    expect(panel.y + panel.height).toBeLessThanOrEqual(
      zone.y + zone.height + 0.5
    );
    expect(panel.width).toBeLessThan(1280 - 200);
    await expect(page.getByTestId('ww-bar')).toBeVisible();
    await page
      .getByTestId('ww-run-split')
      .locator('[data-ww-action="start"]')
      .click({ trial: true, timeout: 5_000 });

    // The YAML panel takes the right side of the zone and the canvas keeps
    // the rest. The node view has no button that docks it.
    await expect(
      ndv.getByRole('button', { name: 'Dock to the side', exact: true })
    ).toHaveCount(0);
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    // The toolbar of the canvas: the node view has a YAML tab of its own.
    await page
      .getByTestId('ve-toolbar')
      .getByRole('button', { name: 'YAML', exact: true })
      .click();
    await expect(ndv).toHaveAttribute('data-state', 'docked');
    const frame0 = await box(canvasFrame(page));
    panel = await box(ndv);
    expect(frame0.x + frame0.width).toBeLessThanOrEqual(panel.x + 0.5);
    expect(panel.x + panel.width).toBeLessThanOrEqual(
      zone.x + zone.width + 0.5
    );

    await page.setViewportSize({ width: 900, height: 800 });
    await expect(ndv).toBeVisible();
    await expect(async () => {
      const frame = await box(canvasFrame(page));
      panel = await box(ndv);
      expect(frame.x + frame.width).toBeGreaterThan(panel.x);
    }).toPass({ timeout: 10_000 });
  });

  // #3507: a long line of YAML needs more than 960px on a wide screen. Each
  // dock keeps its own width.
  test('widens the YAML dock past 960px and keeps a width for each dock', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await page.addInitScript(() =>
      window.localStorage.removeItem('dagu.stepDetailsDrawer.width')
    );
    await page.setViewportSize({ width: 2560, height: 900 });
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);
    const ndv = page.getByTestId('ndv');
    const toolbar = page.getByTestId('ve-toolbar');

    await toolbar.getByRole('button', { name: 'YAML', exact: true }).click();
    await expect(ndv).toHaveAttribute('data-state', 'docked');
    const edge = page.getByRole('separator', { name: 'Resize step details' });

    // A drag to 1400px: the canvas ends at the edge of the YAML.
    const grip = await box(edge);
    const y = grip.y + grip.height / 2;
    await page.mouse.move(grip.x + grip.width / 2, y);
    await page.mouse.down();
    await page.mouse.move(2560 - 1400, y, { steps: 8 });
    await page.mouse.up();
    await expect(async () => {
      const panel = await box(ndv);
      expect(panel.width).toBeCloseTo(1400, -1);
      const frame = await box(canvasFrame(page));
      expect(frame.x + frame.width).toBeLessThanOrEqual(panel.x + 0.5);
    }).toPass();

    // The Workflow dock has a width of its own.
    await toolbar
      .getByRole('button', { name: 'Workflow', exact: true })
      .click();
    const workflow = page.getByTestId('ve-workflow');
    await expect(workflow).toBeVisible();
    await expect(ndv).toBeHidden();
    expect((await box(workflow)).width).toBeLessThan(1300);
  });

  // The right docks take turns: opening one closes the other, and none
  // changes the zoom. The docks have no Maximize button.
  test('takes turns between the docks and keeps the zoom', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);
    const toolbar = page.getByTestId('ve-toolbar');
    const yaml = toolbar.getByRole('button', { name: 'YAML', exact: true });
    const workflow = toolbar.getByRole('button', {
      name: 'Workflow',
      exact: true,
    });
    const zoom = page.getByTestId('flow-zoom-reset');
    const before = await zoom.innerText();

    await yaml.click();
    await expect(page.getByTestId('ndv')).toHaveAttribute(
      'data-state',
      'docked'
    );
    await expect(yaml).toHaveAttribute('aria-pressed', 'true');
    await workflow.click();
    await expect(page.getByTestId('ve-workflow')).toBeVisible();
    await expect(workflow).toHaveAttribute('aria-pressed', 'true');
    await expect(yaml).toHaveAttribute('aria-pressed', 'false');
    await expect(page.getByTestId('ndv')).toBeHidden();
    await expect(
      page.getByRole('button', { name: 'Maximize panel' })
    ).toHaveCount(0);
    await workflow.click();
    await expect(page.getByTestId('ve-workflow')).toBeHidden();
    await expect(workflow).toHaveAttribute('aria-pressed', 'false');
    await expect(zoom).toHaveText(before);
  });

  // Cron and Always on share one slot of the launchers.
  test('draws one launcher for the slot of Cron and Always on', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    const token = await adminToken(request);
    const slots = [
      { extra: '', shown: 'schedule' },
      { extra: 'schedule: "0 * * * *"\n', shown: 'cron' },
      {
        extra:
          'restart_policy: always\nrestart_wait_sec: 5\nhist_retention_runs: 20\n',
        shown: 'always',
      },
    ];
    for (const { extra, shown } of slots) {
      const name = uniqueName('e2e-ws-slot');
      const fileName = await writeLocalDAG(
        name,
        `name: ${name}\n${extra}steps:\n  - name: one\n    run: echo one\n`
      );
      try {
        await waitForDAGAvailable(request, token, fileName);
        await openEditor(page, gotoWorkspace, fileName);
        const canvas = page.getByTestId('ww-canvas');
        for (const id of ['schedule', 'cron', 'always']) {
          await expect(canvas.getByTestId(`ve-launcher-${id}`)).toHaveCount(
            id === shown ? 1 : 0
          );
        }
        if (shown === 'schedule') {
          await expect(
            canvas.getByTestId('ve-launcher-schedule')
          ).toContainText('Cron or Always on');
        }
        if (shown === 'always') {
          await expect(canvas.getByTestId('ve-launcher-always')).toContainText(
            '5 s · 20 runs'
          );
        }
      } finally {
        await request.delete(
          `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
          { headers: { Authorization: `Bearer ${token}` } }
        );
      }
    }
  });

  test('has no horizontal scroll at 375x812 and keeps the Tools button', async ({
    page,
    request,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    await page.setViewportSize({ width: 375, height: 812 });
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, [
      'succeeded',
      'partially_succeeded',
    ]);
    await openEditor(page, gotoWorkspace, fileName);

    await expect(
      page
        .getByTestId('ww-canvas')
        .getByRole('button', { name: 'Tools', exact: true })
    ).toBeVisible();
    await expect(page.getByTestId('flow-minimap')).toBeHidden();
    expect((await documentScrolls(page)).horizontal).toBe(false);

    // The Run panel of the same tab is a bottom sheet at this width.
    const dock = page.getByTestId('ww-dock');
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'open');
    await expect(dock).toHaveAttribute('data-phone-view', 'true');
    expect((await documentScrolls(page)).horizontal).toBe(false);
  });
});

// No dock and no Activity panel opens by default: a browser with no stored
// choice shows the graph alone.
test.describe('workspace defaults on a laptop', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1024,
    'laptop layout only'
  );

  test('shows only the graph at the first render', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await signIn(page);
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );
    await expect(page.getByTestId('ndv')).toBeHidden();
    await expect(page.getByTestId('ve-workflow')).toBeHidden();
    const toolbar = page.getByTestId('ve-toolbar');
    for (const name of ['Workflow', 'YAML']) {
      await expect(
        toolbar.getByRole('button', { name, exact: true })
      ).toHaveAttribute('aria-pressed', 'false');
    }
  });
});
