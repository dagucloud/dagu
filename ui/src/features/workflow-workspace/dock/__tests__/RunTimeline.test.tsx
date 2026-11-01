// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, TriggerType } from '@/api/v1/schema';
import type { ApiDAGRunDetails } from '../../contracts';
import { run } from '../../data/__tests__/fixtures';
import {
  RUN_COUNT_KEY,
  RunTimeline,
  SHOW_TESTS_KEY,
  RunTimelineView,
  type RunTimelineViewProps,
} from '../RunTimeline';
import {
  RUN_MODE_KEY,
  RUN_RANGE_KEY,
  SHOW_CACHED_KEY,
} from '../../data/useVisibleRuns';
import { EXTENT_PX, LANE_PX } from '../runExtents';
import { BAR_MAX_PX, BAR_MIN_PX, BAR_PX, Y_AXIS_PX } from '../runTimelineModel';

const h = vi.hoisted(() => ({
  select: vi.fn(),
  selection: 'latest' as 'latest' | { runId: string },
  latestRunId: 'r6' as string | null,
  history: { runs: [] as unknown[], isLoading: false },
  historyArgs: [] as unknown[],
  requestStart: vi.fn(),
  workspaceRun: () => ({}) as Record<string, unknown>,
}));
h.workspaceRun = () => ({
  selection: h.selection,
  latestRef: h.latestRunId ? { runId: h.latestRunId } : null,
  select: h.select,
});

vi.mock('../../data', () => ({ useWorkspaceRun: () => h.workspaceRun() }));
vi.mock('../../data/useWorkspaceRun', () => ({
  useWorkspaceRun: () => h.workspaceRun(),
}));
vi.mock('../../data/context', () => ({
  useWorkspaceData: () => ({
    fileName: 'demo.yaml',
    dagData: { dag: { name: 'demo' } },
  }),
}));
vi.mock('../../data/useRunHistory', () => ({
  useRunHistory: (args: unknown) => {
    h.historyArgs.push(args);
    return h.history;
  },
}));
vi.mock('../../bar/RunStartContext', () => ({
  useRunStart: () => ({ canStart: true, requestStart: h.requestStart }),
}));

const NOW = Date.parse('2026-03-02T12:00:00Z');

function make(
  id: string,
  status: Status,
  seconds: number | null,
  extra: Partial<ApiDAGRunDetails> = {},
  day = '2026-03-02'
): ApiDAGRunDetails {
  const startedAt = `${day}T10:00:00Z`;
  const finishedAt =
    seconds === null
      ? undefined
      : new Date(Date.parse(startedAt) + seconds * 1000).toISOString();
  return run(id, [], status, {
    startedAt,
    ...(finishedAt ? { finishedAt } : {}),
    ...extra,
  });
}

// Oldest first, as the view takes them.
const RUNS = [
  make('r1', Status.Success, 10, {}, '2026-03-01'),
  make(
    'r2',
    Status.Failed,
    40,
    { triggerType: TriggerType.manual },
    '2026-03-01'
  ),
  make('r3', Status.Aborted, 5),
  make('r4', Status.PartialSuccess, 20),
  make('r5', Status.Queued, null),
  make('r6', Status.Success, 2),
];

function renderView(props: Partial<RunTimelineViewProps> = {}) {
  const onSelect = vi.fn();
  const onCountChange = vi.fn();
  render(
    <MemoryRouter>
      <RunTimelineView
        runs={RUNS}
        loading={false}
        selectedId="r6"
        count={30}
        onSelect={onSelect}
        onCountChange={onCountChange}
        now={NOW}
        {...props}
      />
    </MemoryRouter>
  );
  return { onSelect, onCountChange };
}

/** The bars of the track (not the options of the count select). */
const bars = () =>
  within(screen.getByRole('listbox', { name: 'Runs' })).getAllByRole('option');
const bar = (id: string) => screen.getByTestId(`ww-run-bar-${id}`);
const fill = (id: string) => within(bar(id)).getByTestId('ww-run-bar-fill');
/** The pixel of the left edge of the visible bar of a run. */
const barLeft = (id: string) =>
  Number.parseFloat(bar(id).style.left) +
  Number.parseFloat(fill(id).style.left);

/** A measured track width, for the layout that depends on it. */
function fakeWidth(width: number) {
  class FakeObserver {
    observe() {}
    disconnect() {}
    unobserve() {}
  }
  vi.stubGlobal('ResizeObserver', FakeObserver);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    width,
    height: 90,
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: width,
    bottom: 90,
    toJSON: () => ({}),
  });
}

