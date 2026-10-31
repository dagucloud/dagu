// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  DAGRunConditionStatus,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import DAGStatusOverview from '../../dag-details/DAGStatusOverview';
import {
  PreconditionErrors,
  RunError,
  RunProgress,
  RunStatusMessage,
  RuntimeConditions,
} from '../RunSummaryBlocks';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];

function node(name: string, label: NodeStatusLabel, status: NodeStatus): Node {
  return { step: { name }, status, statusLabel: label } as Node;
}

function run(patch: Partial<DAGRunDetails>): DAGRunDetails {
  return {
    dagRunId: 'run-1',
    name: 'etl',
    rootDAGRunName: 'etl',
    rootDAGRunId: 'run-1',
    log: '/tmp/test.log',
    artifactsAvailable: false,
    nodes: [],
    autoRetryCount: 0,
    autoRetryLimit: 0,
    startedAt: '2026-05-19T01:00:00Z',
    finishedAt: '-',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    ...patch,
  } as DAGRunDetails;
}

const fixtures: Record<string, DAGRunDetails> = {
  'running with mixed steps': run({
    status: Status.Running,
    statusLabel: StatusLabel.running,
    nodes: [
      node('a', NodeStatusLabel.succeeded, NodeStatus.Success),
      node('b', NodeStatusLabel.succeeded, NodeStatus.Success),
      node('c', NodeStatusLabel.failed, NodeStatus.Failed),
      node('d', NodeStatusLabel.retrying, NodeStatus.Retrying),
    ],
  }),
  'finished without steps': run({}),
  'queued with conditions and preconditions': run({
    status: Status.Queued,
    statusLabel: StatusLabel.queued,
    conditions: [
      {
        type: 'Runnable',
        status: DAGRunConditionStatus.False,
        reason: 'MaxConcurrencyReached',
        message: 'The queue is full.',
        checkedAt: '2026-05-19T01:02:03Z',
      },
      {
        type: 'ConcurrencyReady',
        status: DAGRunConditionStatus.True,
        reason: 'ConcurrencyAvailable',
        message: 'Capacity is free.',
        checkedAt: '2026-05-19T01:04:03Z',
      },
    ],
    preconditions: [
      { condition: '${FOO}', expected: 'ready', error: 'FOO is not ready' },
    ],
  }),
  'aborted with an error': run({
    status: Status.Aborted,
    statusLabel: StatusLabel.aborted,
    error: 'killed by signal',
    nodes: [node('a', NodeStatusLabel.aborted, NodeStatus.Aborted)],
  }),
  'waiting for an approval': run({
    status: Status.Waiting,
    statusLabel: StatusLabel.waiting,
    nodes: [node('gate', NodeStatusLabel.waiting, NodeStatus.Waiting)],
  }),
  rejected: run({
    status: Status.Rejected,
    statusLabel: StatusLabel.rejected,
    nodes: [node('gate', NodeStatusLabel.rejected, NodeStatus.Rejected)],
  }),
};

function Blocks({
  status,
  onViewLog,
}: {
  status: DAGRunDetails;
  onViewLog?: (dagRunId: string) => void;
}) {
  return (
    <div className="min-w-0 space-y-2">
      <RunProgress status={status} onViewLog={onViewLog} />
      <RunStatusMessage status={status.status} />
      <RunError error={status.error} />
      <RuntimeConditions conditions={status.conditions} />
      <PreconditionErrors preconditions={status.preconditions} />
    </div>
  );
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date('2026-05-19T01:05:03Z'));
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  localStorage.removeItem('user_preferences');
});

