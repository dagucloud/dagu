// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  RestartPolicy,
  ServiceDAGState,
  Status,
  StatusLabel,
  TriggerType,
} from '@/api/v1/schema';
import { Config, ConfigContext } from '@/contexts/ConfigContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { ServiceDAG } from '../../../hooks/useServiceDAGs';
import DAGRunTable from '../DAGRunTable';

// The summary reads the run history: the test gives it a day of buckets and
// notes what it asks for.
const h = vi.hoisted(() => ({
  samples: [] as Array<{
    source: unknown;
    window: unknown;
    points: number;
    poll: boolean;
  }>,
  availability: [] as unknown[],
  uptime: undefined as number | undefined,
}));

vi.mock('@/features/workflow-workspace/continuous/useContinuousData', () => ({
  useSamples: (
    source: unknown,
    window: unknown,
    points: number,
    poll: boolean
  ) => {
    h.samples.push({ source, window, points, poll });
    return {
      data: {
        resolutionSeconds: 1800,
        from: '2026-11-08T00:00:00Z',
        to: '2026-11-08T12:00:00Z',
        ...(h.uptime === undefined ? {} : { uptime: h.uptime }),
        buckets: [
          {
            time: '2026-11-08T09:00:00Z',
            count: 21_000,
            failures: 0,
            minMs: 1,
            maxMs: 2,
            p50Ms: 1,
            p95Ms: 2,
          },
          {
            time: '2026-11-08T10:00:00Z',
            count: 1_000,
            failures: 100,
            minMs: 1,
            maxMs: 2,
            p50Ms: 1,
            p95Ms: 2,
          },
        ],
      },
    };
  },
  useSampleAvailability: (source: unknown, poll: boolean) => {
    h.availability.push({ source, poll });
    return { data: { lastFailureAt: '2026-11-08T11:00:00Z' } };
  },
}));
vi.mock('../StepDetailsTooltip', () => ({
  StepDetailsTooltip: ({ children }: { children: React.ReactNode }) => (
    <>{children}</>
  ),
}));

const config = {
  apiURL: '',
  basePath: '',
  tz: 'UTC',
  tzOffsetInSec: 0,
} as Config;

const NOW = Date.parse('2026-11-08T12:00:00Z');

function summary(id: string, name: string) {
  return {
    dagRunId: id,
    name,
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    artifactsAvailable: false,
    autoRetryCount: 0,
    triggerType: TriggerType.scheduler,
    queuedAt: '2026-11-08T10:00:00Z',
    startedAt: '2026-11-08T10:00:01Z',
    finishedAt: '2026-11-08T10:00:02Z',
  };
}

const svc: ServiceDAG = {
  fileName: 'svc.yaml',
  name: 'svc',
  restartPolicy: RestartPolicy.always,
  restartWaitSec: 5,
  queue: 'svc',
  state: ServiceDAGState.running,
  suspended: false,
  consecutiveFailures: 0,
};

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function renderTable(
  props: Partial<React.ComponentProps<typeof DAGRunTable>> = {}
) {
  const table = (extra: Partial<React.ComponentProps<typeof DAGRunTable>>) => (
    <MemoryRouter>
      <LocationProbe />
      <ConfigContext.Provider value={config}>
        <UserPreferencesProvider>
          <I18nProvider>
            <DAGRunTable
              dagRuns={[summary('a1', 'alpha'), summary('b1', 'beta')]}
              serviceDags={[svc]}
              serviceWindow={{ from: NOW - 12 * 3_600_000 }}
              onToggleBulkSelect={vi.fn()}
              {...props}
              {...extra}
            />
          </I18nProvider>
        </UserPreferencesProvider>
      </ConfigContext.Provider>
    </MemoryRouter>
  );
  const view = render(table({}));
  return {
    again: (extra: Partial<React.ComponentProps<typeof DAGRunTable>>) =>
      view.rerender(table(extra)),
  };
}

