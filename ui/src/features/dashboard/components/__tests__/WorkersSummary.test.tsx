// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';
import { WorkerHealthStatus, type components } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import WorkersSummary from '../WorkersSummary';

type Worker = components['schemas']['Worker'];

function worker(labels: Record<string, string>): Worker {
  return {
    id: 'worker-1',
    labels,
    totalPollers: 4,
    busyPollers: 0,
    runningTasks: [],
    lastHeartbeatAt: new Date().toISOString(),
    healthStatus: WorkerHealthStatus.healthy,
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
});
