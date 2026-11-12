// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RestartPolicy,
  ServiceDAGRestartHold,
  ServiceDAGState,
  Status,
  type components,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { WorkspaceKind } from '@/lib/workspace';
import { describeServiceDAG } from '../ServiceDAGState';
import { ServiceDAGStrip } from '../ServiceDAGStrip';

type ServiceDAG = components['schemas']['ServiceDAG'];

const hook = vi.hoisted(() => ({
  serviceDAGs: [] as ServiceDAG[],
  error: null as unknown,
  runs: [] as unknown[],
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (...args: unknown[]) => {
    hook.useQuery(...args);
    return { data: { dagRuns: hook.runs }, error: undefined, isLoading: false };
  },
}));

vi.mock('../../hooks/useServiceDAGs', () => ({
  useServiceDAGs: () => ({ serviceDAGs: hook.serviceDAGs, error: hook.error }),
}));

const NOW = Date.parse('2026-10-20T12:00:00Z');

function dag(overrides: Partial<ServiceDAG> = {}): ServiceDAG {
  return {
    fileName: 'worker',
    name: 'worker',
    restartPolicy: RestartPolicy.always,
    restartWaitSec: 5,
    queue: 'worker',
    state: ServiceDAGState.running,
    suspended: false,
    consecutiveFailures: 0,
    ...overrides,
  };
}

describe('describeServiceDAG', () => {
  it('counts the time since a live run started', () => {
    const live = dag({
      latestRun: {
        dagRunId: 'r1',
        status: Status.Running,
        startedAt: '2026-10-20T11:58:46Z',
      },
    });

    expect(describeServiceDAG(live, NOW)).toEqual({
      text: 'Up for {duration}',
      values: { duration: '1m 14s' },
    });
  });

  it('counts down to the time the scheduler starts the next run', () => {
    const ended = dag({
      state: ServiceDAGState.restarting,
      nextRestartAt: '2026-10-20T12:00:02Z',
    });

    expect(describeServiceDAG(ended, NOW)).toEqual({
      text: 'Restart in {duration}',
      values: { duration: '2s' },
    });
    expect(describeServiceDAG(ended, NOW + 5000).text).toBe('Restarting…');
  });

  // The scheduler waits longer for a DAG that keeps failing soon after it
  // starts than restartWaitSec says.
  it('counts down the longer wait of a DAG that keeps failing', () => {
    const crashing = dag({
      state: ServiceDAGState.failing,
      consecutiveFailures: 8,
      nextRestartAt: '2026-10-20T12:04:47Z',
    });

    expect(describeServiceDAG(crashing, NOW)).toEqual({
      text: 'Restart in {duration}',
      values: { duration: '4m 47s' },
    });
  });

  // The countdown would pass with no run while the scheduler starts none.
  it('says why no restart comes', () => {
    expect(
      describeServiceDAG(
        dag({
          state: ServiceDAGState.failing,
          restartHold: ServiceDAGRestartHold.paused,
        }),
        NOW
      ).text
    ).toBe('Restart on hold: the scheduler is paused');
    expect(
      describeServiceDAG(
        dag({
          state: ServiceDAGState.restarting,
          restartHold: ServiceDAGRestartHold.noScheduler,
        }),
        NOW
      ).text
    ).toBe('Restart on hold: no scheduler is running');
  });

  // A list that cannot read the state of a DAG, or an older server, gives no
  // restart time, so the card cannot say that a restart is under way.
  it('says that the next restart is unknown without a restart time', () => {
    expect(
      describeServiceDAG(
        dag({
          state: ServiceDAGState.failing,
          readError: 'status file is corrupt',
        }),
        NOW
      ).text
    ).toBe('Next restart unknown');
    expect(
      describeServiceDAG(dag({ state: ServiceDAGState.restarting }), NOW).text
    ).toBe('Next restart unknown');
  });

  it('says why no run follows', () => {
    expect(
      describeServiceDAG(dag({ state: ServiceDAGState.stopped }), NOW).text
    ).toBe('Stopped by an operator');
    expect(
      describeServiceDAG(dag({ state: ServiceDAGState.suspended }), NOW).text
    ).toBe('No new run');
    expect(
      describeServiceDAG(dag({ state: ServiceDAGState.pending }), NOW).text
    ).toBe('Waiting for a run');
  });
});