// parity:run-overview-progress
// parity:run-overview-status-messages
// parity:test-status-overview
describe('RunSummaryBlocks', () => {
  it.each(Object.entries(fixtures))(
    'renders the same output as DAGStatusOverview for a run %s',
    (_, status) => {
      const onViewLog = vi.fn();
      const overview = render(
        <DAGStatusOverview
          status={status}
          showRunMeta={false}
          onViewLog={onViewLog}
        />
      );
      const expected = overview.container.innerHTML;
      overview.unmount();

      const blocks = render(<Blocks status={status} onViewLog={onViewLog} />);
      expect(blocks.container.innerHTML).toBe(expected);
    }
  );

  it('draws one bar segment per step status with its share', () => {
    const { container } = render(
      <Blocks status={fixtures['running with mixed steps']!} />
    );

    const segments = Array.from(
      container.querySelectorAll<HTMLElement>('.h-full')
    );
    expect(segments.map((segment) => segment.style.width)).toEqual([
      '50%',
      '25%',
      '25%',
    ]);
    expect(segments[0]).toHaveClass('bg-[var(--status-success)]');
    expect(segments[0]!.title).toMatch(/: 2$/);
    expect(segments[1]).toHaveClass('bg-[var(--status-warning)]');
    expect(segments[2]).toHaveClass('bg-[var(--status-error)]');
    expect(screen.getByText('2/4')).toBeInTheDocument();
    expect(screen.getByText('Execution in progress')).toBeInTheDocument();
  });

  it('shows no bar and a zero count for a run without steps', () => {
    const { container } = render(
      <Blocks status={fixtures['finished without steps']!} />
    );

    expect(container.querySelector('.h-full')).toBeNull();
    expect(screen.getByText('0/0')).toBeInTheDocument();
    expect(screen.queryByTestId('dag-run-error')).not.toBeInTheDocument();
    expect(screen.queryByTestId('runtime-conditions')).not.toBeInTheDocument();
  });

  it.each<[Status, string]>([
    [Status.Running, 'Execution in progress'],
    [Status.Queued, 'DAGRun is queued for execution'],
    [Status.Aborted, 'Execution was aborted'],
    [Status.Waiting, 'Waiting for manual action'],
    [Status.Rejected, 'Execution was rejected'],
  ])('gives the message of status %s', (status, message) => {
    render(<RunStatusMessage status={status} />);
    expect(screen.getByText(message)).toBeInTheDocument();
  });

  it('translates the status message', () => {
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <RunStatusMessage status={Status.Rejected} />
        </I18nProvider>
      </UserPreferencesProvider>
    );
    expect(screen.getByText('実行は拒否されました')).toBeInTheDocument();
  });

  it.each([Status.Success, Status.Failed, Status.NotStarted])(
    'gives no message for status %s',
    (status) => {
      const { container } = render(<RunStatusMessage status={status} />);
      expect(container).toBeEmptyDOMElement();
    }
  );

  it('shows the error, the unmet conditions and the preconditions', () => {
    render(
      <Blocks
        status={{
          ...fixtures['queued with conditions and preconditions']!,
          error: 'boom',
        }}
      />
    );

    expect(screen.getByTestId('dag-run-error')).toHaveTextContent('boom');
    expect(screen.getByTestId('runtime-conditions')).toHaveTextContent(
      'The queue is full.'
    );
    expect(screen.queryByText('Capacity is free.')).not.toBeInTheDocument();
    expect(screen.getByText('Run precondition not met')).toBeInTheDocument();
    expect(screen.getByText(/FOO is not ready/)).toBeInTheDocument();
  });

  it('opens the scheduler log of the run from the Log button', async () => {
    vi.useRealTimers();
    const onViewLog = vi.fn();
    render(<Blocks status={fixtures['rejected']!} onViewLog={onViewLog} />);

    const button = screen.getByRole('button', { name: 'Log' });
    expect(button).toHaveAttribute('title', 'View scheduler log');
    await userEvent.setup().click(button);
    expect(onViewLog).toHaveBeenCalledWith('run-1');
  });

  it('hides the Log button without onViewLog', () => {
    render(<Blocks status={fixtures['rejected']!} />);
    expect(
      screen.queryByRole('button', { name: 'Log' })
    ).not.toBeInTheDocument();
  });
});