describe('RunTimelineView', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('draws one bar for each run, oldest on the left', () => {
    renderView();
    const options = bars();
    expect(options.map((o) => o.getAttribute('data-run-id'))).toEqual([
      'r1',
      'r2',
      'r3',
      'r4',
      'r5',
      'r6',
    ]);
  });

  it('sets the bar height from the run time, with a minimum', () => {
    renderView();
    // r2 is the longest run (40s): it takes the whole plot. The height is
    // linear, as the duration axis: 20s is 24px, 10s is 12px, 5s is 6px.
    expect(fill('r2')).toHaveStyle({ height: `${BAR_MAX_PX}px` });
    expect(fill('r4')).toHaveStyle({ height: '24px' });
    expect(fill('r1')).toHaveStyle({ height: '12px' });
    expect(fill('r3')).toHaveStyle({ height: '6px' });
    // No duration at all, and a run under 3px, stay at the minimum.
    expect(fill('r5')).toHaveStyle({ height: `${BAR_MIN_PX}px` });
    expect(fill('r6')).toHaveStyle({ height: `${BAR_MIN_PX}px` });
  });

  it('draws thin bars', () => {
    renderView();
    for (const id of ['r1', 'r3', 'r6']) {
      expect(fill(id)).toHaveStyle({ width: `${BAR_PX}px` });
    }
  });

  it('grows the bar of a running run with the clock', () => {
    const running = make('r7', Status.Running, null, {
      startedAt: '2026-03-02T11:59:00Z',
    });
    renderView({ runs: [...RUNS, running], selectedId: 'r7' });
    // 60s since the start: longer than every finished run.
    expect(fill('r7')).toHaveStyle({ height: `${BAR_MAX_PX}px` });
    // The bar is still: the status dot and the chip carry the live cue.
    expect(fill('r7').className).not.toMatch(/animate-/);
  });

  it('colours each bar with the status palette', () => {
    renderView();
    expect(fill('r1')).toHaveClass('bg-[var(--status-success)]');
    expect(fill('r2')).toHaveClass('bg-[var(--status-error)]');
    expect(fill('r3')).toHaveClass('bg-[var(--status-aborted)]');
    // Queued runs wait like waiting ones: amber.
    expect(fill('r5')).toHaveClass('bg-[var(--status-warning)]');
    expect(bar('r5')).toHaveAttribute('data-tone', 'warning');
    // A partial success mixes the success and the warning colour.
    expect(bar('r4')).toHaveAttribute('data-tone', 'partial');
    expect(fill('r4').style.backgroundImage).toContain('--status-success');
    expect(fill('r4').style.backgroundImage).toContain('--status-warning');
  });

  it('colours a run that has not started with the muted tone', () => {
    renderView({
      runs: [make('n1', Status.NotStarted, null)],
      selectedId: 'n1',
    });
    expect(fill('n1')).toHaveClass('bg-[var(--status-neutral)]');
  });

  it('highlights the selected bar and marks the newest one as the latest', () => {
    renderView({ selectedId: 'r3' });
    expect(bar('r3')).toHaveAttribute('aria-selected', 'true');
    expect(fill('r3')).toHaveClass('ring-1');
    for (const id of ['r1', 'r2', 'r4', 'r5', 'r6']) {
      expect(bar(id)).toHaveAttribute('aria-selected', 'false');
    }
    expect(bar('r6')).toHaveAttribute('data-latest', 'true');
    expect(document.querySelectorAll('[data-latest]')).toHaveLength(1);
  });

  it('names each bar with the run id, the status and the duration', () => {
    renderView();
    expect(bar('r2')).toHaveAccessibleName('Run r2, Failed, 40s');
    expect(bar('r1')).toHaveAccessibleName('Run r1, Succeeded, 10s');
    // No duration for a queued run; the newest run says so.
    expect(bar('r5')).toHaveAccessibleName('Run r5, Queued');
    expect(bar('r6')).toHaveAccessibleName('Run r6, Succeeded, 2s, latest');
    expect(screen.getByRole('listbox', { name: 'Runs' })).toBeInTheDocument();
  });

  it('selects a run with a click', () => {
    const { onSelect } = renderView();
    fireEvent.click(bar('r2'));
    expect(onSelect).toHaveBeenCalledWith('r2');
  });

  it('keeps one tab stop, on the selected bar', () => {
    renderView({ selectedId: 'r3' });
    expect(bar('r3')).toHaveAttribute('tabindex', '0');
    expect(bar('r1')).toHaveAttribute('tabindex', '-1');
    // Without a selected bar, the newest one is the stop.
    cleanup();
    renderView({ selectedId: null });
    expect(bar('r6')).toHaveAttribute('tabindex', '0');
  });

  // parity:history-keyboard parity:chg-history-keys
  describe('keyboard', () => {
    const track = () => screen.getByRole('listbox', { name: 'Runs' });

    it('moves to the older and to the newer run with Left and Right', () => {
      const { onSelect } = renderView({ selectedId: 'r3' });
      fireEvent.keyDown(track(), { key: 'ArrowLeft' });
      expect(onSelect).toHaveBeenLastCalledWith('r2');
      fireEvent.keyDown(track(), { key: 'ArrowRight' });
      expect(onSelect).toHaveBeenLastCalledWith('r4');
    });

    it('jumps to the oldest and to the newest run with Home and End', () => {
      const { onSelect } = renderView({ selectedId: 'r3' });
      fireEvent.keyDown(track(), { key: 'Home' });
      expect(onSelect).toHaveBeenLastCalledWith('r1');
      fireEvent.keyDown(track(), { key: 'End' });
      expect(onSelect).toHaveBeenLastCalledWith('r6');
    });

    it('stops at the ends', () => {
      const { onSelect } = renderView({ selectedId: 'r1' });
      fireEvent.keyDown(track(), { key: 'ArrowLeft' });
      expect(onSelect).not.toHaveBeenCalled();
    });

    it('starts from the newest run when no bar is selected', () => {
      const { onSelect } = renderView({ selectedId: null });
      fireEvent.keyDown(track(), { key: 'ArrowLeft' });
      expect(onSelect).toHaveBeenLastCalledWith('r5');
    });

    // The keys use the runs that the track draws, like the arrows of the
    // run chip: the run before the window is not a step of them.
    it('goes through the runs that the track draws only', () => {
      const { onSelect } = renderView({ selectedId: 'r4', count: 3 });
      // r4, r5, r6 are in view; r3 is older than the window.
      expect(bars()).toHaveLength(3);
      fireEvent.keyDown(track(), { key: 'ArrowLeft' });
      expect(onSelect).not.toHaveBeenCalled();
      fireEvent.keyDown(track(), { key: 'End' });
      expect(onSelect).toHaveBeenLastCalledWith('r6');
    });

    it('leaves a key with a modifier alone', () => {
      const { onSelect } = renderView({ selectedId: 'r3' });
      fireEvent.keyDown(track(), { key: 'ArrowLeft', shiftKey: true });
      expect(onSelect).not.toHaveBeenCalled();
    });

    it('selects the focused bar with Enter', () => {
      const { onSelect } = renderView();
      fireEvent.keyDown(bar('r2'), { key: 'Enter' });
      expect(onSelect).toHaveBeenCalledWith('r2');
    });
  });

  it('tells the start, the status, the duration, the short id and the trigger in a tooltip', async () => {
    const user = userEvent.setup();
    const longId = 'abcdef0123456789';
    renderView({
      runs: RUNS.map((r) =>
        r.dagRunId === 'r2' ? { ...r, dagRunId: longId } : r
      ),
    });
    await user.hover(bar(longId));
    const tooltip = (
      await screen.findAllByTestId('ww-run-timeline-tooltip')
    )[0]!;
    expect(tooltip).toHaveTextContent('2026-03-01');
    expect(tooltip).toHaveTextContent(/\d\d:\d\d:\d\d/);
    expect(tooltip).toHaveTextContent('Failed');
    expect(tooltip).toHaveTextContent('Duration: 40s');
    expect(tooltip).toHaveTextContent('abcdef01');
    expect(tooltip).not.toHaveTextContent(longId);
    expect(tooltip).toHaveTextContent('Manual');
  });

  // The status text of a bar comes from one function: a view with a continuous
  // workflow can say Healthy while the API status stays Running.
  it('prints the status text that the host gives', async () => {
    const user = userEvent.setup();
    renderView({
      statusTextOf: (run) =>
        run.status === Status.Failed ? 'Healthy' : 'Other',
    });
    expect(bar('r2')).toHaveAccessibleName('Run r2, Healthy, 40s');
    await user.hover(bar('r2'));
    const tooltip = (
      await screen.findAllByTestId('ww-run-timeline-tooltip')
    )[0]!;
    expect(tooltip).toHaveTextContent('Healthy');
    expect(bar('r2')).toHaveAttribute('data-status', String(Status.Failed));
  });

  it('shows a cursor line and the time of the run under the pointer', async () => {
    const user = userEvent.setup();
    renderView();
    expect(screen.queryByTestId('ww-run-timeline-cursor')).toBeNull();
    await user.hover(bar('r3'));
    expect(screen.getByTestId('ww-run-timeline-cursor')).toBeInTheDocument();
    expect(screen.getByTestId('ww-run-timeline-cursor-time')).toHaveTextContent(
      /^\d\d:\d\d:\d\d$/
    );
    await user.unhover(bar('r3'));
    expect(screen.queryByTestId('ww-run-timeline-cursor')).toBeNull();
  });

  describe('time axis', () => {
    // 18:00 twice (a minute apart) and 22:00: local times, as the axis.
    const local = (h: number, m = 0) =>
      new Date(2026, 2, 2, h, m).toISOString();
    const evening = [
      make('e1', Status.Success, 5, { startedAt: local(18, 0) }),
      make('e2', Status.Success, 5, { startedAt: local(18, 1) }),
      make('e3', Status.Success, 5, { startedAt: local(22, 0) }),
    ];

    it('leaves a visible empty gap between 18:00 and 22:00', () => {
      renderView({ runs: evening, selectedId: 'e3' });
      const near = barLeft('e2') - barLeft('e1');
      const far = barLeft('e3') - barLeft('e2');
      expect(near).toBeLessThan(10);
      expect(far).toBeGreaterThan(400);
    });

    it('stacks runs of the same instant side by side with at least 3px each', () => {
      const same = Array.from({ length: 6 }, (_, i) =>
        make(`s${i}`, Status.Success, 5, { startedAt: local(18, 0) })
      );
      renderView({ runs: same, selectedId: 's5' });
      const lefts = same.map((r) => barLeft(r.dagRunId)).sort((a, b) => a - b);
      for (let i = 1; i < lefts.length; i += 1) {
        expect(lefts[i]! - lefts[i - 1]!).toBeGreaterThanOrEqual(BAR_PX);
      }
      expect(BAR_PX).toBeGreaterThanOrEqual(3);
    });

    it('labels the axis with the time in the hours between the runs', () => {
      renderView({ runs: evening, selectedId: 'e3' });
      const ticks = screen.getAllByTestId('ww-run-timeline-tick');
      const labels = ticks.map((t) => t.textContent ?? '');
      expect(labels.some((t) => t.startsWith('20:00'))).toBe(true);
      // The first label carries the date under the time.
      expect(labels[0]).toContain('Mar 2 2026');
      expect(screen.getAllByTestId('ww-run-timeline-grid').length).toBe(
        ticks.length
      );
    });

    it('draws a now line and a live extent for a running run', () => {
      const running = make('e4', Status.Running, null, {
        startedAt: local(22, 0),
      });
      renderView({
        runs: [...evening.slice(0, 2), running],
        selectedId: 'e4',
        now: new Date(2026, 2, 2, 23, 0).getTime(),
      });
      expect(screen.getByTestId('ww-run-timeline-now')).toBeInTheDocument();
      // The extent runs from the start of the run to the line of now.
      const live = screen.getByTestId('ww-run-extent-e4');
      expect(
        within(live).getByTestId('ww-run-extent-live')
      ).toBeInTheDocument();
      expect(
        within(live).getByTestId('ww-run-extent-fill').className
      ).not.toMatch(/animate-/);
      expect(Number.parseFloat(live.style.width)).toBeGreaterThan(50);
    });

    it('draws no now line when no run runs', () => {
      renderView({ runs: evening, selectedId: 'e3' });
      expect(screen.queryByTestId('ww-run-timeline-now')).toBeNull();
    });
  });

  describe('duration extents', () => {
    const local = (h: number, m = 0, day = 2) =>
      new Date(2026, 2, day, h, m).toISOString();
    const span = (
      id: string,
      from: string,
      to: string | null,
      status = Status.Success
    ) =>
      run(id, [], status, {
        startedAt: from,
        ...(to ? { finishedAt: to } : {}),
      });
    const extent = (id: string) => screen.getByTestId(`ww-run-extent-${id}`);
    const fillOf = (id: string) =>
      within(extent(id)).getByTestId('ww-run-extent-fill');
    const block = () => screen.getByTestId('ww-run-extents');
    const plot = () => screen.getByTestId('ww-run-timeline-plot');
    // Three runs: a overlaps b, c is alone.
    const trio = [
      span('a', local(8), local(12)),
      span('b', local(9), local(10)),
      span('c', local(14), local(15)),
    ];
    // n runs that all last until 12:00.
    const stack = (n: number) =>
      Array.from({ length: n }, (_, i) =>
        span(`k${i}`, local(8, i), local(12))
      );

    it('draws one extent for each started run, under the bars', () => {
      renderView({ runs: trio, selectedId: 'c' });
      expect(within(block()).getAllByRole('button')).toHaveLength(3);
      expect(fillOf('a')).toHaveClass('bg-[var(--status-success)]');
      expect(fillOf('a')).toHaveStyle({ height: '3px' });
      // Not in the listbox: the bars stay the options.
      expect(bars()).toHaveLength(3);
    });

    it('puts overlapping runs in different lanes', () => {
      renderView({ runs: trio, selectedId: 'c' });
      expect(extent('a')).toHaveAttribute('data-lane', '0');
      expect(extent('b')).toHaveAttribute('data-lane', '1');
      expect(extent('c')).toHaveAttribute('data-lane', '0');
    });

    it('draws 4 lanes at the most and tells the hidden count in a +N badge', async () => {
      const user = userEvent.setup();
      renderView({ runs: stack(6), selectedId: 'k0' });
      const lanes = stack(6).map((r) =>
        Number(extent(r.dagRunId).getAttribute('data-lane'))
      );
      expect(Math.max(...lanes)).toBe(3);
      expect(document.querySelectorAll('[data-overflow="true"]')).toHaveLength(
        2
      );
      const badge = screen.getByTestId('ww-run-timeline-more');
      expect(badge).toHaveTextContent('+2');
      expect(badge).toHaveAccessibleName('2 more runs overlap');
      await user.hover(badge);
      expect(
        (await screen.findAllByText('2 more runs overlap')).length
      ).toBeGreaterThan(0);
    });

    it('shows no badge when no extent is hidden', () => {
      renderView({ runs: stack(4), selectedId: 'k0' });
      expect(screen.queryByTestId('ww-run-timeline-more')).toBeNull();
    });

    it('keeps the block and the track at one size, with 0 and 5 overlaps', () => {
      renderView({ runs: [span('s', local(8), local(9))], selectedId: 's' });
      const height = block().style.height;
      const top = block().style.top;
      const plotHeight = plot().style.height;
      expect(height).toBe('26px');
      cleanup();
      renderView({ runs: stack(5), selectedId: 'k0' });
      expect(block().style.height).toBe(height);
      expect(block().style.top).toBe(top);
      expect(plot().style.height).toBe(plotHeight);
    });

    it('highlights the selected extent and the ones that overlap it', () => {
      renderView({ runs: trio, selectedId: 'a' });
      expect(extent('a')).toHaveAttribute('data-focus', 'in');
      expect(extent('b')).toHaveAttribute('data-focus', 'in');
      expect(extent('c')).toHaveAttribute('data-focus', 'faded');
      expect(fillOf('a')).toHaveClass('opacity-100', 'ring-1');
      expect(fillOf('c')).toHaveClass('opacity-40');
    });

    it('follows the selection, and a run without overlap stands alone', () => {
      renderView({ runs: trio, selectedId: 'c' });
      expect(extent('c')).toHaveAttribute('data-focus', 'in');
      expect(extent('a')).toHaveAttribute('data-focus', 'faded');
      expect(extent('b')).toHaveAttribute('data-focus', 'faded');
    });

    it('highlights the hovered run like the selected one', async () => {
      const user = userEvent.setup();
      renderView({ runs: trio, selectedId: 'c' });
      await user.hover(extent('b'));
      expect(extent('a')).toHaveAttribute('data-focus', 'in');
      expect(extent('b')).toHaveAttribute('data-focus', 'in');
      expect(extent('c')).toHaveAttribute('data-focus', 'faded');
      await user.unhover(extent('b'));
      expect(extent('c')).toHaveAttribute('data-focus', 'in');
    });

    it('highlights through the bar of a run too', async () => {
      const user = userEvent.setup();
      renderView({ runs: trio, selectedId: 'c' });
      await user.hover(bar('a'));
      expect(extent('b')).toHaveAttribute('data-focus', 'in');
    });

    it('selects a run with a click on its extent', () => {
      const { onSelect } = renderView({ runs: trio, selectedId: 'c' });
      fireEvent.click(extent('b'));
      expect(onSelect).toHaveBeenCalledWith('b');
    });

    it('tells the start, the end, the duration and the overlaps in a tooltip', async () => {
      const user = userEvent.setup();
      renderView({ runs: trio, selectedId: 'c' });
      await user.hover(extent('a'));
      const tooltip = (
        await screen.findAllByTestId('ww-run-timeline-tooltip')
      )[0]!;
      expect(tooltip).toHaveTextContent('2026-03-02 08:00:00');
      expect(tooltip).toHaveTextContent('Ended 2026-03-02 12:00:00');
      expect(tooltip).toHaveTextContent('Duration: 4h 0m');
      expect(tooltip).toHaveTextContent('overlaps 1 other run');
    });

    it('says that a run is still running', async () => {
      const user = userEvent.setup();
      renderView({
        runs: [
          span(
            'r',
            new Date(NOW - 3600_000).toISOString(),
            null,
            Status.Running
          ),
        ],
        selectedId: 'r',
      });
      await user.hover(extent('r'));
      const tooltip = (
        await screen.findAllByTestId('ww-run-timeline-tooltip')
      )[0]!;
      expect(tooltip).toHaveTextContent('Still running');
      expect(tooltip).toHaveTextContent('Duration: 1h 0m');
    });

    it('shows the duration inside the extent of a run that spans the view', () => {
      const long = [
        span('long', local(8), local(8, 0, 3)),
        span('short', local(9), local(9, 5)),
      ];
      renderView({ runs: long, selectedId: 'long' });
      expect(
        within(extent('long')).getByTestId('ww-run-extent-label')
      ).toHaveTextContent('1d 0h');
      expect(
        within(extent('short')).queryByTestId('ww-run-extent-label')
      ).toBeNull();
    });

    it('marks a run of more than 10 times the median with a Long run icon', () => {
      const runs = [
        ...Array.from({ length: 5 }, (_, i) =>
          span(`q${i}`, local(8 + i), local(8 + i, 2))
        ),
        span('slow', local(14), local(14, 30)),
      ];
      renderView({ runs, selectedId: 'slow' });
      expect(
        within(extent('slow')).getByRole('img', { name: 'Long run' })
      ).toBeInTheDocument();
      expect(
        within(extent('q0')).queryByRole('img', { name: 'Long run' })
      ).toBeNull();
    });

    it('clips an older run that lasts into the view, with a chevron', async () => {
      const user = userEvent.setup();
      const all = [
        span('old', local(6, 0, 1), local(11)),
        span('n1', local(8), local(9)),
        span('n2', local(10), local(11)),
      ];
      renderView({ runs: all, count: 2, selectedId: 'n2' });
      // The track draws two bars; the older run has an extent but no bar.
      expect(bars()).toHaveLength(2);
      const old = extent('old');
      expect(old).toHaveAttribute('data-clipped', 'true');
      expect(within(old).getByTestId('ww-run-extent-clip')).toBeInTheDocument();
      expect(old).toHaveAccessibleName(/Started before this view/);
      expect(Number.parseFloat(old.style.left)).toBeLessThanOrEqual(0);
      await user.hover(old);
      const tooltip = (
        await screen.findAllByTestId('ww-run-timeline-tooltip')
      )[0]!;
      expect(tooltip).toHaveTextContent('Started before this view');
      // It has no bar of its own.
      expect(screen.queryByTestId('ww-run-bar-old')).toBeNull();
    });

    // The extent of the selected run is drawn like its vertical bar: full
    // opacity, a primary outline, a lane high, a mark at each end. The runs
    // that overlap it keep the thin outline and the others fade.
    describe('selected extent', () => {
      const selectedExtent = () =>
        document.querySelectorAll(
          '[data-testid^="ww-run-extent-"][data-selected]'
        );
      const expectSelectedLook = (id: string) => {
        expect(extent(id)).toHaveAttribute('data-selected', 'true');
        expect(fillOf(id)).toHaveClass('opacity-100', 'ring-1', 'ring-primary');
        expect(fillOf(id)).toHaveStyle({ height: `${LANE_PX}px` });
        expect(fillOf(id)).not.toHaveClass('ring-foreground/60');
      };

      it('highlights the selected extent like its vertical bar', () => {
        renderView({ runs: trio, selectedId: 'a' });
        expectSelectedLook('a');
        expect(bar('a')).toHaveAttribute('aria-selected', 'true');
        expect(fill('a')).toHaveClass('opacity-100', 'ring-1', 'ring-primary');
        expect(selectedExtent()).toHaveLength(1);
      });

      it('marks the start and the end of the selected extent', () => {
        renderView({ runs: trio, selectedId: 'a' });
        expect(
          within(extent('a')).getByTestId('ww-run-extent-start')
        ).toHaveClass('bg-primary');
        expect(
          within(extent('a')).getByTestId('ww-run-extent-end')
        ).toHaveClass('bg-primary');
        for (const id of ['b', 'c']) {
          expect(
            within(extent(id)).queryByTestId('ww-run-extent-start')
          ).toBeNull();
          expect(
            within(extent(id)).queryByTestId('ww-run-extent-end')
          ).toBeNull();
        }
      });

      it('keeps the thin outline for a run that overlaps it, and fades the others', () => {
        renderView({ runs: trio, selectedId: 'a' });
        expect(extent('b')).not.toHaveAttribute('data-selected');
        expect(fillOf('b')).toHaveClass(
          'opacity-100',
          'ring-1',
          'ring-foreground/60'
        );
        expect(fillOf('b')).not.toHaveClass('ring-primary');
        expect(fillOf('b')).toHaveStyle({ height: `${EXTENT_PX}px` });
        expect(fillOf('c')).toHaveClass('opacity-40');
        expect(fillOf('c')).toHaveStyle({ height: `${EXTENT_PX}px` });
      });

      it('keeps the selected look while another run is hovered', async () => {
        const user = userEvent.setup();
        renderView({ runs: trio, selectedId: 'c' });
        await user.hover(extent('a'));
        expectSelectedLook('c');
        expect(extent('a')).toHaveAttribute('data-focus', 'in');
      });

      it('follows the selection when the arrow keys move it', () => {
        function Harness() {
          const [selected, setSelected] = useState('c');
          return (
            <MemoryRouter>
              <RunTimelineView
                runs={trio}
                loading={false}
                selectedId={selected}
                count={30}
                onSelect={setSelected}
                onCountChange={() => undefined}
                now={NOW}
              />
            </MemoryRouter>
          );
        }
        render(<Harness />);
        expectSelectedLook('c');
        fireEvent.keyDown(screen.getByRole('listbox', { name: 'Runs' }), {
          key: 'ArrowLeft',
        });
        expectSelectedLook('b');
        expect(extent('c')).not.toHaveAttribute('data-selected');
        expect(fillOf('c')).toHaveStyle({ height: `${EXTENT_PX}px` });
        fireEvent.keyDown(screen.getByRole('listbox', { name: 'Runs' }), {
          key: 'Home',
        });
        expectSelectedLook('a');
        expect(selectedExtent()).toHaveLength(1);
      });

      it('highlights a running run, with a start mark and the live end', () => {
        const running = span(
          'run',
          new Date(NOW - 3600_000).toISOString(),
          null,
          Status.Running
        );
        renderView({ runs: [trio[0]!, running], selectedId: 'run' });
        expectSelectedLook('run');
        expect(
          within(extent('run')).getByTestId('ww-run-extent-start')
        ).toBeInTheDocument();
        // The dashed live mark is the end of a run that runs.
        expect(
          within(extent('run')).queryByTestId('ww-run-extent-end')
        ).toBeNull();
        expect(
          within(extent('run')).getByTestId('ww-run-extent-live')
        ).toBeInTheDocument();
      });

      // A selected run older than the window moves the window back: the run
      // becomes the oldest bar and its extent starts inside the view.
      it('highlights an older run that the window moves back to', () => {
        const all = [
          span('old', local(6, 0, 1), local(11)),
          span('n1', local(8), local(9)),
          span('n2', local(10), local(11)),
        ];
        renderView({ runs: all, count: 2, selectedId: 'old' });
        expectSelectedLook('old');
        expect(extent('old')).not.toHaveAttribute('data-clipped');
        expect(
          within(extent('old')).getByTestId('ww-run-extent-start')
        ).toBeInTheDocument();
      });

      it('highlights the selected extent alone in a narrow track', () => {
        fakeWidth(300);
        renderView({ runs: stack(4), selectedId: 'k1' });
        expectSelectedLook('k1');
        expect(selectedExtent()).toHaveLength(1);
      });

      it('has no selected extent without a selection', () => {
        renderView({ runs: trio, selectedId: null });
        expect(selectedExtent()).toHaveLength(0);
        expect(screen.queryByTestId('ww-run-extent-start')).toBeNull();
      });
    });

    it('draws only the extent of the selected run in a narrow track', () => {
      fakeWidth(300);
      renderView({ runs: stack(6), selectedId: 'k2' });
      expect(within(block()).getAllByRole('button')).toHaveLength(1);
      expect(extent('k2')).toHaveAttribute('data-lane', '0');
      expect(block().style.height).toBe('5px');
      expect(screen.queryByTestId('ww-run-timeline-more')).toBeNull();
      // The bars stay.
      expect(bars()).toHaveLength(6);
    });
  });

  describe('duration axis', () => {
    const axis = () => screen.getByTestId('ww-run-timeline-yaxis');
    const labels = () =>
      screen
        .getAllByTestId('ww-run-timeline-ytick')
        .map((tick) => tick.textContent);
    const timed = (id: string, hour: number, seconds: number) =>
      make(id, Status.Success, seconds, {
        startedAt: new Date(2026, 2, 2, hour).toISOString(),
        finishedAt: new Date(
          new Date(2026, 2, 2, hour).getTime() + seconds * 1000
        ).toISOString(),
      });
    const three = [
      timed('s2', 8, 2),
      timed('s30', 9, 30),
      timed('s180', 10, 180),
    ];

    it('labels the height with round durations, from the top', () => {
      renderView({ runs: three, selectedId: 's180' });
      expect(labels()).toEqual(['0', '2m 0s', '1m 0s']);
    });

    it('draws a grid line at each label, like the time axis', () => {
      renderView({ runs: three, selectedId: 's180' });
      const lines = screen.getAllByTestId('ww-run-timeline-ygrid');
      expect(lines).toHaveLength(2);
      for (const line of lines) {
        expect(line).toHaveClass('bg-foreground/10', 'h-px');
      }
    });

    it('sets the bar height on the same scale as the labels', () => {
      renderView({ runs: three, selectedId: 's180' });
      // 180s is the whole plot: 30s is a sixth of it, 2s is under 3px.
      expect(fill('s180')).toHaveStyle({ height: `${BAR_MAX_PX}px` });
      expect(fill('s30')).toHaveStyle({ height: '8px' });
      expect(fill('s2')).toHaveStyle({ height: `${BAR_MIN_PX}px` });
      // The label of 2 minutes stands at the height of a 2 minute bar.
      const tick = screen
        .getAllByTestId('ww-run-timeline-ytick')
        .find((t) => t.getAttribute('data-duration') === '120000')!;
      const baseline = 8 + BAR_MAX_PX;
      const centre = Number.parseFloat(tick.style.top) + 6.5;
      expect(baseline - centre).toBeCloseTo((120 / 180) * BAR_MAX_PX, 5);
    });

    it('reserves the gutter at a fixed width, whatever the longest run', () => {
      renderView({ runs: three, selectedId: 's180' });
      const width = axis().style.width;
      const canvasLeft = screen.getByTestId('ww-run-timeline-canvas').style
        .left;
      const plotHeight = screen.getByTestId('ww-run-timeline-plot').style
        .height;
      expect(width).toBe(`${Y_AXIS_PX}px`);
      expect(canvasLeft).toBe(`${Y_AXIS_PX}px`);
      cleanup();
      renderView({
        runs: [timed('s2', 8, 2), timed('s5', 9, 5)],
        selectedId: 's5',
      });
      expect(axis().style.width).toBe(width);
      expect(screen.getByTestId('ww-run-timeline-canvas').style.left).toBe(
        canvasLeft
      );
      expect(screen.getByTestId('ww-run-timeline-plot').style.height).toBe(
        plotHeight
      );
    });

    it('keeps the labels in the muted 11px tabular style', () => {
      renderView({ runs: three, selectedId: 's180' });
      expect(axis()).toHaveClass(
        'text-[11px]',
        'tabular-nums',
        'text-muted-foreground'
      );
    });

    it('draws only the zero label when no run has a duration', () => {
      renderView({
        runs: [make('q', Status.Queued, null)],
        selectedId: 'q',
      });
      expect(labels()).toEqual(['0']);
      expect(screen.queryByTestId('ww-run-timeline-ygrid')).toBeNull();
    });

    it('names the axis, on the linear scale', () => {
      renderView({ runs: three, selectedId: 's180' });
      expect(axis()).toHaveAttribute('data-scale', 'linear');
      expect(axis()).toHaveAccessibleName('Run duration');
    });

    // One run of 27 hours would flatten the others to the minimum on a
    // linear scale: the bars use the square root and say so.
    it('compresses the scale for a 27 hour run and says so', () => {
      const long = timed('long', 12, 27 * 3600);
      renderView({
        runs: [
          timed('a', 8, 2),
          timed('b', 9, 30),
          timed('c', 10, 180),
          timed('d', 11, 3 * 3600),
          long,
        ],
        selectedId: 'long',
      });
      expect(axis()).toHaveAttribute('data-scale', 'sqrt');
      expect(axis()).toHaveAccessibleName('Run duration (square root scale)');
      expect(labels()).toEqual(['0', '1d 0h', '6h 0m']);
      // The 27 hour run takes the plot; the 3 hour run is 16px, not flat.
      expect(fill('long')).toHaveStyle({ height: `${BAR_MAX_PX}px` });
      expect(fill('d')).toHaveStyle({ height: '16px' });
    });

    it('shows the duration at the top of the bar under the pointer', async () => {
      const user = userEvent.setup();
      renderView({ runs: three, selectedId: 's180' });
      expect(
        screen.queryByTestId('ww-run-timeline-cursor-duration')
      ).toBeNull();
      await user.hover(bar('s30'));
      expect(
        screen.getByTestId('ww-run-timeline-cursor-duration')
      ).toHaveTextContent('30s');
      expect(
        screen.getByTestId('ww-run-timeline-cursor-level')
      ).toBeInTheDocument();
      await user.unhover(bar('s30'));
      expect(
        screen.queryByTestId('ww-run-timeline-cursor-duration')
      ).toBeNull();
    });

    it('gives no duration for the cursor of a run without one', async () => {
      const user = userEvent.setup();
      renderView({
        runs: [make('q', Status.Queued, null), timed('s30', 9, 30)],
        selectedId: 's30',
      });
      await user.hover(bar('q'));
      expect(
        screen.queryByTestId('ww-run-timeline-cursor-duration')
      ).toBeNull();
      expect(screen.getByTestId('ww-run-timeline-cursor')).toBeInTheDocument();
    });
  });

  describe('run count', () => {
    it('has no Latest button and no All runs link', () => {
      renderView();
      expect(screen.queryByRole('button', { name: 'Latest' })).toBeNull();
      expect(screen.queryByText('All runs')).toBeNull();
      expect(screen.queryByRole('link')).toBeNull();
    });

    it('offers 5, 10, 20 and 30 runs, and shows the chosen one', () => {
      renderView({ count: 10 });
      const select = screen.getByRole('combobox', {
        name: 'Number of runs shown',
      });
      expect(select).toHaveValue('10');
      expect(
        within(select)
          .getAllByRole('option')
          .map((o) => o.textContent)
      ).toEqual([
        'Last 5 runs',
        'Last 10 runs',
        'Last 20 runs',
        'Last 30 runs',
      ]);
    });

    it('reports the count that the user chooses', () => {
      const { onCountChange } = renderView();
      fireEvent.change(screen.getByRole('combobox'), {
        target: { value: '5' },
      });
      expect(onCountChange).toHaveBeenCalledWith(5);
    });

    it('shows the newest runs of the count', () => {
      renderView({ count: 3, selectedId: 'r6' });
      expect(bars().map((o) => o.getAttribute('data-run-id'))).toEqual([
        'r4',
        'r5',
        'r6',
      ]);
    });

    it('moves the window back to a selected run that is older', () => {
      renderView({ count: 3, selectedId: 'r2' });
      expect(bars().map((o) => o.getAttribute('data-run-id'))).toEqual([
        'r2',
        'r3',
        'r4',
      ]);
      expect(bar('r2')).toHaveAttribute('aria-selected', 'true');
    });

    it('adds the number of runs that the server sends beyond 30', () => {
      const many = Array.from({ length: 45 }, (_, i) =>
        make(`m${i}`, Status.Success, 1)
      );
      renderView({ runs: many, selectedId: 'm44', count: 30 });
      expect(
        within(screen.getByRole('combobox'))
          .getAllByRole('option')
          .map((o) => o.textContent)
      ).toContain('Last 45 runs');
    });
  });

  describe('loading and empty states', () => {
    it('shows a skeleton while the history loads', () => {
      renderView({ runs: [], loading: true, selectedId: null });
      const skeleton = screen.getByTestId('ww-run-timeline-loading');
      expect(skeleton).toHaveAttribute('aria-busy', 'true');
      expect(screen.queryByRole('listbox')).toBeNull();
      expect(screen.queryByText('No runs yet')).toBeNull();
    });

    it('shows No runs yet, with a Start hint', () => {
      const onStart = vi.fn();
      renderView({
        runs: [],
        selectedId: null,
        canStart: true,
        onStart,
      });
      expect(screen.getByText('No runs yet')).toBeInTheDocument();
      expect(
        screen.getByText(/Start the workflow to see its runs here/)
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: /Run…/ }));
      expect(onStart).toHaveBeenCalledTimes(1);
    });

    it('offers no Start without the right to run', () => {
      renderView({ runs: [], selectedId: null, canStart: false });
      expect(screen.queryByRole('button', { name: /Run…/ })).toBeNull();
    });
  });
});

