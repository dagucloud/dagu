// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ModelConfigResponseProvider } from '@/api/v1/schema';
import type { Config } from '@/contexts/ConfigContext';
import { ConfigContext } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ModelFormModal } from '../ModelFormModal';

const fetchMock = vi.fn();

vi.mock('@/lib/authHeaders', () => ({
  getAuthHeaders: () => ({
    'Content-Type': 'application/json',
  }),
}));

const config: Config = {
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
  authMode: 'builtin',
  setupRequired: false,
  oidcEnabled: false,
  oidcButtonLabel: '',
  terminalEnabled: false,
  gitSyncEnabled: false,
  agentEnabled: true,
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
};

const appBarContextValue = {
  title: '',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const baseModel = {
  id: 'local-model',
  name: 'Local Model',
  provider: ModelConfigResponseProvider.local,
  model: 'llama3.2',
  apiKeyConfigured: true,
  baseUrl: '',
  contextWindow: 0,
  maxOutputTokens: 0,
  inputCostPer1M: 0,
  outputCostPer1M: 0,
  supportsThinking: false,
  description: '',
};

function renderModal() {
  return render(
    <ConfigContext.Provider value={config}>
      <AppBarContext.Provider value={appBarContextValue}>
        <ModelFormModal
          open
          model={baseModel}
          presets={[]}
          codexProvider={null}
          onStartProviderLogin={vi.fn()}
          onCompleteProviderLogin={vi.fn()}
          onDisconnectProvider={vi.fn()}
          onClose={vi.fn()}
          onSuccess={vi.fn()}
        />
      </AppBarContext.Provider>
    </ConfigContext.Provider>
  );
}

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({
    ok: true,
    json: async () => ({}),
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal(
    'ResizeObserver',
    class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('ModelFormModal', () => {
  it('shows local optional-auth metadata from the shared provider helper', async () => {
    renderModal();

    expect(await screen.findByText('API Key (optional)')).toBeInTheDocument();
    expect(
      screen.getByText(
        'Leave empty for local endpoints that do not require authentication.'
      )
    ).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText('http://localhost:11434/v1')
    ).toBeInTheDocument();
  });

  it('sends an explicit empty apiKey when clearing a stored key', async () => {
    renderModal();

    fireEvent.click(await screen.findByLabelText('Clear stored API key'));
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/settings/agent/models/local-model?remoteNode=local',
      expect.objectContaining({
        method: 'PATCH',
      })
    );

    const requestBody = JSON.parse(
      fetchMock.mock.calls[0]![1]!.body as string
    );
    expect(requestBody.apiKey).toBe('');
    expect(requestBody.provider).toBe('local');
    expect(requestBody.model).toBe('llama3.2');
  });
});
