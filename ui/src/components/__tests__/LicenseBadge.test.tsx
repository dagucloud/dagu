// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import {
  ConfigContext,
  type Config,
  type LicenseStatus,
} from '@/contexts/ConfigContext';
import { LicenseBadge } from '../LicenseBadge';
import { LicenseFeaturePrompt } from '../LicenseFeaturePrompt';
import { LicenseBanner } from '../LicenseBanner';

const { isAdmin } = vi.hoisted(() => ({ isAdmin: vi.fn(() => true) }));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: isAdmin }));
const paid: LicenseStatus = {
  valid: true,
  plan: 'team',
  expiry: '',
  features: ['audit'],
  gracePeriod: false,
  community: false,
  source: 'file',
  warningCode: '',
};
function renderLicense(overrides: Partial<LicenseStatus> = {}) {
  const license = { ...paid, ...overrides };
  return render(
    <MemoryRouter>
      <ConfigContext.Provider value={{ license } as Config}>
        <LicenseBadge />
        <LicenseFeaturePrompt feature="audit" />
        <LicenseBanner />
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}
beforeEach(() => {
  isAdmin.mockReturnValue(true);
  localStorage.clear();
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date('2026-10-01T00:00:00Z'));
});
afterEach(() => {
  vi.useRealTimers();
});

describe('license presentation', () => {
  it.each([
    [{}, 'Team · Active'],
    [{ plan: 'pro' }, 'Pro · Active'],
    [{ plan: 'enterprise' }, 'Enterprise · Active'],
    [{ plan: 'custom-plan' }, 'Custom-plan · Active'],
    [{ community: true, valid: false }, 'Community'],
    [{ plan: 'trial', expiry: '' }, 'Trial · Active'],
    [{ plan: 'trial', expiry: '2026-10-15T00:00:00Z' }, 'Trial · 14 days left'],
    [{ plan: 'trial', expiry: '2026-10-02T00:00:00Z' }, 'Trial · 1 day left'],
    [
      { plan: 'trial', expiry: '2026-10-01T00:00:00Z' },
      'Trial · Expires today',
    ],
    [{ valid: false, gracePeriod: true }, 'Team · Grace Period'],
    [{ valid: false }, 'Team · Expired'],
    [
      { warningCode: 'MACHINE_LIMIT_EXCEEDED' },
      'Team · License needs attention',
    ],
    [{ community: true, error: 'Invalid token' }, 'License Error'],
  ])('shows accurate license details for %j', async (license, label) => {
    renderLicense(license);
    await userEvent.tab();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(label);
  });

  it('provides keyboard-accessible details and a direct management link', async () => {
    renderLicense({ expiry: '2027-01-01T00:00:00Z' });
    const link = screen.getByRole('link', { name: 'Plan & features' });
    expect(link).toHaveAttribute('href', '/license');
    expect(link).toHaveTextContent(/^Team$/);
    await userEvent.tab();
    const tooltip = await screen.findByRole('tooltip');
    expect(tooltip).toHaveTextContent('Team · Active');
    expect(tooltip).toHaveTextContent('2027-01-01');
  });

  it('provides non-admin details without commercial actions', async () => {
    isAdmin.mockReturnValue(false);
    renderLicense({ valid: false, community: true });
    await userEvent.click(
      screen.getByRole('button', { name: 'Plan & features' })
    );
    expect(
      within(screen.getByRole('menu')).getByText('Community')
    ).toBeVisible();
    expect(
      screen.getAllByText(/Ask your administrator/).length
    ).toBeGreaterThan(0);
    expect(
      screen.queryByRole('link', { name: 'Start free trial' })
    ).not.toBeInTheDocument();
  });

  it('tags trial and comparison links with the feature entry point', () => {
    renderLicense({ community: true, valid: false });
    const link = new URL(
      screen
        .getByRole('link', { name: 'Start free trial' })
        .getAttribute('href')!
    );
    expect(link.origin + link.pathname).toBe('https://console.dagu.sh/signup');
    expect(Object.fromEntries(link.searchParams)).toEqual({
      source: 'dagu-ui',
      medium: 'product',
      content: 'audit',
      deployment: 'self-hosted',
      intent: 'license-trial',
      plan: 'trial',
    });
    expect(screen.getByRole('link', { name: 'View plans' })).toHaveAttribute(
      'href',
      expect.stringContaining('https://dagu.sh/pricing?')
    );
  });

  it('offers renewal when a paid license expires', () => {
    renderLicense({ valid: false });
    expect(screen.getByRole('link', { name: 'Renew license' })).toHaveAttribute(
      'href',
      expect.stringContaining('https://console.dagu.sh/')
    );
  });

  it('keeps new trials calm and warns during the final three days', async () => {
    const view = renderLicense({
      plan: 'trial',
      expiry: '2026-10-15T00:00:00Z',
    });
    expect(
      screen.getByRole('link', { name: 'Plan & features' })
    ).toHaveTextContent(/^Trial$/);
    await userEvent.tab();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      '14 days left'
    );
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    view.unmount();
    renderLicense({ plan: 'trial', expiry: '2026-10-04T00:00:00Z' });
    expect(screen.getByRole('alert')).toHaveTextContent('expires in 3 days');
  });
});
