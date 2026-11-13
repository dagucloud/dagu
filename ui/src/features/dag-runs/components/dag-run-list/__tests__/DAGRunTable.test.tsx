// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel, TriggerType } from '@/api/v1/schema';
import { Input } from '@/components/ui/input';
import { Config, ConfigContext } from '@/contexts/ConfigContext';
import { PeekContext, type PeekController } from '@/features/peek/PeekContext';
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
  proxyEnabled: false,
  proxyButtonLabel: '',
  terminalEnabled: false,
  gitSyncEnabled: false,
  updateAvailable: false,
  latestVersion: '',
  upgradeCommand: '',
  restartRequired: false,
  restartCommand: '',
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

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{`${location.pathname}${location.search}`}</div>
  );
}

const queuedRun = {
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
};

describe('DAGRunTable', () => {
  it('shows a loading row instead of the empty state while the first page loads', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable dagRuns={[]} isLoading />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText('Loading runs...')).toBeInTheDocument();
    expect(screen.queryByText('No runs found')).not.toBeInTheDocument();
  });

  it('shows the empty state once loading finishes with no runs', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable dagRuns={[]} />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText('No runs found')).toBeInTheDocument();
  });

  // A run that waits for an approval has the finish time of its pause: the
  // list showed 0s for a run that had waited 7 minutes. Its data does not
  // change while it waits, so the duration follows the clock.
  it('counts the duration of a waiting run up to now', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-13T10:08:00Z'));
    try {
      render(
        <MemoryRouter>
          <ConfigContext.Provider value={config}>
            <DAGRunTable
              dagRuns={[
                {
                  ...queuedRun,
                  status: Status.Waiting,
                  statusLabel: StatusLabel.waiting,
                  startedAt: '2026-03-13T10:01:00Z',
                  finishedAt: '2026-03-13T10:01:00.370Z',
                },
              ]}
            />
          </ConfigContext.Provider>
        </MemoryRouter>
      );
      expect(screen.getAllByText('7m 0s').length).toBeGreaterThan(0);
      expect(screen.queryByText('0s')).toBeNull();

      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(screen.getAllByText('7m 2s').length).toBeGreaterThan(0);
    } finally {
      vi.useRealTimers();
    }
  });

  // The API reports an unset time as "-".
  it('counts the duration of a running run whose finish is "-"', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-13T10:01:30Z'));
    // The live status dot asks for the reduced motion setting.
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }))
    );
    try {
      render(
        <MemoryRouter>
          <ConfigContext.Provider value={config}>
            <DAGRunTable
              dagRuns={[
                {
                  ...queuedRun,
                  status: Status.Running,
                  statusLabel: StatusLabel.running,
                  startedAt: '2026-03-13T10:01:00Z',
                  finishedAt: '-',
                },
              ]}
            />
          </ConfigContext.Provider>
        </MemoryRouter>
      );
      expect(screen.getAllByText('30s').length).toBeGreaterThan(0);
      expect(screen.queryByText(/NaN/)).toBeNull();
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });

  it('merges the scheduled time into the status cell when it exists', () => {
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

    // The scheduled time follows the status dot as a compact relative time.
    expect(screen.queryByText('Scheduled At')).not.toBeInTheDocument();
    expect(screen.queryByText('2026-03-13 10:00:00')).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Name' })).toBeVisible();
    const statusDot = screen.getByRole('img', { name: 'Failed' });
    expect(statusDot).toHaveAttribute('data-testid', 'status-dot');
    const statusCell = statusDot.closest('td') as HTMLElement;
    expect(statusCell).toHaveTextContent('·');
    const scheduled = screen.getByTitle('2026-03-13T10:00:00Z');
    expect(statusCell).toContainElement(scheduled);
    expect(scheduled).toHaveTextContent(/ago$/);
    expect(screen.getByRole('link', { name: 'scheduled-dag' })).toHaveAttribute(
      'href',
      '/dags/scheduled-dag?remoteNode=local&run=run-1'
    );
    // Queued At is a compact relative time with the absolute time in the tooltip.
    expect(screen.getByTitle('2026-03-13T10:00:30Z')).toHaveTextContent(/ago$/);
    expect(screen.getByText('1/3 auto retries')).toBeInTheDocument();
    expect(screen.queryByText('Select')).not.toBeInTheDocument();
  });

  it('shows the status dot without a status chip in the table', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable dagRuns={[queuedRun]} />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByRole('img', { name: 'Failed' })).toHaveAttribute(
      'data-testid',
      'status-dot'
    );
    // The dot carries the status text in its label only.
    expect(screen.queryByText('Failed')).not.toBeInTheDocument();
    // No schedule time: the time of the run follows the dot.
    expect(screen.getByText('·')).toBeInTheDocument();
  });

  it('selects all the loaded runs from the header checkbox', () => {
    const onSelectAll = vi.fn();

    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[queuedRun, { ...queuedRun, dagRunId: 'run-2' }]}
            selectedRunKeys={new Set()}
            onToggleBulkSelect={vi.fn()}
            onSelectAll={onSelectAll}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    const selectAll = screen.getByRole('checkbox', {
      name: 'Select all loaded runs',
    });
    expect(selectAll).toHaveClass('size-3.5');
    expect(
      screen.getByRole('checkbox', { name: 'Select DAG run alpha run-1' })
    ).toHaveClass('size-3.5');
    fireEvent.click(selectAll);

    expect(onSelectAll).toHaveBeenCalledWith(true);
  });

  it('disables the header checkbox without onSelectAll', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable dagRuns={[queuedRun]} onToggleBulkSelect={vi.fn()} />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.getByRole('checkbox', { name: 'Select all loaded runs' })
    ).toBeDisabled();
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

  it('shows the attributable actor with the trigger type', () => {
    render(
      <MemoryRouter>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[
              {
                dagRunId: 'run-1',
                name: 'manual-dag',
                status: Status.Success,
                statusLabel: StatusLabel.succeeded,
                artifactsAvailable: false,
                autoRetryCount: 0,
                triggerType: TriggerType.manual,
                triggerActor: 'alice',
                startedAt: '2026-03-13T10:01:00Z',
                finishedAt: '2026-03-13T10:02:00Z',
              },
            ]}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText('Manual')).toBeInTheDocument();
    expect(screen.getByText('(alice)')).toHaveClass(
      'text-muted-foreground',
      'font-mono'
    );
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

  it('toggles bulk selection without opening the run', () => {
    const onToggleBulkSelect = vi.fn();

    render(
      <MemoryRouter initialEntries={['/dag-runs']}>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[{ ...queuedRun, name: 'bulk-dag' }]}
            onToggleBulkSelect={onToggleBulkSelect}
          />
          <LocationProbe />
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
    expect(screen.getByTestId('location')).toHaveTextContent('/dag-runs');
    expect(screen.getByTestId('location')).not.toHaveTextContent('/dags/');
  });

  it('opens the run on its workflow page when a row is clicked', () => {
    render(
      <MemoryRouter initialEntries={['/dag-runs']}>
        <ConfigContext.Provider value={config}>
          <DAGRunTable dagRuns={[queuedRun]} />
          <LocationProbe />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    const nameLink = screen.getByRole('link', { name: 'alpha' });
    expect(nameLink).toHaveAttribute(
      'href',
      '/dags/alpha?remoteNode=local&run=run-1'
    );
    fireEvent.click(nameLink.closest('tr') ?? nameLink);

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/alpha?remoteNode=local&run=run-1'
    );
  });

  it('opens available artifacts on the Artifacts tab of the run', () => {
    render(
      <MemoryRouter initialEntries={['/dag-runs']}>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[
              {
                ...queuedRun,
                name: 'artifact-dag',
                status: Status.Success,
                statusLabel: StatusLabel.succeeded,
                artifactsAvailable: true,
              },
            ]}
          />
          <LocationProbe />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(
      screen.getByRole('link', {
        name: 'View artifacts for artifact-dag run-1',
      })
    );

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/artifact-dag?remoteNode=local&run=run-1&dock=artifacts'
    );
  });

  it('opens the run in a new tab under the base path', () => {
    const openMock = vi.spyOn(window, 'open').mockImplementation(() => null);
    render(
      <MemoryRouter>
        <ConfigContext.Provider
          value={{ ...config, basePath: '/dagu' } as Config}
        >
          <DAGRunTable
            dagRuns={[
              {
                ...queuedRun,
                name: 'reporter',
                autoRetryCount: 1,
                autoRetryLimit: 3,
                triggerType: TriggerType.scheduler,
                scheduleTime: '2026-03-13T10:00:00Z',
                startedAt: '2026-03-13T10:00:05Z',
                finishedAt: '2026-03-13T10:01:05Z',
              },
            ]}
          />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    const nameLink = screen.getByRole('link', { name: 'reporter' });
    fireEvent.click(nameLink.closest('tr') ?? nameLink, { ctrlKey: true });

    expect(openMock).toHaveBeenCalledWith(
      '/dagu/dags/reporter?remoteNode=local&run=run-1',
      '_blank',
      'noopener'
    );
  });

  it('opens the highlighted run with Enter', () => {
    render(
      <MemoryRouter initialEntries={['/dag-runs']}>
        <ConfigContext.Provider value={config}>
          <DAGRunTable
            dagRuns={[queuedRun, { ...queuedRun, dagRunId: 'run-2' }]}
          />
          <LocationProbe />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.keyDown(window, { key: 'ArrowDown' });
    fireEvent.keyDown(window, { key: 'Enter' });

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/alpha?remoteNode=local&run=run-2'
    );
  });

  it('ignores Enter shortcuts while a filter input is focused', () => {
    render(
      <MemoryRouter initialEntries={['/dag-runs']}>
        <ConfigContext.Provider value={config}>
          <div>
            <Input aria-label="Filter by workflow name" />
            <DAGRunTable dagRuns={[queuedRun]} />
          </div>
          <LocationProbe />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.keyDown(window, { key: 'ArrowDown' });

    const input = screen.getByRole('textbox', {
      name: 'Filter by workflow name',
    });
    input.focus();
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(screen.getByTestId('location')).toHaveTextContent('/dag-runs');
    expect(screen.getByTestId('location')).not.toHaveTextContent('/dags/');
  });

  // The items of a popup (the Status select, the view menu) are portaled
  // out of the filters and take Enter themselves.
  it.each([
    ['an option of a select', { role: 'option' }],
    [
      'a key that a popup handled',
      { onKeyDown: (event: React.KeyboardEvent) => event.preventDefault() },
    ],
  ])('ignores Enter on %s', (_, props) => {
    render(
      <MemoryRouter initialEntries={['/dag-runs']}>
        <ConfigContext.Provider value={config}>
          <DAGRunTable dagRuns={[queuedRun]} />
          <div data-testid="popup-item" tabIndex={-1} {...props}>
            Running
          </div>
          <LocationProbe />
        </ConfigContext.Provider>
      </MemoryRouter>
    );

    fireEvent.keyDown(window, { key: 'ArrowDown' });
    fireEvent.keyDown(screen.getByTestId('popup-item'), { key: 'Enter' });

    expect(screen.getByTestId('location')).toHaveTextContent('/dag-runs');
    expect(screen.getByTestId('location')).not.toHaveTextContent('/dags/');
  });

  describe('under a peek host', () => {
    function renderUnderPeek(peek: PeekController) {
      render(
        <MemoryRouter initialEntries={['/dag-runs']}>
          <ConfigContext.Provider value={config}>
            <PeekContext.Provider value={peek}>
              <DAGRunTable
                dagRuns={[queuedRun, { ...queuedRun, dagRunId: 'run-2' }]}
              />
            </PeekContext.Provider>
            <LocationProbe />
          </ConfigContext.Provider>
        </MemoryRouter>
      );
    }

    it('shows the peek of a run when its row is clicked', () => {
      const peek = { open: vi.fn(), isOpen: false };
      renderUnderPeek(peek);

      fireEvent.click(screen.getAllByText('run-1')[0]!.closest('tr')!);

      expect(peek.open).toHaveBeenCalledWith({ dag: 'alpha', run: 'run-1' });
      expect(screen.getByTestId('location')).toHaveTextContent(/^\/dag-runs$/);
    });

    it('shows the peek of the highlighted run with Enter', () => {
      const peek = { open: vi.fn(), isOpen: false };
      renderUnderPeek(peek);

      fireEvent.keyDown(window, { key: 'ArrowDown' });
      fireEvent.keyDown(window, { key: 'Enter' });

      expect(peek.open).toHaveBeenCalledWith({ dag: 'alpha', run: 'run-2' });
    });

    // After the arrow keys of the peek moved to run-2 and it closed, Enter
    // in the list goes on from run-2.
    it('moves its highlight to the run that the peek showed', () => {
      const peek = {
        open: vi.fn(),
        isOpen: false,
        shown: { dag: 'alpha', run: 'run-2' },
      };
      renderUnderPeek(peek);

      expect(screen.getAllByText('run-2')[0]!.closest('tr')).toHaveAttribute(
        'data-peek-shown'
      );
      fireEvent.keyDown(window, { key: 'Enter' });

      expect(peek.open).toHaveBeenCalledWith({ dag: 'alpha', run: 'run-2' });
    });

    // A refresh moves the row of the run that the peek showed; the highlight
    // that the list keys moved on stays where they put it.
    it('keeps its highlight when a refresh moves the shown run', () => {
      const peek = {
        open: vi.fn(),
        isOpen: false,
        shown: { dag: 'alpha', run: 'run-2' },
      };
      const runs = ['run-1', 'run-2', 'run-3'].map((dagRunId) => ({
        ...queuedRun,
        dagRunId,
      }));
      const table = (dagRuns: typeof runs) => (
        <MemoryRouter initialEntries={['/dag-runs']}>
          <ConfigContext.Provider value={config}>
            <PeekContext.Provider value={peek}>
              <DAGRunTable dagRuns={dagRuns} />
            </PeekContext.Provider>
          </ConfigContext.Provider>
        </MemoryRouter>
      );
      const view = render(table(runs));

      fireEvent.keyDown(window, { key: 'ArrowDown' });
      // run-1 leaves the list: run-2 moves up a row.
      view.rerender(table(runs.slice(1)));
      fireEvent.keyDown(window, { key: 'Enter' });

      expect(peek.open).toHaveBeenCalledWith({ dag: 'alpha', run: 'run-3' });
    });

    // The keys of the open peek bubble up to the window.
    it('leaves the keys to an open peek', () => {
      const peek = { open: vi.fn(), isOpen: true };
      renderUnderPeek(peek);

      fireEvent.keyDown(window, { key: 'ArrowDown' });
      fireEvent.keyDown(window, { key: 'Enter' });

      expect(peek.open).not.toHaveBeenCalled();
      expect(screen.getByTestId('location')).toHaveTextContent(/^\/dag-runs$/);
    });
  });
});