beforeEach(() => {
  // The chip of a running DAG animates its text unless the user prefers less
  // motion.
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  });
  h.samples.length = 0;
  h.availability.length = 0;
  h.uptime = undefined;
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
  Object.defineProperty(window, 'innerWidth', {
    writable: true,
    configurable: true,
    value: 1024,
  });
});

describe('the row of a service DAG in the list of runs', () => {
  it('stands before the runs', () => {
    renderTable();
    const rows = screen
      .getAllByRole('row')
      .slice(1)
      .map(
        (row) =>
          row.getAttribute('data-service-dag') ??
          within(row).queryByText(/alpha|beta/)?.textContent
      );
    expect(rows).toEqual(['svc', 'alpha', 'beta']);
  });

  // A new run changes the rows below. The row stays mounted: it does not read
  // the run history again for it.
  it('stays mounted while the runs change', () => {
    const { again } = renderTable();
    const row = screen.getByTestId('service-dag');
    const asked = h.samples.length;
    again({ dagRuns: [summary('a2', 'alpha'), summary('b1', 'beta')] });
    expect(screen.getByTestId('service-dag')).toBe(row);
    expect(h.samples.length - asked).toBeLessThan(6);
  });

  it('shows the marker, the compact count, the availability and the last failure', () => {
    renderTable();
    const row = within(screen.getByTestId('service-dag'));
    expect(row.getByText('svc')).toBeInTheDocument();
    expect(row.getByText('Service')).toBeInTheDocument();
    expect(row.getByTestId('compact-count')).toHaveTextContent('22k');
    expect(row.getByTestId('dag-service-availability')).toHaveTextContent(
      '99.54 %'
    );
    expect(row.getByTestId('service-dag-last-failure')).toHaveTextContent(
      /Last failure.*ago/
    );
  });

  // The summary asks the samples of its window: they carry its uptime.
  it('shows the uptime of the window beside the count', () => {
    h.uptime = 0.5;
    renderTable();
    const row = within(screen.getByTestId('service-dag'));
    expect(row.getByTestId('dag-service-uptime')).toHaveTextContent(
      'Uptime 50.00 %'
    );
  });

  // The strip draws one cell for each slot of the window, 60 at the most.
  it('draws at most 60 cells whatever the count of runs', () => {
    renderTable();
    const strip = screen.getByTestId('dag-activity');
    expect(strip.children.length).toBeLessThanOrEqual(60);
    expect(strip).toHaveAttribute('data-runs', '22000');
  });

  it('asks the run history for the window of the filters, once', () => {
    renderTable();
    expect(h.samples[0]).toMatchObject({
      source: { kind: 'run-history', fileName: 'svc.yaml' },
      window: { since: NOW - 12 * 3_600_000 },
      points: 60,
    });
    expect(h.availability[0]).toMatchObject({
      source: { kind: 'run-history', fileName: 'svc.yaml' },
    });
  });

  it('asks for fixed times when the filters give an end date', () => {
    const from = NOW - 48 * 3_600_000;
    const to = NOW - 24 * 3_600_000;
    renderTable({ serviceWindow: { from, to } });
    expect(h.samples[0]!.window).toEqual({ from, to });
  });

  // The chip and the wait tell what the DAG does now.
  it.each([
    [ServiceDAGState.pending, 'Pending'],
    [ServiceDAGState.running, 'Healthy'],
    [ServiceDAGState.restarting, 'Healthy'],
    [ServiceDAGState.failing, 'Failing'],
    [ServiceDAGState.suspended, 'Suspended'],
    [ServiceDAGState.stopped, 'Stopped'],
  ])('shows the state %s and the restart wait', (state, label) => {
    renderTable({ serviceDags: [{ ...svc, state }] });
    const shown = within(screen.getByTestId('service-dag-state'));
    expect(screen.getByTestId('service-dag-state')).toHaveAttribute(
      'data-state',
      state
    );
    expect(shown.getByText(label)).toBeInTheDocument();
    expect(shown.getByText('Wait 5 s')).toBeInTheDocument();
  });

  it('shows no failure for a DAG that does not fail', () => {
    renderTable({
      serviceDags: [
        {
          ...svc,
          state: ServiceDAGState.running,
          consecutiveFailures: 2,
          latestRun: { dagRunId: 'r', status: Status.Failed, error: 'boom' },
        },
      ],
    });
    expect(screen.queryByTestId('service-dag-failure')).toBeNull();
  });

  it('shows the failures in a row and the error of a failing DAG', () => {
    renderTable({
      serviceDags: [
        {
          ...svc,
          state: ServiceDAGState.failing,
          consecutiveFailures: 3,
          latestRun: {
            dagRunId: 'r',
            status: Status.Failed,
            error: 'step build: exit status 2',
          },
        },
      ],
    });
    const failure = within(screen.getByTestId('service-dag-failure'));
    expect(failure.getByText('3 failed in a row')).toBeInTheDocument();
    expect(failure.getByText('step build: exit status 2')).toBeInTheDocument();
  });

  it('shows the read error before the error of the run, and 10+ at the cap', () => {
    renderTable({
      serviceDags: [
        {
          ...svc,
          state: ServiceDAGState.failing,
          consecutiveFailures: 10,
          readError: 'cannot read the status',
          latestRun: { dagRunId: 'r', status: Status.Failed, error: 'boom' },
        },
      ],
    });
    const failure = within(screen.getByTestId('service-dag-failure'));
    expect(failure.getByText('10+ failed in a row')).toBeInTheDocument();
    expect(failure.getByText('cannot read the status')).toBeInTheDocument();
    expect(failure.queryByText('boom')).toBeNull();
  });

  it('shows a failing DAG without a streak by its read error only', () => {
    renderTable({
      serviceDags: [
        {
          ...svc,
          state: ServiceDAGState.failing,
          readError: 'cannot read the status',
        },
      ],
    });
    const failure = within(screen.getByTestId('service-dag-failure'));
    expect(failure.queryByText(/failed in a row/)).toBeNull();
    expect(failure.getByText('cannot read the status')).toBeInTheDocument();
  });

  // The page of the DAG opens, as the row of the Workflows list opens it.
  it('opens the page of the DAG on a click of the row', () => {
    renderTable();
    fireEvent.click(screen.getByTestId('service-dag'));
    expect(screen.getByTestId('location')).toHaveTextContent('/dags/svc.yaml');
  });

  // A service DAG is a row of the list: the list is not empty when only
  // service DAGs remain, and the selection reaches the runs, not the row.
  it('keeps the list when only service DAGs remain', () => {
    renderTable({ dagRuns: [] });
    expect(screen.queryByText('No runs found')).toBeNull();
    expect(screen.getByTestId('service-dag')).toBeInTheDocument();
    expect(screen.queryAllByRole('checkbox')).toHaveLength(1);
  });

  it('shows a card on a narrow screen', () => {
    Object.defineProperty(window, 'innerWidth', {
      writable: true,
      configurable: true,
      value: 400,
    });
    renderTable({
      serviceDags: [
        {
          ...svc,
          state: ServiceDAGState.failing,
          consecutiveFailures: 1,
          latestRun: { dagRunId: 'r', status: Status.Failed, error: 'boom' },
        },
      ],
    });
    const card = screen.getByTestId('service-dag');
    expect(card.tagName).toBe('DIV');
    expect(within(card).getByText('Service')).toBeInTheDocument();
    expect(within(card).getByText('Failing')).toBeInTheDocument();
    expect(within(card).getByText('boom')).toBeInTheDocument();
    fireEvent.click(card);
    expect(screen.getByTestId('location')).toHaveTextContent('/dags/svc.yaml');
  });

  it('draws no row without a window', () => {
    renderTable({ serviceWindow: undefined });
    expect(screen.queryByTestId('service-dag')).toBeNull();
  });
});