describe('ServiceDAGStrip', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    vi.setSystemTime(NOW);
    localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    hook.serviceDAGs = [];
  });

  function renderStrip() {
    return render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter>
            <ServiceDAGStrip />
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );
  }

  // The lists leave out the runs of service DAGs, so a list that cannot be
  // read must not look like one without a service DAG.
  it('says when the service DAGs cannot be read', () => {
    hook.error = { code: 'internal_error', message: 'store is down' };

    renderStrip();

    expect(screen.getByRole('region', { name: 'Service DAGs' })).toBeVisible();
    expect(screen.getByRole('alert')).toHaveTextContent('store is down');
    hook.error = null;
  });

  it('shows only the service DAGs whose name matches a filter', () => {
    hook.serviceDAGs = [
      dag({ fileName: 'ingest', name: 'ingest' }),
      dag({ fileName: 'web-api', name: 'web-api' }),
    ];

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter>
            <ServiceDAGStrip nameFilter="Ingest" />
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(screen.getByRole('link', { name: /ingest/ })).toBeVisible();
    expect(screen.queryByRole('link', { name: /web-api/ })).toBeNull();
  });

  it('shows nothing without a service DAG', () => {
    const { container } = renderStrip();

    expect(container).toBeEmptyDOMElement();
  });

  it('shows one card for each service DAG, linked to its DAG', () => {
    hook.serviceDAGs = [
      dag({
        fileName: 'ingest',
        name: 'ingest',
        state: ServiceDAGState.failing,
        latestRun: {
          dagRunId: 'r1',
          status: Status.Failed,
          startedAt: '2026-10-20T11:59:50Z',
          finishedAt: '2026-10-20T11:59:58Z',
        },
        nextRestartAt: '2026-10-20T12:00:03Z',
      }),
      dag({
        fileName: 'my dag',
        name: 'my dag',
        state: ServiceDAGState.suspended,
      }),
    ];

    renderStrip();

    expect(screen.getByRole('region', { name: 'Service DAGs' })).toBeVisible();
    const ingest = screen.getByRole('link', { name: /ingest/ });
    expect(ingest).toHaveAttribute('href', '/dags/ingest');
    expect(ingest).toHaveTextContent('Failing');
    expect(ingest).toHaveTextContent('Restart in 3s');
    expect(ingest).toHaveAttribute('title', 'Restarts 5s after a run ends');
    expect(screen.getByRole('link', { name: /my dag/ })).toHaveAttribute(
      'href',
      '/dags/my%20dag'
    );
  });
  it('says how many runs in a row failed and why the latest one did', () => {
    hook.serviceDAGs = [
      dag({
        fileName: 'ingest',
        name: 'ingest',
        state: ServiceDAGState.failing,
        consecutiveFailures: 3,
        latestRun: {
          dagRunId: 'r3',
          status: Status.Failed,
          finishedAt: '2026-10-20T11:59:58Z',
          error: 'serve: exit status 3',
        },
      }),
      dag({ fileName: 'flaky', name: 'flaky', consecutiveFailures: 10 }),
      dag({ fileName: 'once', name: 'once', consecutiveFailures: 1 }),
      dag({ fileName: 'healthy', name: 'healthy' }),
    ];

    renderStrip();

    expect(screen.getByRole('link', { name: /ingest/ })).toHaveTextContent(
      '3 failures in a row: serve: exit status 3'
    );
    expect(screen.getByRole('link', { name: /flaky/ })).toHaveTextContent(
      '10+ failures in a row'
    );
    expect(screen.getByRole('link', { name: /once/ })).toHaveTextContent(
      'Last run failed'
    );
    expect(screen.getByRole('link', { name: /healthy/ })).not.toHaveTextContent(
      /failed|failures/
    );
  });

  // A run that waits for a person does nothing until someone acts on it.
  it('says that a service DAG waits for input', () => {
    hook.serviceDAGs = [
      dag({
        state: ServiceDAGState.waiting,
        latestRun: {
          dagRunId: 'r1',
          status: Status.Waiting,
          startedAt: '2026-10-20T11:58:00Z',
        },
      }),
    ];

    renderStrip();

    const card = screen.getByRole('link', { name: /worker/ });
    expect(card).toHaveTextContent('Waiting for input');
    expect(card).toHaveTextContent('Waiting');
    expect(card).not.toHaveTextContent('Running');
  });

  it('says why the state of a service DAG cannot be read', () => {
    hook.serviceDAGs = [
      dag({
        state: ServiceDAGState.failing,
        readError: 'status file is corrupt',
      }),
    ];

    renderStrip();

    expect(screen.getByRole('link', { name: /worker/ })).toHaveTextContent(
      'status file is corrupt'
    );
  });
});

