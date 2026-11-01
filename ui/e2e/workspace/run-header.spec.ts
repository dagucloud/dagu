// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { APIRequestContext, Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { expect, signIn, test } from './fixtures';

// parity:chg-run-url-latest (the redirect to the latest run) and the run
// header: facts as wide as their text, the whole run ID, no tooltip that
// repeats the bar, and "Healthy" for a run that never ends.

const DOCK_KEYS = [
  'dagu.workspace.dockHeight',
  'dagu.workspace.dockOpen',
  'dagu.workspace.dockOpen.v2',
];

/** The widest gap between two facts that the owner accepts, in px. */
const MAX_GAP_PX = 12;

// The second keeps each run in its own start second: the order is fixed.
const FINISHED = `
steps:
  - name: hello
    run: |
      sleep 1
      echo hello
`;

// A step that repeats for ever, every second.
const CONTINUOUS = `
steps:
  - name: heartbeat
    run: echo beat
    repeat_policy:
      repeat: while
      interval_sec: 1
`;

// The continuous step runs while another step fails.
const CONTINUOUS_FAILING = `
steps:
  - name: heartbeat
    run: echo beat
    repeat_policy:
      repeat: while
      interval_sec: 1
  - name: broken
    run: exit 3
`;

// A run that stays active, with a step done: the chip shows "1/2".
const SLOW = `
steps:
  - name: quick
    run: echo quick
  - name: slow
    depends: [quick]
    run: sleep 60
`;

async function adminToken(request: APIRequestContext): Promise<string> {
  const stack = await loadStack();
  return loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
}

