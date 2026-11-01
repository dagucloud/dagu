// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import useSWR, { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import type { ApiDAGDetails, ApiDAGRunDetails } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import { propsEqual } from '../propsEqual';
import { RunStepList } from '../RunStepList';
import { RunTimelineView } from '../RunTimeline';
import {
  Fragment,
  isValidElement,
  useContext,
  type ComponentProps,
} from 'react';
import { FreezeWhenHidden } from '../FreezeWhenHidden';
import {
  WorkspaceDataContext,
  type WorkspaceDataValue,
} from '../../data/context';

// Counts of the renders of the memoized rows. A row of the step list renders
// one status dot, and a bar or an extent of the timeline one tooltip trigger
// (counted by the test ID of its target), for each render of its own.
const counts = vi.hoisted(() => ({ dots: 0, rows: new Map<string, number>() }));

vi.mock('@/components/ui/status-dot', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/components/ui/status-dot')>();
  return {
    ...actual,
    StatusDot: (props: ComponentProps<typeof actual.StatusDot>) => {
      counts.dots++;
      return <actual.StatusDot {...props} />;
    },
  };
});
vi.mock('@/components/ui/tooltip', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/components/ui/tooltip')>();
  return {
    ...actual,
    TooltipTrigger: (props: ComponentProps<typeof actual.TooltipTrigger>) => {
      const id = isValidElement<{ 'data-testid'?: string }>(props.children)
        ? props.children.props['data-testid']
        : undefined;
      if (id) counts.rows.set(id, (counts.rows.get(id) ?? 0) + 1);
      return <actual.TooltipTrigger {...props} />;
    },
  };
});

/** Renders of the timeline rows whose test ID starts with the prefix. */
function rowRenders(prefix: string): number {
  let total = 0;
  for (const [id, n] of counts.rows) if (id.startsWith(prefix)) total += n;
  return total;
}

afterEach(() => {
  counts.dots = 0;
  counts.rows.clear();
  vi.useRealTimers();
});

describe('propsEqual', () => {
  it('equals the same values and plain objects with the same entries', () => {
    const fn = () => {};
    expect(
      propsEqual(
        { a: 1, fn, box: { left: 1, right: 2 }, ids: ['x', 'y'] },
        { a: 1, fn, box: { left: 1, right: 2 }, ids: ['x', 'y'] }
      )
    ).toBe(true);
  });

  it('differs on a value, a function, an entry or a key', () => {
    const base = { a: 1, fn: () => {}, box: { left: 1 } };
    expect(propsEqual(base, { ...base, a: 2 })).toBe(false);
    expect(propsEqual(base, { ...base, fn: () => {} })).toBe(false);
    expect(propsEqual(base, { ...base, box: { left: 2 } })).toBe(false);
    expect(propsEqual(base, { ...base, extra: 1 } as typeof base)).toBe(false);
  });

  it('does not look inside a nested object twice', () => {
    expect(
      propsEqual({ box: { in: { a: 1 } } }, { box: { in: { a: 1 } } })
    ).toBe(false);
  });
});

function barRuns(): ApiDAGRunDetails[] {
  return ['r1', 'r2', 'r3'].map((id, i) =>
    run(id, [], Status.Success, {
      startedAt: `2026-03-02T1${i}:00:00Z`,
      finishedAt: `2026-03-02T1${i}:00:30Z`,
    })
  );
}

describe('RunTimelineView render cost', () => {
  // The hover state belongs to the view: a move of the pointer over a bar
  // renders the view, and no bar or extent.
  it('does not render the bars again when the pointer hovers one', async () => {
    const runs = barRuns();
    render(
      <MemoryRouter>
        <RunTimelineView
          runs={runs}
          loading={false}
          selectedId="r3"
          count={30}
          onSelect={vi.fn()}
          onCountChange={vi.fn()}
          now={Date.parse('2026-03-02T15:00:00Z')}
        />
      </MemoryRouter>
    );
    const bar = screen.getByTestId('ww-run-bar-r1');
    expect(rowRenders('ww-run-bar-')).toBeGreaterThan(0);
    counts.rows.clear();
    await userEvent.setup().hover(bar);
    expect(screen.getByTestId('ww-run-timeline-cursor')).toBeTruthy();
    // The extents in focus change their look, so only the bars are pinned.
    expect(rowRenders('ww-run-bar-')).toBe(0);
    await userEvent.setup().unhover(bar);
    expect(rowRenders('ww-run-bar-')).toBe(0);
  });

  // A closed dock hides the timeline: the clock of a running run stops.
  it('has no clock while the controls host is null', () => {
    vi.useFakeTimers();
    const running = [
      run('r1', [], Status.Running, { startedAt: '2026-03-02T10:00:00Z' }),
    ];
    const view = (host: HTMLElement | null | undefined) => (
      <MemoryRouter>
        <RunTimelineView
          runs={running}
          loading={false}
          selectedId="r1"
          count={30}
          onSelect={vi.fn()}
          onCountChange={vi.fn()}
          controlsHost={host}
        />
      </MemoryRouter>
    );
    const { rerender, unmount } = render(view(null));
    expect(vi.getTimerCount()).toBe(0);
    rerender(view(undefined));
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    rerender(view(null));
    expect(vi.getTimerCount()).toBe(0);
    unmount();
  });
});

