// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import { Input } from '@/components/ui/input';
import { Config, ConfigContext } from '@/contexts/ConfigContext';
import DAGRunTable from '../DAGRunTable';

vi.mock('../StepDetailsTooltip', () => ({
  StepDetailsTooltip: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

const config = {
  apiURL: '',
  basePath: '',
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
  agentEnabled: false,
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
} as Config;

describe('DAGRunTable', () => {
  it('shows the scheduled at column and value when schedule time exists', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[
              {
                dagRunId: 'run-1',
                name: 'scheduled-dag',
                status: Status.Failed,
                statusLabel: StatusLabel.failed,
                artifactsAvailable: false,
                autoRetryCount: 1,
                autoRetryLimit: 3,
                triggerType: TriggerType.scheduler,
                queuedAt: '2026-03-13T10:00:30Z',
                scheduleTime: '2026-03-13T10:00:00Z',
                startedAt: '',
                finishedAt: '',
              },
            ]}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText('Scheduled At')).toBeInTheDocument();
    expect(screen.getByText('2026-03-13T10:00:00Z')).toBeInTheDocument();
    expect(screen.getByText('2026-03-13T10:00:30Z')).toBeInTheDocument();
    expect(screen.getByText('1/3 auto retries')).toBeInTheDocument();
    expect(screen.queryByText('Select')).not.toBeInTheDocument();
  });

  it('shows the profile column when a run uses a runtime profile', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[
              {
                dagRunId: 'run-1',
                name: 'profiled-dag',
                status: Status.Success,
                statusLabel: StatusLabel.succeeded,
                artifactsAvailable: false,
                autoRetryCount: 0,
                autoRetryLimit: 0,
                triggerType: TriggerType.manual,
                queuedAt: '2026-03-13T10:00:30Z',
                startedAt: '2026-03-13T10:01:00Z',
                finishedAt: '2026-03-13T10:02:00Z',
                profileName: 'prod',
              },
            ]}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText('Profile')).toBeInTheDocument();
    expect(screen.getByText('prod')).toBeInTheDocument();
  });

  it('omits the profile column when no runs use a runtime profile', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[
              {
                dagRunId: 'run-1',
                name: 'default-profile-dag',
                status: Status.Success,
                statusLabel: StatusLabel.succeeded,
                artifactsAvailable: false,
                autoRetryCount: 0,
                autoRetryLimit: 0,
                triggerType: TriggerType.manual,
                queuedAt: '2026-03-13T10:00:30Z',
                startedAt: '2026-03-13T10:01:00Z',
                finishedAt: '2026-03-13T10:02:00Z',
              },
            ]}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.queryByText('Profile')).not.toBeInTheDocument();
  });

  it('toggles bulk selection without opening the focused run', () => {
    const onSelectDAGRun = vi.fn();
    const onToggleBulkSelect = vi.fn();

    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[
              {
                dagRunId: 'run-1',
                name: 'bulk-dag',
                status: Status.Failed,
                statusLabel: StatusLabel.failed,
                artifactsAvailable: false,
                autoRetryCount: 0,
                autoRetryLimit: 0,
                triggerType: TriggerType.manual,
                queuedAt: '2026-03-13T10:00:30Z',
                startedAt: '2026-03-13T10:01:00Z',
                finishedAt: '2026-03-13T10:02:00Z',
              },
            ]}
            onSelectDAGRun={onSelectDAGRun}
            onToggleBulkSelect={onToggleBulkSelect}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Select DAG run bulk-dag run-1' })
    );

    expect(onToggleBulkSelect).toHaveBeenCalledWith({
      name: 'bulk-dag',
      dagRunId: 'run-1',
    });
    expect(onSelectDAGRun).not.toHaveBeenCalled();
  });

  it('ignores Enter shortcuts while a filter input is focused', () => {
    const onSelectDAGRun = vi.fn();

    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <div>
            <Input aria-label="Filter by DAG name" />
            <DAGRunTable
              dagRuns={[
                {
                  dagRunId: 'run-1',
                  name: 'alpha',
                  status: Status.Failed,
                  statusLabel: StatusLabel.failed,
                  artifactsAvailable: false,
                  autoRetryCount: 0,
                  autoRetryLimit: 0,
                  triggerType: TriggerType.manual,
                  queuedAt: '2026-03-13T10:00:30Z',
                  startedAt: '2026-03-13T10:01:00Z',
                  finishedAt: '2026-03-13T10:02:00Z',
                },
              ]}
              onSelectDAGRun={onSelectDAGRun}
            />
          </div>
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.keyDown(window, { key: 'ArrowDown' });

    const input = screen.getByRole('textbox', { name: 'Filter by DAG name' });
    input.focus();
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(onSelectDAGRun).not.toHaveBeenCalled();
  });
});