function headers(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}` };
}

async function createDAG(
  request: APIRequestContext,
  token: string,
  created: string[],
  spec: string
): Promise<{ dagName: string; fileName: string }> {
  const dagName = uniqueName('e2e-ws-head');
  const fileName = await writeLocalDAG(dagName, spec);
  created.push(fileName);
  await waitForDAGAvailable(request, token, fileName);
  return { dagName, fileName };
}

function query(page: Page, key: string): string | null {
  return new URL(page.url()).searchParams.get(key);
}

async function openWorkflow(
  page: Page,
  gotoWorkspace: (page: Page, target: string) => Promise<void>,
  fileName: string
): Promise<void> {
  await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);
  await expect(page.getByTestId('workflow-workspace')).toBeVisible({
    timeout: 30_000,
  });
  await expect(page.getByTestId('ww-run-chip')).toBeVisible({
    timeout: 30_000,
  });
}

/**
 * Sets the viewport so that the workflow bar is `barWidth` wide: the page has
 * a constant frame around the bar.
 */
async function setBarWidth(page: Page, barWidth: number): Promise<void> {
  const bar = page.getByTestId('ww-bar');
  const size = page.viewportSize()!;
  const current = (await bar.boundingBox())!.width;
  await page.setViewportSize({
    width: Math.round(size.width + barWidth - current),
    height: size.height,
  });
  await expect
    .poll(async () => Math.round((await bar.boundingBox())!.width))
    .toBe(barWidth);
}

interface Slot {
  x: number;
  width: number;
}

interface SlotBoxes {
  chip: Slot | null;
  identity: Slot | null;
  count: Slot | null;
  latest: Slot | null;
  facts: Slot | null;
  stale: Slot | null;
  actions: Slot | null;
}

/**
 * What does not depend on the text of the facts: the x of each slot before
 * the facts and of the facts from the chip, the widths of the slots, the width of the run
 * icons. The stale slot comes after the facts: only its width is fixed.
 */
function fixedGeometry(boxes: SlotBoxes) {
  // The x is from the left edge of the chip: the name before it differs.
  const from = (slot: Slot | null) =>
    slot && boxes.chip
      ? { x: Math.round(slot.x - boxes.chip.x), width: Math.round(slot.width) }
      : null;
  return {
    identity: from(boxes.identity),
    count: from(boxes.count),
    latest: from(boxes.latest),
    factsX: from(boxes.facts)?.x,
    staleWidth: boxes.stale?.width,
    actionsWidth: boxes.actions?.width,
  };
}

/** The boxes of the fixed slots of the run chip, in px with a tenth. */
async function slotBoxes(page: Page): Promise<SlotBoxes> {
  return page.evaluate(() => {
    const slot = (id: string) => {
      const element = document.querySelector(`[data-testid="${id}"]`);
      if (!element) return null;
      const rect = element.getBoundingClientRect();
      return {
        x: Math.round(rect.x * 10) / 10,
        width: Math.round(rect.width * 10) / 10,
      };
    };
    return {
      chip: slot('ww-run-chip'),
      identity: slot('ww-run-chip-identity'),
      count: slot('ww-run-count-slot'),
      latest: slot('ww-run-latest'),
      facts: slot('ww-run-facts'),
      stale: slot('ww-run-stale-slot'),
      actions: slot('ww-bar-run-actions'),
    };
  });
}

interface FactBox {
  title: string;
  left: number;
  right: number;
}

/** The boxes of the facts of the row, from left to right. */
async function factBoxes(page: Page): Promise<FactBox[]> {
  return page.getByTestId('run-facts-inline').evaluate((row) =>
    Array.from(row.children).map((child) => {
      const rect = child.getBoundingClientRect();
      return {
        title: child.getAttribute('title') ?? '',
        left: rect.left,
        right: rect.right,
      };
    })
  );
}

test.describe('workspace run header', () => {
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 1152,
    'desktop layout only'
  );

  const created: string[] = [];
  const stopped: Array<{ dagName: string; runId: string }> = [];

  test.beforeEach(async ({ page }) => {
    await page.addInitScript((keys) => {
      for (const key of keys) window.localStorage.removeItem(key);
    }, DOCK_KEYS);
    await signIn(page);
  });

  test.afterEach(async ({ request }) => {
    const token = await adminToken(request);
    for (const { dagName, runId } of stopped.splice(0)) {
      await request.post(
        `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(
          runId
        )}/stop?remoteNode=local`,
        { headers: headers(token) }
      );
    }
    for (const fileName of created.splice(0)) {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: headers(token) }
      );
    }
  });

  // parity:chg-run-url-latest
  test('names the latest run in the URL and keeps the dock closed', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await createDAG(
      request,
      token,
      created,
      FINISHED
    );
    const older = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, older, ['succeeded']);
    const newest = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, newest, ['succeeded']);

    await page.setViewportSize({ width: 1920, height: 900 });
    await openWorkflow(page, gotoWorkspace, fileName);
    await expect.poll(() => query(page, 'run')).toBe(newest);
    const chip = page.getByTestId('ww-run-chip');
    await expect(
      chip.getByRole('button', { name: 'Latest run' })
    ).toBeDisabled();
    // The redirect is no request to open the Run panel.
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );
    // A reload keeps it closed: the mark of the redirect is in the history entry.
    await page.reload();
    await expect(page.getByTestId('ww-run-chip')).toBeVisible({
      timeout: 30_000,
    });
    expect(query(page, 'run')).toBe(newest);
    await expect(page.getByTestId('ww-dock')).toHaveAttribute(
      'data-state',
      'collapsed'
    );

    // An older run: the chip offers the way back, with its icon.
    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?run=${encodeURIComponent(older)}`
    );
    const back = page
      .getByTestId('ww-run-chip')
      .getByRole('button', { name: 'Latest run' });
    await expect(back).toBeEnabled({ timeout: 30_000 });
    await expect(back.locator('svg')).toHaveCount(1);
    await back.click();
    await expect.poll(() => query(page, 'run')).toBe(newest);
    await expect(
      page
        .getByTestId('ww-run-chip')
        .getByRole('button', { name: 'Latest run' })
    ).toBeDisabled();
  });

  test('keeps the facts as wide as their text, with the whole ID and no repeating tooltip', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await createDAG(
      request,
      token,
      created,
      FINISHED
    );
    const runId = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, runId, ['succeeded']);

    await page.setViewportSize({ width: 1920, height: 900 });
    await openWorkflow(page, gotoWorkspace, fileName);

    const ALL = ['Trigger', 'Started', 'Duration', 'Run ID'];
    for (const [barWidth, expected] of [
      [1760, ALL],
      [1440, ALL],
      [1280, ALL],
      [1216, ['Started', 'Duration', 'Run ID']],
      [1152, ['Duration', 'Run ID']],
    ] as const) {
      await test.step(`bar of ${barWidth}px`, async () => {
        await setBarWidth(page, barWidth);
        const row = page.getByTestId('ww-run-facts');
        await expect(row).toBeVisible();

        // The facts go in the order trigger, start, duration.
        await expect
          .poll(async () => (await factBoxes(page)).map((box) => box.title))
          .toEqual([...expected]);
        const boxes = await factBoxes(page);
        const titles = boxes.map((box) => box.title);
        // No big empty gap between two facts.
        for (let i = 1; i < boxes.length; i += 1) {
          const gap = boxes[i]!.left - boxes[i - 1]!.right;
          expect(gap, `${titles[i - 1]} to ${titles[i]}`).toBeLessThanOrEqual(
            MAX_GAP_PX
          );
          expect(gap).toBeGreaterThanOrEqual(0);
        }

        // The whole ID, with no cut: the text fits its box.
        const id = row.locator('code');
        await expect(id).toHaveText(runId);
        const cut = await id.evaluate(
          (el) => el.scrollWidth > el.clientWidth + 1
        );
        expect(cut).toBe(false);
        await expect(id).not.toHaveAttribute('title', /.+/);

        // The status icon says only what the row leaves out: nothing at a bar
        // that shows every fact, the trigger below 1280px. Never the ID.
        const identity = page.getByTestId('ww-run-chip-identity');
        await identity.hover();
        await page.waitForTimeout(700);
        const tooltip = page.getByRole('tooltip');
        if (expected.includes('Trigger')) {
          await expect(tooltip).toHaveCount(0);
        } else {
          await expect(tooltip).toContainText('Manual');
          await expect(tooltip).not.toContainText(runId);
        }
        await page.mouse.move(5, 500);
      });
    }

    // Under 1152px the row is gone, and the tooltip of the status has the ID.
    await setBarWidth(page, 1100);
    await expect(page.getByTestId('ww-run-facts')).toHaveCount(0);
    await page.getByTestId('ww-run-chip-identity').hover();
    await expect(page.getByRole('tooltip')).toContainText(runId);
  });

  test('reads Healthy for a run that never ends, and keeps a failure visible', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(150_000);
    const token = await adminToken(request);
    const good = await createDAG(request, token, created, CONTINUOUS);
    const goodRun = await startDAG(request, token, good.fileName);
    stopped.push({ dagName: good.dagName, runId: goodRun });
    await expect
      .poll(async () => {
        const response = await request.get(
          `/api/v1/dag-runs/${encodeURIComponent(good.dagName)}/${encodeURIComponent(goodRun)}?remoteNode=local`,
          { headers: headers(token) }
        );
        const body = (await response.json()) as {
          dagRunDetails: { nodes: Array<{ doneCount: number }> };
        };
        return body.dagRunDetails.nodes[0]?.doneCount ?? 0;
      })
      .toBeGreaterThan(1);

    await page.setViewportSize({ width: 1920, height: 900 });
    await openWorkflow(page, gotoWorkspace, good.fileName);
    const chip = page.getByTestId('ww-run-chip');
    await expect(chip.getByRole('img', { name: 'Healthy' })).toBeVisible({
      timeout: 30_000,
    });
    // The raw API status stays in the data attribute.
    await expect(chip).toHaveAttribute('data-status', '1');
    // The repeat count takes the place of the progress "0/1".
    await expect(chip.getByTestId('repeat-badge')).toContainText('×');
    await expect(chip.getByRole('group', { name: 'Run progress' })).toHaveCount(
      0
    );
    await expect(
      chip.getByRole('button', { name: 'Stop', exact: true })
    ).toHaveCount(0);
    await expect(
      page
        .getByTestId('ww-bar')
        .getByRole('button', { name: 'Stop', exact: true })
    ).toBeVisible();

    // A failed step of the same kind of run stays visible.
    const bad = await createDAG(request, token, created, CONTINUOUS_FAILING);
    const badRun = await startDAG(request, token, bad.fileName);
    stopped.push({ dagName: bad.dagName, runId: badRun });
    await openWorkflow(page, gotoWorkspace, bad.fileName);
    const badChip = page.getByTestId('ww-run-chip');
    await expect(
      badChip.getByRole('button', { name: 'Failed at broken' })
    ).toBeVisible({ timeout: 30_000 });
    await expect(badChip.getByRole('img', { name: 'Healthy' })).toHaveCount(0);
  });

  // The chip keeps the place of each of its parts when a run starts or ends,
  // when the stale dot appears, and between the latest button on and off. The
  // facts after the slots are as wide as their text and may differ.
  test('keeps the slots of the chip in place in every state of a run', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(240_000);
    const token = await adminToken(request);

    // A finished run: the reference.
    const finished = await createDAG(request, token, created, FINISHED);
    const older = await startDAG(request, token, finished.fileName);
    await waitForRunStatus(request, token, finished.dagName, older, [
      'succeeded',
    ]);
    const newest = await startDAG(request, token, finished.fileName);
    await waitForRunStatus(request, token, finished.dagName, newest, [
      'succeeded',
    ]);
    await page.setViewportSize({ width: 1920, height: 900 });
    await openWorkflow(page, gotoWorkspace, finished.fileName);
    await expect(
      page
        .getByTestId('ww-run-chip')
        .getByRole('button', { name: 'Latest run' })
    ).toBeDisabled();
    await setBarWidth(page, 1440);
    const reference = await slotBoxes(page);
    expect(
      reference.count?.width,
      'the count slot has a width'
    ).toBeGreaterThan(30);
    expect(reference.latest?.width, 'the latest button has a width').toBe(28);
    expect(
      reference.actions?.width,
      'the run icons have a slot'
    ).toBeGreaterThan(28);

    const sameAsReference = async (state: string) => {
      await setBarWidth(page, 1440);
      // The slots of the chip: the same x and width; the facts start at the
      // same x. The run icons have the same width.
      await expect
        .poll(async () => fixedGeometry(await slotBoxes(page)), {
          message: state,
        })
        .toEqual(fixedGeometry(reference));
    };

    // A pinned older run: the latest button is on and keeps its box.
    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(finished.fileName)}?run=${encodeURIComponent(older)}`
    );
    await expect(
      page
        .getByTestId('ww-run-chip')
        .getByRole('button', { name: 'Latest run' })
    ).toBeEnabled({ timeout: 30_000 });
    await sameAsReference('pinned older run');

    // A run that is active, with its progress.
    const slow = await createDAG(request, token, created, SLOW);
    const slowRun = await startDAG(request, token, slow.fileName);
    stopped.push({ dagName: slow.dagName, runId: slowRun });
    await openWorkflow(page, gotoWorkspace, slow.fileName);
    await expect(page.getByTestId('ww-run-progress')).toContainText('1/2', {
      timeout: 60_000,
    });
    await sameAsReference('running run with progress');

    // A run that never ends, with its repeat count.
    const good = await createDAG(request, token, created, CONTINUOUS);
    const goodRun = await startDAG(request, token, good.fileName);
    stopped.push({ dagName: good.dagName, runId: goodRun });
    await openWorkflow(page, gotoWorkspace, good.fileName);
    await expect(
      page.getByTestId('ww-run-count-slot').getByTestId('repeat-badge')
    ).toContainText('×', { timeout: 60_000 });
    await sameAsReference('running continuous run');

    // A run that used another definition: the stale dot fills its slot.
    await writeLocalDAG(
      finished.dagName,
      `${FINISHED}
  - name: added-after-run
    depends: [hello]
    run: echo added
