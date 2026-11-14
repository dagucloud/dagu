// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  AvailabilityWindowWindow,
  StepEventKind,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { ContinuousPanel } from '../ContinuousPanel';
import type { SamplesWindow } from '../useContinuousData';

type StepAvailability = components['schemas']['StepAvailability'];
type StepEvents = components['schemas']['StepEvents'];
type StepSamples = components['schemas']['StepSamples'];
type Node = components['schemas']['Node'];

// useQuery and useStepSamples are answered by path. A value of null is an
// error, undefined is still loading.
const h = vi.hoisted(() => ({
  responses: {} as Record<string, unknown>,
  calls: [] as Array<{
    path: string;
    query: Record<string, unknown>;
    config: Record<string, unknown> | undefined;
  }>,
  samples: [] as Array<{
    window: SamplesWindow;
    points: number;
    poll: boolean;
  }>,
  /** The paths that the panel read again with mutate. */
  reads: [] as string[],
}));

function answer(data: unknown, path: string) {
  const mutate = () => {
    h.reads.push(path);
    return Promise.resolve(undefined);
  };
  return data === null
    ? { data: undefined, error: new Error('failed'), mutate }
    : { data, error: undefined, mutate };
}

vi.mock('@/hooks/api', () => ({
  useQuery: (
    path: string,
    init: { params: { query: Record<string, unknown> } },
    config?: Record<string, unknown>
  ) => {
    h.calls.push({ path, query: init.params.query, config });
    const data =
      typeof h.responses[path] === 'function'
        ? (h.responses[path] as (q: Record<string, unknown>) => unknown)(
            init.params.query
          )
        : h.responses[path];
    return answer(data, path);
  },
}));
// The samples hook has tests of its own: here it records what the panel asks.
vi.mock('../useContinuousData', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../useContinuousData')>()),
  useStepSamples: (
    _target: unknown,
    window: SamplesWindow,
    points: number,
    poll: boolean
  ) => {
    h.samples.push({ window, points, poll });
    return answer(h.responses[SAMPLES], SAMPLES);
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const AVAIL = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/availability';
const SAMPLES = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/samples';
const EVENTS = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/events';

const NOW = Date.parse('2026-10-28T12:00:00Z');

const node = {
  step: { name: 'heartbeat' },
  startedAt: '2026-10-01T00:00:00Z',
  continuous: true,
} as unknown as Node;

const availability: StepAvailability = {
  windows: [
    {
      window: AvailabilityWindowWindow.Value24h,
      count: 86400,
      failures: 17,
      availability: 0.9998,
    },
    {
      window: AvailabilityWindowWindow.Value7d,
      count: 604800,
      failures: 100,
      availability: 0.9998348,
    },
    {
      window: AvailabilityWindowWindow.Value30d,
      count: 2592000,
      failures: 0,
      availability: 1,
    },
    { window: AvailabilityWindowWindow.Value365d, count: 0, failures: 0 },
  ],
  streak: 1234567,
  lastFailureAt: '2026-10-28T11:00:00Z',
  totalCount: 31_000_000,
};

function samples(count: number, failEvery = 0): StepSamples {
  return {
    resolutionSeconds: 60,
    from: new Date(NOW - 86_400_000).toISOString(),
    to: new Date(NOW).toISOString(),
    buckets: Array.from({ length: count }, (_, i) => ({
      time: new Date(NOW - (count - i) * 60_000).toISOString(),
      count: 60,
      failures: failEvery && i % failEvery === 0 ? 4 : 0,
      minMs: 3,
      maxMs: 90,
      p50Ms: 12,
      p95Ms: 40,
    })),
  };
}

const events: StepEvents = {
  events: [
    {
      time: '2026-10-28T11:30:00Z',
      kind: StepEventKind.recovered,
      failedCount: 120,
      outageSeconds: 125,
    },
    {
      time: '2026-10-28T11:28:00Z',
      kind: StepEventKind.failed,
      exitCode: 7,
      message: 'connection refused',
    },
  ],
  nextCursor: 'cursor-2',
};

/** The x of the last bar: the right end of the plot. */
function plotEnd(svg: SVGElement): number {
  return Number(svg.getAttribute('width')) - 1;
}

/** The latest window that the panel asked the samples of. */
function lastSamples() {
  return h.samples[h.samples.length - 1]!;
}

const STARTED_MS = Date.parse('2026-10-01T00:00:00Z');

function panel(active: boolean) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>
        <ContinuousPanel
          name="demo"
          runId="run-1"
          node={node}
          active={active}
          startedAt="2026-10-01T00:00:00Z"
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

function renderPanel(active = false) {
  return render(panel(active));
}

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
  h.calls.length = 0;
  h.samples.length = 0;
  h.reads.length = 0;
  h.responses = {};
});
afterEach(() => {
  vi.useRealTimers();
});

