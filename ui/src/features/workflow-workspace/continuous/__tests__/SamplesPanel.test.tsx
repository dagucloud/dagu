// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AvailabilityWindowWindow,
  RunHistoryEventKind,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { runHistorySource, stepSource } from '../sampleSource';
import { SamplesPanel, type SamplesPanelProps } from '../SamplesPanel';
import type { SamplesWindow } from '../useContinuousData';

type SampleSeries = components['schemas']['SampleSeries'];
type SampleAvailability = components['schemas']['SampleAvailability'];
type RunHistoryEvents = components['schemas']['RunHistoryEvents'];

// useQuery answers by path; useSamples records what the panel asks.
const h = vi.hoisted(() => ({
  responses: {} as Record<string, unknown>,
  samples: [] as Array<{
    source: unknown;
    window: SamplesWindow;
    points: number;
    poll: boolean;
  }>,
  paths: [] as string[],
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: async () => ({}) }),
  useQuery: (path: string, init: unknown) => {
    if (init === null) return { data: undefined, error: undefined };
    h.paths.push(path);
    return {
      data: h.responses[path],
      error: undefined,
      mutate: () => Promise.resolve(undefined),
    };
  },
}));
vi.mock('../useContinuousData', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../useContinuousData')>()),
  useSamples: (
    source: unknown,
    window: SamplesWindow,
    points: number,
    poll: boolean
  ) => {
    h.samples.push({ source, window, points, poll });
    return {
      data: h.responses.samples,
      error: undefined,
      mutate: () => Promise.resolve(undefined),
    };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const NOW = Date.parse('2026-11-08T12:00:00Z');
const AVAIL = '/dags/{fileName}/run-history/availability';
const EVENTS = '/dags/{fileName}/run-history/events';

const availability: SampleAvailability = {
  windows: [
    {
      window: AvailabilityWindowWindow.Value24h,
      count: 14_400,
      failures: 3,
      availability: 0.9998,
      uptime: 0.9712,
    },
    {
      window: AvailabilityWindowWindow.Value7d,
      count: 100_000,
      failures: 40_000,
      availability: 0.6,
    },
  ],
  streak: 9_000,
  lastFailureAt: '2026-11-08T11:00:00Z',
  totalCount: 1_200_000,
};

function series(count: number, failEvery = 0): SampleSeries {
  return {
    resolutionSeconds: 3600,
    from: new Date(NOW - 3 * 365 * 86_400_000).toISOString(),
    to: new Date(NOW).toISOString(),
    buckets: Array.from({ length: count }, (_, i) => ({
      time: new Date(NOW - (count - i) * 3_600_000).toISOString(),
      count: 600,
      failures: failEvery && i % failEvery === 0 ? 2 : 0,
      minMs: 100,
      maxMs: 900,
      p50Ms: 300,
      p95Ms: 700,
    })),
  };
}

const events: RunHistoryEvents = {
  events: [
    {
      time: '2026-11-08T11:30:00Z',
      kind: RunHistoryEventKind.gap,
      runId: 'restart-aaaaaaaaaaaa-20261108T113000',
      gapSeconds: 90,
    },
    {
      time: '2026-11-08T11:00:00Z',
      kind: RunHistoryEventKind.failed,
      runId: 'restart-bbbbbbbbbbbb-20261108T110000',
      message: 'connection refused',
    },
  ],
};

const history = runHistorySource('svc.yaml');

function panel(props: Partial<SamplesPanelProps> = {}) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>
        <SamplesPanel
          source={history}
          active={false}
          range={{ preset: '7d' }}
          {...props}
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

const lastSamples = () => h.samples[h.samples.length - 1]!;

beforeEach(() => {
  vi.useFakeTimers({
    toFake: [
      'Date',
      'setInterval',
      'clearInterval',
      'setTimeout',
      'clearTimeout',
    ],
  });
  vi.setSystemTime(NOW);
  h.samples.length = 0;
  h.paths.length = 0;
  h.responses = { [AVAIL]: availability, [EVENTS]: events, samples: series(5) };
});
afterEach(() => {
  vi.useRealTimers();
});

describe('SamplesPanel with a run history', () => {
  it('reads the history routes and asks for the window of the host', () => {
    render(panel());
    expect(h.paths).toContain(AVAIL);
    expect(h.paths).toContain(EVENTS);
    expect(lastSamples().source).toEqual(history);
    expect(lastSamples().window).toEqual({
      preset: '7d',
      startedAt: undefined,
    });
  });

  // The host chooses the window: no period buttons of its own.
  it('shows no period buttons when the host chooses the range', () => {
    render(panel());
    expect(screen.queryByLabelText('Period')).toBeNull();
  });

  // A service DAG has two figures for each window: the share of the window in
  // which a run was active, and the share of the runs that did not fail.
  it('shows the uptime and the success rate of each window', () => {
    render(panel());
    const strip = within(screen.getByTestId('continuous-availability'));
    expect(strip.getByTestId('continuous-uptime-24h')).toHaveTextContent(
      'Uptime 24h97.12 %'
    );
    expect(strip.getByTestId('continuous-availability-24h')).toHaveTextContent(
      'Success rate 24h99.98 %'
    );
    // A window with no uptime reads a dash, not 0 %.
    expect(strip.getByTestId('continuous-uptime-7d')).toHaveTextContent('-');
    expect(strip.getByTestId('continuous-availability-7d')).toHaveTextContent(
      '60.00 %'
    );
    expect(strip.getByTestId('continuous-uptime-365d')).toHaveTextContent('-');
  });

  it('shows the runs, the streak and the compact count of the history', () => {
    render(panel());
    const strip = within(screen.getByTestId('continuous-availability'));
    expect(strip.getByTestId('continuous-total')).toHaveTextContent('Runs');
    expect(
      within(strip.getByTestId('continuous-total')).getByTestId('compact-count')
    ).toHaveTextContent('1.2M');
    expect(
      within(strip.getByTestId('continuous-streak')).getByTestId(
        'compact-count'
      )
    ).toHaveTextContent('9k');
    expect(strip.getByTestId('continuous-last-failure')).not.toHaveTextContent(
      'None'
    );
  });

  it('starts again at the window that the host chooses next', () => {
    const view = render(panel());
    const from = NOW - 86_400_000;
    view.rerender(panel({ range: { from, to: NOW } }));
    // A fixed window waits for the rest that a zoom has.
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(lastSamples().window).toEqual({ from, to: NOW });
    view.rerender(panel({ range: { preset: '1y' } }));
    expect(lastSamples().window).toEqual({
      preset: '1y',
      startedAt: undefined,
    });
  });

  it('shows the empty state of the window', () => {
    h.responses.samples = series(0);
    render(panel());
    expect(screen.getByTestId('continuous-empty')).toHaveTextContent(
      'No runs in this window'
    );
  });

  // A run in progress has no sample until it ends: the buckets of a window
  // that it fills have up time only. Its band shows it, and opens it.
  it('shows the run in progress in place of the empty state', () => {
    const only = series(3);
    h.responses.samples = {
      ...only,
      buckets: only.buckets.map((b) => ({ ...b, count: 0, upMs: 3_600_000 })),
    };
    const onOpenRun = vi.fn();
    render(
      panel({
        liveRun: { id: 'run-1', startedAt: NOW - 3 * 86_400_000 },
        onOpenRun,
      })
    );
    expect(screen.queryByTestId('continuous-empty')).toBeNull();
    const band = screen.getByTestId('continuous-live-run');
    expect(band).toHaveAccessibleName(/^Running since /);
    fireEvent.click(band);
    expect(onOpenRun).toHaveBeenCalledWith('run-1');
  });

  // A window in the past ends before the run in progress started.
  it('shows the empty state of a window before the run in progress', () => {
    h.responses.samples = {
      ...series(0),
      from: new Date(NOW - 2 * 86_400_000).toISOString(),
      to: new Date(NOW - 86_400_000).toISOString(),
    };
    render(panel({ liveRun: { id: 'run-1', startedAt: NOW - 3_600_000 } }));
    expect(screen.getByTestId('continuous-empty')).toBeInTheDocument();
  });

  // The server cuts the window to the samples it has: the note follows the
  // window that the chart draws, not the one that it asked for.
  it('gives the window that the chart shows to the note of the host', () => {
    const from = NOW - 2 * 86_400_000;
    h.responses.samples = { ...series(5), from: new Date(from).toISOString() };
    const notice = vi.fn<(window: { from: number; to: number }) => ReactNode>(
      () => <p data-testid="note">note</p>
    );
    render(panel({ notice }));
    expect(screen.getByTestId('note')).toBeInTheDocument();
    expect(notice).toHaveBeenLastCalledWith({ from, to: NOW });
  });
});

describe('the events of a run history', () => {
  const links = (retained: string[]) => ({
    has: (id: string) => retained.includes(id),
    open: vi.fn(),
  });

  it('shows the gap and the run of each event', () => {
    render(panel({ links: links([]) }));
    const rows = screen.getAllByTestId('continuous-event');
    expect(rows.map((r) => r.getAttribute('data-kind'))).toEqual([
      'gap',
      'failed',
    ]);
    expect(rows[0]).toHaveTextContent('Gap');
    expect(rows[0]).toHaveTextContent('no run for 1m 30s');
    expect(rows[1]).toHaveTextContent('connection refused');
    // The scheduler ID is short and tells the runs apart.
    const ids = screen.getAllByTestId('continuous-event-run');
    expect(ids.map((i) => i.textContent)).toEqual([
      '1108T113000',
      '1108T110000',
    ]);
  });

  it('links a retained run and writes a dropped run as text', () => {
    const open = links(['restart-bbbbbbbbbbbb-20261108T110000']);
    render(panel({ links: open }));
    const ids = screen.getAllByTestId('continuous-event-run');
    expect(ids[0]!.tagName).toBe('SPAN');
    expect(ids[1]!.tagName).toBe('BUTTON');
    fireEvent.click(ids[1]!);
    expect(open.open).toHaveBeenCalledWith(
      'restart-bbbbbbbbbbbb-20261108T110000'
    );
  });

  it('writes every run as text when the host has no links', () => {
    render(panel());
    for (const id of screen.getAllByTestId('continuous-event-run')) {
      expect(id.tagName).toBe('SPAN');
    }
  });
});

describe('SamplesPanel with a step', () => {
  // A step has no run ID in its events and counts executions.
  it('keeps the labels of the executions of a step', () => {
    h.responses = {
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/availability': availability,
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/events': { events: [] },
      samples: series(3),
    };
    render(
      panel({
        source: stepSource('demo', 'run-1', 'beat'),
        range: undefined,
      })
    );
    expect(screen.getByTestId('continuous-total')).toHaveTextContent(
      'Executions'
    );
    expect(screen.queryByTestId('continuous-event-run')).toBeNull();
  });
});
