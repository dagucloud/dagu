// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AgentBashPolicyDefaultBehavior,
  AgentBashPolicyDenyBehavior,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import AgentSettingsPage from '..';
import AgentToolsPage from '../../agent-tools';

const getMock = vi.fn();
const patchMock = vi.fn();

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    GET: getMock,
    PATCH: patchMock,
    PUT: vi.fn(),
    DELETE: vi.fn(),
  }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useIsAdmin: () => true,
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useUpdateConfig: () => vi.fn(),
  useConfig: () => ({
    apiURL: '/api/v1',
  }),
}));

vi.mock('@/features/agent/hooks/useAgentAuthProviders', () => ({
  useAgentAuthProviders: () => ({
    providerMap: {},
    isLoading: false,
    error: null,
    startLogin: vi.fn(),
    completeLogin: vi.fn(),
    disconnect: vi.fn(),
  }),
}));

function createAgentData() {
  return {
    enabled: true,
    defaultModelId: 'main',
    selectedSoulId: undefined,
    toolPolicy: {
      tools: { shell: true },
      bash: {
        rules: [],
        defaultBehavior: AgentBashPolicyDefaultBehavior.allow,
        denyBehavior: AgentBashPolicyDenyBehavior.ask_user,
      },
    },
    webSearch: { enabled: true },
  };
}

function renderPage(page: React.ReactElement) {
  return render(
    <AppBarContext.Provider
      value={
        {
          setTitle: vi.fn(),
          selectedRemoteNode: 'local',
        } as never
      }
    >
      {page}
    </AppBarContext.Provider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  getMock.mockImplementation((path: string) => {
    if (path === '/settings/agent/tools') {
      return Promise.resolve({
        data: {
          tools: [
            {
              name: 'shell',
              label: 'Shell',
              description: 'Run shell commands.',
            },
          ],
        },
      });
    }
    if (path === '/settings/agent') {
      return Promise.resolve({ data: createAgentData() });
    }
    if (path === '/settings/agent/models') {
      return Promise.resolve({
        data: {
          defaultModelId: 'main',
          models: [
            {
              id: 'main',
              name: 'Main Model',
              provider: 'openai',
              model: 'gpt-4.1',
              apiKeyConfigured: true,
            },
          ],
        },
      });
    }
    if (path === '/settings/agent/model-presets') {
      return Promise.resolve({ data: { presets: [] } });
    }
    if (path === '/settings/agent/souls') {
      return Promise.resolve({
        data: { souls: [{ id: 'default', name: 'Default Soul' }] },
      });
    }
    return Promise.resolve({ data: {} });
  });
  patchMock.mockResolvedValue({ data: createAgentData() });
});

describe('agent settings split pages', () => {
  it('keeps tool permissions out of the settings page', async () => {
    renderPage(<AgentSettingsPage />);

    expect(await screen.findByText('Main Model')).toBeVisible();
    expect(screen.queryByLabelText('Enable Agent')).not.toBeInTheDocument();
    expect(screen.queryByText('Agent Personality')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /save settings/i })
    ).not.toBeInTheDocument();
    expect(screen.queryByText('Tool Permissions')).not.toBeInTheDocument();
    expect(screen.queryByText('Web Search')).not.toBeInTheDocument();
    expect(screen.queryByText('Shell')).not.toBeInTheDocument();
  });

  it('keeps the enable agent switch out of the models page', async () => {
    renderPage(<AgentSettingsPage />);

    expect(await screen.findByText('Main Model')).toBeVisible();
    expect(screen.queryByLabelText('Enable Agent')).not.toBeInTheDocument();
  });

  it('shows tool permissions and web access controls on the tools page', async () => {
    renderPage(<AgentToolsPage />);

    expect(await screen.findByText('Tool Permissions')).toBeVisible();
    expect(screen.getByText('Shell')).toBeVisible();
    expect(screen.getByText('Web Search Backend')).toBeVisible();
    expect(screen.getByText('Model Web Search')).toBeVisible();
    expect(screen.queryByText('Main Model')).not.toBeInTheDocument();
    expect(screen.queryByText('Models')).not.toBeInTheDocument();
    expect(getMock).not.toHaveBeenCalledWith(
      '/settings/agent/models',
      expect.anything()
    );
  });
});
