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
  it('renders the first runtime condition for a queued DAG-run', () => {
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
          type: 'Queued',
          status: DAGRunConditionStatus.True,
          reason: 'QueueCapacity',
          message: 'DAG-run is waiting for a worker.',
          checkedAt: '2026-05-19T01:02:03Z',
        },
        {
          type: 'Queued',
          status: DAGRunConditionStatus.Unknown,
          reason: 'WorkerHeartbeatMissing',
          message: 'Worker state is still being checked.',
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

    expect(screen.getByText('QueueCapacity')).toBeInTheDocument();
    expect(
      screen.getByText('DAG-run is waiting for a worker.')
    ).toBeInTheDocument();
    expect(screen.getByText(/Checked May 19, 01:02:03/)).toBeInTheDocument();
    expect(screen.getByText('WorkerHeartbeatMissing')).toBeInTheDocument();
    expect(
      screen.getByText('Worker state is still being checked.')
    ).toBeInTheDocument();
    expect(screen.getByText(/Checked May 19, 01:03:03/)).toBeInTheDocument();
  });
});
