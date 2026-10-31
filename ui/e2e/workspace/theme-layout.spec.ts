// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Page } from '@playwright/test';
import {
  loadStack,
  loginViaAPI,
  startDAG,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from '../helpers/e2e';
import { test as base, expect, signIn } from './fixtures';

// parity:crit-theme-awareness (the bar and the Rename and Delete buttons in
// light and dark, in en and ja, at three widths). The bar is one: the run chip
// or, in a narrow bar, the status dot of the run, with the run icons. The page
// is checked with no ?run (the latest run) and with ?run (a pinned run).

const VIEWPORTS = [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 375, height: 812 },
] as const;
const THEMES = ['light', 'dark'] as const;
const LOCALES = ['en', 'ja'] as const;

/** WCAG AA for normal text. */
const MIN_CONTRAST = 4.5;
const BAR_HEIGHT = 40;

// Labels, a schedule and a run fill every slot of the bar.
function spec(): string {
  return `
description: Fill the workflow bar for the theme and layout checks.
labels:
  - team-alpha
  - nightly
schedule: "0 3 1 1 *"
steps:
  - name: greet
    run: echo hello
`;
}

interface BarReport {
  pageScrollsX: boolean;
  bar: { top: number; bottom: number; height: number } | null;
  /** Relative luminance of the background behind the root (0 to 1). */
  backdropLuminance: number | null;
  /** Visible elements outside the vertical band of the bar. */
  outside: string[];
  /** Visible elements whose content is wider than their box. */
  truncated: string[];
  /** Visible text whose contrast is under the minimum. */
  lowContrast: string[];
}

/**
 * Runs in the page. The colours come from getComputedStyle; a 1x1 canvas
 * turns any CSS colour syntax into sRGB bytes.
 */
