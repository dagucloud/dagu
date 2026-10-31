// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  RepeatMode,
  ReusedInputVia,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import TimelineChart from '../TimelineChart';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];
type SubDAGRun = components['schemas']['SubDAGRun'];
type SubDAGRunDetail = components['schemas']['SubDAGRunDetail'];

const useQueryMock = useQuery as unknown as {
  mockImplementation: (
    fn: (path: string, init?: unknown, config?: unknown) => unknown
  ) => void;
  mockReturnValue: (value: unknown) => void;
};

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local', 'remote-a'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'remote-a',
  selectRemoteNode: vi.fn(),
};
const repeatPolicy = { repeat: RepeatMode.While };

function node(overrides: Partial<Node> = {}): Node {
  const { step: stepOverrides, ...nodeOverrides } = overrides;

  return {
    stdout: '',
    stderr: '',
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: '2026-01-01T00:00:10Z',
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    retryCount: 0,
    doneCount: 1,
    ...nodeOverrides,
    step: {
      name: 'step-a',
      ...stepOverrides,
    },
  };
}

function dagRun(overrides: Partial<DAGRunDetails> = {}): DAGRunDetails {
  return {
    dagRunId: 'root-run',
    name: 'root-dag',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    autoRetryCount: 0,
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: '2026-01-01T00:01:00Z',
    artifactsAvailable: false,
    rootDAGRunName: 'root-dag',
    rootDAGRunId: 'root-run',
    log: '',
    nodes: [node()],
    ...overrides,
  };
}

function subRun(
  dagRunId: string,
  overrides: Partial<SubDAGRun> = {}
): SubDAGRun {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: `{"ITEM":"${dagRunId}"}`,
    ...overrides,
  };
}

function subRunDetail(
  dagRunId: string,
  overrides: Partial<SubDAGRunDetail> = {}
): SubDAGRunDetail {
  return {
    dagRunId,
    dagName: 'child-dag',
    params: `{"ITEM":"${dagRunId}"}`,
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    startedAt: '2026-01-01T00:00:01Z',
    finishedAt: '2026-01-01T00:00:05Z',
    ...overrides,
  };
}

function parallelNode(
  stepName: string,
  runs: SubDAGRun[],
  overrides: Partial<Node> = {}
): Node {
  return node({
    step: {
      name: stepName,
      call: 'child-dag',
      parallel: { items: runs.map((run) => run.dagRunId) },
      ...overrides.step,
    },
    subRuns: runs,
    ...overrides,
  });
}

function renderChart(
  status: DAGRunDetails,
  options: {
    appBarOverride?: Partial<typeof appBarValue>;
    onOpenSubRun?: (entry: { name: string; dagRunId: string }) => void;
  } = {}
) {
  return render(
    <AppBarContext.Provider
      value={{ ...appBarValue, ...options.appBarOverride }}
    >
      <TimelineChart status={status} onOpenSubRun={options.onOpenSubRun} />
    </AppBarContext.Provider>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  useQueryMock.mockReturnValue({
    data: { subRuns: [] },
    mutate: vi.fn(),
  });
});

