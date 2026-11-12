// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Status, TriggerType } from '@/api/v1/schema';
import {
  RunFacts,
  hasHiddenFacts,
  visibleFacts,
  type InlineFact,
} from '../RunFacts';
import { FACT_MIN_BAR_PX, factsShownAt } from '../useFactTiers';

const run = {
  dagRunId: 'abcdef0123456789abcdefg',
  status: Status.Success,
  startedAt: '2026-03-13T10:00:30Z',
  finishedAt: '2026-03-13T10:02:30Z',
  queuedAt: '2026-03-13T10:00:00Z',
  triggerType: TriggerType.manual,
  workerId: 'worker-a',
};

function show(
  layout: 'inline' | 'stacked',
  props: Partial<typeof run> = {},
  shown?: ReadonlySet<InlineFact>
) {
  return render(
    <MemoryRouter>
      <RunFacts dagRun={{ ...run, ...props }} layout={layout} shown={shown} />
    </MemoryRouter>
  );
}

afterEach(() => vi.useRealTimers());

// One renderer gives the two layouts: the row and the tooltip.
describe('RunFacts row', () => {
  it('gives the trigger, the start, the duration and the run ID', () => {
    show('inline');
    expect(screen.getByTitle('Trigger')).toHaveTextContent('Manual');
    expect(screen.getByTitle('Duration')).toHaveTextContent('2m');
    expect(screen.getByTitle('Started')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Copy run ID' })
    ).toBeInTheDocument();
  });

  it('shows the whole run ID, with no title and no cut', () => {
    show('inline');
    const id = screen.getByText('abcdef0123456789abcdefg');
    expect(id).not.toHaveAttribute('title');
    expect(id.className).not.toMatch(/truncate|ellipsis/);
  });

  it('keeps the secondary facts out of the row', () => {
    show('inline');
    expect(screen.queryByTitle('Worker')).toBeNull();
    expect(screen.queryByTitle('Queue wait')).toBeNull();
  });

  // The facts are as wide as their text, with one small gap: no fixed slot.
  it('sizes the facts to their text with a gap of 6px', () => {
    show('inline');
    expect(screen.getByTestId('run-facts-inline')).toHaveClass('gap-x-1.5');
    for (const title of ['Trigger', 'Started', 'Duration']) {
      const fact = screen.getByTitle(title);
      expect(fact).toHaveClass('shrink-0');
      expect(fact.className).not.toMatch(/(^| )w-/);
    }
  });

  // A ticking text keeps the width of its widest value of the tier.
  it('keeps the width of the duration and of the start while they tick', () => {
    show('inline');
    const duration = within(screen.getByTitle('Duration')).getByText('2m 0s');
    expect(duration).toHaveStyle({ minWidth: '7ch' });
    expect(duration).toHaveClass('tabular-nums');
  });

  it('drops the facts that the bar leaves out', () => {
    show('inline', {}, new Set<InlineFact>(['id']));
    expect(screen.queryByTitle('Trigger')).toBeNull();
    expect(screen.queryByTitle('Started')).toBeNull();
    expect(screen.queryByTitle('Duration')).toBeNull();
    expect(screen.getByText('abcdef0123456789abcdefg')).toBeInTheDocument();
  });

  it('has no duration for a run that did not start', () => {
    show('inline', { startedAt: '-', finishedAt: '-' });
    expect(screen.getByTitle('Queued')).toBeInTheDocument();
    expect(screen.queryByTitle('Duration')).toBeNull();
  });

  it('leaves the trigger out when the failed step takes its place', () => {
    render(
      <MemoryRouter>
        <RunFacts dagRun={run} layout="inline" failed />
      </MemoryRouter>
    );
    expect(screen.queryByTitle('Trigger')).toBeNull();
    expect(screen.getByTitle('Duration')).toBeInTheDocument();
  });

  it('counts the duration of a running run each second', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-13T10:00:10Z'));
    show('inline', {
      status: Status.Running,
      startedAt: '2026-03-13T10:00:00Z',
      finishedAt: '-',
    });
    const duration = screen.getByTitle('Duration');
    expect(within(duration).getByText('10s')).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    expect(within(duration).getByText('13s')).toBeInTheDocument();
  });

  it('copies the whole run ID from the row', async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, 'writeText');
    show('inline');
    await user.click(screen.getByRole('button', { name: 'Copy run ID' }));
    expect(writeText).toHaveBeenCalledWith('abcdef0123456789abcdefg');
  });
});

