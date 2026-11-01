// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  BuildExecutionDecision,
  BuildExecutionPhase,
  NodeStatus,
  RepeatMode,
  ReusedInputVia,
  Status,
} from '@/api/v1/schema';
import { RunMeta } from '@/features/visual-editor/canvas/nodes/NodeParts';
import { getEventHandlers } from '@/features/dags/lib/getEventHandlers';
import type { ApiDAGDetails, ApiDAGRunDetails } from '../../contracts';
import { runHandlers } from '../../data/handlerMapping';
import { node, run } from '../../data/__tests__/fixtures';
import { RunStepList, type RunStepListProps } from '../RunStepList';

const HANDLER_EVENTS = [
  'onInit',
  'onWait',
  'onSuccess',
  'onFailure',
  'onAbort',
  'onExit',
] as const;

function dagOf(steps: string[], handlerOn: Record<string, string> = {}) {
  return {
    name: 'demo',
    steps: steps.map((name) => ({ name })),
    handlerOn: Object.fromEntries(
      Object.entries(handlerOn).map(([event, name]) => [event, { name }])
    ),
  } as unknown as ApiDAGDetails;
}

/** A run whose six handlers are set in an order unlike getEventHandlers. */
function runWithHandlers(nodes = [node('b'), node('a'), node('c')]) {
  const details = run('run-1', nodes, Status.Success);
  for (const event of [...HANDLER_EVENTS].reverse()) {
    (details as unknown as Record<string, unknown>)[event] = node(event);
  }
  return details;
}

function renderList(
  details: ApiDAGRunDetails,
  props: Partial<RunStepListProps> = {}
) {
  const onSelect = vi.fn();
  const view = render(
    <RunStepList
      dag={dagOf(['a', 'b'])}
      details={details}
      handlers={runHandlers(details)}
      selected={null}
      onSelect={onSelect}
      {...props}
    />,
    { wrapper: MemoryRouter }
  );
  return { ...view, onSelect };
}

function rowOf(name: string): HTMLElement {
  const row = document.querySelector<HTMLElement>(
    `li[data-step="${CSS.escape(name)}"]`
  );
  if (!row) throw new Error(`no row ${name}`);
  return row;
}

/** The rows and the group heading, in document order. */
function outline(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('li')).map((li) =>
    li.hasAttribute('data-summary')
      ? 'summary'
      : (li.getAttribute('data-step') ?? `# ${li.textContent}`)
  );
}

const scrollIntoView = Element.prototype.scrollIntoView;

afterEach(() => {
  vi.useRealTimers();
  Element.prototype.scrollIntoView = scrollIntoView;
});

