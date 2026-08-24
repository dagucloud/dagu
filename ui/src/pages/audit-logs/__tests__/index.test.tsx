// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';
import { AuthProvider } from '@/contexts/AuthContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import AuditLogsPage, { getQuickFilterValue } from '..';

const auditEntry = {
  id: 'audit-001',
  timestamp: '2026-08-24T08:15:30Z',
  category: 'notification',
  action: 'notification_route_set_update',
  source: 'mcp',
  surface: 'mcp',
  result: 'succeeded',
  correlationId: 'corr-001',
  resourceType: 'notification_route_set',
  resourceId: 'route-alerts-critical',
  workspace: 'platform-team',
  credentialId: 'credential-001',
  credentialType: 'api_key',
  mcpTool: 'notification_route_set_update',
  userId: 'user-001',
  username: 'alice@example.com',
  details: JSON.stringify({ route: 'alerts-critical', changes: 2 }),
  ipAddress: '203.0.113.45',
};
const originalClipboard = Object.getOwnPropertyDescriptor(
  navigator,
  'clipboard'
);

function makeConfig(): Config {
  return {
    apiURL: '/api/v1',
    basePath: '/',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: 'local',
    initialWorkspaces: [],
    authMode: 'none',
    setupRequired: false,
    oidcEnabled: false,
    oidcButtonLabel: '',
    proxyEnabled: false,
    proxyButtonLabel: '',
    terminalEnabled: false,
    gitSyncEnabled: false,
    updateAvailable: false,
    latestVersion: '',
    permissions: {
      writeDags: true,
      runDags: true,
    },
    license: {
      valid: true,
      plan: 'enterprise',
      expiry: '',
      features: ['audit'],
      gracePeriod: false,
      community: false,
      source: 'test',
      warningCode: '',
    },
    paths: {
      dagsDir: '',
      logDir: '',
      suspendFlagsDir: '',
      adminLogsDir: '',
      baseConfig: '',
      dagRunsDir: '',
      queueDir: '',
      procDir: '',
      serviceRegistryDir: '',
      configFileUsed: '',
      gitSyncDir: '',
      auditLogsDir: '',
    },
  };
}

function renderPage() {
  return render(
    <ConfigContext.Provider value={makeConfig()}>
      <AuthProvider>
        <AppBarContext.Provider
          value={{
            title: '',
            setTitle: () => undefined,
            remoteNodes: ['local'],
            setRemoteNodes: () => undefined,
            selectedRemoteNode: 'local',
            selectRemoteNode: () => undefined,
          }}
        >
          <AuditLogsPage />
        </AppBarContext.Provider>
      </AuthProvider>
    </ConfigContext.Provider>
  );
}

function stubAuditResponse(entry = auditEntry) {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ entries: [entry], total: 1 }),
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

describe('getQuickFilterValue', () => {
  it.each([
    ['mcp', 'mcp', 'mcp', 'all', 'mcp'],
    ['all', 'rest', 'rest_api', 'all', 'rest'],
    ['all', 'all', 'all', 'failed', 'failed'],
    ['all', 'all', 'all', 'denied', 'denied'],
    ['all', 'all', 'all', 'all', 'all'],
  ])(
    'derives %s/%s/%s/%s as %s',
    (category, source, surface, result, expected) => {
      expect(getQuickFilterValue(category, source, surface, result)).toBe(
        expected
      );
    }
  );
});

