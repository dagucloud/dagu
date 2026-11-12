// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { WorkerHealthStatus, type components } from '@/api/v1/schema';
import WorkerList from '../WorkerList';

type Worker = components['schemas']['Worker'];

const worker = {
  id: 'worker-1',
  labels: {},
  healthStatus: WorkerHealthStatus.healthy,
  busyPollers: 1,
  totalPollers: 2,
  lastHeartbeatAt: '2026-05-19T01:00:00Z',
  runningTasks: [
    { dagName: 'etl', dagRunId: 'run-1', startedAt: '2026-05-19T01:00:00Z' },
  ],
} as Worker;

describe('WorkerList', () => {
  // A link opens the run from the keyboard, and in a new tab on demand.
  it('links a running task to its run', () => {
    render(
      <MemoryRouter>
        <WorkerList workers={[worker]} isLoading={false} />
      </MemoryRouter>
    );
    fireEvent.click(screen.getByText('worker-1'));

    expect(screen.getByRole('link', { name: /etl/ })).toHaveAttribute(
      'href',
      '/dags/etl?remoteNode=local&run=run-1'
    );
  });
});
