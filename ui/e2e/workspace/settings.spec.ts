// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Page } from '@playwright/test';
import { expect, signIn, test } from './fixtures';

// The old section paths now open a tab of the Workflow panel. The old
// settings path held the runtime profile: it opens the Overview tab.
const PANEL_PATHS = [
  { path: 'notifications', wtab: 'notifications', label: 'Notifications' },
  { path: 'incidents', wtab: 'incidents', label: 'Incidents' },
  { path: 'wiki', wtab: 'wiki', label: 'Wiki' },
] as const;

/** Waits until the page is at the path and has these query values. */
async function expectAt(
  page: Page,
  pathname: string,
  query: Record<string, string>
): Promise<void> {
  await expect
    .poll(() => {
      const url = new URL(page.url());
      const values = Object.keys(query).map(
        (key) => `${key}=${url.searchParams.get(key)}`
      );
      return [url.pathname, ...values];
    })
    .toEqual([
      pathname,
      ...Object.entries(query).map(([key, value]) => `${key}=${value}`),
    ]);
}

test.describe('workspace runtime profile', () => {
  test.beforeEach(async ({ page }) => {
    await signIn(page);
  });

  // parity:settings-default-profile parity:wiki-list
  // parity:shell-wiki-tab-hidden-on-page
  test('opens the panel that the old section path names', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    const { fileName } = await fixtureDAG('visual-params.yaml', 'e2e-ws-set');
    const base = `/dags/${encodeURIComponent(fileName)}`;
    const tabs = page.getByTestId('ww-workflow-tabs');

    await gotoWorkspace(page, `${base}/settings?remoteNode=local`);
    // The editor opens the panel and takes ?panel out of the URL.
    await expectAt(page, base, { remoteNode: 'local', panel: 'null' });
    await expect(
      tabs.getByRole('tab', { name: 'Overview', exact: true })
    ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
    await expect(page.getByTestId('ww-runtime-profile')).toBeVisible();

    for (const { path, wtab, label } of PANEL_PATHS) {
      await gotoWorkspace(page, `${base}/${path}?remoteNode=local`);
      await expectAt(page, base, { remoteNode: 'local', wtab });
      await expect(
        tabs.getByRole('tab', { name: label, exact: true })
      ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
    }
  });

  // The Overview tab holds the runtime profile; the Profile tab and the table
  // of steps are gone, and the settings dialog with them.
  test('shows the runtime profile in the Overview tab', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    const { fileName } = await fixtureDAG('visual-params.yaml', 'e2e-ws-set');
    const base = `/dags/${encodeURIComponent(fileName)}`;

    await gotoWorkspace(page, `${base}?panel=workflow&remoteNode=local`);
    const profile = page.getByTestId('ww-runtime-profile');
    await expect(profile).toBeVisible({ timeout: 30_000 });
    await expect(profile.getByTestId('ww-rp-applies')).toBeVisible();
    await expect(profile.getByTestId('ww-rp-name')).toBeVisible();
    await expect(
      profile.getByText('Default profile', { exact: true })
    ).toBeVisible();

    const tabs = page.getByTestId('ww-workflow-tabs');
    // A narrow strip shows the tabs as icons: their names stay.
    const names = ['Overview', 'Wiki', 'Incidents', 'Notifications'];
    await expect(tabs.getByRole('tab')).toHaveCount(names.length);
    for (const [index, name] of names.entries()) {
      await expect(tabs.getByRole('tab').nth(index)).toHaveAccessibleName(name);
    }

    // The old Profile tab link opens the Overview.
    await gotoWorkspace(
      page,
      `${base}?panel=workflow&wtab=profile&remoteNode=local`
    );
    await expect(
      tabs.getByRole('tab', { name: 'Overview', exact: true })
    ).toHaveAttribute('aria-selected', 'true', { timeout: 30_000 });
    await expect(page.getByTestId('ww-runtime-profile')).toBeVisible();
  });

  // The draft lives in the Notifications tab of the Workflow panel: a tab
  // change and the close of the panel ask first.
  test('asks before it leaves a dirty notifications draft', async ({
    page,
    fixtureDAG,
    gotoWorkspace,
  }) => {
    const { fileName } = await fixtureDAG('visual-params.yaml', 'e2e-ws-set');
    const base = `/dags/${encodeURIComponent(fileName)}`;

    await gotoWorkspace(page, `${base}/notifications?remoteNode=local`);
    const panel = page.getByTestId('ve-workflow');
    const tabs = panel.getByTestId('ww-workflow-tabs');
    const notifications = tabs.getByRole('tab', {
      name: 'Notifications',
      exact: true,
    });
    await expect(notifications).toHaveAttribute('aria-selected', 'true', {
      timeout: 30_000,
    });
    // Configuring an override makes a draft that is not saved.
    await panel
      .getByRole('button', { name: 'Configure workflow override' })
      .click({ timeout: 30_000 });

    const confirm = page.getByRole('dialog', { name: 'Leave without saving?' });

    // A tab change.
    await tabs.getByRole('tab', { name: 'Wiki', exact: true }).click();
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Cancel' }).click();
    await expect(confirm).toBeHidden();
    await expect(notifications).toHaveAttribute('aria-selected', 'true');
    await expectAt(page, base, { remoteNode: 'local', wtab: 'notifications' });

    // The close of the panel.
    await panel.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Cancel' }).click();
    await expect(confirm).toBeHidden();
    await expect(panel).toBeVisible();

    await panel.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Leave', exact: true }).click();
    await expect(panel).toBeHidden();
  });
});