function inspect({
  rootSelector,
  minContrast,
  clipsInside,
  shrinks,
}: {
  rootSelector: string;
  minContrast: number;
  /** An element that may clip its own content; its box is still checked. */
  clipsInside?: string;
  /** Elements that shrink and clip by design, with their content. */
  shrinks?: string;
}): BarReport {
  const canvas = document.createElement('canvas');
  canvas.width = 1;
  canvas.height = 1;
  const ctx = canvas.getContext('2d', { willReadFrequently: true })!;
  const rgba = (css: string): [number, number, number, number] => {
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = '#000';
    ctx.fillStyle = css;
    ctx.fillRect(0, 0, 1, 1);
    const [r = 0, g = 0, b = 0, a = 0] = ctx.getImageData(0, 0, 1, 1).data;
    return [r, g, b, a / 255];
  };
  const over = (
    [r, g, b, a]: [number, number, number, number],
    [br, bg, bb]: [number, number, number]
  ): [number, number, number] => [
    r * a + br * (1 - a),
    g * a + bg * (1 - a),
    b * a + bb * (1 - a),
  ];
  const backdrop = (el: Element): [number, number, number] => {
    const layers: Array<[number, number, number, number]> = [];
    for (let node: Element | null = el; node; node = node.parentElement) {
      const colour = rgba(getComputedStyle(node).backgroundColor);
      if (colour[3] > 0) layers.push(colour);
      if (colour[3] >= 1) break;
    }
    let out: [number, number, number] = [255, 255, 255];
    for (const layer of layers.reverse()) out = over(layer, out);
    return out;
  };
  const luminance = (rgb: [number, number, number]) => {
    const [r = 0, g = 0, b = 0] = rgb.map((v) => {
      const c = v / 255;
      return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const describe = (el: Element) => {
    const id = el.getAttribute('data-testid');
    const text = (el.textContent ?? '').trim().slice(0, 40);
    return `${el.tagName.toLowerCase()}${id ? `[${id}]` : ''} "${text}"`;
  };
  const visible = (el: Element) => {
    if (!(el instanceof HTMLElement)) return false;
    if (!el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) {
      return false;
    }
    const rect = el.getBoundingClientRect();
    // sr-only text is 1px wide.
    return rect.width > 1 && rect.height > 1;
  };
  const disabled = (el: Element) =>
    !!el.closest(':disabled, [aria-disabled="true"], [data-disabled]');
  const ownText = (el: Element) =>
    Array.from(el.childNodes).some(
      (n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? '').trim()
    );

  const report: BarReport = {
    pageScrollsX:
      document.documentElement.scrollWidth > window.innerWidth ||
      document.body.scrollWidth > window.innerWidth,
    bar: null,
    backdropLuminance: null,
    outside: [],
    truncated: [],
    lowContrast: [],
  };
  const root = document.querySelector(rootSelector);
  if (!root) return report;
  const box = root.getBoundingClientRect();
  report.bar = { top: box.top, bottom: box.bottom, height: box.height };
  report.backdropLuminance = luminance(backdrop(root));

  for (const el of [root, ...Array.from(root.querySelectorAll('*'))]) {
    if (!visible(el)) continue;
    const html = el as HTMLElement;
    const rect = html.getBoundingClientRect();
    if (rect.top < box.top - 0.5 || rect.bottom > box.bottom + 0.5) {
      report.outside.push(describe(el));
    }
    const clipped = clipsInside ? el.parentElement?.closest(clipsInside) : null;
    if (
      !clipped &&
      !(shrinks && el.closest(shrinks)) &&
      html.clientWidth > 0 &&
      html.scrollWidth > html.clientWidth
    ) {
      report.truncated.push(
        `${describe(el)} ${html.scrollWidth}>${html.clientWidth}`
      );
    }
    if (ownText(el) && !disabled(el)) {
      const bg = backdrop(el);
      const fg = over(rgba(getComputedStyle(el).color), bg);
      const [hi, lo] = [luminance(fg), luminance(bg)].sort((a, b) => b - a);
      const ratio = ((hi ?? 0) + 0.05) / ((lo ?? 0) + 0.05);
      if (ratio < minContrast) {
        report.lowContrast.push(`${describe(el)} ${ratio.toFixed(2)}`);
      }
    }
  }
  return report;
}

// The run chip and the run icons share the bar with the name, the labels and
// the trigger. The workflow name and the label chips shrink first and end
// with an ellipsis; the name keeps its full text in its title. The run facts
// (RunMetaLine) are in the tooltip of the chip and in the dock, not in the bar.
const NARROW_BAR_MAX = 1100;
const SHRINKING =
  '[data-testid="ww-bar-labels"], [data-testid="ww-label-chips"], h1';

async function inspectBar(page: Page): Promise<BarReport> {
  return page.evaluate(inspect, {
    rootSelector: '[data-testid="ww-bar"]',
    minContrast: MIN_CONTRAST,
    clipsInside: '[data-testid="ww-run-chip"]',
    shrinks: SHRINKING,
  });
}

/** A shrunk workflow name stays readable through its title. */
async function expectNameInTitle(page: Page, where: string): Promise<void> {
  const name = page.getByTestId('ww-bar').locator('h1');
  await expect(name, `workflow name (${where})`).toBeVisible();
  const full = ((await name.textContent()) ?? '').trim();
  expect(full, `workflow name text (${where})`).not.toBe('');
  await expect(name, `workflow name title (${where})`).toHaveAttribute(
    'title',
    full
  );
}

/**
 * Rename is a pencil right after the workflow name; Delete is an icon button
 * at the end of the bar, on the row of Refresh. Both keep a name and stay in
 * the bar, and the keyboard reaches them.
 */
async function checkEditButtons(page: Page, where: string): Promise<void> {
  const bar = page.getByTestId('ww-bar');
  // The names are translated: the test ids find the buttons in every language.
  const rename = bar.getByTestId('ww-bar-rename');
  const remove = bar.getByTestId('ww-bar-delete');
  await expect(rename, `Rename (${where})`).toBeVisible();
  await expect(remove, `Delete (${where})`).toBeVisible();
  const [barBox, renameBox, removeBox] = await Promise.all([
    bar.boundingBox(),
    rename.boundingBox(),
    remove.boundingBox(),
  ]);
  for (const box of [renameBox, removeBox]) {
    expect(box!.y, `button row (${where})`).toBeGreaterThanOrEqual(barBox!.y);
    expect(box!.y + box!.height, `button row (${where})`).toBeLessThanOrEqual(
      barBox!.y + barBox!.height + 0.5
    );
    expect(box!.x + box!.width, `button in bar (${where})`).toBeLessThanOrEqual(
      barBox!.x + barBox!.width + 0.5
    );
  }
  const nameBox = await bar.locator('h1').boundingBox();
  const gap = renameBox!.x - (nameBox!.x + nameBox!.width);
  expect(gap, `Rename next to the name (${where})`).toBeGreaterThanOrEqual(
    -0.5
  );
  expect(gap, `Rename next to the name (${where})`).toBeLessThanOrEqual(12);
  expect(removeBox!.x, `Delete after Rename (${where})`).toBeGreaterThan(
    renameBox!.x
  );
  await rename.focus();
  await expect(rename, `Rename focus (${where})`).toBeFocused();
  await remove.focus();
  await expect(remove, `Delete focus (${where})`).toBeFocused();
}

type RanDAG = { fileName: string; runId: string };

/** Writes the DAG, runs it to success, and deletes it after the test. */
const test = base.extend<{ ranDAG: RanDAG }>({
  ranDAG: async ({ request }, use) => {
    const stack = await loadStack();
    const token = await loginViaAPI(
      request,
      stack.auth.adminUsername,
      stack.auth.adminPassword
    );
    const dagName = uniqueName('e2e-ws-theme');
    const fileName = await writeLocalDAG(dagName, spec());
    try {
      await waitForDAGAvailable(request, token, fileName);
      const runId = await startDAG(request, token, fileName);
      await waitForRunStatus(request, token, dagName, runId, ['succeeded']);
      await use({ fileName, runId });
    } finally {
      await request.delete(
        `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=local`,
        { headers: { Authorization: `Bearer ${token}` } }
      );
    }
  },
});

type Route = 'latest' | 'pinned';
const ROUTES: readonly Route[] = ['latest', 'pinned'];

/** The path of a route; the pinned route names the run in ?run. */
function routePath({ fileName, runId }: RanDAG, route: Route): string {
  const base = `/dags/${encodeURIComponent(fileName)}`;
  return route === 'latest' ? base : `${base}?run=${runId}`;
}

/** Sets the theme and the language before each page load. */
async function usePreferences(
  page: Page,
  theme: (typeof THEMES)[number] | 'system',
  locale: (typeof LOCALES)[number]
): Promise<void> {
  await page.addInitScript(
    ([t, l]) => {
      try {
        const raw = window.localStorage.getItem('user_preferences');
        const saved = raw ? JSON.parse(raw) : {};
        window.localStorage.setItem(
          'user_preferences',
          JSON.stringify({ ...saved, theme: t, locale: l })
        );
      } catch {
        // The theme and language checks then fail.
      }
    },
    [theme, locale] as const
  );
}

/**
 * What the bar shows for the run: the run chip or, in a narrow bar, the
 * status dot of the run.
 */
function runState(page: Page) {
  return page
    .locator(
      '[data-testid="ww-run-chip"][role="group"], [data-testid="ww-bar-status-dot"]'
    )
    .filter({ visible: true })
    .first();
}

test.describe('workspace theme and layout', () => {
  // Each test sets its own viewports; one project is enough.
  test.skip(
    ({ viewport }) => (viewport?.width ?? 0) < 768,
    'runs once, in the desktop project'
  );

  for (const theme of THEMES) {
    for (const locale of LOCALES) {
      test(`fits the bar in ${theme} and ${locale}`, async ({
        page,
        ranDAG,
        gotoWorkspace,
      }) => {
        test.setTimeout(180_000);
        // Sign in with the English form, then switch the preferences.
        await signIn(page);
        await usePreferences(page, theme, locale);

        for (const viewport of VIEWPORTS) {
          await page.setViewportSize(viewport);
          for (const route of ROUTES) {
            await gotoWorkspace(page, routePath(ranDAG, route));
            await expect(page.getByTestId('ww-bar')).toBeVisible({
              timeout: 30_000,
            });
            const html = page.locator('html');
            await expect(html).toHaveAttribute('lang', locale);
            if (theme === 'dark') {
              await expect(html).toHaveClass(/\bdark\b/);
            } else {
              await expect(html).not.toHaveClass(/\bdark\b/);
            }
            await expect(runState(page)).toBeVisible({
              timeout: 30_000,
            });

            const where = `${route} ${viewport.width}x${viewport.height} ${theme} ${locale}`;
            await expect(async () => {
              const report = await inspectBar(page);
              expect(report.pageScrollsX, `page scrolls (${where})`).toBe(
                false
              );
              expect(report.bar, `bar (${where})`).not.toBeNull();
              if (viewport.width >= 768) {
                expect(report.bar!.height, `bar height (${where})`).toBeCloseTo(
                  BAR_HEIGHT,
                  0
                );
                expect(report.outside, `second row (${where})`).toEqual([]);
              }
              if (viewport.width >= 1024) {
                expect(report.truncated, `truncated (${where})`).toEqual([]);
              }
              expect(report.lowContrast, `contrast (${where})`).toEqual([]);
            }).toPass({ timeout: 10_000 });

            if (viewport.width <= NARROW_BAR_MAX) {
              await expectNameInTitle(page, where);
            }

            await checkEditButtons(page, where);
          }
        }
      });
    }
  }

  // 'system' follows the colour scheme of the operating system: the bar and
  // the graph change with it, and the step text stays readable.
  test('follows the system theme in the bar and the graph', async ({
    page,
    ranDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    await signIn(page);
    await usePreferences(page, 'system', 'en');
    await page.setViewportSize({ width: 1440, height: 900 });
    const node =
      '[data-testid="ww-canvas"] [data-testid="ve-canvas-node-steps[0]"]';

    for (const colorScheme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme });
      await gotoWorkspace(page, routePath(ranDAG, 'latest'));
      await expect(page.locator(node)).toBeVisible({ timeout: 30_000 });
      const html = page.locator('html');
      if (colorScheme === 'dark') {
        await expect(html).toHaveClass(/\bdark\b/);
      } else {
        await expect(html).not.toHaveClass(/\bdark\b/);
      }
      await expect(async () => {
        for (const rootSelector of ['[data-testid="ww-bar"]', node]) {
          const report = await page.evaluate(inspect, {
            rootSelector,
            minContrast: MIN_CONTRAST,
          });
          const where = `${rootSelector} system ${colorScheme}`;
          expect(report.lowContrast, `contrast (${where})`).toEqual([]);
          const lum = report.backdropLuminance ?? -1;
          if (colorScheme === 'dark') {
            expect(lum, `dark backdrop (${where})`).toBeLessThan(0.2);
          } else {
            expect(lum, `light backdrop (${where})`).toBeGreaterThan(0.6);
          }
        }
      }).toPass({ timeout: 10_000 });
    }
  });

  // The YAML panel sits in the graph zone, under the bar; it must not cover
  // the bar, its Run controls or its Rename and Delete buttons.
  test('keeps the bar above the YAML panel', async ({
    page,
    ranDAG,
    gotoWorkspace,
  }) => {
    test.setTimeout(120_000);
    await signIn(page);
    await page.setViewportSize({ width: 1440, height: 900 });
    await gotoWorkspace(page, routePath(ranDAG, 'latest'));
    // The YAML button opens the whole file; no step is selected.
    await page
      .getByTestId('ve-toolbar')
      .getByRole('button', { name: 'YAML', exact: true })
      .click();
    const ndv = page.getByTestId('ndv');
    await expect(ndv).toHaveAttribute('data-state', 'docked', {
      timeout: 30_000,
    });
    await expect(runState(page)).toBeVisible({ timeout: 30_000 });

    await expect(async () => {
      const covered = await page.evaluate(() => {
        const bar = document.querySelector('[data-testid="ww-bar"]')!;
        const out: string[] = [];
        for (const control of Array.from(bar.querySelectorAll('a, button'))) {
          if (control.closest('[data-testid="ww-run-chip"]')) continue;
          // A disabled control takes no pointer event, so no hit test fits it:
          // the caret of the draft menu is disabled while the draft is clean.
          if (control instanceof HTMLButtonElement && control.disabled) {
            continue;
          }
          const rect = control.getBoundingClientRect();
          if (rect.width < 2 || rect.height < 2) continue;
          const hit = document.elementFromPoint(
            rect.left + rect.width / 2,
            rect.top + rect.height / 2
          );
          if (!hit || !control.contains(hit)) {
            out.push(
              control.getAttribute('aria-label') ?? control.textContent ?? ''
            );
          }
        }
        return out;
      });
      expect(covered).toEqual([]);
      const panel = await ndv.boundingBox();
      const bar = await page.getByTestId('ww-bar').boundingBox();
      expect(panel!.y).toBeGreaterThanOrEqual(bar!.y + bar!.height - 0.5);
    }).toPass({ timeout: 10_000 });

    await expect(
      page.getByTestId('ww-bar').getByRole('button', { name: 'Rename' })
    ).toBeVisible();
  });
});
