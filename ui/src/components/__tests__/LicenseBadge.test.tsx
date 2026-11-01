// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { SWRConfig } from 'swr';
import {
  LicenseMonitoringLevel,
  LicenseStatusResponseConnectedVia,
} from '@/api/v1/schema';
import {
  ConfigContext,
  type Config,
  type LicenseStatus,
} from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useClient } from '@/hooks/api';
import type { LicenseMonitoring } from '@/hooks/useLicenseMonitoring';
import { LicenseBadge } from '../LicenseBadge';
import { LicenseFeaturePrompt } from '../LicenseFeaturePrompt';
import { LicenseBanner } from '../LicenseBanner';

vi.mock('@/hooks/api', async () => {
  const { default: useSWR } = await import('swr');
  const useClient = vi.fn();
  return {
    useClient,
    // As in swr-openapi, null params skip the request.
    useQuery: (path: string, params: unknown, options: object) =>
      useSWR(
        params === null ? null : [path, params],
        async () => {
          const result = await useClient().GET(path, params);
          if (result.error) throw result.error;
          return result.data;
        },
        options
      ),
  };
});
const { isAdmin } = vi.hoisted(() => ({ isAdmin: vi.fn(() => true) }));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: isAdmin }));

const now = new Date('2026-10-01T00:00:00Z');
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
const connected: Partial<LicenseStatus> = {
  connectedVia: LicenseStatusResponseConnectedVia.console,
  workspace: 'acme',
  serverName: 'build-01',
  serverId: 'srv-123',
};
const minutesAgo = (minutes: number) =>
  new Date(now.getTime() - minutes * 60_000).toISOString();
const reporting = (lastReportAt?: string): LicenseMonitoring => ({
  level: LicenseMonitoringLevel.runs,
  configured: false,
  chosen: true,
  noticeDismissed: false,
  errorsOffered: false,
  lastReportAt,
});

const get = vi.fn();
function serveMonitoring(monitoring: LicenseMonitoring) {
  get.mockResolvedValue({ data: monitoring });
}

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

function renderBadge(
  overrides: Partial<LicenseStatus> = {},
  { compact = false } = {}
) {
  const license = { ...paid, ...overrides };
  return render(
    <MemoryRouter>
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <ConfigContext.Provider value={{ license } as Config}>
          <LicenseBadge compact={compact} />
        </ConfigContext.Provider>
      </SWRConfig>
    </MemoryRouter>
  );
}

function sidebarLink() {
  return screen.getByRole('link', { name: 'Plan & features' });
}

beforeEach(() => {
  isAdmin.mockReturnValue(true);
  localStorage.clear();
  get.mockReset();
  get.mockReturnValue(new Promise(() => {}));
  vi.mocked(useClient).mockReturnValue({ GET: get } as never);
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(now);
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
    [
      { plan: 'trial', expiry: '2026-10-15T00:00:00Z' },
      'Trial · Expires in 14 days',
    ],
    [
      { plan: 'trial', expiry: '2026-10-02T00:00:00Z' },
      'Trial · Expires in 1 day',
    ],
    [
      { plan: 'trial', expiry: '2026-10-01T00:00:00Z' },
      'Trial · Expires today',
    ],
    [{ expiry: '2026-10-21T00:00:00Z' }, 'Team · Expires in 20 days'],
    [{ valid: false, gracePeriod: true }, 'Team · Grace period'],
    [{ valid: false }, 'Team · Expired'],
    [{ warningCode: 'MACHINE_LIMIT_EXCEEDED' }, 'Team · Needs attention'],
    [{ community: true, error: 'Invalid token' }, 'License error'],
    [
      { plan: 'free', features: [], expiry: '2026-10-02T00:00:00Z' },
      'Free · Monitoring only',
    ],
  ])('shows accurate license details for %j', async (license, label) => {
    renderLicense(license);
    await userEvent.tab();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(label);
  });

  it('provides keyboard-accessible details and a direct management link', async () => {
    renderLicense({ expiry: '2027-01-01T00:00:00Z' });
    const link = screen.getByRole('link', { name: 'Plan & features' });
    expect(link).toHaveAttribute('href', '/license');
    expect(link).toHaveTextContent(/^Team plan$/);
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

  // A Community server starts its trial by connecting with it chosen; one on
  // free monitoring starts it from its page in Dagu Console.
  it('starts the trial where this server can start it', () => {
    const view = renderLicense({ community: true, valid: false });
    expect(
      screen.getByRole('link', { name: 'Start free trial' })
    ).toHaveAttribute('href', '/license?trial=1#monitoring');
    expect(screen.getByRole('link', { name: 'View plans' })).toHaveAttribute(
      'href',
      expect.stringContaining('content=audit')
    );
    view.unmount();

    renderLicense({
      plan: 'free',
      features: [],
      consoleUrl: 'https://console.dagu.sh/servers?server=srv-1',
    });
    expect(
      screen.getByRole('link', { name: 'Start free trial' })
    ).toHaveAttribute('href', 'https://console.dagu.sh/servers?server=srv-1');
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
    ).toHaveTextContent(/^Trial plan$/);
    await userEvent.tab();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Expires in 14 days'
    );
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    view.unmount();
    renderLicense({ plan: 'trial', expiry: '2026-10-04T00:00:00Z' });
    expect(screen.getByRole('alert')).toHaveTextContent('expires in 3 days');
  });
});

