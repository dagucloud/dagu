// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Status, TriggerType } from '@/api/v1/schema';
import { RunMetaLine } from '../RunMetaLine';

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

describe('RunMetaLine', () => {
  it('describes how, where, and how long the run went', () => {
    render(<RunMetaLine dagRun={run} />);

    expect(screen.getByTitle('Trigger')).toHaveTextContent('Manual · alice');
    expect(screen.getByTitle('Duration')).toHaveTextContent('2m');
    expect(screen.getByTitle('Queue wait')).toHaveTextContent('30s');
    expect(screen.getByTitle('Worker')).toHaveTextContent('worker-a');
    expect(screen.getByTitle('Profile')).toHaveTextContent('prod');
    expect(screen.getByText('region=eu')).toBeInTheDocument();
  });

  // Items such as a running duration change width; the ID stays at the end.
  it('keeps the run ID at the right edge of the line', () => {
    render(<RunMetaLine dagRun={run} className="flex-1" />);

    expect(screen.getByTitle('Run ID')).toHaveClass('ml-auto');
    expect(screen.getByTitle('Run ID').closest('dl')?.parentElement).toHaveClass(
      'flex-1'
    );
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

  it('leaves copying to a parent that announces copies itself', () => {
    const onCopyRunId = vi.fn();
    render(<RunMetaLine dagRun={run} onCopyRunId={onCopyRunId} />);

    fireEvent.click(screen.getByRole('button', { name: 'Copy run ID' }));

    expect(onCopyRunId).toHaveBeenCalled();
    expect(
      screen.queryByText('Run ID copied to clipboard')
    ).not.toBeInTheDocument();
  });
});
