// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, signIn, test } from './fixtures';

test.describe('workspace harness', () => {
  // Proves the projects, the fixtures and the stack: the workflow page is
  // the workspace, with its bar, and it has no classic tab row.
  test('opens a fixture DAG on the workspace', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await signIn(page);
    const { dagName, fileName } = await fixtureDAG('visual-params.yaml');

    await gotoWorkspace(page, `/dags/${encodeURIComponent(fileName)}`);

    await expect(page.getByTestId('ww-bar')).toBeVisible({ timeout: 30_000 });
    await expect(
      page.locator(`a[href*="/dags/${dagName}/history"]`)
    ).toHaveCount(0);
  });

  // The old switch between the two pages is gone: ?ui has no effect.
  test('loads the workspace for an old ?ui link', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    await signIn(page);
    const { fileName } = await fixtureDAG('visual-params.yaml');

    await gotoWorkspace(
      page,
      `/dags/${encodeURIComponent(fileName)}?ui=classic`
    );

    await expect(page.getByTestId('ww-bar')).toBeVisible({ timeout: 30_000 });
    // The bar has no entry for the classic page.
    await expect(page.getByRole('link', { name: 'Classic page' })).toHaveCount(
      0
    );
    await expect(
      page.getByTestId('ww-bar').getByRole('button', { name: 'Rename' })
    ).toBeVisible();
  });
});
