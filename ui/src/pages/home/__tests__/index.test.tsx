// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { UserRole } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
import { defaultWorkspaceSelection } from '@/lib/workspace';

import HomePage from '..';

const useAuthMock = vi.fn();
const useIsAdminMock = vi.fn();
const useCanAccessSystemStatusMock = vi.fn();
const useCanViewEventLogsMock = vi.fn();
const useCanManageWebhooksMock = vi.fn();
const useCanViewAuditLogsMock = vi.fn();
const useHasFeatureMock = vi.fn();

vi.mock('@/contexts/AuthContext', () => ({
  useAuth: () => useAuthMock(),
  useIsAdmin: () => useIsAdminMock(),
  useCanAccessSystemStatus: () => useCanAccessSystemStatusMock(),
  useCanViewEventLogs: () => useCanViewEventLogsMock(),
  useCanManageWebhooks: () => useCanManageWebhooksMock(),
  useCanViewAuditLogs: () => useCanViewAuditLogsMock(),
}));

vi.mock('@/hooks/useLicense', () => ({
  useHasFeature: (feature: string) => useHasFeatureMock(feature),
}));

const config = {
  title: 'Dagu',
  navbarColor: '',
  authMode: 'builtin',
  terminalEnabled: true,
  gitSyncEnabled: true,
  agentEnabled: true,
  permissions: {
    writeDags: true,
    runDags: true,
  },
} as Config;

function renderHome(configOverride: Partial<Config> = {}): void {
  render(
    <MemoryRouter>
      <ConfigContext.Provider value={{ ...config, ...configOverride }}>
        <AppBarContext.Provider
          value={{
            title: '',
            setTitle: vi.fn(),
            remoteNodes: ['local'],
            setRemoteNodes: vi.fn(),
            selectedRemoteNode: 'local',
            selectRemoteNode: vi.fn(),
            workspaces: [],
            workspaceError: null,
            workspaceSelection: defaultWorkspaceSelection(),
            selectWorkspace: vi.fn(),
            createWorkspace: vi.fn(),
            deleteWorkspace: vi.fn(),
          }}
        >
          <HomePage />
        </AppBarContext.Provider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
}

describe('HomePage', () => {
  beforeEach(() => {
    useAuthMock.mockReturnValue({
      user: { id: '1', username: 'admin', role: UserRole.admin },
    });
    useIsAdminMock.mockReturnValue(true);
    useCanAccessSystemStatusMock.mockReturnValue(true);
    useCanViewEventLogsMock.mockReturnValue(true);
    useCanManageWebhooksMock.mockReturnValue(true);
    useCanViewAuditLogsMock.mockReturnValue(true);
    useHasFeatureMock.mockReturnValue(true);
  });

  it('renders grouped navigation cards for the main app areas', () => {
    renderHome();

    expect(
      screen.getByRole('heading', { name: 'Home' })
    ).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Overview/i })).toHaveAttribute(
      'href',
      '/'
    );
    expect(screen.getByRole('link', { name: /DAG Runs/i })).toHaveAttribute(
      'href',
      '/dag-runs'
    );
    expect(
      screen.getByRole('link', { name: /Administration/i })
    ).toHaveAttribute('href', '/administration');
  });
});