describe('RunTimeline', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  function renderTimeline(url = '/dags/demo/executions') {
    // The history is newest first, as the API sends it.
    h.history = { runs: [...RUNS].reverse(), isLoading: false };
    render(
      <MemoryRouter initialEntries={[url]}>
        <RunTimeline />
      </MemoryRouter>
    );
  }

  it('highlights the latest run while the selection follows it', () => {
    h.selection = 'latest';
    renderTimeline();
    expect(bar('r6')).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByRole('button', { name: 'Latest' })).toBeNull();
  });

  it('highlights the pinned run', () => {
    h.selection = { runId: 'r2' };
    renderTimeline();
    expect(bar('r2')).toHaveAttribute('aria-selected', 'true');
    expect(bar('r6')).toHaveAttribute('aria-selected', 'false');
  });

  // The page opens on ?run=<id>: the extent of that run is highlighted
  // like its vertical bar, without a click.
  it('highlights the extent of the pinned run at load', () => {
    h.selection = { runId: 'r2' };
    renderTimeline();
    const extent = screen.getByTestId('ww-run-extent-r2');
    expect(extent).toHaveAttribute('data-selected', 'true');
    expect(within(extent).getByTestId('ww-run-extent-fill')).toHaveClass(
      'ring-primary',
      'opacity-100'
    );
    expect(within(extent).getByTestId('ww-run-extent-start')).toBeVisible();
    expect(screen.getByTestId('ww-run-extent-r6')).not.toHaveAttribute(
      'data-selected'
    );
  });

  it('highlights the extent of the latest run while the selection follows it', () => {
    h.selection = 'latest';
    renderTimeline();
    expect(screen.getByTestId('ww-run-extent-r6')).toHaveAttribute(
      'data-selected',
      'true'
    );
    expect(document.querySelectorAll('[data-selected]')).toHaveLength(1);
  });

  it('selects a run with replace', () => {
    h.selection = 'latest';
    renderTimeline();
    fireEvent.click(bar('r3'));
    expect(h.select).toHaveBeenLastCalledWith('r3', { replace: true });
  });

  it('shows 30 runs at first, from the 30 that the server sends', () => {
    h.selection = 'latest';
    h.history = {
      runs: Array.from({ length: 30 }, (_, i) =>
        make(`x${i}`, Status.Success, 1)
      ),
      isLoading: false,
    };
    render(
      <MemoryRouter initialEntries={['/dags/demo/executions']}>
        <RunTimeline />
      </MemoryRouter>
    );
    expect(bars()).toHaveLength(30);
    expect(screen.getByRole('combobox')).toHaveValue('30');
  });

  it('keeps the chosen count in the browser', () => {
    h.selection = 'latest';
    renderTimeline();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '5' } });
    expect(window.localStorage.getItem(RUN_COUNT_KEY)).toBe('5');
    expect(bars()).toHaveLength(5);
    // A new mount reads it back.
    cleanup();
    renderTimeline();
    expect(screen.getByRole('combobox')).toHaveValue('5');
    expect(bars()).toHaveLength(5);
  });

  it('ignores a stored count that is not offered', () => {
    window.localStorage.setItem(RUN_COUNT_KEY, '7');
    h.selection = 'latest';
    renderTimeline();
    expect(screen.getByRole('combobox')).toHaveValue('30');
  });

  it('still works when the storage throws', () => {
    h.selection = 'latest';
    const get = vi
      .spyOn(Storage.prototype, 'getItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    const set = vi
      .spyOn(Storage.prototype, 'setItem')
      .mockImplementation(() => {
        throw new Error('blocked');
      });
    renderTimeline();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '10' } });
    expect(bars()).toHaveLength(6);
    expect(screen.getByRole('combobox')).toHaveValue('10');
    get.mockRestore();
    set.mockRestore();
  });
});