describe('ContinuousPanel', () => {
  it('shows the availability, the streak, the last failure and the count', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel();
    const strip = within(screen.getByTestId('continuous-availability'));
    expect(strip.getByTestId('continuous-availability-24h')).toHaveTextContent(
      '99.98 %'
    );
    expect(strip.getByTestId('continuous-availability-7d')).toHaveTextContent(
      '99.98 %'
    );
    expect(strip.getByTestId('continuous-availability-30d')).toHaveTextContent(
      '100.00 %'
    );
    // No sample in the window: no number.
    expect(strip.getByTestId('continuous-availability-365d')).toHaveTextContent(
      '-'
    );
    const streak = within(strip.getByTestId('continuous-streak'));
    expect(streak.getByTestId('compact-count')).toHaveTextContent('1.2M');
    expect(streak.getByTestId('compact-count')).toHaveAttribute(
      'title',
      '1,234,567'
    );
    const total = within(strip.getByTestId('continuous-total'));
    expect(total.getByTestId('compact-count')).toHaveTextContent('31M');
    expect(total.getByTestId('compact-count')).toHaveAttribute(
      'title',
      '31,000,000'
    );
    expect(strip.getByTestId('continuous-last-failure')).not.toHaveTextContent(
      'None'
    );
  });

  it('draws one bar for each bucket, and no more', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(300, 10),
      [EVENTS]: events,
    };
    renderPanel();
    const chart = screen.getByTestId('continuous-chart');
    expect(chart).toHaveAttribute('data-buckets', '300');
    // 300 p95 bars, a red part for every 10th bucket.
    expect(chart.querySelectorAll('rect')).toHaveLength(300 + 30);
    expect(screen.getAllByTestId('continuous-fail')).toHaveLength(30);
  });

  // The width of the chart gives `points`: 640 px of default minus the axis.
  it('asks for the buckets of the last 24 hours, at most one for each 4 px', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel();
    const call = h.samples[0]!;
    expect(call.points).toBe(Math.floor((640 - 24 - 44) / 4));
    expect(call.window).toEqual({ preset: '24h', startedAt: STARTED_MS });
  });

  it('shows a bar tooltip with the bucket numbers on hover', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(300, 10),
      [EVENTS]: events,
    };
    renderPanel();
    const svg = screen.getByTestId('continuous-chart').querySelector('svg')!;
    svg.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 572, height: 96 }) as DOMRect;
    // The last bar is at the right end of the plot.
    fireEvent.pointerMove(svg, { clientX: plotEnd(svg), clientY: 40 });
    const tip = within(screen.getByTestId('continuous-tooltip'));
    expect(
      tip.getByText('Slow tail (p95)').nextElementSibling
    ).toHaveTextContent('40ms');
    expect(tip.getByText('Median (p50)').nextElementSibling).toHaveTextContent(
      '12ms'
    );
    expect(tip.getByText('Executions').nextElementSibling).toHaveTextContent(
      '60'
    );
  });

  it('shows the empty state when no sample is there', () => {
    h.responses = {
      [AVAIL]: { windows: [], streak: 0, totalCount: 0 },
      [SAMPLES]: samples(0),
      [EVENTS]: { events: [] },
    };
    renderPanel();
    expect(screen.getByTestId('continuous-empty')).toHaveTextContent(
      'No samples yet'
    );
    expect(screen.queryByTestId('continuous-chart')).toBeNull();
    expect(screen.getByText('No events yet')).toBeInTheDocument();
  });

  it('shows an error when no query answers', () => {
    h.responses = { [AVAIL]: null, [SAMPLES]: null, [EVENTS]: null };
    renderPanel();
    expect(screen.getAllByRole('alert')[0]).toHaveTextContent(
      'Could not load the samples of this step'
    );
  });

  it('shows loading text while the queries are open', () => {
    renderPanel();
    expect(screen.getByText('Loading availability...')).toBeInTheDocument();
    expect(screen.getByText('Loading samples...')).toBeInTheDocument();
  });

  // The first page shows at once; Load more asks the next page with the cursor.
  it('lists the events and loads the next page with the cursor', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: (query: Record<string, unknown>) =>
        query.before === 'cursor-2'
          ? {
              events: [
                { time: '2026-10-28T10:00:00Z', kind: StepEventKind.started },
              ],
            }
          : events,
    };
    renderPanel();
    const rows = screen.getAllByTestId('continuous-event');
    expect(rows.map((r) => r.getAttribute('data-kind'))).toEqual([
      'recovered',
      'failed',
    ]);
    expect(rows[0]).toHaveTextContent('120 failed');
    expect(rows[0]).toHaveTextContent('outage 2m 5s');
    expect(rows[1]).toHaveTextContent('exit 7');
    expect(rows[1]).toHaveTextContent('connection refused');

    fireEvent.click(screen.getByTestId('continuous-events-more'));
    expect(
      screen
        .getAllByTestId('continuous-event')
        .map((r) => r.getAttribute('data-kind'))
    ).toEqual(['recovered', 'failed', 'started']);
    expect(screen.queryByTestId('continuous-events-more')).toBeNull();
    const second = h.calls.filter(
      (c) => c.path === EVENTS && c.query.before === 'cursor-2'
    );
    expect(second.length).toBeGreaterThan(0);
  });

  it('lists the events of a bucket after a click on it', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(300),
      [EVENTS]: events,
    };
    renderPanel();
    const svg = screen.getByTestId('continuous-chart').querySelector('svg')!;
    svg.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 572, height: 96 }) as DOMRect;
    fireEvent.pointerDown(svg, { clientX: plotEnd(svg), pointerId: 1 });
    fireEvent.pointerUp(svg, { clientX: plotEnd(svg), pointerId: 1 });
    const call = h.calls
      .filter((c) => c.path === EVENTS)
      .find((c) => c.query.from !== undefined)!;
    expect(
      Date.parse(call.query.to as string) -
        Date.parse(call.query.from as string)
    ).toBe(60_000);
    fireEvent.click(screen.getByRole('button', { name: 'Show all events' }));
    expect(
      screen.queryByRole('button', { name: 'Show all events' })
    ).toBeNull();
  });

  it('asks for another window when a preset is chosen', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel();
    fireEvent.click(screen.getByRole('button', { name: '30d' }));
    expect(lastSamples().window).toEqual({
      preset: '30d',
      startedAt: STARTED_MS,
    });
  });

  // The latency labels take the left 44 px of the chart: a zoom at the left
  // edge of the plot keeps the start of the window.
  it('zooms around the time under the pointer', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel();
    const chart = screen.getByTestId('continuous-chart');
    chart.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 44 + 572, height: 124 }) as DOMRect;
    fireEvent.wheel(chart, { deltaY: -100, clientX: 44 });
    // Past the rest that a zoom waits for.
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    const window = lastSamples().window as { from: number; to: number };
    expect(window.from).toBe(NOW - 86_400_000);
    expect(window.to).toBeLessThan(NOW);
  });

  // A horizontal swipe has no vertical delta: it neither zooms nor ends
  // the live preset, and the page keeps the scroll.
  it('ignores a wheel without a vertical delta', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel();
    const before = lastSamples().window;
    const chart = screen.getByTestId('continuous-chart');
    chart.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 44 + 572, height: 124 }) as DOMRect;
    expect(
      fireEvent.wheel(chart, { deltaX: 80, deltaY: 0, clientX: 300 })
    ).toBe(true);
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(lastSamples().window).toEqual(before);
    expect(before).toHaveProperty('preset');
  });

  // A drag pans on each move: the window is asked for once the drag rests,
  // so the moves leave no request and no cache entry of their own.
  it('asks for a dragged window once it rests', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(300),
      [EVENTS]: events,
    };
    renderPanel();
    const keys = () =>
      new Set(h.samples.map((c) => JSON.stringify([c.window, c.points])));
    const svg = screen.getByTestId('continuous-chart').querySelector('svg')!;
    // A drag to the right moves the window back in time.
    fireEvent.pointerDown(svg, { clientX: 300, pointerId: 1 });
    for (const x of [320, 340, 360, 380]) {
      fireEvent.pointerMove(svg, { clientX: x, pointerId: 1 });
    }
    fireEvent.pointerUp(svg, { clientX: 380, pointerId: 1 });
    expect(keys().size).toBe(1);
    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(keys().size).toBe(2);
    expect(lastSamples().window).toHaveProperty('to');
  });

  // The browser can take the pointer during a drag and send no pointerup: the
  // drag ends, and a later move points at a bar again.
  it('ends a drag that the browser cancels', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(300),
      [EVENTS]: events,
    };
    renderPanel();
    const svg = screen.getByTestId('continuous-chart').querySelector('svg')!;
    svg.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 572, height: 96 }) as DOMRect;
    fireEvent.pointerDown(svg, { clientX: 10, pointerId: 1 });
    fireEvent.pointerCancel(svg, { pointerId: 1 });
    fireEvent.pointerMove(svg, { clientX: plotEnd(svg), clientY: 40 });
    expect(screen.getByTestId('continuous-tooltip')).toBeInTheDocument();
  });

  // Polling: a short preset of an active run polls; a long one is read once.
  // A preset keeps its window across the clock (the samples hook reads the
  // window that ends at each read). The availability and the events refresh
  // on a timer.
  it('polls the 24h range of an active run and not a long range', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel(true);
    expect(h.calls.find((c) => c.path === AVAIL)!.config?.refreshInterval).toBe(
      60_000
    );
    expect(lastSamples()).toMatchObject({
      window: { preset: '24h' },
      poll: true,
    });
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(
      new Set(h.samples.map((c) => JSON.stringify([c.window, c.points])))
    ).toHaveProperty('size', 1);

    fireEvent.click(screen.getByRole('button', { name: '1y' }));
    expect(lastSamples()).toMatchObject({
      window: { preset: '1y' },
      poll: false,
    });
  });

  it('does not poll a run that ended', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    renderPanel(false);
    expect(h.calls.find((c) => c.path === AVAIL)!.config?.refreshInterval).toBe(
      0
    );
    expect(lastSamples().poll).toBe(false);
    h.calls.length = 0;
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(h.calls).toHaveLength(0);
  });

  // A run can end between two polls, and nothing polls after it: the end of
  // the run (its last samples, the stopped event) is read once more.
  it('reads the step once more when the run ends', () => {
    h.responses = {
      [AVAIL]: availability,
      [SAMPLES]: samples(5),
      [EVENTS]: events,
    };
    const view = renderPanel(true);
    expect(h.reads).toEqual([]);

    view.rerender(panel(false));
    expect([...h.reads].sort()).toEqual([AVAIL, EVENTS, SAMPLES].sort());

    view.rerender(panel(false));
    expect(h.reads).toHaveLength(3);
  });
});