`
    );
    await openWorkflow(page, gotoWorkspace, finished.fileName);
    await expect(page.getByTestId('ww-run-stale-dot')).toBeVisible({
      timeout: 60_000,
    });
    await sameAsReference('stale run');
  });

  // The Run details button of the compact bar (the wide bar has none) opens
  // a popover with what the bar does not show: the time of the reused steps
  // and the inputs used. Esc closes it and the focus returns to the button.
  test('opens the run details popover and closes it', async ({
    page,
    request,
    gotoWorkspace,
  }) => {
    test.setTimeout(180_000);
    const token = await adminToken(request);
    const { dagName, fileName } = await createDAG(
      request,
      token,
      created,
      `
steps:
  - name: slow
    run: |
      sleep 3
      echo slow
  - name: use
    depends: [slow]
    run: echo use
`
    );
    const full = await startDAG(request, token, fileName);
    await waitForRunStatus(request, token, dagName, full, ['succeeded']);
    const reuse = await startDAG(request, token, fileName, {
      steps: ['use'],
      outputsFromRunId: full,
    });
    await waitForRunStatus(request, token, dagName, reuse, ['succeeded']);

    // The chip shows at the default width; the compact bar replaces it at 900.
    await openWorkflow(page, gotoWorkspace, fileName);
    await expect.poll(() => query(page, 'run')).toBe(reuse);
    await page.setViewportSize({ width: 900, height: 900 });

    const button = page
      .getByTestId('ww-bar-compact-run')
      .getByRole('button', { name: 'Run details' });
    await expect(button).toHaveAttribute('aria-expanded', 'false');
    await expect(page.getByRole('dialog', { name: 'Run details' })).toHaveCount(
      0
    );
    await button.click();
    const dialog = page.getByRole('dialog', { name: 'Run details' });
    await expect(dialog).toBeVisible();
    await expect(button).toHaveAttribute('aria-expanded', 'true');
    await expect(dialog.getByTestId('ww-run-reused-time')).toContainText(
      'Reused time'
    );
    // The inputs used are folded; opened, they name the source run.
    const fold = dialog.getByRole('button', { name: /^Inputs used/ });
    await expect(fold).toHaveAttribute('aria-expanded', 'false');
    await fold.click();
    await expect(dialog.getByTestId('inputs-used-source')).toContainText(
      full.slice(0, 8)
    );
    const box = (await dialog.boundingBox())!;
    expect(box.width).toBeLessThanOrEqual(384 + 1);
    expect(box.y + box.height).toBeLessThanOrEqual(900);

    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(button).toBeFocused();

    // A click outside closes it too.
    await button.click();
    await expect(dialog).toBeVisible();
    await page.mouse.click(5, 500);
    await expect(dialog).toHaveCount(0);
  });
});
