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
import { presetsFor } from '../../rangePresets';
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

  // A panel with period buttons starts at 24 hours. "All" starts at the start
  // of a run: a history has none, so it has no such button.
  it('offers the period buttons without All for a history', () => {
    render(panel({ range: undefined, presets: presetsFor('service') }));
    const group = within(screen.getByLabelText('Period'));
    expect(group.getAllByRole('button').map((b) => b.textContent)).toEqual([
      '1h',
      '6h',
      '24h',
      '7d',
      '30d',
      '1y',
      '3y',
    ]);
    expect(lastSamples().window).toMatchObject({ preset: '24h' });
    fireEvent.click(group.getByText('30d'));
    expect(lastSamples().window).toMatchObject({ preset: '30d' });
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

  // Chart of 3 years in a 640 px panel: one element for a bucket, none for
  // a run (600 runs per bucket here, 360 000 in all).
  it('draws one bar for each bucket of the response and no more', () => {
    h.responses.samples = series(600, 10);
    render(panel({ range: { preset: '3y' } }));
    const chart = screen.getByTestId('continuous-chart');
    expect(chart).toHaveAttribute('data-buckets', '600');
    expect(chart.querySelectorAll('rect')).toHaveLength(600 + 60);
    const inChart = chart.querySelectorAll('*').length;
    // The bars, the axis and the labels: the same order as the buckets.
    expect(inChart).toBeLessThan(800);
    console.info(
      `history chart, 600 buckets: ${inChart} elements in the chart, ` +
        `${document.body.querySelectorAll('*').length} in the page`
    );
  });

  it('shows the empty state of the window', () => {
    h.responses.samples = series(0);
    render(panel());
    expect(screen.getByTestId('continuous-empty')).toHaveTextContent(
      'No runs in this window'
    );
  });

  // A bucket that has up time and no run is not a sample: no bar, and the
  // window still has no runs.
  it('shows the empty state when the buckets have up time only', () => {
    const only = series(3);
    h.responses.samples = {
      ...only,
      buckets: only.buckets.map((b) => ({ ...b, count: 0, upMs: 3_600_000 })),
    };
    render(panel());
    expect(screen.getByTestId('continuous-empty')).toBeInTheDocument();
  });

  it('gives the window that the chart asks to the note of the host', () => {
    const notice = vi.fn<(window: { from: number; to: number }) => ReactNode>(
      () => <p data-testid="note">note</p>
    );
    render(panel({ notice }));
    expect(screen.getByTestId('note')).toBeInTheDocument();
    const window = notice.mock.calls[0]![0];
    expect(window.to - window.from).toBe(7 * 86_400_000);
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
      'aaaa-113000',
      'bbbb-110000',
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