describe('RunStepList', () => {
  // parity:steps-table-layout parity:run-steps-table parity:history-node-tables
  // parity:lifecycle-handlers-table parity:run-lifecycle-handlers parity:dup-14
  it('lists the summary, the steps in run order, then the six handlers', () => {
    const details = runWithHandlers();
    const { container } = renderList(details);

    expect(outline(container)).toEqual([
      'summary',
      'b',
      'a',
      'c',
      '# Lifecycle handlers',
      ...getEventHandlers(details).map((n) => n.step.name),
    ]);
    expect(getEventHandlers(details).map((n) => n.step.name)).toEqual([
      ...HANDLER_EVENTS,
    ]);
  });

  it('shows no handler group when the run has no handler', () => {
    const { container } = renderList(run('run-1', [node('a')]));

    expect(outline(container)).toEqual(['summary', 'a']);
    expect(screen.queryByText('Lifecycle handlers')).not.toBeInTheDocument();
  });

  // parity:history-node-tables parity:dup-14
  it('marks the run nodes that the saved definition does not have', () => {
    const details = runWithHandlers();
    renderList(details, {
      dag: dagOf(['a', 'b'], { failure: 'onFailure', exit: 'onExit' }),
    });

    const marker = 'not in current definition';
    for (const name of ['c', 'onInit', 'onWait', 'onSuccess', 'onAbort']) {
      expect(within(rowOf(name)).getByText(marker)).toBeInTheDocument();
    }
    for (const name of ['a', 'b', 'onFailure', 'onExit']) {
      expect(within(rowOf(name)).queryByText(marker)).not.toBeInTheDocument();
    }
  });

  // parity:steps-name-description parity:common-status-chips
  it('shows the status dot, the name and the description', () => {
    renderList(
      run('run-1', [
        node('a', NodeStatus.Success, {
          step: { description: 'Load the data' },
        }),
        node('b', NodeStatus.Failed),
      ])
    );

    const a = rowOf('a');
    expect(within(a).getByRole('img', { name: 'Succeeded' })).toHaveClass(
      'bg-[var(--status-success)]'
    );
    expect(within(a).getByRole('button', { name: 'a' })).toBeInTheDocument();
    expect(within(a).getByText('Load the data')).toBeInTheDocument();
    expect(within(rowOf('b')).getByRole('img', { name: 'Failed' })).toHaveClass(
      'bg-[var(--status-error)]'
    );
  });

  // The dots take their colour from lib/statusPalette.ts.
  // parity:common-status-chips parity:dup-23
  it('gives an aborted run and step the aborted colour', () => {
    const { container } = renderList(
      run('run-1', [node('a', NodeStatus.Aborted)], Status.Aborted)
    );

    expect(
      within(rowOf('a')).getByRole('img', { name: 'Aborted' })
    ).toHaveClass('bg-[var(--status-aborted)]');
    const summary = container.querySelector<HTMLElement>('li[data-summary]')!;
    expect(within(summary).getByRole('img', { name: 'Aborted' })).toHaveClass(
      'bg-[var(--status-aborted)]'
    );
  });

  // A step behind a failed step is Aborted: the row says it in words and
  // keeps the reason in a tooltip.
  it('labels an aborted step with its status and error', () => {
    renderList(
      run(
        'run-1',
        [
          node('bad', NodeStatus.Failed),
          node('after', NodeStatus.Aborted, { error: 'upstream failed' }),
        ],
        Status.Failed
      )
    );

    const label = within(rowOf('after')).getByTestId('run-step-aborted');
    expect(label).toHaveTextContent('Aborted');
    expect(label).toHaveAttribute('title', 'upstream failed');
    expect(within(rowOf('bad')).queryByTestId('run-step-aborted')).toBeNull();
  });

  it('pulses the dot of a running step and gives skipped the neutral colour', () => {
    renderList(
      run(
        'run-1',
        [node('live', NodeStatus.Running), node('skip', NodeStatus.Skipped)],
        Status.Running
      )
    );

    expect(
      within(rowOf('live')).getByRole('img', { name: 'Running' })
    ).toHaveClass('bg-[var(--status-running)]', 'motion-safe:animate-pulse');
    expect(
      within(rowOf('skip')).getByRole('img', { name: 'Skipped' })
    ).toHaveClass('bg-[var(--status-neutral)]');
  });

  // The header chip pulses for the run: the summary row keeps its dot still.
  it('keeps the dot of the summary row of a running run still', () => {
    const { container } = renderList(
      run('run-1', [node('live', NodeStatus.Running)], Status.Running)
    );
    const summary = container.querySelector<HTMLElement>('li[data-summary]')!;
    const dot = within(summary).getByTestId('status-dot');
    expect(dot.className).toContain('motion-safe:animate-none');
    expect(dot.className).not.toContain('motion-safe:animate-pulse');
  });

  // parity:steps-name-description
  it('shows the sub-DAG icon with its tooltip and the sub-run count', async () => {
    renderList(
      run('run-1', [
        node('fan', NodeStatus.Success, {
          step: { call: 'child' },
          subRuns: [{ dagRunId: 's1', dagName: 'child' }],
          subRunsRepeated: [{ dagRunId: 's2', dagName: 'child' }],
        }),
        node('plain'),
      ])
    );

    const icon = within(rowOf('fan')).getByTestId('run-step-subdag');
    expect(icon).toHaveTextContent('2');
    expect(within(icon).getByTitle('2 sub-runs')).toBeInTheDocument();
    expect(
      within(rowOf('plain')).queryByTestId('run-step-subdag')
    ).not.toBeInTheDocument();

    await userEvent.hover(icon);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      'Sub DAG Run: child'
    );
  });

  // parity:steps-retry-count-badge parity:run-step-repeat-badge
  // parity:steps-build-decision-badge
  it('shows the retry count, the repeat marker and the build badge', async () => {
    renderList(
      run('run-1', [
        node('flaky', NodeStatus.Success, { retryCount: 2 }),
        node('loop', NodeStatus.Success, {
          doneCount: 2,
          step: {
            repeatPolicy: {
              repeat: RepeatMode.While,
              interval: 10,
              limit: 3,
            },
          },
        }),
        node('cached', NodeStatus.Success, {
          build: {
            decision: BuildExecutionDecision.reuse,
            phase: BuildExecutionPhase.complete,
            reason: 'fingerprint_match',
            detail: 'inputs unchanged',
            producerRun: { name: 'demo', id: 'run-0' },
          },
        }),
        node('plain'),
      ])
    );

    expect(
      within(rowOf('flaky')).getByLabelText('Retried 2×')
    ).toBeInTheDocument();
    const repeat = within(rowOf('loop')).getByTestId('repeat-badge');
    expect(repeat).toHaveTextContent('×2');
    expect(repeat.getAttribute('title')).toBe(
      'repeat while exit 0 · every 10s · max 3\niteration 2 of 3'
    );
    expect(
      within(rowOf('plain')).queryByTestId('repeat-badge')
    ).not.toBeInTheDocument();
    const plain = rowOf('plain');
    expect(within(plain).queryByTestId('retry-count')).not.toBeInTheDocument();
    expect(
      within(plain).queryByTestId('run-step-build')
    ).not.toBeInTheDocument();

    const build = within(rowOf('cached')).getByTestId('run-step-build');
    expect(build).toHaveTextContent('reused');
    await userEvent.hover(build);
    const tooltip = await screen.findByRole('tooltip');
    expect(tooltip).toHaveTextContent('fingerprint match');
    expect(tooltip).toHaveTextContent('No executor ran.');
    expect(tooltip).toHaveTextContent('inputs unchanged');
    const producer = within(tooltip).getByRole('link', {
      name: 'Produced by demo:run-0',
    });
    expect(producer.getAttribute('href')).toMatch(/^\/dags\/demo\?.*run=run-0/);
  });

  // parity:steps-last-run
  it('shows when each step started and ticks a running duration', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-15T02:00:05Z'));
    renderList(
      run(
        'run-1',
        [
          node('live', NodeStatus.Running, {
            startedAt: '2026-01-15T02:00:00Z',
            finishedAt: '-',
          }),
          node('done'),
        ],
        Status.Running
      )
    );

    const duration = (name: string) =>
      within(rowOf(name)).getByTestId('run-step-duration');
    expect(duration('live')).toHaveTextContent('5s');
    expect(duration('done')).toHaveTextContent('3s');
    expect(within(rowOf('done')).getByText('5s ago')).toHaveAttribute(
      'title',
      expect.stringContaining('2026-01-15')
    );

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(duration('live')).toHaveTextContent('6s');
    expect(duration('done')).toHaveTextContent('3s');
  });

  // A finished step reads as on the canvas. parity:dup-23
  it('shows the duration of a finished step in the glance format', () => {
    renderList(
      run('run-1', [
        node('quick', NodeStatus.Success, {
          finishedAt: '2026-01-15T02:00:00.850Z',
        }),
        node('long', NodeStatus.Success, {
          finishedAt: '2026-01-15T03:02:03Z',
        }),
        node('broken', NodeStatus.Failed, { finishedAt: '-' }),
      ])
    );

    const duration = (name: string) =>
      within(rowOf(name)).queryByTestId('run-step-duration');
    expect(duration('quick')).toHaveTextContent('850ms');
    expect(duration('long')).toHaveTextContent('1h 2m');
    expect(duration('long')).not.toHaveTextContent('3s');
    expect(duration('broken')).not.toBeInTheDocument();
  });

  // The API times have a resolution of one second. A step of 0 ms reads
  // "<1s" and sits below the step that it waits for.
  it('reads <1s for a step of 0 ms and puts it under its dependency', () => {
    const same = {
      startedAt: '2026-01-15T02:00:00Z',
      finishedAt: '2026-01-15T02:00:00Z',
    };
    const { container } = renderList(
      run('run-1', [
        node('second', NodeStatus.Success, {
          ...same,
          step: { depends: ['first'] },
        }),
        node('first', NodeStatus.Success, same),
      ])
    );

    expect(outline(container)).toEqual(['summary', 'first', 'second']);
    expect(
      within(rowOf('first')).getByTestId('run-step-duration')
    ).toHaveTextContent('<1s');
    expect(container).not.toHaveTextContent('0ms');
  });

  it('reports a click on a row and on the summary row', () => {
    const { onSelect, container } = renderList(run('run-1', [node('a')]), {
      selected: 'a',
    });

    fireEvent.click(within(rowOf('a')).getByRole('button', { name: 'a' }));
    expect(onSelect).toHaveBeenLastCalledWith('a');

    fireEvent.click(container.querySelector('li[data-summary]')!);
    expect(onSelect).toHaveBeenLastCalledWith(null);
  });

  it('highlights the selected row and scrolls it into view', () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    const details = run('run-1', [node('a'), node('b')]);
    const { rerender, container } = renderList(details);

    const summary = container.querySelector('li[data-summary]')!;
    expect(summary).toHaveAttribute('aria-current', 'true');
    expect(summary).toHaveAttribute('data-selected', 'true');
    expect(scroll).not.toHaveBeenCalled();

    rerender(
      <RunStepList
        dag={dagOf(['a', 'b'])}
        details={details}
        handlers={[]}
        selected="b"
        onSelect={() => {}}
      />
    );

    expect(rowOf('b')).toHaveAttribute('aria-current', 'true');
    expect(rowOf('a')).not.toHaveAttribute('aria-current');
    expect(summary).not.toHaveAttribute('aria-current');
    // The selected row carries data-selected next to aria-current.
    expect(rowOf('b')).toHaveAttribute('data-selected', 'true');
    expect(rowOf('a')).not.toHaveAttribute('data-selected');
    expect(summary).not.toHaveAttribute('data-selected');
    expect(scroll).toHaveBeenCalledTimes(1);
    expect(scroll.mock.contexts[0]).toBe(rowOf('b'));
  });

  // The summary row names the run; its facts live once in the RunChip
  // (row and tooltip) and the run details popover. parity:dup-7 parity:dup-17
  it('does not repeat the run facts in the list', () => {
    const { container } = renderList(
      run('run-xyz', [node('a')], Status.Success, {
        params: 'region=eu',
        workerId: 'worker-a',
      })
    );

    const summary = container.querySelector<HTMLElement>('li[data-summary]')!;
    expect(summary).toHaveTextContent('Run summary');
    expect(
      within(summary).getByRole('img', { name: 'Succeeded' })
    ).toBeInTheDocument();
    expect(container).not.toHaveTextContent('run-xyz');
    expect(container).not.toHaveTextContent('region=eu');
    expect(container).not.toHaveTextContent('worker-a');
  });

  // A skipped step did not run: no duration, whatever its timestamps say.
  it('shows no duration for a skipped step', () => {
    renderList(
      run('run-1', [
        node('ran', NodeStatus.Success),
        node('skip', NodeStatus.Skipped),
      ])
    );

    expect(
      within(rowOf('ran')).getByTestId('run-step-duration')
    ).toBeInTheDocument();
    expect(
      within(rowOf('skip')).queryByTestId('run-step-duration')
    ).not.toBeInTheDocument();
  });

  // A grey dot alone reads as a step that did not run yet.
  it('labels a skipped step Skipped, and no other step', () => {
    renderList(
      run('run-1', [
        node('ran', NodeStatus.Success),
        node('skip', NodeStatus.Skipped),
      ])
    );

    expect(
      within(rowOf('skip')).getByTestId('run-step-skipped')
    ).toHaveTextContent('Skipped');
    expect(
      within(rowOf('ran')).queryByTestId('run-step-skipped')
    ).not.toBeInTheDocument();
  });

  // A step that the run reused took its time in another run: the row says
  // "Reused" and shows that time, in place of "Skipped".
  it('shows Reused and the original duration for a step that the run reused', () => {
    const details = run(
      'run-1',
      [node('ran', NodeStatus.Success), node('skip', NodeStatus.Skipped)],
      Status.Success,
      {
        reusedInputs: [
          {
            step: 'skip',
            runId: '034mroF1udXVyvPz00bn5a',
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:20Z',
            via: ReusedInputVia.run,
          },
        ],
      }
    );
    renderList(details);
    const reused = within(rowOf('skip')).getByTestId('run-step-reused');
    expect(reused).toHaveTextContent('Reused');
    expect(within(reused).getByTestId('run-step-duration')).toHaveTextContent(
      '20s'
    );
    expect(reused).toHaveAttribute('title', 'Took this long in run 034mroF1');
    expect(
      within(rowOf('skip')).queryByTestId('run-step-skipped')
    ).not.toBeInTheDocument();
    // A step that was skipped for no reuse keeps its label.
    expect(
      within(rowOf('ran')).queryByTestId('run-step-reused')
    ).not.toBeInTheDocument();
  });

  // The dot of a reused step is hatched, green when the source step succeeded
  // and red when it did not; a step that ran keeps its solid dot.
  it('hatches the dot of a reused step, green or red, and keeps the solid dot of one that ran', () => {
    const details = run(
      'run-1',
      [
        node('fetch', NodeStatus.Skipped),
        node('extract', NodeStatus.Skipped, {
          startedAt: '-',
          finishedAt: '-',
          doneCount: 0,
          step: { depends: ['fetch'] },
        }),
        node('load', NodeStatus.Success, { step: { depends: ['extract'] } }),
      ],
      Status.Success,
      {
        labels: ['dagu.outputs-from=034mroF1udXVyvPz00bn5a'],
        reusedInputs: [
          {
            step: 'fetch',
            runId: '034mroF1udXVyvPz00bn5a',
            startedAt: '2026-01-01T00:00:00Z',
            finishedAt: '2026-01-01T00:00:20Z',
            via: ReusedInputVia.run,
          },
        ],
      }
    );
    renderList(details);
    const dot = (name: string) => within(rowOf(name)).getByTestId('status-dot');

    expect(dot('fetch')).toHaveAttribute('data-reused', 'success');
    expect(dot('fetch')).toHaveAccessibleName('Reused, succeeded');
    expect(dot('fetch')).toHaveAttribute(
      'title',
      'Reused from run 034mroF1: succeeded'
    );
    expect(dot('extract')).toHaveAttribute('data-reused', 'failed');
    expect(dot('extract')).toHaveAccessibleName('Reused, failed');
    expect(dot('extract').style.backgroundImage).toContain(
      'repeating-linear-gradient'
    );
    expect(dot('load')).not.toHaveAttribute('data-reused');
    expect(dot('load').style.backgroundImage).toBe('');
  });

  // '3h ago' on every row says nothing: the start time stays in a tooltip and
  // for screen readers only, and a row is one line high.
  it('keeps the start time out of sight and the row on one line', () => {
    renderList(run('run-1', [node('done')]));

    const row = rowOf('done');
    expect(within(row).getByText(/ago$/)).toHaveClass('sr-only');
    expect(
      within(row).getByTestId('run-step-duration').parentElement
    ).toHaveAttribute(
      'title',
      expect.stringMatching(/^2026-01-1\d \d\d:\d\d:\d\d$/)
    );
    expect(row).toHaveClass('min-h-6');
  });
});

