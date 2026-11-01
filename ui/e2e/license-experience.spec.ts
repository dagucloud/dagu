// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, test, type Page } from '@playwright/test';
import { loadStack, loginViaUI } from './helpers/e2e';
import type { LicenseStatus } from '../src/contexts/ConfigContext';
import {
  LicenseMonitoringLevel,
  LicenseStatusResponseConnectedVia,
} from '../src/api/v1/schema';
import type { LicenseMonitoring } from '../src/hooks/useLicenseMonitoring';

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

// settled waits for open and close animations, so screenshots show the end
// state.
async function settled(page: Page) {
  await page.waitForFunction(() =>
    document.getAnimations().every((a) => a.playState !== 'running')
  );
}

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
    .filter({ hasText: /^(Community|Team plan)$/ });
  await expect(badge).toHaveText('Community');
  const monitoringLink = sidebar.getByRole('link', {
    name: 'External Monitoring',
  });
  await expect(monitoringLink).toHaveAttribute('href', '/license#monitoring');
  await monitoringLink.click();
  await expect(
    page.getByRole('region', { name: 'Monitor this server for free' })
  ).toBeInViewport();
  await badge.click();
  await expect(
    page.getByRole('heading', { name: 'Plan & features' })
  ).toBeVisible();
  // A Community server starts the trial by connecting with it chosen.
  await expect(
    page.getByRole('checkbox', {
      name: /Also try paid features free for 14 days/,
    })
  ).not.toBeChecked();
  await expect(
    page.getByRole('link', { name: 'Start free trial' })
  ).toHaveCount(0);
  await page.getByRole('button', { name: 'Use a license key' }).click();
  await page
    .getByLabel('License key', { exact: true })
    .fill('DAGU-E2E-EXAMPLE');
  await page.getByRole('button', { name: 'Activate', exact: true }).click();
  await expect(badge).toHaveText('Team plan');
  await expect(page.getByText('License activated.')).toBeVisible();
  await expect(page.getByText('Included', { exact: true })).toHaveCount(5);
  await expect(page.getByRole('link', { name: 'Setup guide' })).toHaveAttribute(
    'href',
    'https://docs.dagu.sh/server-admin/authentication/oidc'
  );
  await page.screenshot({
    path: testInfo.outputPath('license-active-desktop.png'),
    fullPage: true,
  });
  await sidebar.getByRole('combobox', { name: 'Theme' }).click();
  await page.getByRole('option', { name: 'Dark' }).click();
  await expect(page.locator('html')).toHaveClass(/dark/);
  await page.screenshot({
    path: testInfo.outputPath('license-active-dark.png'),
    fullPage: true,
  });
  await sidebar.getByRole('combobox', { name: 'Theme' }).click();
  await page.getByRole('option', { name: 'Light' }).click();
  await badge.focus();
  await expect(page.getByRole('tooltip')).toContainText('Team · Active');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('tooltip')).toBeHidden();
  // Collapsed, the sidebar shows the license only when it needs attention.
  await sidebar.getByRole('button', { name: 'Collapse sidebar' }).click();
  await expect(badge).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath('license-active-collapsed.png'),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileBadge = page
    .locator('header')
    .getByRole('link', { name: 'Plan & features' });
  await expect(mobileBadge).toHaveText('Team plan');
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
  ).toHaveAttribute('href', '/license?trial=1#monitoring');
  await expect(
    page.getByRole('link', { name: 'Activate a key' })
  ).toHaveAttribute('href', '/license#activate');
});

