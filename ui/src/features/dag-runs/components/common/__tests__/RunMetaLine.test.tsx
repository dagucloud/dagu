// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Status, TriggerType } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { RunMetaLine } from '../RunMetaLine';

// The renders of the copy button: the facts of the row render with it.
const copyRenders = vi.hoisted(() => ({ count: 0 }));
vi.mock('@/components/ui/copy-button', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/components/ui/copy-button')>();
  return {
    ...actual,
    CopyButton: (props: Parameters<typeof actual.CopyButton>[0]) => {
      copyRenders.count++;
      return actual.CopyButton(props);
    },
  };
});

const run = {
  dagRunId: 'run-42',
  status: Status.Success,
  startedAt: '2026-03-13T10:00:30Z',
  finishedAt: '2026-03-13T10:02:30Z',
  queuedAt: '2026-03-13T10:00:00Z',
  triggerType: TriggerType.manual,
  triggerActor: 'alice',
  workerId: 'worker-a',
  profileName: 'prod',
  params: 'region=eu',
};

describe('RunMetaLine tick', () => {
  afterEach(() => {
    vi.useRealTimers();
    copyRenders.count = 0;
  });

  // The duration of a running run ticks each second. Only the duration item
  // renders for it; the other facts of the inline row stay.
  it('renders the duration alone each second in the inline row', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-13T10:01:00Z'));
    render(
      <RunMetaLine
        inline
        dagRun={{ ...run, status: Status.Running, finishedAt: '-' }}
      />
    );
    expect(screen.getByTitle('Duration')).toHaveTextContent('30s');
    const before = copyRenders.count;
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.getByTitle('Duration')).toHaveTextContent('33s');
    expect(copyRenders.count).toBe(before);
  });
});