describe('AuditLogsPage', () => {
  beforeEach(() => {
    localStorage.clear();
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });
    stubAuditResponse();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    if (originalClipboard) {
      Object.defineProperty(navigator, 'clipboard', originalClipboard);
    } else {
      Reflect.deleteProperty(navigator, 'clipboard');
    }
  });

  it('routes audit requests to the selected remote node', async () => {
    renderPage();

    await screen.findByText('notification_route_set_update');
    const requestUrl = vi.mocked(fetch).mock.calls[0]?.[0];
    expect(requestUrl).toEqual(expect.stringContaining('remoteNode=local'));
  });

  it('opens a complete audit entry from the table', async () => {
    renderPage();

    await screen.findByText('notification_route_set_update');
    fireEvent.click(
      screen.getByRole('button', {
        name: 'View details for notification_route_set_update',
      })
    );

    const dialog = await screen.findByRole('dialog', {
      name: 'Audit entry',
    });
    expect(within(dialog).getByText('audit-001')).toBeInTheDocument();
    expect(
      within(dialog).getByText('route-alerts-critical')
    ).toBeInTheDocument();
    expect(within(dialog).getByText('credential-001')).toBeInTheDocument();
    expect(within(dialog).getByText('203.0.113.45')).toBeInTheDocument();
    expect(
      within(dialog).getByText(/"route": "alerts-critical"/)
    ).toBeInTheDocument();
  });

  it('shows and copies the complete raw audit entry', async () => {
    renderPage();

    await screen.findByText('notification_route_set_update');
    fireEvent.click(
      screen.getByRole('button', {
        name: 'View details for notification_route_set_update',
      })
    );

    const dialog = await screen.findByRole('dialog', {
      name: 'Audit entry',
    });
    fireEvent.click(within(dialog).getByRole('tab', { name: 'Raw JSON' }));

    expect(within(dialog).getByText(/"id": "audit-001"/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy JSON' }));
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
      JSON.stringify(auditEntry, null, 2)
    );
  });

  it('opens the focused audit row with the keyboard', async () => {
    renderPage();

    const row = await screen.findByRole('row', {
      name: /notification_route_set_update/,
    });
    row.focus();
    fireEvent.keyDown(row, { key: 'Enter' });

    expect(
      await screen.findByRole('dialog', { name: 'Audit entry' })
    ).toBeInTheDocument();
  });

  it('summarizes structured details for scanning', async () => {
    renderPage();

    expect(
      await screen.findByText('route: alerts-critical • changes: 2')
    ).toBeInTheDocument();
  });

  it('keeps existing rows visible while filters refresh', async () => {
    renderPage();

    await screen.findByText('notification_route_set_update');
    let resolveRefresh!: (response: Response) => void;
    vi.mocked(fetch).mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          resolveRefresh = resolve;
        })
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Failed audit entries' })
    );
    await waitFor(() => expect(resolveRefresh).toBeTypeOf('function'));

    expect(
      screen.getByText('notification_route_set_update')
    ).toBeInTheDocument();

    resolveRefresh({
      ok: true,
      json: async () => ({ entries: [auditEntry], total: 1 }),
    } as Response);
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Refresh audit logs' })
      ).toBeInTheDocument()
    );
  });

  it('keeps advanced filters compact and clears their values', async () => {
    renderPage();

    await screen.findByText('notification_route_set_update');
    expect(
      screen.queryByPlaceholderText('Credential ID')
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Filters' }));
    const workspaceInput = screen.getByPlaceholderText('Workspace');
    fireEvent.change(workspaceInput, { target: { value: 'platform-team' } });

    expect(
      screen.getByRole('button', { name: 'Filters (1)' })
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear all filters' }));
    expect(workspaceInput).toHaveValue('');
  });

  it('shows malformed detail payloads verbatim', async () => {
    stubAuditResponse({ ...auditEntry, details: 'unparseable audit details' });
    renderPage();

    await screen.findByText('notification_route_set_update');
    fireEvent.click(
      screen.getByRole('button', {
        name: 'View details for notification_route_set_update',
      })
    );

    const dialog = await screen.findByRole('dialog', { name: 'Audit entry' });
    expect(
      within(dialog).getByText('unparseable audit details')
    ).toBeInTheDocument();
  });

  it('states when an entry has no additional details', async () => {
    stubAuditResponse({ ...auditEntry, details: '' });
    renderPage();

    await screen.findByText('notification_route_set_update');
    fireEvent.click(
      screen.getByRole('button', {
        name: 'View details for notification_route_set_update',
      })
    );

    const dialog = await screen.findByRole('dialog', { name: 'Audit entry' });
    expect(
      within(dialog).getByText('No additional details.')
    ).toBeInTheDocument();
  });

  it('supports keyboard access to inspector tabs and scroll panels', async () => {
    renderPage();

    await screen.findByText('notification_route_set_update');
    fireEvent.click(
      screen.getByRole('button', {
        name: 'View details for notification_route_set_update',
      })
    );

    const dialog = await screen.findByRole('dialog', { name: 'Audit entry' });
    const detailsTab = within(dialog).getByRole('tab', { name: 'Details' });
    expect(within(dialog).getByRole('tabpanel')).toHaveAttribute(
      'tabindex',
      '0'
    );
    detailsTab.focus();
    fireEvent.keyDown(detailsTab, { key: 'ArrowRight' });

    expect(
      within(dialog).getByRole('tab', { name: 'Raw JSON' })
    ).toHaveAttribute('aria-selected', 'true');
    expect(within(dialog).getByRole('tabpanel')).toHaveAttribute(
      'tabindex',
      '0'
    );
    expect(within(dialog).getByText(/"id": "audit-001"/)).toBeInTheDocument();
  });
});