// Two views of one node write one duration text, in a slot that keeps its
// width while the value ticks.
describe('RunStepList duration', () => {
  it('shows the text of the canvas card for the same node', () => {
    for (const finishedAt of [
      '2026-01-15T02:00:00.850Z',
      '2026-01-15T02:00:03Z',
      '2026-01-15T02:02:05Z',
      '2026-01-15T03:02:03Z',
    ]) {
      const done = node('done', NodeStatus.Success, { finishedAt });
      const card = render(<RunMeta run={done} />);
      const cardText = card.container.textContent;
      card.unmount();
      renderList(run('run-1', [done]));
      expect(cardText).toBeTruthy();
      expect(
        within(rowOf('done')).getByTestId('run-step-duration')
      ).toHaveTextContent(cardText!);
      document.body.innerHTML = '';
    }
  });

  it('gives every duration the same minimum width', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-15T02:00:05Z'));
    renderList(
      run(
        'run-1',
        [
          node('live', NodeStatus.Running, { finishedAt: '-' }),
          node('long', NodeStatus.Success, {
            finishedAt: '2026-01-15T03:02:03Z',
          }),
        ],
        Status.Running
      )
    );

    for (const name of ['live', 'long']) {
      const slot = within(rowOf(name)).getByTestId('run-step-duration');
      expect(slot).toHaveClass('min-w-[7ch]', 'tabular-nums');
    }
  });
});

describe('RunStepList keyboard', () => {
  it('moves the selection with the arrow keys, Home and End', () => {
    const details = run('run-1', [node('a'), node('b'), node('c')]);
    const { onSelect } = renderList(details, { selected: 'b' });
    const list = screen.getByRole('list', { name: 'Run steps' });

    fireEvent.keyDown(list, { key: 'ArrowDown' });
    expect(onSelect).toHaveBeenLastCalledWith('c');
    fireEvent.keyDown(list, { key: 'ArrowUp' });
    expect(onSelect).toHaveBeenLastCalledWith('a');
    fireEvent.keyDown(list, { key: 'Home' });
    expect(onSelect).toHaveBeenLastCalledWith(null);
    fireEvent.keyDown(list, { key: 'End' });
    expect(onSelect).toHaveBeenLastCalledWith('c');
  });

  it('stops at the ends of the list', () => {
    const details = run('run-1', [node('a')]);
    const { onSelect } = renderList(details, { selected: 'a' });
    fireEvent.keyDown(screen.getByRole('list', { name: 'Run steps' }), {
      key: 'ArrowDown',
    });
    expect(onSelect).not.toHaveBeenCalled();
  });
});
