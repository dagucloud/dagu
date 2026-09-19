// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import {
  enqueueDAG,
  loadStack,
  loginViaAPI,
  loginViaUI,
  uniqueName,
  waitForDAGAvailable,
  waitForRunStatus,
  writeLocalDAG,
} from './helpers/e2e';

test('browses artifacts and returns from previews with the keyboard', async ({
  page,
  request,
}, testInfo) => {
  const stack = await loadStack();
  const token = await loginViaAPI(
    request,
    stack.auth.adminUsername,
    stack.auth.adminPassword
  );
  const name = uniqueName('e2e-artifacts');
  const fileName = await writeLocalDAG(
    name,
    `
name: ${name}
type: graph
steps:
  - id: first
    action: artifact.write
    with:
      path: a/a.txt
      content: First artifact
  - id: second
    action: artifact.write
    with:
      path: a/b.txt
      content: |
        ${Array.from({ length: 100 }, (_, i) => `Artifact line ${i}`).join('\n        ')}
  - id: html
    action: artifact.write
    with:
      path: z/report.html
      content: '<h1>Artifact report</h1><button>Embedded control</button>'
`
  );
  await waitForDAGAvailable(request, token, fileName);
  const olderRun = await enqueueDAG(request, token, fileName);
  await waitForRunStatus(request, token, name, olderRun, ['succeeded']);
  const newerRun = await enqueueDAG(request, token, fileName);
  await waitForRunStatus(request, token, name, newerRun, ['succeeded']);

  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await page.goto(`/artifacts?name=${encodeURIComponent(name)}`);
  const tree = page.getByRole('tree', { name: 'Artifacts', exact: true });
  const first = tree.getByRole('treeitem', { name: 'a.txt', exact: true });
  const second = tree.getByRole('treeitem', { name: 'b.txt', exact: true });
  await expect(first).toBeFocused();
  await page.keyboard.press('j');
  await expect(second).toBeFocused();
  await page.keyboard.press('Enter');
  const preview = page.getByRole('region', { name: 'b.txt', exact: true });
  await expect(preview).toBeFocused();
  await expect(preview).toContainText('Artifact line 99');
  await page.keyboard.press('ArrowDown');
  await expect
    .poll(() => preview.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(0);
  await expect(second).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Escape');
  await expect(second).toBeFocused();

  await page.keyboard.press('j');
  const folder = tree.getByRole('treeitem', { name: 'z', exact: true });
  await expect(folder).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  const html = tree.getByRole('treeitem', { name: 'report.html', exact: true });
  await expect(html).toBeFocused();
  await page.keyboard.press('Enter');
  const htmlPreview = page.getByRole('region', {
    name: 'report.html',
    exact: true,
  });
  await expect(htmlPreview).toBeFocused();
  await expect(
    page
      .frameLocator('iframe[title="HTML artifact preview"]')
      .getByRole('heading', { name: 'Artifact report' })
  ).toBeVisible();
  await page.keyboard.press('Tab');
  const embeddedControl = page
    .frameLocator('iframe[title="HTML artifact preview"]')
    .getByRole('button', { name: 'Embedded control' });
  await expect(embeddedControl).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(embeddedControl).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(
    page.getByRole('button', { name: 'Download', exact: true })
  ).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(html).toBeFocused();
  await page.keyboard.press('j');
  await expect(
    tree.getByRole('treeitem', { name, exact: true }).nth(1)
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect(first.nth(1)).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(
    page.getByRole('link', { name: 'Open DAG run', exact: true })
  ).toBeFocused();
  await expect(
    page.getByRole('link', { name: 'Open DAG run', exact: true })
  ).toHaveAttribute('href', `/dag-runs/${name}/${olderRun}`);
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('/');
  const filter = page.getByPlaceholder('Filter by file name...');
  await expect(filter).toBeFocused();
  await filter.pressSequentially('jk');
  await expect(filter).toHaveValue('jk');
  await page.keyboard.press('Escape');
  await expect(first.nth(1)).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath('artifact-keyboard.png') });

  await page.goto(`/dag-runs/${name}/${newerRun}`);
  await page.getByRole('button', { name: 'Artifacts', exact: true }).click();
  const runFile = page.getByRole('treeitem', { name: 'a.txt', exact: true });
  await runFile.focus();
  await page.keyboard.press('j');
  await expect(
    page.getByRole('treeitem', { name: 'b.txt', exact: true })
  ).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('region', { name: 'b.txt', exact: true })
  ).toBeFocused();
  await page.keyboard.press('Escape');
  await page.keyboard.press('k');
  await expect(runFile).toBeFocused();
});
