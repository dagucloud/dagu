// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import {
  PlaintextReason,
  WorkerConnection,
  WorkerHealthStatus,
  WorkerVersionDrift,
  type components,
} from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import WorkersSummary from '../WorkersSummary';

type Worker = components['schemas']['Worker'];

function worker(
  labels: Record<string, string>,
  versionDrift = WorkerVersionDrift.none
): Worker {
  return {
    id: 'worker-1',
    labels,
    totalPollers: 4,
    busyPollers: 0,
    runningTasks: [],
    lastHeartbeatAt: new Date().toISOString(),
    healthStatus: WorkerHealthStatus.healthy,
    version: '2.27.0',
    versionDrift,
  };
}

function renderSummary(workers: Worker[]) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <WorkersSummary workers={workers} isLoading={false} />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

describe('WorkersSummary', () => {
  it('shows every worker label', () => {
    renderSummary([
      worker({
        pool: 'general',
        label1: 'test',
        label2: 'another-test',
        os: 'linux',
        arch: 'amd64',
      }),
    ]);

    for (const label of [
      'pool=general',
      'label1=test',
      'label2=another-test',
      'os=linux',
      'arch=amd64',
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });

  it('counts the workers on an older version than the server', () => {
    renderSummary([
      worker({}, WorkerVersionDrift.behind),
      { ...worker({}), id: 'worker-2' },
    ]);

    expect(screen.getByText('1 on an older version')).toBeInTheDocument();
  });

  it('leaves the count out when every worker matches the server', () => {
    renderSummary([worker({})]);

    expect(screen.queryByText(/on an older version/)).not.toBeInTheDocument();
  });

  it('shows how each worker connects', () => {
    renderSummary([
      { ...worker({}), connection: WorkerConnection.certificate },
      { ...worker({}), id: 'worker-2', connection: WorkerConnection.tls },
      { ...worker({}), id: 'worker-3' },
    ]);

    expectConnection('Certificate');
    expectConnection('TLS');
    expectConnection('Connection not reported');
  });

  // Plaintext can be on purpose, as behind a service mesh, so the line names
  // the reason and the change to make, linked to the docs.
  it('names why a plaintext worker uses h2c and what to change', () => {
    renderSummary([
      {
        ...worker({}),
        connection: WorkerConnection.plaintext,
        plaintextReason: PlaintextReason.peer_insecure,
      },
      {
        ...worker({}),
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
});

// The list renders each connection line twice, in the Connection column and
// under the worker ID, and its width shows one of them.
function expectConnection(text: string | RegExp) {
  expect(screen.getAllByText(text)).toHaveLength(2);
}
