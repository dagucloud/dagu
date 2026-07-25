// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import DAGRunDetailsContent from '../DAGRunDetailsContent';

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock('@/features/dag-runs/components/common', () => ({
  DAGRunActions: () => null,
}));

vi.mock('@/features/dags/components', () => ({
  DAGStatus: () => null,
}));

vi.mock('@/components/ui/matrix-text', () => ({
  MatrixText: ({ text }: { text: string }) => text,
}));

const useBoundedDAGRunDetailsMock = vi.mocked(useBoundedDAGRunDetails);

describe('DAGRunDetailsContent', () => {
  beforeEach(() => {
    useBoundedDAGRunDetailsMock.mockReturnValue({
      data: {
        name: 'root',
        dagRunId: 'root-run',
        rootDAGRunName: 'root',
        rootDAGRunId: 'root-run',
        status: Status.Running,
        statusLabel: StatusLabel.running,
        startedAt: '',
        finishedAt: '',
        autoRetryCount: 0,
      },
      refresh: vi.fn(),
    } as never);
  });

  it('shows the root run status for a child run', () => {
    render(
      <MemoryRouter>
        <RemoteNodeProvider remoteNode="local">
          <DAGRunDetailsContent
            name="child"
            dagRun={
              {
                name: 'child',
                dagRunId: 'child-run',
                rootDAGRunName: 'root',
                rootDAGRunId: 'root-run',
                status: Status.Success,
                statusLabel: StatusLabel.succeeded,
                startedAt: '',
                finishedAt: '',
                autoRetryCount: 0,
              } as never
            }
            refreshFn={vi.fn()}
          />
        </RemoteNodeProvider>
      </MemoryRouter>
    );

    expect(screen.getByText('Root:')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'root' })).toBeInTheDocument();
    expect(screen.getByText(StatusLabel.running)).toBeInTheDocument();
    expect(useBoundedDAGRunDetailsMock).toHaveBeenCalledWith({
      target: {
        remoteNode: 'local',
        name: 'root',
        dagRunId: 'root-run',
      },
      enabled: true,
      pollIntervalMs: 2000,
    });
  });
});