describe('sidebar license line', () => {
  it.each([
    ['community', { community: true, valid: false }, 'Community', 'muted'],
    ['an active license', connected, 'Team plan · acme', 'muted'],
    [
      // A trial says how long it has left only once it is close to the end.
      'a trial with more than three days left',
      { ...connected, plan: 'trial', expiry: '2026-10-05T00:00:00Z' },
      'Trial plan · acme',
      'muted',
    ],
    [
      'a trial within three days',
      { ...connected, plan: 'trial', expiry: '2026-10-04T00:00:00Z' },
      'Trial plan · Expires in 3 days',
      'warning',
    ],
    [
      'a paid license with more than 30 days left',
      { ...connected, expiry: '2026-11-01T00:00:00Z' },
      'Team plan · acme',
      'muted',
    ],
    [
      'a paid license within 30 days',
      { ...connected, expiry: '2026-10-31T00:00:00Z' },
      'Team plan · Expires in 30 days',
      'warning',
    ],
    [
      'the grace period',
      { ...connected, valid: false, gracePeriod: true },
      'Team plan · Grace period',
      'warning',
    ],
    [
      'a license that needs attention',
      { ...connected, warningCode: 'MACHINE_LIMIT_EXCEEDED' },
      'Team plan · Needs attention',
      'warning',
    ],
    [
      'an expired license',
      { ...connected, valid: false },
      'Team plan · Expired',
      'destructive',
    ],
    [
      'a license error',
      { community: true, valid: false, error: 'Invalid token' },
      'License error',
      'destructive',
    ],
    [
      // A free license unlocks nothing that its expiry would take away.
      'a free license',
      {
        ...connected,
        plan: 'free',
        features: [],
        valid: false,
        gracePeriod: true,
      },
      'Free plan · Monitoring only',
      'muted',
    ],
  ])('names %s', (_case, license, text, tone) => {
    renderBadge(license);

    const link = sidebarLink();
    expect(link).toHaveTextContent(new RegExp(`^${text}$`));
    expect(link).toHaveClass(
      tone === 'muted' ? 'text-muted-foreground' : `text-${tone}`
    );
  });

  it('says when monitoring stopped reporting', async () => {
    serveMonitoring(reporting(minutesAgo(11)));
    renderBadge(connected);

    await waitFor(() =>
      expect(sidebarLink()).toHaveTextContent(/^Team plan · Not reporting$/)
    );
    expect(sidebarLink()).toHaveClass('text-warning');
    expect(get).toHaveBeenCalledWith('/license/monitoring', {
      params: { query: { remoteNode: 'local' } },
    });
    await userEvent.tab();
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      /Not reporting since \d{4}-\d{2}-\d{2} \d{2}:\d{2}/
    );
  });

  it('shows the workspace while reports arrive', async () => {
    serveMonitoring(reporting(minutesAgo(9)));
    renderBadge(connected);
    await userEvent.tab();

    const tooltip = await screen.findByRole('tooltip');
    await waitFor(() =>
      expect(tooltip).toHaveTextContent(
        'Monitoring' + 'Health and run status, last report 9 minutes ago'
      )
    );
    expect(sidebarLink()).toHaveTextContent(/^Team plan · acme$/);
    expect(tooltip).toHaveTextContent('Workspace' + 'acme');
    expect(tooltip).toHaveTextContent('Server' + 'build-01');
  });

  it('shows monitoring that is off', async () => {
    serveMonitoring({ ...reporting(), level: LicenseMonitoringLevel.off });
    renderBadge(connected);
    await userEvent.tab();

    await waitFor(async () =>
      expect(await screen.findByRole('tooltip')).toHaveTextContent(
        'Monitoring' + 'Off'
      )
    );
  });

  it('asks non-admins nothing about monitoring', async () => {
    isAdmin.mockReturnValue(false);
    renderBadge(connected);

    await userEvent.click(
      screen.getByRole('button', { name: 'Plan & features' })
    );
    const menu = screen.getByRole('menu');
    expect(menu).toHaveTextContent('Team · Active');
    expect(menu).not.toHaveTextContent('Monitoring');
    expect(get).not.toHaveBeenCalled();
  });

  it.each([
    ['while the status loads', undefined, 'Checking license…'],
    [
      'when the status cannot be read',
      new Error('unreachable'),
      'License status unavailable',
    ],
  ])('says so %s', (_case, error, text) => {
    render(
      <MemoryRouter>
        <LicenseContext.Provider value={{ error, mutate: vi.fn() }}>
          <LicenseBadge />
        </LicenseContext.Provider>
      </MemoryRouter>
    );

    expect(sidebarLink()).toHaveTextContent(new RegExp(`^${text}$`));
    expect(sidebarLink()).toHaveClass('text-muted-foreground');
  });

  describe('collapsed', () => {
    it('shows nothing while nothing needs attention', () => {
      renderBadge(connected, { compact: true });

      expect(
        screen.queryByRole('link', { name: 'Plan & features' })
      ).not.toBeInTheDocument();
    });

    it('shows an alert icon with the details', async () => {
      renderBadge({ ...connected, valid: false }, { compact: true });

      const link = sidebarLink();
      expect(link).toHaveTextContent(/^$/);
      expect(link).toHaveClass('text-destructive');
      expect(link.querySelector('svg')).not.toBeNull();
      await userEvent.tab();
      expect(await screen.findByRole('tooltip')).toHaveTextContent(
        'Team · Expired'
      );
    });
  });
});
