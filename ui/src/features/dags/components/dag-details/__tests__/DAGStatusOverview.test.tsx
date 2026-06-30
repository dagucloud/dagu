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

  it('renders runtime conditions with Runnable as the summary before non-True details', () => {
    const runnableCheckedAt = '2026-05-19T01:02:03Z';
    const workerCheckedAt = '2026-05-19T01:03:03Z';

    const { container } = render(
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
              type: 'WorkerReady',
              status: DAGRunConditionStatus.Unknown,
              reason: 'WorkerStateUnknown',
              message: 'Worker availability is still being checked.',
              checkedAt: workerCheckedAt,
            },
            {
              type: 'Runnable',
              status: DAGRunConditionStatus.False,
              reason: 'MaxConcurrencyReached',
              message:
                'The DAG-run cannot start because the queue active-run concurrency limit has been reached.',
              checkedAt: runnableCheckedAt,
            },
            {
              type: 'ConcurrencyReady',
              status: DAGRunConditionStatus.True,
              reason: 'ConcurrencyAvailable',
              message: 'The queue active-run concurrency limit has capacity.',
              checkedAt: '2026-05-19T01:04:03Z',
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
    expect(screen.getByText('Cannot start')).toBeInTheDocument();
    expect(
      screen.getByText(
        'The DAG-run cannot start because the queue active-run concurrency limit has been reached.'
      )
    ).toBeInTheDocument();
    expect(screen.getByText('Worker not ready')).toBeInTheDocument();
    expect(
      screen.getByText('Worker availability is still being checked.')
    ).toBeInTheDocument();
    expect(
      screen.getByText(dayjs(runnableCheckedAt).format('YYYY-MM-DD HH:mm:ss'))
    ).toBeInTheDocument();
    expect(
      screen.getByText(dayjs(workerCheckedAt).format('YYYY-MM-DD HH:mm:ss'))
    ).toBeInTheDocument();
    expect(screen.queryByText('Concurrency ready')).not.toBeInTheDocument();
    expect(
      screen.queryByText('The queue active-run concurrency limit has capacity.')
    ).not.toBeInTheDocument();

    const runtimeText =
      container.querySelector('[data-testid="runtime-conditions"]')
        ?.textContent ?? '';
    expect(runtimeText.indexOf('Cannot start')).toBeLessThan(
      runtimeText.indexOf('Worker not ready')
    );
    expect(
      runtimeText.indexOf(
        'The DAG-run cannot start because the queue active-run concurrency limit has been reached.'
      )
    ).toBeLessThan(
      runtimeText.indexOf('Worker availability is still being checked.')
    );
    expect(screen.getByText('DAGRun Precondition Unmet')).toBeInTheDocument();
  });

  it('labels Runnable Unknown conditions by reason instead of always showing startup text', () => {
    render(
      <DAGStatusOverview
        status={{
          dagRunId: 'run-5',
          name: 'queued-dag',
          rootDAGRunName: 'queued-dag',
          rootDAGRunId: 'run-5',
          log: '/tmp/test.log',
          status: Status.Queued,
          statusLabel: StatusLabel.queued,
          startedAt: '-',
          finishedAt: '-',
          nodes: [],
          autoRetryCount: 0,
          autoRetryLimit: 0,
          artifactsAvailable: false,
          conditions: [
            {
              type: 'Runnable',
              status: DAGRunConditionStatus.Unknown,
              reason: 'AssignmentUnavailable',
              message:
                'Dagu cannot determine whether worker assignment can proceed.',
              checkedAt: '2026-05-19T01:02:03Z',
            },
          ],
          preconditions: [],
        }}
      />
    );

    expect(screen.getByText('Worker assignment unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Startup not observed')).not.toBeInTheDocument();
  });
});
