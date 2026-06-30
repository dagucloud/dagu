// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  DAGRunConditionStatus,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import QueueRunsTable from '../QueueRunsTable';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tzOffsetInSec: 0 }),
}));

afterEach(() => {
  cleanup();
});

describe('QueueRunsTable', () => {
  it('renders only the Runnable summary when a queued DAG-run also has detail conditions', () => {
    const queuedRun: components['schemas']['DAGRunSummary'] = {
      dagRunId: 'run-1',
      name: 'queued-dag',
      status: Status.Queued,
      statusLabel: StatusLabel.queued,
      startedAt: '-',
      finishedAt: '-',
      artifactsAvailable: false,
      autoRetryCount: 0,
      queuedAt: '2026-05-19T01:00:00Z',
      conditions: [
        {
          type: 'WorkerReady',
          status: DAGRunConditionStatus.Unknown,
          reason: 'WorkerStateUnknown',
          message: 'Worker availability is still being checked.',
          checkedAt: '2026-05-19T01:02:03Z',
        },
        {
          type: 'Runnable',
          status: DAGRunConditionStatus.False,
          reason: 'MaxConcurrencyReached',
          message:
            'The DAG-run cannot start because the queue active-run concurrency limit has been reached.',
          checkedAt: '2026-05-19T01:03:03Z',
        },
      ],
    };

    render(
      <QueueRunsTable
        items={[queuedRun]}
        onDAGRunClick={vi.fn()}
        showQueuedAt
      />
    );

    expect(
      screen.getByText('Cannot start')
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        'The DAG-run cannot start because the queue active-run concurrency limit has been reached.'
      )
    ).toBeInTheDocument();
    expect(screen.getByText(/Checked May 19, 01:03:03/)).toBeInTheDocument();
    expect(screen.queryByText('Worker not ready')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Worker availability is still being checked.')
    ).not.toBeInTheDocument();
    expect(screen.queryByText('WorkerStateUnknown')).not.toBeInTheDocument();
  });

  it('falls back to a deterministic non-True condition when no Runnable condition is present', () => {
    const queuedRun: components['schemas']['DAGRunSummary'] = {
      dagRunId: 'run-2',
      name: 'queued-dag',
      status: Status.Queued,
      statusLabel: StatusLabel.queued,
      startedAt: '-',
      finishedAt: '-',
      artifactsAvailable: false,
      autoRetryCount: 0,
      queuedAt: '2026-05-19T01:00:00Z',
      conditions: [
        {
          type: 'WorkerReady',
          status: DAGRunConditionStatus.Unknown,
          reason: 'WorkerStateUnknown',
          message: 'Worker availability is still being checked.',
          checkedAt: '2026-05-19T01:02:03Z',
        },
        {
          type: 'ConcurrencyReady',
          status: DAGRunConditionStatus.False,
          reason: 'MaxConcurrencyReached',
          message: 'The queue active-run concurrency limit has been reached.',
          checkedAt: '2026-05-19T01:03:03Z',
        },
      ],
    };

    render(
      <QueueRunsTable
        items={[queuedRun]}
        onDAGRunClick={vi.fn()}
        showQueuedAt
      />
    );

    expect(screen.getByText('Concurrency not ready')).toBeInTheDocument();
    expect(
      screen.getByText('The queue active-run concurrency limit has been reached.')
    ).toBeInTheDocument();
    expect(screen.getByText(/Checked May 19, 01:03:03/)).toBeInTheDocument();
    expect(screen.queryByText('Worker not ready')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Worker availability is still being checked.')
    ).not.toBeInTheDocument();
  });

  it('ignores satisfied Runnable summaries when choosing the queue row condition', () => {
    const queuedRun: components['schemas']['DAGRunSummary'] = {
      dagRunId: 'run-3',
      name: 'queued-dag',
      status: Status.Queued,
      statusLabel: StatusLabel.queued,
      startedAt: '-',
      finishedAt: '-',
      artifactsAvailable: false,
      autoRetryCount: 0,
      queuedAt: '2026-05-19T01:00:00Z',
      conditions: [
        {
          type: 'Runnable',
          status: DAGRunConditionStatus.True,
          reason: 'Ready',
          message: 'The DAG-run is ready to start.',
          checkedAt: '2026-05-19T01:02:03Z',
        },
        {
          type: 'WorkerReady',
          status: DAGRunConditionStatus.Unknown,
          reason: 'WorkerStateUnknown',
          message: 'Worker availability is still being checked.',
          checkedAt: '2026-05-19T01:03:03Z',
        },
      ],
    };

    render(
      <QueueRunsTable
        items={[queuedRun]}
        onDAGRunClick={vi.fn()}
        showQueuedAt
      />
    );

    expect(screen.getByText('Worker readiness unknown')).toBeInTheDocument();
    expect(
      screen.getByText('Worker availability is still being checked.')
    ).toBeInTheDocument();
    expect(
      screen.queryByText('The DAG-run is ready to start.')
    ).not.toBeInTheDocument();
  });

  it('labels Runnable Unknown summaries by reason instead of always showing startup text', () => {
    const queuedRun: components['schemas']['DAGRunSummary'] = {
      dagRunId: 'run-4',
      name: 'queued-dag',
      status: Status.Queued,
      statusLabel: StatusLabel.queued,
      startedAt: '-',
      finishedAt: '-',
      artifactsAvailable: false,
      autoRetryCount: 0,
      queuedAt: '2026-05-19T01:00:00Z',
      conditions: [
        {
          type: 'Runnable',
          status: DAGRunConditionStatus.Unknown,
          reason: 'AssignmentUnavailable',
          message: 'Dagu cannot determine whether worker assignment can proceed.',
          checkedAt: '2026-05-19T01:03:03Z',
        },
      ],
    };

    render(
      <QueueRunsTable
        items={[queuedRun]}
        onDAGRunClick={vi.fn()}
        showQueuedAt
      />
    );

    expect(screen.getByText('Worker assignment unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Startup not observed')).not.toBeInTheDocument();
  });
});