// The track uses the whole width: the bars follow it.
describe('RunTimelineView width', () => {
  type Callback = (entries: { contentRect: { width: number } }[]) => void;
  let callbacks: Callback[] = [];

  function measure(width: number) {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      width,
      height: 90,
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      right: width,
      bottom: 90,
      toJSON: () => ({}),
    });
  }

  function fakeResizeObserver(width: number) {
    callbacks = [];
    class FakeObserver {
      constructor(callback: Callback) {
        callbacks.push(callback);
      }
      observe() {}
      disconnect() {}
      unobserve() {}
    }
    vi.stubGlobal('ResizeObserver', FakeObserver);
    measure(width);
  }
  // The track reads its border box on each resize.
  const resize = (width: number) =>
    act(() => {
      measure(width);
      for (const callback of callbacks) {
        callback([{ contentRect: { width } }]);
      }
    });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const many = (n: number) =>
    Array.from({ length: n }, (_, i) => {
      const start = new Date(2026, 2, 2, 8, i * 10);
      return make(`m${i}`, Status.Success, i + 1, {
        startedAt: start.toISOString(),
        finishedAt: new Date(start.getTime() + (i + 1) * 1000).toISOString(),
      });
    });

  it('places the oldest bar at the left and the newest at the right of the track', () => {
    fakeResizeObserver(1000);
    renderView({ runs: many(10), selectedId: 'm9' });
    // The canvas is as wide as the track minus the gutter of the duration axis.
    const canvas = 1000 - Y_AXIS_PX;
    expect(barLeft('m0')).toBeLessThan(14);
    expect(barLeft('m9')).toBeGreaterThan(canvas - 14 - BAR_PX);
  });

  it('draws the axis again when the track resizes', () => {
    fakeResizeObserver(1000);
    renderView({ runs: many(10), selectedId: 'm9' });
    const wide = barLeft('m9');
    resize(500);
    expect(barLeft('m9')).toBeLessThan(wide - 400);
    expect(barLeft('m9')).toBeGreaterThan(500 - Y_AXIS_PX - 14 - BAR_PX);
  });

  it('never overlaps the bars of 30 runs in a narrow track', () => {
    fakeResizeObserver(300);
    const crowd = Array.from({ length: 30 }, (_, i) =>
      make(`c${i}`, Status.Success, 1, {
        startedAt: new Date(2026, 2, 2, 8, 0, i).toISOString(),
      })
    );
    renderView({ runs: crowd, selectedId: 'c29' });
    const lefts = crowd.map((r) => barLeft(r.dagRunId)).sort((a, b) => a - b);
    for (let i = 1; i < lefts.length; i += 1) {
      expect(lefts[i]! - lefts[i - 1]!).toBeGreaterThanOrEqual(BAR_PX);
    }
    expect(lefts[29]! + BAR_PX).toBeLessThanOrEqual(300);
  });

  it('draws a bar for each run, never for each step', () => {
    fakeResizeObserver(400);
    const withSteps = many(12).map((r) => ({
      ...r,
      nodes: [{}, {}, {}, {}] as unknown as ApiDAGRunDetails['nodes'],
    }));
    renderView({ runs: withSteps, selectedId: 'm11' });
    expect(bars()).toHaveLength(12);
  });
});