describe('TimelineChart', () => {
  it('passes null query init when there are no eligible parallel child rows', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return { data: { subRuns: [] }, mutate: vi.fn() } as never;
    });

    renderChart(dagRun());

    expect(queryCalls).toContainEqual({
      path: '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
      init: null,
    });
  });

  it('does not render child rows for non-parallel sub-DAG calls', () => {
    useQueryMock.mockReturnValue({
      data: { subRuns: [subRunDetail('child-run-1')] },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'call-child', call: 'child-dag' },
            subRuns: [subRun('child-run-1')],
          }),
        ],
      })
    );

    expect(screen.getByText('call-child')).toBeInTheDocument();
    expect(screen.queryByText('#01')).not.toBeInTheDocument();
  });

  it('does not expand archived parallel batches when subRunsRepeated is present', () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('archived-batch-1'),
          subRunDetail('parallel-run-1'),
          subRunDetail('parallel-run-2'),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          parallelNode(
            'parallel-call',
            [subRun('parallel-run-1'), subRun('parallel-run-2')],
            {
              subRunsRepeated: [subRun('archived-batch-1')],
            }
          ),
        ],
      })
    );

    expect(
      screen
        .getAllByTestId('timeline-row')
        .map((row) => row.getAttribute('data-row-id'))
    ).toEqual([
      'step:parallel-call',
      'subdag:parallel-call:parallel-run-1',
      'subdag:parallel-call:parallel-run-2',
    ]);
    expect(
      screen.queryByTestId('timeline-bar-subdag:parallel-call:archived-batch-1')
    ).not.toBeInTheDocument();
  });

  it('keeps ordinary calls collapsed while expanding parallel and repeat in one chart', () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('ordinary-run'),
          subRunDetail('parallel-run-1'),
          subRunDetail('repeat-run-1'),
          subRunDetail('repeat-run-2'),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'ordinary-call', call: 'child-dag' },
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:05Z',
            subRuns: [subRun('ordinary-run')],
          }),
          parallelNode('parallel-call', [subRun('parallel-run-1')], {
            startedAt: '2026-01-01T00:00:10Z',
            finishedAt: '2026-01-01T00:00:20Z',
          }),
          node({
            step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
            startedAt: '2026-01-01T00:00:30Z',
            finishedAt: '2026-01-01T00:00:50Z',
            subRunsRepeated: [subRun('repeat-run-1')],
            subRuns: [subRun('repeat-run-2')],
          }),
        ],
      })
    );

    expect(
      screen
        .getAllByTestId('timeline-row')
        .map((row) => row.getAttribute('data-row-id'))
    ).toEqual([
      'step:ordinary-call',
      'step:parallel-call',
      'subdag:parallel-call:parallel-run-1',
      'step:repeat-child',
      'subdag:repeat-child:repeat-run-1',
      'subdag:repeat-child:repeat-run-2',
    ]);
    expect(
      screen.queryByTestId('timeline-bar-subdag:ordinary-call:ordinary-run')
    ).not.toBeInTheDocument();
  });

  it('fetches and renders repeat-policy child rows in archive-then-current order', () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('repeat-run-1'),
          subRunDetail('repeat-run-2'),
          subRunDetail('repeat-run-3'),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
            subRunsRepeated: [subRun('repeat-run-1'), subRun('repeat-run-2')],
            subRuns: [subRun('repeat-run-3')],
          }),
        ],
      })
    );

    expect(
      screen
        .getAllByTestId('timeline-row')
        .map((row) => row.getAttribute('data-row-id'))
    ).toEqual([
      'step:repeat-child',
      'subdag:repeat-child:repeat-run-1',
      'subdag:repeat-child:repeat-run-2',
      'subdag:repeat-child:repeat-run-3',
    ]);
    expect(screen.getByText('#01')).toBeInTheDocument();
    expect(screen.getByText('#02')).toBeInTheDocument();
    expect(screen.getByText('#03')).toBeInTheDocument();
  });

  it('opens a parallel child run when its timeline bar is clicked', async () => {
    const onOpenSubRun = vi.fn();
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [subRunDetail('child-run-1')],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [parallelNode('parallel-call', [subRun('child-run-1')])],
      }),
      { onOpenSubRun }
    );

    const bar = screen.getByTestId(
      'timeline-bar-subdag:parallel-call:child-run-1'
    );
    expect(bar).toHaveAttribute('role', 'button');
    expect(bar).toHaveAttribute('tabIndex', '0');
    expect(bar).toHaveAccessibleName('Open child-dag run child-run-1');

    await userEvent.click(bar);

    expect(onOpenSubRun).toHaveBeenCalledWith({
      name: 'child-dag',
      dagRunId: 'child-run-1',
    });
  });

  it('opens a repeat-policy child run when Enter is pressed on its bar', async () => {
    const onOpenSubRun = vi.fn();
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [subRunDetail('repeat-run-1'), subRunDetail('repeat-run-2')],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
            subRunsRepeated: [subRun('repeat-run-1')],
            subRuns: [subRun('repeat-run-2')],
          }),
        ],
      }),
      { onOpenSubRun }
    );

    const bar = screen.getByTestId(
      'timeline-bar-subdag:repeat-child:repeat-run-2'
    );
    expect(bar).toHaveAccessibleName('Open child-dag run repeat-run-2');
    bar.focus();
    await userEvent.keyboard('{Enter}');

    expect(onOpenSubRun).toHaveBeenCalledWith({
      name: 'child-dag',
      dagRunId: 'repeat-run-2',
    });
  });

  it('opens a repeat-policy child run when Space is pressed on its bar', async () => {
    const onOpenSubRun = vi.fn();
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [subRunDetail('repeat-run-1'), subRunDetail('repeat-run-2')],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
            subRunsRepeated: [subRun('repeat-run-1')],
            subRuns: [subRun('repeat-run-2')],
          }),
        ],
      }),
      { onOpenSubRun }
    );

    const bar = screen.getByTestId(
      'timeline-bar-subdag:repeat-child:repeat-run-2'
    );
    bar.focus();
    await userEvent.keyboard(' ');

    expect(onOpenSubRun).toHaveBeenCalledWith({
      name: 'child-dag',
      dagRunId: 'repeat-run-2',
    });
  });

  it('does not treat parent step bars as buttons', () => {
    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'ordinary-step' },
          }),
        ],
      })
    );

    const bar = screen.getByTestId('timeline-bar-step:ordinary-step');
    expect(bar).not.toHaveAttribute('role', 'button');
    expect(bar).not.toHaveAttribute('tabIndex');
    expect(bar).not.toHaveAttribute('aria-label');
  });

  it('opens a repeat-policy child run when its timeline bar is clicked', async () => {
    const onOpenSubRun = vi.fn();
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [subRunDetail('repeat-run-1'), subRunDetail('repeat-run-2')],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
            subRunsRepeated: [subRun('repeat-run-1')],
            subRuns: [subRun('repeat-run-2')],
          }),
        ],
      }),
      { onOpenSubRun }
    );

    await userEvent.click(
      screen.getByTestId('timeline-bar-subdag:repeat-child:repeat-run-2')
    );

    expect(onOpenSubRun).toHaveBeenCalledWith({
      name: 'child-dag',
      dagRunId: 'repeat-run-2',
    });
  });

  it('keeps tooltips for child rows after click wiring', async () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [subRunDetail('repeat-run-1')],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'repeat-child', call: 'child-dag', repeatPolicy },
            subRunsRepeated: [subRun('repeat-run-1')],
          }),
        ],
      })
    );

    await userEvent.hover(
      screen.getByTestId('timeline-bar-subdag:repeat-child:repeat-run-1')
    );

    expect(await screen.findAllByText('DAG: child-dag')).not.toHaveLength(0);
    expect(screen.getAllByText('Run ID: repeat-run-1')).not.toHaveLength(0);
  });

  it('queries root timeline details with the root DAG name and run ID', () => {
    const queryCalls: Array<{
      path: string;
      init?: unknown;
      config?: unknown;
    }> = [];
    useQueryMock.mockImplementation((path, init, config) => {
      queryCalls.push({ path, init, config });
      return { data: { subRuns: [] }, mutate: vi.fn() } as never;
    });

    renderChart(
      dagRun({
        status: Status.Running,
        nodes: [parallelNode('parallel-call', [subRun('child-run-1')])],
      })
    );

    expect(queryCalls[0]).toEqual(
      expect.objectContaining({
        path: '/dag-runs/{name}/{dagRunId}/sub-dag-runs',
        init: {
          params: {
            path: {
              name: 'root-dag',
              dagRunId: 'root-run',
            },
            query: {
              remoteNode: 'remote-a',
              parentSubDAGRunId: undefined,
            },
          },
        },
        config: {
          refreshInterval: 3000,
        },
      })
    );
  });

  it('queries nested timeline details with parentSubDAGRunId set to the current sub-DAG run ID', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return { data: { subRuns: [] }, mutate: vi.fn() } as never;
    });

    renderChart(
      dagRun({
        name: 'child-dag',
        dagRunId: 'child-run',
        rootDAGRunName: 'root-dag',
        rootDAGRunId: 'root-run',
        nodes: [parallelNode('nested-parallel', [subRun('grandchild-run')])],
      })
    );

    expect(queryCalls[0]?.init).toEqual({
      params: {
        path: {
          name: 'root-dag',
          dagRunId: 'root-run',
        },
        query: {
          remoteNode: 'remote-a',
          parentSubDAGRunId: 'child-run',
        },
      },
    });
  });

  it('renders child rows under a parallel parent with child tooltip details', async () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('child-run-1', { params: '{"SCOPE":"item1"}' }),
          subRunDetail('child-run-2', { params: '{"SCOPE":"item2"}' }),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          parallelNode('parallel-call', [
            subRun('child-run-1'),
            subRun('child-run-2'),
          ]),
        ],
      })
    );

    const rows = screen.getAllByTestId('timeline-row');
    expect(rows.map((row) => row.getAttribute('data-row-id'))).toEqual([
      'step:parallel-call',
      'subdag:parallel-call:child-run-1',
      'subdag:parallel-call:child-run-2',
    ]);
    expect(screen.getByText('#01')).toBeInTheDocument();
    expect(screen.getByText('#02')).toBeInTheDocument();

    await userEvent.hover(
      screen.getByTestId('timeline-bar-subdag:parallel-call:child-run-1')
    );

    expect(await screen.findAllByText('DAG: child-dag')).not.toHaveLength(0);
    expect(screen.getAllByText('Run ID: child-run-1')).not.toHaveLength(0);
    expect(screen.getAllByText('Params: {"SCOPE":"item1"}')).not.toHaveLength(
      0
    );
  });

  it('filters endpoint details for multiple parallel nodes to matching run IDs', () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('b-run', { dagName: 'child-b' }),
          subRunDetail('unrelated-run'),
          subRunDetail('a-run', { dagName: 'child-a' }),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [
          parallelNode('parallel-a', [subRun('a-run')], {
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:10Z',
          }),
          parallelNode('parallel-b', [subRun('b-run')], {
            startedAt: '2026-01-01T00:00:20Z',
            finishedAt: '2026-01-01T00:00:30Z',
          }),
        ],
      })
    );

    expect(
      screen
        .getAllByTestId('timeline-row')
        .map((row) => row.getAttribute('data-row-id'))
    ).toEqual([
      'step:parallel-a',
      'subdag:parallel-a:a-run',
      'step:parallel-b',
      'subdag:parallel-b:b-run',
    ]);
    expect(screen.getAllByText('#01')).toHaveLength(2);
  });

  it('keeps parent errors in the step tooltip', async () => {
    renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'failing-step' },
            status: NodeStatus.Failed,
            statusLabel: NodeStatusLabel.failed,
            error: 'parent exploded',
          }),
        ],
      })
    );

    await userEvent.hover(screen.getByTestId('timeline-bar-step:failing-step'));

    expect(
      await screen.findAllByText('Error: parent exploded')
    ).not.toHaveLength(0);
  });

  it('displays child DAG-run queued status as Queued, not Skipped', async () => {
    useQueryMock.mockReturnValue({
      data: {
        subRuns: [
          subRunDetail('queued-run', {
            status: Status.Queued,
            statusLabel: StatusLabel.queued,
          }),
        ],
      },
      mutate: vi.fn(),
    });

    renderChart(
      dagRun({
        nodes: [parallelNode('parallel-call', [subRun('queued-run')])],
      })
    );

    await userEvent.hover(
      screen.getByTestId('timeline-bar-subdag:parallel-call:queued-run')
    );

    const tooltip = (await screen.findAllByText('Queued'))
      .map((element) => element.closest('[data-slot="tooltip-content"]'))
      .find((element): element is HTMLElement => element !== null);
    expect(tooltip).toBeInTheDocument();
    expect(
      within(tooltip as HTMLElement).queryByText('Skipped')
    ).not.toBeInTheDocument();
  });

  // The first and last tick labels were cut by the edges of the chart.
  it('anchors the first axis label at the left edge and the last at the right', () => {
    const { container } = renderChart(
      dagRun({ nodes: [node({ step: { name: 'a' } })] })
    );

    const labels = Array.from(
      container.querySelectorAll<HTMLElement>('.font-mono')
    )
      .map((span) => span.parentElement as HTMLElement)
      .filter((el) => el.style.left.endsWith('%'));
    expect(labels).toHaveLength(6);
    expect(labels[0]!.style.transform).toBe('none');
    expect(labels[1]!.style.transform).toBe('translateX(-50%)');
    expect(labels[5]!.style.transform).toBe('translateX(-100%)');
    // Both keep a gap to the edge, so no digit touches the border.
    expect(labels[0]).toHaveClass('pl-2');
    expect(labels[5]).toHaveClass('pr-2');
  });

  // A short run repeated '06:40:46' on several ticks.
  it('writes no axis label twice on a run shorter than 10 seconds', () => {
    const { container } = renderChart(
      dagRun({
        nodes: [
          node({
            step: { name: 'a' },
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:03Z',
          }),
        ],
      })
    );

    const labels = Array.from(
      container.querySelectorAll<HTMLElement>('.font-mono')
    )
      .filter((span) => span.parentElement?.style.left.endsWith('%'))
      .map((span) => span.textContent);
    expect(labels).toHaveLength(6);
    expect(new Set(labels).size).toBe(6);
  });

  // A skipped step did not run: no bar of the length of its timestamps.
  it('draws a skipped step as a dotted hairline and a ran step as a bar', () => {
    renderChart(
      dagRun({
        nodes: [
          node({ step: { name: 'ran' } }),
          node({
            step: { name: 'skipped' },
            status: NodeStatus.Skipped,
            statusLabel: NodeStatusLabel.skipped,
          }),
        ],
      })
    );

    expect(screen.getByTestId('timeline-bar-step:ran')).toHaveClass('h-5');
    const skipped = screen.getByTestId('timeline-bar-step:skipped');
    expect(skipped).toHaveClass('h-0', 'border-dotted', 'before:h-5');
    expect(skipped).not.toHaveClass('h-5');
  });

  it('shows a row for each lifecycle handler', () => {
    renderChart(
      dagRun({
        nodes: [node({ step: { name: 'a' } })],
        onExit: node({
          step: { name: 'onExit' },
          startedAt: '2026-01-01T00:00:12Z',
          finishedAt: '2026-01-01T00:00:13Z',
        }),
      })
    );

    expect(
      screen.getByTestId('timeline-bar-handler:onExit')
    ).toBeInTheDocument();
  });

  // The times of a run that an older version stored have a resolution of one
  // second: three fast steps of a chain carry the same second, and the bars
  // still read left to right.
  describe('steps of 0 ms in a chain', () => {
    const same = {
      startedAt: '2026-01-01T00:00:05Z',
      finishedAt: '2026-01-01T00:00:05Z',
    };
    const chain = () =>
      dagRun({
        nodes: [
          node({ ...same, step: { name: 'third', depends: ['second'] } }),
          node({ ...same, step: { name: 'first' } }),
          node({ ...same, step: { name: 'second', depends: ['first'] } }),
        ],
      });
    const percent = (el: HTMLElement, key: 'left' | 'width') =>
      parseFloat(el.style[key]);

    it('orders the rows by dependency and starts each bar after the previous one', () => {
      renderChart(chain());

      expect(
        screen
          .getAllByTestId('timeline-row')
          .map((row) => row.getAttribute('data-row-id'))
      ).toEqual(['step:first', 'step:second', 'step:third']);
      const bars = ['first', 'second', 'third'].map((name) =>
        screen.getByTestId(`timeline-bar-step:${name}`)
      );
      for (let i = 1; i < bars.length; i++) {
        const before = bars[i - 1]!;
        expect(percent(bars[i]!, 'left')).toBeGreaterThanOrEqual(
          percent(before, 'left') + percent(before, 'width')
        );
      }
    });

    it('keeps the real times in the tooltip and says why the bar moved', async () => {
      renderChart(chain());

      await userEvent.hover(screen.getByTestId('timeline-bar-step:second'));

      expect(await screen.findAllByText('<1s')).not.toHaveLength(0);
      expect(
        screen.getAllByText(
          'Shown after first (times are rounded to the second)'
        )
      ).not.toHaveLength(0);
      expect(screen.queryByText('0ms')).not.toBeInTheDocument();
      const times = screen.getAllByText(/→/)[0]!.textContent!;
      const [from, to] = times.split(' → ');
      expect(from).toBe(to);
    });

    it('reads the real small durations of a run with millisecond times', async () => {
      renderChart(
        dagRun({
          nodes: [
            node({
              startedAt: '2026-01-01T00:00:05.010Z',
              finishedAt: '2026-01-01T00:00:05.197Z',
              step: { name: 'first' },
            }),
            node({
              startedAt: '2026-01-01T00:00:05.200Z',
              finishedAt: '2026-01-01T00:00:05.212Z',
              step: { name: 'second', depends: ['first'] },
            }),
          ],
        })
      );

      await userEvent.hover(screen.getByTestId('timeline-bar-step:second'));

      expect(await screen.findAllByText('12ms')).not.toHaveLength(0);
      expect(screen.queryByText('<1s')).not.toBeInTheDocument();
      expect(screen.queryByText(/Shown after/)).not.toBeInTheDocument();
    });

    it('does not claim a shift for the first step', async () => {
      renderChart(chain());

      await userEvent.hover(screen.getByTestId('timeline-bar-step:first'));

      expect(await screen.findAllByText('<1s')).not.toHaveLength(0);
      expect(screen.queryByText(/Shown after/)).not.toBeInTheDocument();
    });
  });
});

