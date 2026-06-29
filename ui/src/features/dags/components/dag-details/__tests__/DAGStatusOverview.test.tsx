// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  DAGRunConditionStatus,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import dayjs from '@/lib/dayjs';
import DAGStatusOverview from '../DAGStatusOverview';

afterEach(() => {
  cleanup();
});

describe('DAGStatusOverview', () => {
  it('renders schedule time when present', () => {
    const scheduleTime = '2026-03-13T10:00:00Z';

    render(
      <DAGStatusOverview
        status={{
          dagRunId: 'run-1',
          name: 'scheduled-dag',
          rootDAGRunName: 'scheduled-dag',
          rootDAGRunId: 'run-1',
          log: '/tmp/test.log',
          artifactsAvailable: false,
          nodes: [],
          autoRetryCount: 0,
          autoRetryLimit: 0,
          startedAt: '2026-03-13T10:01:00Z',
          finishedAt: '2026-03-13T10:02:00Z',
          status: Status.Success,
          statusLabel: StatusLabel.succeeded,
          queuedAt: '2026-03-13T10:00:30Z',
          scheduleTime,
        }}
      />
    );

    expect(screen.getByText('Scheduled')).toBeInTheDocument();
    expect(
      screen.getByText(dayjs(scheduleTime).format('YYYY-MM-DD HH:mm:ss'))
    ).toBeInTheDocument();
    expect(screen.getByText('Queued')).toBeInTheDocument();
  });

  it('omits the scheduled label when schedule time is missing', () => {
    render(
      <DAGStatusOverview
        status={{
          dagRunId: 'run-2',
          name: 'manual-dag',
          rootDAGRunName: 'manual-dag',
          rootDAGRunId: 'run-2',
          log: '/tmp/test.log',
          artifactsAvailable: false,
          nodes: [],
          autoRetryCount: 0,
          autoRetryLimit: 0,
          startedAt: '2026-03-13T10:01:00Z',
          finishedAt: '2026-03-13T10:02:00Z',
          status: Status.Success,
          statusLabel: StatusLabel.succeeded,
        }}
      />
    );

    expect(screen.queryByText('Scheduled')).not.toBeInTheDocument();
  });

  it('renders a retrying node segment in the overview bar', () => {
    const { container } = render(
      <DAGStatusOverview
        status={{
          dagRunId: 'run-3',
          name: 'retrying-dag',
          rootDAGRunName: 'retrying-dag',
          rootDAGRunId: 'run-3',
          log: '/tmp/test.log',
          artifactsAvailable: false,
          autoRetryCount: 0,
          autoRetryLimit: 0,
          startedAt: '2026-03-13T10:01:00Z',
          finishedAt: '-',
          status: Status.Running,
          statusLabel: StatusLabel.running,
          nodes: [
            {
              status: NodeStatus.Retrying,
              statusLabel: NodeStatusLabel.retrying,
              step: { name: 'flaky' },
            } as never,
          ],
        }}
      />
    );

    expect(container.querySelector('.bg-\\[\\#e37400\\]')).not.toBeNull();
  });

  it('renders runtime conditions separately from precondition errors', () => {
    const checkedAt = '2026-05-19T01:02:03Z';

    render(
      <DAGStatusOverview
        status={{
          dagRunId: 'run-4',
          name: 'queued-dag',
          rootDAGRunName: 'queued-dag',
          rootDAGRunId: 'run-4',
          log: '/tmp/test.log',
          artifactsAvailable: false,
          nodes: [],
          autoRetryCount: 0,
          autoRetryLimit: 0,
          startedAt: '-',
          finishedAt: '-',
          status: Status.Queued,
          statusLabel: StatusLabel.queued,
          conditions: [
            {
              type: 'Queued',
              status: DAGRunConditionStatus.True,
              reason: 'QueueCapacity',
              message: 'DAG-run is waiting for a worker.',
              checkedAt,
            },
          ],
          preconditions: [
            {
              condition: '${FOO}',
              expected: 'ready',
              error: 'FOO is not ready',
            },
          ],
        }}
      />
    );

    expect(screen.getByText('Runtime Conditions')).toBeInTheDocument();
    expect(screen.getByText('QueueCapacity')).toBeInTheDocument();
    expect(
      screen.getByText('DAG-run is waiting for a worker.')
    ).toBeInTheDocument();
    expect(
      screen.getByText(dayjs(checkedAt).format('YYYY-MM-DD HH:mm:ss'))
    ).toBeInTheDocument();
    expect(screen.getByText('DAGRun Precondition Unmet')).toBeInTheDocument();
  });
});