describe('RunMetaLine', () => {
  // The header and the steps write one duration in the same two units.
  it('writes a long duration with its two largest units', () => {
    render(
      <RunMetaLine
        dagRun={{
          ...run,
          startedAt: '2026-03-13T10:00:00Z',
          finishedAt: '2026-03-13T11:02:03Z',
        }}
      />
    );

    expect(screen.getByTitle('Duration')).toHaveTextContent('1h 2m');
    expect(screen.getByTitle('Duration')).not.toHaveTextContent('3s');
  });

  it('describes how, where, and how long the run went', () => {
    render(<RunMetaLine dagRun={run} />);

    expect(screen.getByTitle('Trigger')).toHaveTextContent('Manual · alice');
    expect(screen.getByTitle('Duration')).toHaveTextContent('2m');
    expect(screen.getByTitle('Queue wait')).toHaveTextContent('30s');
    expect(screen.getByTitle('Worker')).toHaveTextContent('worker-a');
    expect(screen.getByTitle('Profile')).toHaveTextContent('prod');
    expect(screen.getByText('region=eu')).toBeInTheDocument();
  });

  it('omits start and duration until a queued run starts', () => {
    render(
      <RunMetaLine
        dagRun={{
          ...run,
          status: Status.Queued,
          startedAt: '-',
          finishedAt: '-',
        }}
      />
    );

    expect(screen.queryByTitle('Started')).not.toBeInTheDocument();
    expect(screen.queryByTitle('Duration')).not.toBeInTheDocument();
    expect(screen.queryByTitle('Queue wait')).not.toBeInTheDocument();
  });

  it('copies and announces the run ID on its own', async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    render(<RunMetaLine dagRun={run} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy run ID' }));
    });

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('run-42');
    expect(screen.getByText('Run ID copied to clipboard')).toBeInTheDocument();
  });

  it('leaves the announcement to a parent that announces copies itself', async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    const onRunIdCopied = vi.fn();
    render(<RunMetaLine dagRun={run} onRunIdCopied={onRunIdCopied} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy run ID' }));
    });

    expect(onRunIdCopied).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByText('Run ID copied to clipboard')
    ).not.toBeInTheDocument();
  });

  // The button is the CopyButton of every host: a check mark and the copied
  // name, for 2 s.
  it('shows the check mark and the copied name after the copy', async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    render(<RunMetaLine dagRun={run} />);

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy run ID' }));
    });

    const done = screen.getByRole('button', { name: 'Copied' });
    expect(done.querySelector('.lucide-check')).not.toBeNull();
  });

  // The workflow header keeps the facts on one line, and the container width
  // decides which of them stay. The run page keeps its wrapping line.
  describe('compact', () => {
    it('keeps the facts on a line that does not wrap', () => {
      render(<RunMetaLine dagRun={run} compact />);

      const list = screen.getByTitle('Duration').closest('dl')!;
      expect(list).toHaveClass('flex-nowrap');
      expect(list).not.toHaveClass('flex-wrap');
    });

    it('drops the secondary facts when the container has a middle width', () => {
      render(<RunMetaLine dagRun={run} compact />);

      for (const title of ['Trigger', 'Queue wait', 'Worker', 'Profile']) {
        expect(screen.getByTitle(title)).toHaveClass(
          'hidden',
          '@max-3xl:flex',
          '@7xl:flex'
        );
      }
      // Start, duration and the run ID never go: the ID shortens instead.
      for (const title of ['Started', 'Duration', 'Run ID']) {
        expect(screen.getByTitle(title)).not.toHaveClass('hidden');
      }
      expect(screen.getByText('run-42')).toHaveClass('truncate');
    });

    // The run page header has no other place for Trigger and Worker.
    it('keeps the secondary facts and wraps with keepSecondary', () => {
      render(<RunMetaLine dagRun={run} compact keepSecondary />);

      for (const title of ['Trigger', 'Worker']) {
        expect(screen.getByTitle(title)).not.toHaveClass('hidden');
      }
      const list = screen.getByTitle('Duration').closest('dl')!;
      expect(list).toHaveClass('flex-wrap');
      expect(list).not.toHaveClass('flex-nowrap');
    });

    // An inline box kept the width of the whole ID, so the line cut the ID
    // in the middle of a glyph. A block box ends it with an ellipsis.
    it('shortens the run ID with an ellipsis inside its fact', () => {
      render(<RunMetaLine dagRun={run} compact />);

      const box = screen.getByText('run-42').parentElement!;
      expect(box).toHaveClass('flex', 'min-w-0');
      expect(box).not.toHaveClass('inline-flex');
    });

    it('shows the start as a short lapse of time such as "19m ago"', () => {
      render(
        <RunMetaLine
          dagRun={{
            ...run,
            startedAt: new Date(Date.now() - 19 * 60_000).toISOString(),
          }}
          compact
        />
      );

      expect(screen.getByTitle('Started')).toHaveTextContent('19m ago');
    });

    it('does not change the default line', () => {
      render(<RunMetaLine dagRun={run} />);

      expect(screen.getByText('run-42').parentElement).toHaveClass(
        'inline-flex'
      );
      expect(screen.getByTitle('Trigger')).not.toHaveClass('hidden');
      expect(screen.getByTitle('Duration').closest('dl')).toHaveClass(
        'flex-wrap'
      );
    });
  });

  // The run page is where a run is selected, so the ID is its door.
  describe('run link', () => {
    it('turns the run ID into a link when a page is given', () => {
      render(
        <MemoryRouter>
          <RunMetaLine dagRun={run} runHref="/dag-runs/etl/run-42" />
        </MemoryRouter>
      );

      const link = screen.getByRole('link', { name: 'Open run run-42' });
      expect(link).toHaveAttribute('href', '/dag-runs/etl/run-42');
      expect(link).toHaveTextContent('run-42');
      expect(link).toHaveAttribute('title', 'Open run');
    });

    it('keeps the run ID as text without a page', () => {
      render(<RunMetaLine dagRun={run} />);

      expect(screen.queryByRole('link')).not.toBeInTheDocument();
      expect(screen.getByText('run-42')).toBeInTheDocument();
    });
  });

  // The times that the overview showed (Scheduled, Queued, Started, Finished)
  // move to a tooltip on Started. parity:overview-run-meta
  // parity:run-overview-meta
  describe('times', () => {
    const timed = { ...run, scheduleTime: '2026-03-13T09:59:00Z' };

    // The tooltip must read as RelativeTime's own absolute time.
    function absoluteOf(timestamp: string): string {
      const view = render(<RelativeTime timestamp={timestamp} />);
      const title = view.container.firstElementChild!.getAttribute('title')!;
      view.unmount();
      return title;
    }

    async function openTimesByFocus(): Promise<HTMLElement> {
      await userEvent.tab();
      return screen.findByRole('tooltip');
    }

    function rowsOf(tooltip: HTMLElement): [string, string][] {
      const terms = within(tooltip).getAllByRole('term');
      const values = within(tooltip).getAllByRole('definition');
      return terms.map((term, i) => [
        term.textContent ?? '',
        values[i]!.textContent ?? '',
      ]);
    }

    it('lists the four times on hover, in order and in absolute form', async () => {
      const expected: [string, string][] = [
        ['Scheduled', absoluteOf(timed.scheduleTime)],
        ['Queued', absoluteOf(timed.queuedAt)],
        ['Started', absoluteOf(timed.startedAt)],
        ['Finished', absoluteOf(timed.finishedAt)],
      ];
      render(<RunMetaLine dagRun={timed} showTimes />);

      await userEvent.hover(
        within(screen.getByTitle('Started')).getByText(/ago$/)
      );

      expect(rowsOf(await screen.findByRole('tooltip'))).toEqual(expected);
    });

    it('opens on keyboard focus of the start', async () => {
      render(<RunMetaLine dagRun={timed} showTimes />);

      const tooltip = await openTimesByFocus();

      expect(screen.getByTitle('Started')).toContainElement(
        document.activeElement as HTMLElement
      );
      expect(tooltip).toHaveTextContent('Finished');
    });

    it('lists only the start when no other time is set', async () => {
      render(
        <RunMetaLine
          dagRun={{
            ...run,
            status: Status.Running,
            queuedAt: undefined,
            finishedAt: '-',
          }}
          showTimes
        />
      );

      expect(rowsOf(await openTimesByFocus())).toEqual([
        ['Started', absoluteOf(run.startedAt)],
      ]);
    });

    it('puts the times on the queue time until the run starts', async () => {
      render(
        <RunMetaLine
          dagRun={{
            ...run,
            status: Status.Queued,
            startedAt: '-',
            finishedAt: '-',
          }}
          showTimes
        />
      );

      expect(screen.queryByTitle('Started')).not.toBeInTheDocument();
      const tooltip = await openTimesByFocus();
      expect(screen.getByTitle('Queued')).toContainElement(
        document.activeElement as HTMLElement
      );
      expect(rowsOf(tooltip)).toEqual([['Queued', absoluteOf(run.queuedAt)]]);
    });

    it('keeps the short start and the tooltip in the compact line', async () => {
      const startedAt = new Date(Date.now() - 19 * 60_000).toISOString();
      render(
        <RunMetaLine dagRun={{ ...timed, startedAt }} compact showTimes />
      );

      const started = screen.getByTitle('Started');
      expect(started).toHaveTextContent('19m ago');
      expect(started).toHaveClass('shrink-0');
      expect(rowsOf(await openTimesByFocus()).map(([label]) => label)).toEqual([
        'Scheduled',
        'Queued',
        'Started',
        'Finished',
      ]);
    });

    // A narrow line, such as the workflow bar, moves the duration into the
    // tooltip and keeps its room for the run ID.
    it('moves the duration into the tooltip with durationInTimes', async () => {
      render(<RunMetaLine dagRun={timed} compact showTimes durationInTimes />);

      expect(screen.queryByTitle('Duration')).not.toBeInTheDocument();
      expect(rowsOf(await openTimesByFocus())).toEqual([
        ['Scheduled', absoluteOf(timed.scheduleTime)],
        ['Queued', absoluteOf(timed.queuedAt)],
        ['Started', absoluteOf(timed.startedAt)],
        ['Finished', absoluteOf(timed.finishedAt)],
        ['Duration', '2m 0s'],
      ]);
    });

    it('keeps the duration on the line without showTimes', () => {
      render(<RunMetaLine dagRun={timed} durationInTimes />);

      expect(screen.getByTitle('Duration')).toHaveTextContent('2m');
    });

    it('adds nothing to the line without showTimes', () => {
      const { container } = render(<RunMetaLine dagRun={timed} />);

      expect(container.querySelector('[tabindex]')).toBeNull();
      expect(
        within(screen.getByTitle('Started')).getByTitle(
          absoluteOf(timed.startedAt)
        )
      ).toBeInTheDocument();
      expect(screen.queryByTitle('Scheduled')).not.toBeInTheDocument();
    });
  });
});