describe('RunFacts tooltip', () => {
  const all = new Set<InlineFact>(['trigger', 'started', 'duration', 'id']);

  it('does not list the local worker, which says nothing', () => {
    show('stacked', { workerId: 'local' }, all);
    expect(screen.queryByTitle('Worker')).toBeNull();
  });

  it('lists only the worker when the row shows every fact', () => {
    show('stacked', {}, all);
    expect(screen.getByTitle('Worker')).toHaveTextContent('worker-a');
    expect(screen.queryByTitle('Trigger')).toBeNull();
    expect(screen.queryByTitle('Started')).toBeNull();
    expect(screen.queryByTitle('Duration')).toBeNull();
    expect(screen.queryByText('abcdef0123456789abcdefg')).toBeNull();
  });

  it('lists the facts that the row leaves out, with the whole run ID', () => {
    show('stacked', {}, new Set<InlineFact>(['started', 'duration']));
    expect(screen.getByTitle('Trigger')).toHaveTextContent('Manual');
    expect(screen.getByText('abcdef0123456789abcdefg')).toBeInTheDocument();
    expect(screen.queryByTitle('Duration')).toBeNull();
  });

  it('shows the start as an absolute time when the row leaves it out', () => {
    show('stacked', {}, new Set<InlineFact>());
    expect(screen.getByTitle('Started')).toHaveTextContent('2026-03-13');
  });

  it('shows the parameters open', () => {
    show('stacked', { params: 'a=1' } as Partial<typeof run>, all);
    expect(screen.getByText('a=1')).toBeVisible();
  });
});

describe('hasHiddenFacts', () => {
  const all = new Set<InlineFact>(['trigger', 'started', 'duration', 'id']);

  it('is false when the row shows everything and nothing is extra', () => {
    expect(
      hasHiddenFacts({ ...run, workerId: undefined, queuedAt: undefined }, all)
    ).toBe(false);
  });

  it.each([
    ['a worker', { workerId: 'w' }],
    ['a profile', { profileName: 'p' }],
    ['parameters', { params: 'a=1' }],
    ['a queue wait', { queuedAt: '2026-03-13T10:00:00Z' }],
  ])('is true with %s', (_, extra) => {
    expect(hasHiddenFacts({ ...run, workerId: undefined, ...extra }, all)).toBe(
      true
    );
  });

  it('is true when a fact that exists is left out', () => {
    const noTrigger = new Set<InlineFact>(['started', 'duration', 'id']);
    expect(hasHiddenFacts({ ...run, workerId: undefined }, noTrigger)).toBe(
      true
    );
  });

  it('is false for a trigger that the run does not have', () => {
    const noTrigger = new Set<InlineFact>(['started', 'duration', 'id']);
    expect(
      hasHiddenFacts(
        {
          ...run,
          workerId: undefined,
          queuedAt: undefined,
          triggerType: undefined,
        },
        noTrigger
      )
    ).toBe(false);
  });

  it('counts the trigger of a failed run as shown by the failed step', () => {
    expect(visibleFacts(all, true).has('trigger')).toBe(false);
  });
});

describe('factsShownAt', () => {
  it('drops the trigger, then the start, then the duration, then the ID', () => {
    const at = (width: number) => [...factsShownAt(width, false)].sort();
    expect(at(1280)).toEqual(['duration', 'id', 'started', 'trigger']);
    expect(at(1279)).toEqual(['duration', 'id', 'started']);
    expect(at(1216)).toEqual(['duration', 'id', 'started']);
    expect(at(1215)).toEqual(['duration', 'id']);
    expect(at(1152)).toEqual(['duration', 'id']);
    expect(factsShownAt(FACT_MIN_BAR_PX.id - 1, false).size).toBe(0);
  });

  it('never shows the trigger of a failed run, and drops the rest earlier', () => {
    expect(factsShownAt(1920, true).has('trigger')).toBe(false);
    expect(factsShownAt(1920, true).has('id')).toBe(true);
    expect([...factsShownAt(1152, true)]).toEqual(['id']);
  });
});