// The time that a reused step took in its source run is carried into the
// timeline of the run that reuses it, as a hatched ghost row.
describe('TimelineChart ghost rows', () => {
  const reused = () =>
    dagRun({
      startedAt: '2026-01-08T10:00:00Z',
      finishedAt: '2026-01-08T10:00:05Z',
      reusedInputs: [
        {
          step: 'slow',
          runId: '034mroF1udXVyvPz00bn5a',
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:20Z',
          capturedAt: '2026-01-01T00:00:20Z',
          via: ReusedInputVia.run,
        },
      ],
      nodes: [
        node({
          step: { name: 'slow' },
          status: NodeStatus.Skipped,
          startedAt: '2026-01-01T00:00:00Z',
          finishedAt: '2026-01-01T00:00:20Z',
        }),
        node({
          step: { name: 'use', depends: ['slow'] },
          startedAt: '2026-01-08T10:00:00Z',
          finishedAt: '2026-01-08T10:00:05Z',
        }),
      ],
    });

  it('draws a hatched bar with the run and the time that the step took', () => {
    renderChart(reused());
    const bar = screen.getByTestId('timeline-bar-step:slow');
    expect(bar).toHaveAttribute('data-ghost', 'true');
    expect(bar.style.backgroundImage).toContain('repeating-linear-gradient');
    expect(screen.getByTestId('timeline-ghost-label')).toHaveTextContent(
      'run 034mroF1 · 20s'
    );
    // The step that ran has a plain bar.
    const ran = screen.getByTestId('timeline-bar-step:use');
    expect(ran).not.toHaveAttribute('data-ghost');
    expect(ran.style.backgroundImage).toBe('');
  });

  it('scales the ghost like the other bars: 20 s against 5 s', () => {
    renderChart(reused());
    const width = (id: string) =>
      Number.parseFloat(
        screen.getByTestId(`timeline-bar-step:${id}`).style.width
      );
    expect(width('slow') / width('use')).toBeCloseTo(4, 0);
  });

  it('draws the ghost before the start of the run and fits the axis around it', () => {
    renderChart(reused());
    const left = (id: string) =>
      Number.parseFloat(
        screen.getByTestId(`timeline-bar-step:${id}`).style.left
      );
    const width = (id: string) =>
      Number.parseFloat(
        screen.getByTestId(`timeline-bar-step:${id}`).style.width
      );
    // The ghost ends where the step that ran starts: no overlap.
    expect(left('slow') + width('slow')).toBeCloseTo(left('use'), 5);
    expect(left('slow')).toBeLessThan(left('use'));
    // Nothing is clipped: the axis starts before the ghost.
    expect(left('slow')).toBeGreaterThanOrEqual(0);
    expect(left('use') + width('use')).toBeLessThanOrEqual(100);
  });

  it('marks the start of the run and hatches the cached time before it', () => {
    renderChart(reused());
    expect(screen.getByTestId('timeline-run-start')).toHaveTextContent(
      /Run starts \d\d:\d\d:\d\d/
    );
    const zones = screen.getAllByTestId('timeline-cached-zone');
    // One for the strip of the label, one for each row.
    expect(zones).toHaveLength(3);
    expect(zones[0]!.style.backgroundImage).toContain(
      'repeating-linear-gradient'
    );
    const use = screen.getByTestId('timeline-bar-step:use');
    expect(Number.parseFloat(zones[0]!.style.width)).toBeCloseTo(
      Number.parseFloat(use.style.left),
      5
    );
  });

  it('says in the tooltip that the step was reused, from which run, and when it ran there', async () => {
    renderChart(reused());
    await userEvent.hover(screen.getByTestId('timeline-bar-step:slow'));
    const source = (await screen.findAllByTestId('timeline-ghost-source'))[0]!;
    expect(source).toHaveTextContent('Reused from run 034mroF1udXVyvPz00bn5a');
    expect(source).toHaveTextContent(/In that run: 2026-01-01 .* → /);
    expect(screen.getAllByText('Reused').length).toBeGreaterThan(0);
    expect(screen.getAllByText('20s').length).toBeGreaterThan(0);
  });

  it('puts the label of a ghost near the right edge on the left of its bar', () => {
    const run = reused();
    run.reusedInputs = [
      ...(run.reusedInputs ?? []),
      {
        step: 'late',
        runId: '034mroF1udXVyvPz00bn5a',
        startedAt: '2026-01-01T00:00:20Z',
        finishedAt: '2026-01-01T00:00:21Z',
        via: ReusedInputVia.run,
      },
    ];
    run.nodes = [
      ...run.nodes,
      node({
        step: { name: 'late', depends: ['slow'] },
        status: NodeStatus.Skipped,
        startedAt: '2026-01-01T00:00:20Z',
        finishedAt: '2026-01-01T00:00:21Z',
      }),
    ];
    renderChart(run);
    const labels = screen.getAllByTestId('timeline-ghost-label');
    // The first ghost starts at the left: its label stands on its bar. The
    // chained ghost starts after it, at the right: its label stands before it.
    expect(labels[0]!.style.left).not.toBe('');
    expect(labels[labels.length - 1]!.style.right).not.toBe('');
  });

  it('draws a run that reused nothing as before', () => {
    renderChart(dagRun());
    expect(screen.queryByTestId('timeline-ghost-label')).toBeNull();
    expect(screen.queryByTestId('timeline-run-start')).toBeNull();
    expect(screen.queryByTestId('timeline-cached-zone')).toBeNull();
  });
});