describe('ServiceDAGStrip runs', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    vi.setSystemTime(NOW);
    localStorage.clear();
    hook.useQuery.mockClear();
    hook.serviceDAGs = [dag({ fileName: 'ingest', name: 'ingest' })];
    hook.runs = [
      {
        name: 'ingest',
        dagRunId: 'run-2',
        status: Status.Failed,
        statusLabel: 'failed',
        startedAt: '2026-10-20T11:59:40Z',
        finishedAt: '2026-10-20T11:59:45Z',
      },
      {
        name: 'ingest',
        dagRunId: 'run-1',
        status: Status.Success,
        statusLabel: 'succeeded',
        startedAt: '2026-10-20T11:59:30Z',
        finishedAt: '2026-10-20T11:59:35Z',
      },
    ];
  });

  afterEach(() => {
    vi.useRealTimers();
    hook.serviceDAGs = [];
    hook.runs = [];
  });

  function renderStrip(expandable: boolean) {
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter>
            <AppBarContext.Provider
              value={
                {
                  selectedRemoteNode: 'local',
                  workspaceSelection: {
                    kind: WorkspaceKind.workspace,
                    workspace: 'ops',
                  },
                } as never
              }
            >
              <ServiceDAGStrip expandable={expandable} />
            </AppBarContext.Provider>
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );
  }

  // A service DAG restarts all the time, so its runs are read only on request.
  it('reads the runs of a service DAG only once its card is opened', () => {
    renderStrip(true);

    const toggle = screen.getByRole('button', { name: 'Show runs of ingest' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(hook.useQuery).not.toHaveBeenCalled();

    fireEvent.click(toggle);

    expect(
      screen.getByRole('button', { name: 'Hide runs of ingest' })
    ).toHaveAttribute('aria-expanded', 'true');
    expect(hook.useQuery).toHaveBeenCalledWith(
      '/dag-runs/{name}',
      expect.objectContaining({
        params: expect.objectContaining({ path: { name: 'ingest' } }),
      }),
      expect.anything()
    );
    // A DAG moved between workspaces keeps its runs in the old one.
    expect(hook.useQuery.mock.calls[0]?.[1]).toMatchObject({
      params: { query: { workspace: 'ops' } },
    });
    const list = screen.getByRole('list');
    expect(within(list).getAllByRole('link')).toHaveLength(2);
    expect(within(list).getByText('run-2')).toBeInTheDocument();
    expect(screen.getByText(/Last 2 runs/)).toHaveTextContent(
      'Last 2 runs · 1 succeeded · 1 failed'
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Hide runs of ingest' })
    );
    expect(screen.queryByRole('list')).toBeNull();
  });

  // A run that ended with some failures has not succeeded.
  it('counts only the runs that succeeded', () => {
    hook.runs = [
      {
        name: 'ingest',
        dagRunId: 'run-3',
        status: Status.PartialSuccess,
        statusLabel: 'partially_succeeded',
        startedAt: '2026-10-20T11:59:50Z',
        finishedAt: '2026-10-20T11:59:55Z',
      },
      hook.runs[1],
    ];
    renderStrip(true);

    fireEvent.click(
      screen.getByRole('button', { name: 'Show runs of ingest' })
    );

    expect(screen.getByText(/Last 2 runs/)).toHaveTextContent(
      'Last 2 runs · 1 succeeded'
    );
  });

  it('has no toggle unless the strip is expandable', () => {
    renderStrip(false);

    expect(screen.queryByRole('button', { name: /runs of ingest/ })).toBeNull();
  });
});