test('connects a community server with the trial through Dagu Console', async ({
  page,
  context,
}, testInfo) => {
  const stack = await loadStack();
  const connectUrl = 'https://console.dagu.test/servers/connect?code=abc';
  let license = community;
  let approved = false;
  let monitoring: LicenseMonitoring = {
    level: LicenseMonitoringLevel.off,
    configured: false,
    chosen: false,
    noticeDismissed: false,
  };
  await context.route('https://console.dagu.test/**', (route) =>
    route.fulfill({ contentType: 'text/html', body: '<h1>Approve</h1>' })
  );
  // Keep license mutations inside this browser context; the shared stack is unchanged.
  await page.route('**/api/v1/license/**', async (route) => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get('remoteNode')).toBe('local');
    if (url.pathname.endsWith('/license/monitoring')) {
      await route.fulfill({ json: monitoring });
      return;
    }
    if (url.pathname.endsWith('/connect')) {
      if (route.request().method() === 'POST') {
        const { report } = route.request().postDataJSON();
        // The server saves the level chosen when connecting, and reports
        // once connected.
        monitoring = {
          ...monitoring,
          level: report,
          chosen: true,
          lastReportAt: new Date().toISOString(),
          lastReport: JSON.stringify({
            protocol: 1,
            license_id: 'lic-1',
            server_id: 'srv-123',
            heartbeat_secret: '[redacted]',
            health: { version: '2.19.0', os: 'linux', arch: 'amd64' },
          }),
        };
      }
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

  const offer = page.getByRole('region', {
    name: 'Monitor this server for free',
  });
  await offer
    .getByRole('checkbox', { name: /Also try paid features free for 14 days/ })
    .click();
  await offer.screenshot({
    path: testInfo.outputPath('monitoring-offer-trial.png'),
  });
  const popup = page.waitForEvent('popup');
  await offer.getByRole('button', { name: 'Connect and start trial' }).click();
  // The approval page offers the trial first.
  await expect(await popup).toHaveURL(connectUrl + '&trial=1');
  await expect(page.getByText('ABCD1234')).toBeVisible();

  license = {
    ...team,
    plan: 'trial',
    connectedVia: LicenseStatusResponseConnectedVia.console,
    serverName: 'build-01',
    workspace: 'acme',
    serverId: 'srv-123',
    lastCheckIn: new Date().toISOString(),
    consoleUrl: 'https://console.dagu.test/servers?server=srv-123',
  };
  approved = true;
  await expect(page.getByText('Connected to acme.')).toBeVisible();
  // The level was chosen when connecting, so nothing asks again.
  await expect(page.getByRole('dialog')).toHaveCount(0);

  const panel = page.getByRole('region', { name: 'Overview' });
  await expect(panel).toContainText('build-01');
  await expect(panel).toContainText('acme');
  await expect(
    page.getByRole('link', { name: 'Manage in Dagu Console' })
  ).toHaveAttribute('href', 'https://console.dagu.test/servers?server=srv-123');

  const card = page.getByRole('region', { name: 'Monitoring' });
  await expect(card).toContainText('Health, run status, and error messages');
  await expect(card).toContainText('Last report');
  await card.screenshot({ path: testInfo.outputPath('monitoring-card.png') });
  await card.getByRole('button', { name: 'View last report' }).click();
  const report = page.getByRole('dialog', { name: 'Last report' });
  await expect(report).toContainText('"heartbeat_secret": "[redacted]"');
  await settled(page);
  await page.screenshot({
    path: testInfo.outputPath('monitoring-last-report.png'),
    fullPage: true,
  });
});

test('starts monitoring when a server connects from the free offer', async ({
  page,
  context,
}, testInfo) => {
  const stack = await loadStack();
  const connectUrl =
    'https://console.dagu.test/servers/connect?code=abc&report=runs';
  let license = community;
  let approved = false;
  let connectBody: unknown;
  let monitoring: LicenseMonitoring = {
    level: LicenseMonitoringLevel.off,
    configured: false,
    chosen: false,
    noticeDismissed: false,
  };
  await context.route('https://console.dagu.test/**', (route) =>
    route.fulfill({ contentType: 'text/html', body: '<h1>Approve</h1>' })
  );
  // Keep license mutations inside this browser context; the shared stack is unchanged.
  await page.route('**/api/v1/license/**', async (route) => {
    const url = new URL(route.request().url());
    expect(url.searchParams.get('remoteNode')).toBe('local');
    if (url.pathname.endsWith('/license/monitoring')) {
      await route.fulfill({ json: monitoring });
      return;
    }
    if (url.pathname.endsWith('/connect')) {
      if (route.request().method() === 'POST') {
        connectBody = route.request().postDataJSON();
        // The server saves the level chosen when connecting.
        const { report } = connectBody as { report: LicenseMonitoringLevel };
        monitoring = { ...monitoring, level: report, chosen: true };
      }
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
  await page.goto('/license#monitoring');

  const offer = page.getByRole('region', {
    name: 'Monitor this server for free',
  });
  await expect(
    offer.getByRole('radio', {
      name: 'Health, run status, and error messages (recommended)',
    })
  ).toBeChecked();
  await offer.screenshot({
    path: testInfo.outputPath('monitoring-offer-desktop.png'),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(offer).toContainText(
    'Reporting starts as soon as the server connects.'
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth
    )
  ).toBe(true);
  await offer.screenshot({
    path: testInfo.outputPath('monitoring-offer-mobile.png'),
  });

  const popup = page.waitForEvent('popup');
  await offer.getByRole('button', { name: 'Connect for free' }).click();
  await expect(await popup).toHaveURL(connectUrl);
  expect(connectBody).toEqual({ report: 'errors' });
  await expect(page.getByText('ABCD1234')).toBeVisible();

  license = {
    ...community,
    valid: true,
    community: false,
    plan: 'free',
    source: 'file',
    connectedVia: LicenseStatusResponseConnectedVia.console,
    workspace: 'acme',
    serverId: 'srv-123',
    lastCheckIn: new Date().toISOString(),
  };
  approved = true;
  await expect(page.getByText('Connected to acme.')).toBeVisible();
  const card = page.getByRole('region', { name: 'Monitoring' });
  await expect(card).toContainText('Health, run status, and error messages');
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('shows a license that needs attention in the sidebar and on the page', async ({
  page,
}, testInfo) => {
  const stack = await loadStack();
  // Ends an hour inside two days, so the count does not depend on the time.
  const expiry = new Date(Date.now() + 2 * 86_400_000 - 3_600_000);
  await page.route('**/api/v1/license/status*', (route) =>
    route.fulfill({
      json: { ...team, plan: 'trial', expiry: expiry.toISOString() },
    })
  );
  await loginViaUI(page, stack.auth.adminUsername, stack.auth.adminPassword);
  const sidebar = page.getByTestId('app-sidebar');
  const line = sidebar
    .getByRole('link', { name: 'Plan & features', exact: true })
    .filter({ hasText: /^Trial plan/ });
  await expect(line).toHaveText('Trial plan · Expires in 2 days');
  await line.click();
  await expect(
    page.getByRole('status').filter({ hasText: 'License expires in 2 days' })
  ).toContainText('View plans');
  await page.screenshot({
    path: testInfo.outputPath('license-trial-ending.png'),
    fullPage: true,
  });

  await sidebar.getByRole('button', { name: 'Collapse sidebar' }).click();
  const icon = sidebar.locator('a[href="/license"]').filter({ hasText: /^$/ });
  await expect(icon).toHaveCount(1);
  await icon.focus();
  await expect(page.getByRole('tooltip')).toContainText(
    'Trial · Expires in 2 days'
  );
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
  const sidebar = page.getByTestId('app-sidebar');
  await expect(
    sidebar.getByRole('link', { name: 'External Monitoring' })
  ).toHaveCount(0);
  await sidebar.getByRole('button', { name: 'Plan & features' }).click();
  await expect(page.getByRole('menu')).toContainText('Community');
  await expect(page.getByRole('menu')).toContainText(guidance);
});
