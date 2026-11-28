// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import {
  PlaintextReason,
  WorkerConnection,
  WorkerHealthStatus,
  WorkerVersionDrift,
  type components,
} from '@/api/v1/schema';
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
  version: '2.27.0',
  versionDrift: WorkerVersionDrift.none,
} as Worker;

// The list renders each connection line twice, in the Connection column and
// under the worker ID, and its width shows one of them.
function expectConnection(text: string | RegExp) {
  expect(screen.getAllByText(text)).toHaveLength(2);
}

function renderList(workers: Worker[]) {
  return render(
    <MemoryRouter>
      <WorkerList workers={workers} isLoading={false} />
    </MemoryRouter>
  );
}

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

  it('shows the version each worker runs', () => {
    renderList([worker]);

    expect(screen.getByText('v2.27.0')).toBeInTheDocument();
    expect(screen.queryByText(/than the server/)).not.toBeInTheDocument();
  });

  // Workers behind the server are counted above the list, so they stand out
  // among many.
  it('marks and counts workers on an older version than the server', () => {
    renderList([
      {
        ...worker,
        version: '2.25.1',
        versionDrift: WorkerVersionDrift.behind,
      },
      { ...worker, id: 'worker-2' },
    ]);

    expect(screen.getByText('older than the server')).toBeInTheDocument();
    expect(
      screen.getByText('1 worker runs an older Dagu version than the server.')
    ).toBeInTheDocument();
  });

  it('marks a worker on a newer version than the server', () => {
    renderList([{ ...worker, versionDrift: WorkerVersionDrift.ahead }]);

    expect(screen.getByText('newer than the server')).toBeInTheDocument();
    expect(screen.queryByText(/older Dagu version/)).not.toBeInTheDocument();
  });

  it('shows how each worker connects', () => {
    renderList([
      { ...worker, connection: WorkerConnection.certificate },
      { ...worker, id: 'worker-2', connection: WorkerConnection.tls },
      { ...worker, id: 'worker-3', connection: undefined },
    ]);

    expectConnection('Certificate');
    expectConnection('TLS');
    expectConnection('Connection not reported');
  });

  // Plaintext can be on purpose, as behind a service mesh, so the line names
  // the reason and the change without a warning.
  it('names why a plaintext worker uses h2c and what to change', () => {
    renderList([
      {
        ...worker,
        connection: WorkerConnection.plaintext,
        plaintextReason: PlaintextReason.peer_insecure,
      },
      {
        ...worker,
        id: 'worker-2',
        connection: WorkerConnection.plaintext,
        plaintextReason: PlaintextReason.older_version,
      },
    ]);

    for (const link of screen.getAllByRole('link', { name: 'Plaintext' })) {
      expect(link).toHaveAttribute(
        'href',
        'https://docs.dagu.sh/server-admin/distributed/transport-security'
      );
    }
    expectConnection(/peer\.insecure: true in its config/);
    expectConnection('Remove peer.insecure: true to use TLS');
    expectConnection(/older version/);
    expectConnection('Upgrade it to use TLS');
  });

  it('tells a worker that reports no version', () => {
    renderList([
      {
        ...worker,
        version: undefined,
        versionDrift: WorkerVersionDrift.unknown,
      },
    ]);

    expect(screen.getByText('Version not reported')).toBeInTheDocument();
  });
});
