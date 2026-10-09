// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test } from '@playwright/test';
import { loadStack, loginViaUI } from './helpers/e2e';
import type { LicenseStatus } from '../src/contexts/ConfigContext';
import { LicenseStatusResponseConnectedVia } from '../src/api/v1/schema';

const community: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};
const team: LicenseStatus = {
  ...community,
  valid: true,
  community: false,
  plan: 'team',
  source: 'file',
  features: ['audit', 'rbac', 'sso'],
};

test('shows activation, benefits, and deactivation on desktop and mobile', async ({
  page,
}, testInfo) => {
  const stack = await loadStack();
  let license = community;
  // Keep license mutations inside this browser context; the shared stack is unchanged.
  await page.route('**/api/v1/license/**', async (route) => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get('remoteNode')).toBe('local');
    if (url.pathname.endsWith('/activate')) {
      expect(route.request().postDataJSON()).toEqual({
        key: 'DAGU-E2E-EXAMPLE',
      });
      license = team;
    } else if (url.pathname.endsWith('/deactivate')) {
      license = community;
    }
    await route.fulfill({ json: license });
  });
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  const sidebar = page.getByTestId('app-sidebar');
  const badge = sidebar
    .getByRole('link', { name: 'Plan & features', exact: true })
    .filter({ hasText: /^(Community|Team)$/ });
  await expect(badge).toHaveText('Community');
  await badge.click();
  await expect(
    page.getByRole('heading', { name: 'Plan & features' })
  ).toBeVisible();
  const trialURL = new URL(
    (await page
      .getByRole('link', { name: 'Start free trial' })
      .getAttribute('href')) ?? ''
  );
  expect(trialURL.pathname).toBe('/signup');
  expect(trialURL.searchParams.get('content')).toBe('plan-page');
  await page
    .getByLabel('License key', { exact: true })
    .fill('DAGU-E2E-EXAMPLE');
  await page.getByRole('button', { name: 'Activate', exact: true }).click();
  await expect(badge).toHaveText('Team');
  await expect(
    page.getByText('Team activated. Explore your included features below.')
  ).toBeVisible();
  await expect(page.getByText('Included', { exact: true })).toHaveCount(5);
  await expect(page.getByRole('link', { name: 'Setup guide' })).toHaveAttribute(
    'href',
    'https://docs.dagu.sh/server-admin/authentication/oidc'
  );
  await page.screenshot({
    path: testInfo.outputPath('license-active-desktop.png'),
    fullPage: true,
  });
  await sidebar.getByRole('button', { name: 'Dark Mode' }).click();
  await expect(page.locator('html')).toHaveClass(/dark/);
  await page.screenshot({
    path: testInfo.outputPath('license-active-dark.png'),
    fullPage: true,
  });
  await sidebar.getByRole('button', { name: 'Light Mode' }).click();
  await badge.focus();
  await expect(page.getByRole('tooltip')).toContainText('Team · Active');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('tooltip')).toBeHidden();
  await sidebar.getByRole('button', { name: 'Collapse sidebar' }).click();
  await expect(badge).toBeVisible();
  await badge.focus();
  await expect(page.getByRole('tooltip')).toContainText('Team · Active');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('tooltip')).toBeHidden();
  await page.screenshot({
    path: testInfo.outputPath('license-active-collapsed.png'),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileBadge = page
    .locator('header')
    .getByRole('link', { name: 'Plan & features' });
  await expect(mobileBadge).toHaveText('Team');
  await mobileBadge.click();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('tooltip')).toBeHidden();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth
    )
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath('license-active-mobile.png'),
    fullPage: true,
  });
  await page
    .getByRole('button', { name: 'Deactivate License', exact: true })
    .click();
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Deactivate', exact: true })
    .click();
  await expect(mobileBadge).toHaveText('Community');
  await page.goto('/incidents');
  await expect(
    page.getByRole('heading', { name: 'Incident routing' })
  ).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Start free trial' })
  ).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Activate a key' })
  ).toHaveAttribute('href', '/license#activate');
});

test('connects a community server through Dagu Console', async ({
  page,
  context,
}) => {
  const stack = await loadStack();
  const connectUrl = 'https://console.dagu.test/servers/connect?code=abc';
  let license = community;
  let approved = false;
  await context.route('https://console.dagu.test/**', (route) =>
    route.fulfill({ contentType: 'text/html', body: '<h1>Approve</h1>' })
  );
  // Keep license mutations inside this browser context; the shared stack is unchanged.
  await page.route('**/api/v1/license/**', async (route) => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get('remoteNode')).toBe('local');
    if (url.pathname.endsWith('/connect')) {
      await route.fulfill({
        json: approved
          ? { state: 'granted' }
          : {
              state: 'pending',
              connectUrl,
              code: 'ABCD1234',
              expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
            },
      });
      return;
    }
    await route.fulfill({ json: license });
  });
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await page.goto('/license');

  const popup = page.waitForEvent('popup');
  await page.getByRole('button', { name: 'Connect to Dagu Console' }).click();
  await expect(await popup).toHaveURL(connectUrl);
  await expect(page.getByText('ABCD1234')).toBeVisible();

  license = {
    ...team,
    connectedVia: LicenseStatusResponseConnectedVia.console,
    serverName: 'build-01',
    workspace: 'acme',
    serverId: 'srv-123',
    lastCheckIn: new Date().toISOString(),
    consoleUrl: 'https://console.dagu.test/servers?server=srv-123',
  };
  approved = true;
  await expect(
    page.getByText('Team connected. Explore your included features below.')
  ).toBeVisible();
  const panel = page.getByRole('region', { name: 'This server' });
  await expect(panel).toContainText('build-01');
  await expect(panel).toContainText('acme');
  await expect(
    panel.getByRole('link', { name: 'Manage in Dagu Console' })
  ).toHaveAttribute('href', 'https://console.dagu.test/servers?server=srv-123');
});

test('gives non-admins status and administrator guidance', async ({ page }) => {
  const stack = await loadStack();
  await page.route('**/api/v1/license/status*', (route) =>
    route.fulfill({ json: community })
  );
  await page.route('**/api/v1/auth/me', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    await route.fulfill({
      json: { ...body, user: { ...body.user, role: 'developer' } },
    });
  });
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  await page.goto('/incidents');
  const guidance =
    'Ask your administrator to manage the license and available features.';
  await expect(page.getByText(guidance)).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Start free trial' })
  ).toHaveCount(0);
  await page
    .getByTestId('app-sidebar')
    .getByRole('button', { name: 'Plan & features' })
    .click();
  await expect(page.getByRole('menu')).toContainText('Community');
  await expect(page.getByRole('menu')).toContainText(guidance);
});
