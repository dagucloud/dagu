// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locator, Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  openColumnTab,
  openStepForm,
  startDAG,
  waitForRunStatus,
} from '../helpers/e2e';
import { expect, signIn, test } from './fixtures';

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
      // Collapsed: a laptop would open the Run panel by default.
      window.localStorage.setItem('dagu.workspace.dockOpen.v2', 'false');
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

  // The toolbar of the canvas is pinned to the top right of the zone: Save
  // stays at the same x and y with the YAML and the Workflow tabs of the
  // right column and with the node view, clean and dirty.
  test('pins the canvas toolbar to the zone when a panel opens, at five widths', async ({
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
    const expectSame = async (
      reference: Awaited<ReturnType<typeof parts>>,
      where: string
    ) => {
      const now = await settled();
      for (const part of ['row', 'toolbar', 'slot', 'save'] as const) {
        for (const key of ['x', 'y', 'width', 'height'] as const) {
          expect(now[part][key], `${where}: ${part}.${key}`).toBeCloseTo(
            reference[part][key],
            0
          );
        }
      }
    };

    // The YAML tab of the right column is open at every width.
    await openColumnTab(page, 'YAML');
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

        // The tabs of the right column, beside the zone: the YAML sits right
        // of the zone, and a switch of tab keeps the zone.
        await openColumnTab(page, 'YAML');
        await expect(ndv).toHaveAttribute('data-state', 'docked');
        expect((await box(ndv)).x).toBeGreaterThanOrEqual(
          zone.x + zone.width - 0.5
        );
        await expectSame(reference, 'YAML tab');
        await openColumnTab(page, 'Workflow');
        await expect(page.getByTestId('ve-workflow')).toBeVisible();
        await expect(ndv).toBeHidden();
        await expectSame(reference, 'Workflow tab');
        await openColumnTab(page, 'YAML');
        await expect(ndv).toHaveAttribute('data-state', 'docked');
        await expectSame(reference, 'YAML tab again');

        // The node view.
        await page
          .getByTestId('ww-canvas')
          .getByTestId('ve-canvas-node-steps[0]')
          .dblclick();
        await expect(ndv).toHaveAttribute('data-state', 'expanded');
        // The expanded node view covers the whole zone, the row included.
        expect((await box(ndv)).y).toBeCloseTo(zone.y, 0);
        await expectSame(reference, 'node view');
        // Closed, the view gives the YAML tab back.
        await ndv.getByRole('button', { name: 'Close', exact: true }).click();
        await expect(ndv).toHaveAttribute('data-state', 'docked');
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

  test('keeps the node view in the graph area and the YAML beside the canvas at 1280px and 900px', async ({
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

    // The expanded view fills the graph area, the zone and the right column:
    // not the window, and it leaves the bar and the sidebar free.
    const zone = await box(page.getByTestId('ww-canvas'));
    const area = await box(page.getByTestId('ww-panel-layer'));
    const bar = await box(page.getByTestId('ww-bar'));
    let panel = await box(ndv);
    expect(panel.x).toBeGreaterThanOrEqual(zone.x - 0.5);
    expect(panel.y).toBeGreaterThanOrEqual(bar.y + bar.height - 0.5);
    expect(panel.x + panel.width).toBeLessThanOrEqual(
      area.x + area.width + 0.5
    );
    expect(panel.y + panel.height).toBeLessThanOrEqual(
      area.y + area.height + 0.5
    );
    expect(panel.width).toBeLessThan(1280 - 200);
    await expect(page.getByTestId('ww-bar')).toBeVisible();
    await page
      .getByTestId('ww-run-split')
      .locator('[data-ww-action="start"]')
      .click({ trial: true, timeout: 5_000 });

    // The YAML tab of the right column sits beside the canvas, which keeps
    // the rest. The node view has no button that docks it.
    await expect(
      ndv.getByRole('button', { name: 'Dock to the side', exact: true })
    ).toHaveCount(0);
    await ndv.getByRole('button', { name: 'Close', exact: true }).click();
    await openColumnTab(page, 'YAML');
    await expect(ndv).toHaveAttribute('data-state', 'docked');
    const expectBeside = async () => {
      const frame = await box(canvasFrame(page));
      panel = await box(ndv);
      expect(frame.x + frame.width).toBeLessThanOrEqual(panel.x + 0.5);
      expect(panel.width).toBeGreaterThan(0);
    };
    await expectBeside();

    await page.setViewportSize({ width: 900, height: 800 });
    await expect(ndv).toBeVisible();
    await expect(expectBeside).toPass({ timeout: 10_000 });
  });

  // #3507: a long line of YAML needs more than 960px on a wide screen.
  test('widens the right column past 960px for both of its tabs', async ({
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
    const column = page.getByTestId('ww-right-column');
    const area = await box(page.getByTestId('ww-panel-layer'));

    await openColumnTab(page, 'YAML');
    await expect(ndv).toHaveAttribute('data-state', 'docked');
    const edge = column.getByRole('separator', { name: 'Resize step details' });

    // A drag to 1400px: the YAML follows the column, and the canvas ends at
    // its edge.
    const grip = await box(edge);
    const y = grip.y + grip.height / 2;
    await page.mouse.move(grip.x + grip.width / 2, y);
    await page.mouse.down();
    await page.mouse.move(area.x + area.width - 1400, y, { steps: 8 });
    await page.mouse.up();
    await expect(async () => {
      expect((await box(column)).width).toBeCloseTo(1400, -1);
      // The YAML fills the column, inside its 1px left border.
      const panel = await box(ndv);
      expect(
        Math.abs(panel.width - (await box(column)).width)
      ).toBeLessThanOrEqual(1);
      const frame = await box(canvasFrame(page));
      expect(frame.x + frame.width).toBeLessThanOrEqual(panel.x + 0.5);
    }).toPass();

    // The Workflow tab has the same width.
    await openColumnTab(page, 'Workflow');
    await expect(page.getByTestId('ve-workflow')).toBeVisible();
    expect((await box(column)).width).toBeCloseTo(1400, -1);
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

// A laptop opens the YAML and the Run panel until the user closes them.
test.describe('workspace defaults on a laptop', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1024,
    'laptop layout only'
  );
  test.use({ storageState: { cookies: [], origins: [] } });

  test('opens the YAML and the Run panel, and remembers a close', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await signIn(page);
    const { fileName } = await fixtureDAG('visual-params.yaml');
    await openEditor(page, gotoWorkspace, fileName);
    const ndv = page.getByTestId('ndv');
    const dock = page.getByTestId('ww-dock');
    await expect(ndv).toHaveAttribute('data-state', 'docked', {
      timeout: 30_000,
    });
    await expect(dock).toHaveAttribute('data-state', 'open');

    // The right column collapses to its rail.
    const column = page.getByTestId('ww-right-column');
    await column.getByRole('button', { name: 'Collapse', exact: true }).click();
    await dock
      .getByRole('button', { name: 'Activity', exact: true })
      .first()
      .click();
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
    await page.reload();
    await expect(
      page.getByTestId('ww-canvas').getByTestId('ve-canvas-node-steps[0]')
    ).toBeVisible({ timeout: 30_000 });
    await expect(column).toHaveAttribute('data-tab', 'collapsed');
    await expect(ndv).toBeHidden();
    await expect(dock).toHaveAttribute('data-state', 'collapsed');
  });
});