describe('RunTimelineView clock', () => {
  const HOUR = 3_600_000;
  // Three finished runs over three hours, and one that runs.
  function liveRuns(): ApiDAGRunDetails[] {
    const start = Date.parse('2026-03-02T09:00:00Z');
    const finished = [0, 1, 2].map((i) =>
      run(`f${i}`, [], Status.Success, {
        startedAt: new Date(start + i * HOUR).toISOString(),
        finishedAt: new Date(start + i * HOUR + 600_000).toISOString(),
      })
    );
    return [
      ...finished,
      run('live', [], Status.Running, {
        startedAt: new Date(start + 3 * HOUR).toISOString(),
      }),
    ];
  }

  function view(runs: ApiDAGRunDetails[]) {
    return (
      <MemoryRouter>
        <RunTimelineView
          runs={runs}
          loading={false}
          selectedId="live"
          count={30}
          onSelect={vi.fn()}
          onCountChange={vi.fn()}
        />
      </MemoryRouter>
    );
  }

  // The ticks, the labels and the finished bars keep their style and do not
  // render between two steps of the layout; the running bar and its extent
  // render each second for their texts.
  it('renders only the running bar and extent each second', () => {
    vi.useFakeTimers();
    vi.setSystemTime(Date.parse('2026-03-02T12:00:05Z'));
    const runs = liveRuns();
    render(view(runs));
    const grid = () =>
      screen
        .getAllByTestId('ww-run-timeline-grid')
        .map((el) => (el as HTMLElement).style.left);
    const fill = (id: string) =>
      screen.getByTestId(`ww-run-bar-${id}`).querySelector('span')!.style
        .height;
    const gridBefore = grid();
    const heightBefore = fill('f0');
    expect(gridBefore.length).toBeGreaterThan(0);
    counts.rows.clear();
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    // One bar and one extent (the running run): two renders, not eight.
    expect(Object.fromEntries(counts.rows)).toEqual({
      'ww-run-bar-live': 1,
      'ww-run-extent-live': 1,
    });
    expect(grid()).toEqual(gridBefore);
    expect(fill('f0')).toBe(heightBefore);
    // The running run reads the time: its label moved on.
    expect(
      screen.getByTestId('ww-run-bar-live').getAttribute('aria-label')
    ).toContain('Running, 6s');
  });

  it('moves the grid when a step of the clock passes', () => {
    vi.useFakeTimers();
    vi.setSystemTime(Date.parse('2026-03-02T13:00:00Z'));
    render(view(liveRuns()));
    const grid = () =>
      screen
        .getAllByTestId('ww-run-timeline-grid')
        .map((el) => (el as HTMLElement).style.left);
    const before = grid();
    act(() => {
      vi.advanceTimersByTime(2 * HOUR);
    });
    expect(grid()).not.toEqual(before);
  });

  it('stops the clock of the bars while the view is hidden', () => {
    vi.useFakeTimers();
    const hidden = (
      <MemoryRouter>
        <RunTimelineView
          runs={liveRuns()}
          loading={false}
          selectedId="live"
          count={30}
          onSelect={vi.fn()}
          onCountChange={vi.fn()}
          controlsHost={null}
        />
      </MemoryRouter>
    );
    render(hidden);
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('FreezeWhenHidden', () => {
  const dataA = { fileName: 'a' } as unknown as WorkspaceDataValue;
  const dataB = { fileName: 'b' } as unknown as WorkspaceDataValue;
  const renders = vi.hoisted(() => ({ child: 0 }));

  function Child() {
    renders.child += 1;
    return null;
  }

  function Host({
    hidden,
    data,
    label,
  }: {
    hidden: boolean;
    data: WorkspaceDataValue;
    label: string;
  }) {
    return (
      <WorkspaceDataContext.Provider value={data}>
        <FreezeWhenHidden hidden={hidden}>
          <Fragment key={label}>
            <Child />
          </Fragment>
        </FreezeWhenHidden>
      </WorkspaceDataContext.Provider>
    );
  }

  // The tree below a hidden panel keeps its last render and the last data of
  // the workspace; it renders once when the panel shows again.
  it('does not render the children for a parent render or new data while hidden', () => {
    renders.child = 0;
    const { rerender } = render(<Host hidden={false} data={dataA} label="x" />);
    expect(renders.child).toBe(1);
    rerender(<Host hidden data={dataA} label="x" />);
    const after = renders.child;
    rerender(<Host hidden data={dataB} label="x" />);
    rerender(<Host hidden data={dataB} label="y" />);
    expect(renders.child).toBe(after);
    rerender(<Host hidden={false} data={dataB} label="y" />);
    expect(renders.child).toBeGreaterThan(after);
  });

  it('gives the readers the data that it had when it was shown', () => {
    let seen: string | undefined;
    function Reader() {
      seen = useContext(WorkspaceDataContext)?.fileName;
      return null;
    }
    const tree = (hidden: boolean, data: WorkspaceDataValue) => (
      <WorkspaceDataContext.Provider value={data}>
        <FreezeWhenHidden hidden={hidden}>
          <Reader />
        </FreezeWhenHidden>
      </WorkspaceDataContext.Provider>
    );
    const { rerender } = render(tree(false, dataA));
    expect(seen).toBe('a');
    rerender(tree(true, dataB));
    expect(seen).toBe('a');
    rerender(tree(false, dataB));
    expect(seen).toBe('b');
  });

  // A frozen body keeps the props of a run that was active: the polls of its
  // hooks stop while it is hidden, whatever interval the hook asks for.
  it('stops the SWR polls below it while hidden', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const fetcher = vi.fn(async () => 'ok');
    function Poller() {
      useSWR('poll', fetcher, { refreshInterval: 1000 });
      return null;
    }
    const tree = (hidden: boolean) => (
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <FreezeWhenHidden hidden={hidden}>
          <Poller />
        </FreezeWhenHidden>
      </SWRConfig>
    );
    const advance = (ms: number) =>
      act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
      });

    const { rerender } = render(tree(false));
    await advance(1500);
    expect(fetcher.mock.calls.length).toBeGreaterThan(1);

    rerender(tree(true));
    const hiddenCalls = fetcher.mock.calls.length;
    await advance(5000);
    expect(fetcher).toHaveBeenCalledTimes(hiddenCalls);

    rerender(tree(false));
    await advance(1500);
    expect(fetcher.mock.calls.length).toBeGreaterThan(hiddenCalls);
  });

  // A run can end and write its last log while the body is hidden; once it
  // shows, its queries are read again even when nothing polls them.
  it('reads the queries below it once when it shows again', async () => {
    let version = 1;
    const fetcher = vi.fn(async () => `v${version}`);
    function Reader() {
      const { data } = useSWR('log', fetcher, { revalidateOnFocus: false });
      return <span>{data ?? ''}</span>;
    }
    const tree = (hidden: boolean) => (
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <FreezeWhenHidden hidden={hidden}>
          <Reader />
        </FreezeWhenHidden>
      </SWRConfig>
    );

    const { rerender } = render(tree(false));
    expect(await screen.findByText('v1')).toBeInTheDocument();
    rerender(tree(true));
    version = 2;
    rerender(tree(false));
    expect(await screen.findByText('v2')).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  // A query that mounts as the subtree shows, as the log of another run,
  // makes its own first read and is not read a second time.
  it('reads a query that mounts as it shows only once', async () => {
    const fetcher = vi.fn(async (key: string) => key);
    function Reader({ runKey }: { runKey: string }) {
      const { data } = useSWR(runKey, fetcher, { revalidateOnFocus: false });
      return <span>{data ?? ''}</span>;
    }
    const tree = (hidden: boolean, runKey: string) => (
      <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
        <FreezeWhenHidden hidden={hidden}>
          <Reader runKey={runKey} />
        </FreezeWhenHidden>
      </SWRConfig>
    );

    const { rerender } = render(tree(false, 'log-1'));
    expect(await screen.findByText('log-1')).toBeInTheDocument();
    rerender(tree(true, 'log-1'));
    rerender(tree(false, 'log-2'));
    expect(await screen.findByText('log-2')).toBeInTheDocument();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(fetcher.mock.calls.filter(([key]) => key === 'log-2')).toHaveLength(
      1
    );
    expect(fetcher.mock.calls.filter(([key]) => key === 'log-1')).toHaveLength(
      1
    );
  });
});

describe('RunStepList render cost', () => {
  const dag = {
    name: 'demo',
    steps: [{ name: 'a' }, { name: 'b' }, { name: 'c' }],
  } as unknown as ApiDAGDetails;

  function list(details: ApiDAGRunDetails, selected: string | null = null) {
    return (
      <MemoryRouter>
        <RunStepList
          dag={dag}
          details={details}
          handlers={[]}
          selected={selected}
          onSelect={vi.fn()}
        />
      </MemoryRouter>
    );
  }

  const make = (statusOfB: NodeStatus) =>
    run(
      'run-1',
      [
        node('a', NodeStatus.Success),
        node('b', statusOfB),
        node('c', NodeStatus.Success),
      ],
      Status.Success
    );

  it('renders only the row whose step changed', () => {
    const { rerender } = render(list(make(NodeStatus.Success)));
    counts.dots = 0;
    // A copy of the run with the same content: only the summary row renders.
    rerender(list(make(NodeStatus.Success)));
    expect(counts.dots).toBe(1);
    counts.dots = 0;
    rerender(list(make(NodeStatus.Failed)));
    // The summary row and the row of b.
    expect(counts.dots).toBe(2);
  });

  it('renders the two rows that a new selection touches', () => {
    const details = make(NodeStatus.Success);
    const { rerender } = render(list(details, null));
    counts.dots = 0;
    act(() => {
      rerender(list(details, 'b'));
    });
    // The summary row and the row of b: a, c and the others stay.
    expect(counts.dots).toBe(2);
  });
});
