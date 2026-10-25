// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RestartPolicy,
  AlwaysOnDAGState,
  Status,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { describeAlwaysOn } from '../AlwaysOnState';
import { AlwaysOnStrip } from '../AlwaysOnStrip';

type AlwaysOnDAG = components['schemas']['AlwaysOnDAG'];

const hook = vi.hoisted(() => ({
  alwaysOnDAGs: [] as AlwaysOnDAG[],
  runs: [] as unknown[],
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (...args: unknown[]) => {
    hook.useQuery(...args);
    return { data: { dagRuns: hook.runs }, error: undefined, isLoading: false };
  },
}));

vi.mock('../../hooks/useAlwaysOnDAGs', () => ({
  useAlwaysOnDAGs: () => ({ alwaysOnDAGs: hook.alwaysOnDAGs, error: null }),
}));

const NOW = Date.parse('2026-10-20T12:00:00Z');

function dag(overrides: Partial<AlwaysOnDAG> = {}): AlwaysOnDAG {
  return {
    fileName: 'worker',
    name: 'worker',
    restartPolicy: RestartPolicy.always,
    restartWaitSec: 5,
    state: AlwaysOnDAGState.running,
    suspended: false,
    ...overrides,
  };
}

describe('describeAlwaysOn', () => {
  it('counts the time since a live run started', () => {
    const live = dag({
      latestRun: {
        dagRunId: 'r1',
        status: Status.Running,
        startedAt: '2026-10-20T11:58:46Z',
      },
    });

    expect(describeAlwaysOn(live, NOW)).toEqual({
      text: 'Up for {duration}',
      values: { duration: '1m 14s' },
    });
  });

  it('counts down to the next run after a run ends', () => {
    const ended = dag({
      state: AlwaysOnDAGState.restarting,
      latestRun: {
        dagRunId: 'r1',
        status: Status.Success,
        startedAt: '2026-10-20T11:59:50Z',
        finishedAt: '2026-10-20T11:59:57Z',
      },
    });

    expect(describeAlwaysOn(ended, NOW)).toEqual({
      text: 'Restart in {seconds}s',
      values: { seconds: 2 },
    });
    expect(describeAlwaysOn(ended, NOW + 5000).text).toBe('Restarting…');
  });

  it('says why no run follows', () => {
    expect(describeAlwaysOn(dag({ state: AlwaysOnDAGState.stopped }), NOW).text).toBe(
      'Stopped by an operator'
    );
    expect(describeAlwaysOn(dag({ state: AlwaysOnDAGState.suspended }), NOW).text).toBe(
      'No new run'
    );
    expect(describeAlwaysOn(dag({ state: AlwaysOnDAGState.pending }), NOW).text).toBe(
      'Waiting for a run'
    );
  });
});

describe('AlwaysOnStrip', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    vi.setSystemTime(NOW);
    localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    hook.alwaysOnDAGs = [];
  });

  function renderStrip() {
    return render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter>
            <AlwaysOnStrip />
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );
  }

  it('shows nothing without an always-on DAG', () => {
    const { container } = renderStrip();

    expect(container).toBeEmptyDOMElement();
  });

  it('shows one card for each always-on DAG, linked to its DAG', () => {
    hook.alwaysOnDAGs = [
      dag({
        fileName: 'ingest',
        name: 'ingest',
        state: AlwaysOnDAGState.failing,
        latestRun: {
          dagRunId: 'r1',
          status: Status.Failed,
          startedAt: '2026-10-20T11:59:50Z',
          finishedAt: '2026-10-20T11:59:58Z',
        },
      }),
      dag({ fileName: 'my dag', name: 'my dag', state: AlwaysOnDAGState.suspended }),
    ];

    renderStrip();

    expect(screen.getByRole('region', { name: 'Always on' })).toBeVisible();
    const ingest = screen.getByRole('link', { name: /ingest/ });
    expect(ingest).toHaveAttribute('href', '/dags/ingest');
    expect(ingest).toHaveTextContent('Failing');
    expect(ingest).toHaveTextContent('Restart in 3s');
    expect(ingest).toHaveAttribute(
      'title',
      'Restarts 5s after a run ends'
    );
    expect(screen.getByRole('link', { name: /my dag/ })).toHaveAttribute(
      'href',
      '/dags/my%20dag'
    );
  });
});

describe('AlwaysOnStrip runs', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    vi.setSystemTime(NOW);
    localStorage.clear();
    hook.useQuery.mockClear();
    hook.alwaysOnDAGs = [dag({ fileName: 'ingest', name: 'ingest' })];
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
    hook.alwaysOnDAGs = [];
    hook.runs = [];
  });

  function renderStrip(expandable: boolean) {
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <MemoryRouter>
            <AlwaysOnStrip expandable={expandable} />
          </MemoryRouter>
        </I18nProvider>
      </UserPreferencesProvider>
    );
  }

  // An always-on DAG restarts all the time, so its runs are read only on request.
  it('reads the runs of an always-on DAG only once its card is opened', () => {
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
    const list = screen.getByRole('list');
    expect(within(list).getAllByRole('link')).toHaveLength(2);
    expect(within(list).getByText('run-2')).toBeInTheDocument();
    expect(screen.getByText(/Last 2 runs/)).toHaveTextContent(
      'Last 2 runs · 1 succeeded · 1 failed'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Hide runs of ingest' }));
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

    fireEvent.click(screen.getByRole('button', { name: 'Show runs of ingest' }));

    expect(screen.getByText(/Last 2 runs/)).toHaveTextContent(
      'Last 2 runs · 1 succeeded'
    );
  });

  it('has no toggle unless the strip is expandable', () => {
    renderStrip(false);

    expect(screen.queryByRole('button', { name: /runs of ingest/ })).toBeNull();
  });
});
