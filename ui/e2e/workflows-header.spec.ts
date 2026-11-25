// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, type Locator, test } from '@playwright/test';
import { loadStack, loginViaUI } from './helpers/e2e';

type Box = { x: number; y: number; width: number; height: number };

async function boxOf(locator: Locator): Promise<Box> {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  return box!;
}

function overlaps(a: Box, b: Box): boolean {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

// On a phone the header of both modes wraps instead of squeezing: the mode
// switch keeps both buttons whole, and the count stays clear of New.
test('fits the Workflows header on a phone screen', async ({ page }) => {
  const stack = await loadStack();
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await page.setViewportSize({ width: 375, height: 812 });

  for (const path of ['/dags', '/dag-runs']) {
    await page.goto(path);
    const count = page.getByTestId('workflow-count');
    await expect(count).toBeVisible({ timeout: 30_000 });

    const modeSwitch = page.getByRole('group', { name: 'Show', exact: true });
    const switchBox = await boxOf(modeSwitch);
    for (const name of ['Workflows', 'Runs']) {
      const button = await boxOf(
        modeSwitch.getByRole('button', { name, exact: true })
      );
      expect(button.x + button.width).toBeLessThanOrEqual(
        switchBox.x + switchBox.width
      );
    }

    const create = page.getByRole('button', { name: 'Create new DAG' });
    expect(overlaps(await boxOf(count), await boxOf(create))).toBe(false);
  }
});
