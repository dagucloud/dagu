// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import * as React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import APIDocsPage from '../index';

const fetchJsonMock = vi.fn();
const scalarViewerMock = vi.fn();

vi.mock('@/lib/fetchJson', () => ({
  default: (...args: unknown[]) => fetchJsonMock(...args),
}));

vi.mock('../ScalarViewer', () => ({
  default: (props: Record<string, unknown>) => {
    scalarViewerMock(props);
    return <div data-testid="scalar-viewer">viewer</div>;
  },
}));

function makeConfig(overrides: Partial<Config> = {}): Config {
  return {
    apiURL: '/api/v1',
    basePath: '/',
    title: 'Dagu',
    navbarColor: '',
    tz: 'UTC',
    tzOffsetInSec: 0,
    version: 'test',
    maxDashboardPageLimit: 100,
    remoteNodes: '',
    initialWorkspaces: [],
    authMode: 'none',
    setupRequired: false,
    oidcEnabled: false,
    oidcButtonLabel: '',
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
      plan: 'community',
      expiry: '',
      features: [],
      gracePeriod: false,
      community: true,
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
    ...overrides,
  };
}

type AppBarContextValue = React.ContextType<typeof AppBarContext>;

function makeAppBarContext(
  overrides: Partial<AppBarContextValue> = {}
): AppBarContextValue {
  return {
    title: '',
    setTitle: () => undefined,
    remoteNodes: ['local'],
    setRemoteNodes: () => undefined,
    selectedRemoteNode: 'local',
    selectRemoteNode: () => undefined,
    ...overrides,
  };
}

function renderPage(
  configOverrides: Partial<Config> = {},
  appBarOverrides: Partial<AppBarContextValue> = {}
) {
  return render(
    <ConfigContext.Provider value={makeConfig(configOverrides)}>
      <AppBarContext.Provider value={makeAppBarContext(appBarOverrides)}>
        <APIDocsPage />
      </AppBarContext.Provider>
    </ConfigContext.Provider>
  );
}

describe('APIDocsPage', () => {
  beforeEach(() => {
    fetchJsonMock.mockReset();
    scalarViewerMock.mockReset();
    localStorage.clear();
  });

  it('shows a loading state while the OpenAPI document is in flight', () => {
    fetchJsonMock.mockReturnValue(new Promise(() => undefined));

    renderPage();

    expect(screen.getByText('Loading API reference')).toBeInTheDocument();
    expect(fetchJsonMock).toHaveBeenCalledWith(
      '/openapi.json?remoteNode=local'
    );
  });

  it('uses the selected remote node for the OpenAPI document and raw JSON link', async () => {
    fetchJsonMock.mockResolvedValue({
      openapi: '3.0.0',
      info: {
        title: 'Dagu',
      },
    });

    renderPage({}, { selectedRemoteNode: 'worker-a' });

    expect(await screen.findByTestId('scalar-viewer')).toBeInTheDocument();
    expect(fetchJsonMock).toHaveBeenCalledWith(
      '/openapi.json?remoteNode=worker-a'
    );
    expect(screen.getByRole('link', { name: /raw json/i })).toHaveAttribute(
      'href',
      '/api/v1/openapi.json?remoteNode=worker-a'
    );
  });

  it('shows an error state when the document fetch fails', async () => {
    fetchJsonMock.mockRejectedValue(new Error('request failed'));

    renderPage();

    expect(
      await screen.findByText('Unable to load the API reference')
    ).toBeInTheDocument();
    expect(screen.getByText('request failed')).toBeInTheDocument();
  });

  it('renders the viewer once the document loads', async () => {
    fetchJsonMock.mockResolvedValue({
      openapi: '3.0.0',
      info: {
        title: 'Dagu',
      },
    });

    renderPage();

    expect(await screen.findByTestId('scalar-viewer')).toBeInTheDocument();
    await waitFor(() => {
      expect(scalarViewerMock).toHaveBeenCalledWith(
        expect.objectContaining({
          spec: expect.objectContaining({
            openapi: '3.0.0',
          }),
        })
      );
    });
  });

  it('keeps the latest reload result when requests finish out of order', async () => {
    let resolveFirst: (value: Record<string, unknown>) => void = () =>
      undefined;
    let resolveSecond: (value: Record<string, unknown>) => void = () =>
      undefined;
    const firstRequest = new Promise<Record<string, unknown>>((resolve) => {
      resolveFirst = resolve;
    });
    const secondRequest = new Promise<Record<string, unknown>>((resolve) => {
      resolveSecond = resolve;
    });

    fetchJsonMock
      .mockReturnValueOnce(firstRequest)
      .mockReturnValueOnce(secondRequest);

    renderPage();
    fireEvent.click(screen.getByRole('button', { name: /reload/i }));

    await act(async () => {
      resolveSecond({
        openapi: '3.1.0',
        info: {
          title: 'Latest',
        },
      });
      await secondRequest;
    });

    expect(await screen.findByTestId('scalar-viewer')).toBeInTheDocument();
    expect(scalarViewerMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          openapi: '3.1.0',
        }),
      })
    );

    await act(async () => {
      resolveFirst({
        openapi: '3.0.0',
        info: {
          title: 'Stale',
        },
      });
      await firstRequest;
    });

    expect(scalarViewerMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        spec: expect.objectContaining({
          openapi: '3.1.0',
        }),
      })
    );
  });

  it('prefills the builtin bearer token for the viewer', async () => {
    localStorage.setItem('dagu_auth_token', 'builtin-token');
    fetchJsonMock.mockResolvedValue({
      openapi: '3.0.0',
      info: {
        title: 'Dagu',
      },
    });

    renderPage({ authMode: 'builtin' });

    expect(await screen.findByTestId('scalar-viewer')).toBeInTheDocument();
    await waitFor(() => {
      expect(scalarViewerMock).toHaveBeenCalledWith(
        expect.objectContaining({
          preferredBearerToken: 'builtin-token',
        })
      );
    });
  });
});