// A test run (a step run of an unsaved draft) is drawn apart: hatched, with a
// flask, and named in its tooltip. It can be selected while the server keeps it.
describe('test runs in the timeline', () => {
  const TEST = make('t1', Status.Success, 3, { ephemeral: true });
  // Oldest first.
  const WITH_TEST = [...RUNS, TEST];

  beforeEach(() => {
    window.localStorage.clear();
    h.historyArgs = [];
  });

  it('hatches the bar and the extent of a test run and keeps its status colour', () => {
    renderView({ runs: WITH_TEST, selectedId: 'r6' });
    const barFill = fill('t1');
    expect(barFill.style.backgroundImage).toContain(
      'repeating-linear-gradient'
    );
    expect(barFill.className).toContain('--status-success');
    expect(bar('t1')).toHaveAttribute('data-test-run', 'true');
    const extentFill = within(
      screen.getByTestId('ww-run-extent-t1')
    ).getByTestId('ww-run-extent-fill');
    expect(extentFill.style.backgroundImage).toContain(
      'repeating-linear-gradient'
    );
    // A run that stays has no hatch.
    expect(fill('r6').style.backgroundImage).toBe('');
    expect(bar('r6')).not.toHaveAttribute('data-test-run');
  });

  it('keeps the hatch over the mix of a partial success', () => {
    const partial = make('t2', Status.PartialSuccess, 3, { ephemeral: true });
    renderView({ runs: [...RUNS, partial], selectedId: 'r6' });
    const image = fill('t2').style.backgroundImage;
    expect(image).toContain('repeating-linear-gradient');
    expect(image).toContain('--status-warning');
  });

  it('puts a flask on the bar of a test run only', () => {
    renderView({ runs: WITH_TEST, selectedId: 'r6' });
    expect(
      within(bar('t1')).getByTestId('ww-run-bar-flask')
    ).toBeInTheDocument();
    expect(within(bar('r6')).queryByTestId('ww-run-bar-flask')).toBeNull();
  });

  it('starts the tooltip of a test run with "Test run, not kept"', async () => {
    const user = userEvent.setup();
    renderView({ runs: WITH_TEST, selectedId: 'r6' });
    await user.hover(bar('t1'));
    const tooltip = (
      await screen.findAllByTestId('ww-run-timeline-tooltip')
    )[0]!;
    expect(tooltip.firstElementChild).toHaveTextContent('Test run, not kept');
    expect(tooltip).toHaveTextContent('Succeeded');
  });

  it('names a test run in the accessible name of its bar', () => {
    renderView({ runs: WITH_TEST, selectedId: 'r6' });
    expect(bar('t1')).toHaveAccessibleName('Run t1, Test run, Succeeded, 3s');
  });

  it('never counts a test run in the median that decides a long run', () => {
    // Ten test runs of one second would make a run of 11 minutes look long.
    const tests = Array.from({ length: 10 }, (_, i) =>
      make(`q${i}`, Status.Success, 1, { ephemeral: true })
    );
    const slow = make('slow', Status.Success, 11 * 60);
    const normal = [
      make('n1', Status.Success, 5 * 60),
      make('n2', Status.Success, 6 * 60),
    ];
    renderView({ runs: [...normal, slow, ...tests], selectedId: 'slow' });
    expect(screen.queryByTestId('ww-run-extent-long')).toBeNull();
    // With the median of the runs that stay (6 min), no run lasts ten times it.
  });

  it('selects a test run with a click', () => {
    const { onSelect } = renderView({ runs: WITH_TEST, selectedId: 'r6' });
    fireEvent.click(bar('t1'));
    expect(onSelect).toHaveBeenCalledWith('t1');
  });

  describe('the toggle', () => {
    // A filter with nothing to choose stays, greyed and off.
    it('is greyed and off when the history holds no test run', () => {
      const onShowTestsChange = vi.fn();
      renderView({ hasTestRuns: false, onShowTestsChange });
      const toggle = screen.getByTestId('ww-run-timeline-tests');
      expect(toggle).toBeDisabled();
      fireEvent.click(toggle);
      expect(onShowTestsChange).not.toHaveBeenCalled();
    });

    it('is pressed by default and gives the new state on a click', () => {
      const onShowTestsChange = vi.fn();
      renderView({
        runs: WITH_TEST,
        hasTestRuns: true,
        showTests: true,
        onShowTestsChange,
      });
      const toggle = screen.getByTestId('ww-run-timeline-tests');
      expect(toggle).toHaveAttribute('aria-pressed', 'true');
      expect(toggle).toHaveAccessibleName('Hide test runs');
      fireEvent.click(toggle);
      expect(onShowTestsChange).toHaveBeenCalledWith(false);
    });

    it('offers to show them again when they are hidden', () => {
      renderView({
        hasTestRuns: true,
        showTests: false,
        onShowTestsChange: vi.fn(),
      });
      const toggle = screen.getByTestId('ww-run-timeline-tests');
      expect(toggle).toHaveAttribute('aria-pressed', 'false');
      expect(toggle).toHaveAccessibleName('Show test runs');
    });
  });

  describe('the trigger filter', () => {
    it('is greyed and off when the history has one trigger type', () => {
      renderView({
        triggerTypes: [TriggerType.manual],
        onHiddenTypesChange: vi.fn(),
      });
      expect(screen.getByTestId('ww-run-timeline-types')).toBeDisabled();
    });

    it('lists the types, checked unless hidden, and gives the new list on a click', async () => {
      const onHiddenTypesChange = vi.fn();
      renderView({
        triggerTypes: [TriggerType.manual, TriggerType.webhook],
        hiddenTypes: [TriggerType.webhook],
        onHiddenTypesChange,
      });
      const button = screen.getByTestId('ww-run-timeline-types');
      expect(button).toHaveAttribute('aria-pressed', 'true');
      await userEvent.click(button);
      const manual = await screen.findByTestId('ww-run-timeline-type-manual');
      expect(manual).toHaveAttribute('aria-checked', 'true');
      expect(
        screen.getByTestId('ww-run-timeline-type-webhook')
      ).toHaveAttribute('aria-checked', 'false');
      await userEvent.click(manual);
      expect(onHiddenTypesChange).toHaveBeenCalledWith([
        TriggerType.webhook,
        TriggerType.manual,
      ]);
    });
  });

  describe('RunTimeline', () => {
    function renderTimeline(
      runsNewestFirst: unknown[],
      selection: typeof h.selection = 'latest'
    ) {
      h.selection = selection;
      h.history = { runs: runsNewestFirst, isLoading: false };
      render(
        <MemoryRouter>
          <RunTimeline />
        </MemoryRouter>
      );
    }

    it('reads the history with the test runs and polls it', () => {
      renderTimeline([...RUNS].reverse());
      expect(h.historyArgs[0]).toMatchObject({
        enabled: true,
        includeEphemeral: true,
        pollTestRuns: true,
      });
    });

    it('draws the test runs, and the latest run stays the newest run that is kept', () => {
      h.latestRunId = null;
      renderTimeline([TEST, ...[...RUNS].reverse()]);
      expect(bar('t1')).toHaveAttribute('data-test-run', 'true');
      expect(bar('r6')).toHaveAttribute('data-latest', 'true');
      expect(bar('t1')).not.toHaveAttribute('data-latest');
      expect(bar('r6')).toHaveAttribute('aria-selected', 'true');
      h.latestRunId = 'r6';
    });

    it('highlights a test run that is selected', () => {
      renderTimeline([TEST, ...[...RUNS].reverse()], { runId: 't1' });
      expect(bar('t1')).toHaveAttribute('aria-selected', 'true');
    });

    it('counts the test runs in the number of runs shown', () => {
      renderTimeline([TEST, ...[...RUNS].reverse()]);
      fireEvent.change(screen.getByRole('combobox'), {
        target: { value: '5' },
      });
      expect(bars()).toHaveLength(5);
      // The newest of the five is the test run.
      expect(bars().map((b) => b.getAttribute('data-run-id'))).toContain('t1');
      expect(bars().map((b) => b.getAttribute('data-run-id'))).not.toContain(
        'r1'
      );
    });

    it('hides the test runs with the toggle, and keeps the choice in the browser', () => {
      renderTimeline([TEST, ...[...RUNS].reverse()]);
      expect(bars()).toHaveLength(7);
      fireEvent.click(screen.getByTestId('ww-run-timeline-tests'));
      expect(bars()).toHaveLength(6);
      expect(screen.queryByTestId('ww-run-bar-t1')).toBeNull();
      expect(window.localStorage.getItem(SHOW_TESTS_KEY)).toBe('0');
      // The toggle stays: the history still holds a test run.
      expect(screen.getByTestId('ww-run-timeline-tests')).toHaveAttribute(
        'aria-pressed',
        'false'
      );
    });

    it('reads a hidden state from the browser and works when the storage throws', () => {
      window.localStorage.setItem(SHOW_TESTS_KEY, '0');
      renderTimeline([TEST, ...[...RUNS].reverse()]);
      expect(bars()).toHaveLength(6);
      cleanup();
      const get = vi
        .spyOn(Storage.prototype, 'getItem')
        .mockImplementation(() => {
          throw new Error('blocked');
        });
      const set = vi
        .spyOn(Storage.prototype, 'setItem')
        .mockImplementation(() => {
          throw new Error('blocked');
        });
      renderTimeline([TEST, ...[...RUNS].reverse()]);
      expect(bars()).toHaveLength(7);
      fireEvent.click(screen.getByTestId('ww-run-timeline-tests'));
      expect(bars()).toHaveLength(6);
      get.mockRestore();
      set.mockRestore();
    });

    it('greys the toggle when the history holds no test run', () => {
      renderTimeline([...RUNS].reverse());
      expect(screen.getByTestId('ww-run-timeline-tests')).toBeDisabled();
    });
  });
});

// The cached time: a run that reused steps of other runs counts their time.
describe('cached time in the timeline', () => {
  // The cached run reused 10 s and 20 s: 30 s of cached time. Its own
  // time is 10 s; the other run took 40 s.
  const reuse = [
    {
      step: 'a',
      runId: 'src',
      via: 'run',
      startedAt: '2026-03-01T08:00:00Z',
      finishedAt: '2026-03-01T08:00:10Z',
    },
    {
      step: 'b',
      runId: 'src',
      via: 'run',
      startedAt: '2026-03-01T08:00:10Z',
      finishedAt: '2026-03-01T08:00:30Z',
    },
  ] as unknown as ApiDAGRunDetails['reusedInputs'];
  const CACHED = make('c1', Status.Success, 10, { reusedInputs: reuse });
  const BIG = make('big', Status.Success, 40, {}, '2026-03-01');
  const PAIR = [BIG, CACHED];

  beforeEach(() => {
    window.localStorage.clear();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('draws the bar as tall as the total time, with a hatched foot', () => {
    renderView({
      runs: PAIR,
      selectedId: 'c1',
      hasCachedTime: true,
      showCached: true,
      onShowCachedChange: vi.fn(),
    });
    // Total 40 s = the longest run: both bars are as tall as the plot.
    expect(fill('c1').style.height).toBe(`${BAR_MAX_PX}px`);
    expect(fill('big').style.height).toBe(`${BAR_MAX_PX}px`);
    const foot = within(bar('c1')).getByTestId('ww-run-bar-cached');
    // 30 of 40 s is cached.
    expect(Number.parseFloat(foot.style.height)).toBe(BAR_MAX_PX * 0.75);
    expect(foot.style.backgroundImage).toContain('repeating-linear-gradient');
    expect(within(bar('big')).queryByTestId('ww-run-bar-cached')).toBeNull();
  });

  it('draws the extent from the start of the cached time and hatches that part', () => {
    renderView({
      runs: PAIR,
      selectedId: 'c1',
      hasCachedTime: true,
      showCached: true,
      onShowCachedChange: vi.fn(),
    });
    const extent = screen.getByTestId('ww-run-extent-c1');
    const hatch = within(extent).getByTestId('ww-run-extent-cached');
    expect(Number.parseFloat(hatch.style.width)).toBeGreaterThan(0);
    expect(extent).toHaveAttribute('data-cached', 'true');
  });

  it('uses the own time alone when the cached time is off', () => {
    renderView({
      runs: PAIR,
      selectedId: 'c1',
      hasCachedTime: true,
      showCached: false,
      onShowCachedChange: vi.fn(),
    });
    expect(within(bar('c1')).queryByTestId('ww-run-bar-cached')).toBeNull();
    expect(screen.queryByTestId('ww-run-extent-cached')).toBeNull();
    // 10 s of 40 s.
    expect(fill('c1').style.height).toBe(`${BAR_MAX_PX * 0.25}px`);
  });

  it('tells the total, the own and the cached time in the tooltip', async () => {
    const user = userEvent.setup();
    renderView({
      runs: PAIR,
      hasCachedTime: true,
      showCached: true,
      onShowCachedChange: vi.fn(),
    });
    await user.hover(bar('c1'));
    const tooltip = (
      await screen.findAllByTestId('ww-run-timeline-tooltip')
    )[0]!;
    expect(tooltip).toHaveTextContent('Total 40s = own 10s + cached 30s');
  });

  it('greys the toggle unless a run has cached time', () => {
    renderView({ runs: PAIR, onShowCachedChange: vi.fn() });
    expect(screen.getByTestId('ww-run-timeline-cached')).toBeDisabled();
  });

  it('toggles the cached time', () => {
    const onShowCachedChange = vi.fn();
    renderView({
      runs: PAIR,
      hasCachedTime: true,
      showCached: true,
      onShowCachedChange,
    });
    const toggle = screen.getByTestId('ww-run-timeline-cached');
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(toggle);
    expect(onShowCachedChange).toHaveBeenCalledWith(false);
  });

  it('keeps the choice in the browser, on by default', () => {
    h.selection = 'latest';
    h.history = { runs: [...PAIR].reverse(), isLoading: false };
    render(
      <MemoryRouter>
        <RunTimeline />
      </MemoryRouter>
    );
    expect(screen.getByTestId('ww-run-timeline-cached')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    fireEvent.click(screen.getByTestId('ww-run-timeline-cached'));
    expect(window.localStorage.getItem(SHOW_CACHED_KEY)).toBe('0');
    expect(screen.queryByTestId('ww-run-bar-cached')).toBeNull();
  });
});

describe('time range in the timeline', () => {
  // Oldest first: three days, a day apart.
  const DAYS = ['2026-02-27', '2026-03-01', '2026-03-02'].map((d, i) =>
    make(`d${i}`, Status.Success, 30, {}, d)
  );

  beforeEach(() => {
    window.localStorage.clear();
    h.selection = 'latest';
    h.latestRunId = 'd2';
    h.historyArgs = [];
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
    h.latestRunId = 'r6';
  });

  function renderTimeline() {
    h.history = { runs: [...DAYS].reverse(), isLoading: false };
    render(
      <MemoryRouter>
        <RunTimeline />
      </MemoryRouter>
    );
  }
  const choose = (value: string) =>
    fireEvent.change(screen.getByRole('combobox'), { target: { value } });

  it('offers the presets and a custom range next to the counts', () => {
    renderTimeline();
    expect(
      within(screen.getByRole('combobox'))
        .getAllByRole('option')
        .map((o) => o.textContent)
    ).toEqual([
      'Last 5 runs',
      'Last 10 runs',
      'Last 20 runs',
      'Last 30 runs',
      'Last hour',
      'Last 6 hours',
      'Last 24 hours',
      'Last 7 days',
      'Custom range',
    ]);
  });

  it('shows the runs whose extent overlaps the range, and remembers the range', () => {
    renderTimeline();
    expect(bars()).toHaveLength(3);
    // The clock is at 2026-03-02 12:00; the runs are at 10:00 of the 27th, the 1st
    // and the 2nd.
    choose('range:7d');
    expect(bars()).toHaveLength(3);
    expect(window.localStorage.getItem(RUN_MODE_KEY)).toBe('range');
    expect(window.localStorage.getItem(RUN_RANGE_KEY)).toBe('{"preset":"7d"}');
    expect(screen.getByRole('combobox')).toHaveValue('range:7d');
    choose('range:24h');
    expect(bars().map((o) => o.getAttribute('data-run-id'))).toEqual(['d2']);
    choose('range:1h');
    expect(screen.getByTestId('ww-run-timeline-empty')).toBeVisible();
    choose('10');
    expect(window.localStorage.getItem(RUN_MODE_KEY)).toBe('count');
    expect(bars()).toHaveLength(3);
  });

  it('says that no run is in the range', () => {
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(
      RUN_RANGE_KEY,
      JSON.stringify({ preset: 'custom', from: 1000, to: 2000 })
    );
    renderTimeline();
    expect(screen.getByTestId('ww-run-timeline-empty')).toHaveTextContent(
      'No runs in this time range'
    );
  });

  it('opens two datetime boxes for a custom range and refuses a start after the end', () => {
    renderTimeline();
    choose('range:custom');
    const from = screen.getByTestId('ww-run-timeline-range-from');
    const to = screen.getByTestId('ww-run-timeline-range-to');
    fireEvent.change(from, { target: { value: '2026-02-26T00:00' } });
    fireEvent.change(to, { target: { value: '2026-03-02T00:00' } });
    const stored = JSON.parse(window.localStorage.getItem(RUN_RANGE_KEY)!);
    expect(stored).toMatchObject({ preset: 'custom' });
    expect(stored.from).toBe(new Date('2026-02-26T00:00').getTime());
    // A start after the end is not applied.
    fireEvent.change(from, { target: { value: '2026-03-05T00:00' } });
    expect(screen.getByTestId('ww-run-timeline-range-error')).toBeVisible();
    expect(JSON.parse(window.localStorage.getItem(RUN_RANGE_KEY)!)).toEqual(
      stored
    );
  });

  it('keeps the slots of the header row whatever the state', () => {
    renderTimeline();
    const row = screen.getByTestId('ww-run-timeline-controls');
    const slots = () => Array.from(row.children).length;
    const before = slots();
    choose('range:custom');
    expect(slots()).toBe(before);
    choose('5');
    expect(slots()).toBe(before);
  });

  it('notes that only the last 30 runs are loaded when the range reaches further', () => {
    const many = Array.from({ length: 30 }, (_, i) =>
      make(`m${i}`, Status.Success, 1, {}, '2026-03-01')
    );
    h.history = { runs: many, isLoading: false };
    window.localStorage.setItem(RUN_MODE_KEY, 'range');
    window.localStorage.setItem(RUN_RANGE_KEY, '{"preset":"7d"}');
    render(
      <MemoryRouter>
        <RunTimeline />
      </MemoryRouter>
    );
    expect(screen.getByTestId('ww-run-timeline-note')).toHaveTextContent(
      'Only the last 30 runs are loaded'
    );
  });
});
