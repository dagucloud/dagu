// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import RunProgressModal from '../RunProgressModal';

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock('../RunOutput', () => ({
  default: ({
    dagRun,
    onInspect,
  }: {
    dagRun: components['schemas']['DAGRunDetails'];
    onInspect: (node: components['schemas']['Node']) => void;
  }) => (
    <div>
      Run output for {dagRun.dagRunId}
      <button type="button" onClick={() => onInspect(dagRun.nodes![0]!)}>
        Inspect step
      </button>
    </div>
  ),
}));

vi.mock('@/features/dags/components/visualization', () => ({
  DAGGraph: ({
    dagRun,
    onClickStep,
  }: {
    dagRun: components['schemas']['DAGRunDetails'];
    onClickStep: (id: string) => void;
  }) => (
    <div>
      Visualization for {dagRun.dagRunId}
      <button type="button" onClick={() => onClickStep('node_62_75_69_6c_64')}>
        Open graph step
      </button>
    </div>
  ),
}));

const dagRun = {
  name: 'example',
  dagRunId: 'run-1',
  status: Status.Running,
  statusLabel: StatusLabel.running,
  startedAt: '',
  finishedAt: '',
  artifactsAvailable: false,
  nodes: [
    {
      step: { name: 'build' },
      status: NodeStatus.Running,
      statusLabel: NodeStatusLabel.running,
    },
  ],
} as components['schemas']['DAGRunDetails'];

const appBarValue = {
  selectedRemoteNode: 'edge',
};

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
});

function LocationProbe() {
  const location = useLocation();
  return (
    <output aria-label="Location">{location.pathname + location.search}</output>
  );
}

function renderModal() {
  return render(
    <MemoryRouter initialEntries={['/dags']}>
      <AppBarContext.Provider value={appBarValue as never}>
        <LocationProbe />
        <RunProgressModal
          dagName="example"
          dagRunId="run-1"
          visible={true}
          dismissModal={vi.fn()}
        />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('RunProgressModal', () => {
  it('loads and displays the submitted run output', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: dagRun,
      error: null,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    });

    renderModal();

    expect(useBoundedDAGRunDetails).toHaveBeenCalledWith({
      target: {
        remoteNode: 'edge',
        name: 'example',
        dagRunId: 'run-1',
      },
      enabled: true,
      pollIntervalMs: 2000,
    });
    expect(screen.getByRole('dialog', { name: 'Run progress' })).toBeVisible();
    expect(screen.getByText('Run output for run-1')).toBeVisible();
  });

  it('switches between output and visualization in the modal', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: dagRun,
      error: null,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    });

    renderModal();

    fireEvent.click(screen.getByRole('button', { name: 'Visualization' }));
    expect(screen.getByText('Visualization for run-1')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Run output' }));
    expect(screen.getByText('Run output for run-1')).toBeVisible();
  });

  it('opens existing run details for a graph step', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: dagRun,
      error: null,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    });

    renderModal();

    fireEvent.click(screen.getByRole('button', { name: 'Visualization' }));
    fireEvent.click(screen.getByRole('button', { name: 'Open graph step' }));

    expect(screen.getByLabelText('Location')).toHaveTextContent(
      '/dag-runs/example/run-1?remoteNode=edge&step=build'
    );
  });

  it('opens existing run details from the details button', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: dagRun,
      error: null,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    });

    renderModal();

    fireEvent.click(screen.getByRole('button', { name: 'View details' }));

    expect(screen.getByLabelText('Location')).toHaveTextContent(
      '/dag-runs/example/run-1?remoteNode=edge'
    );
  });

  it('opens existing run details for a selected step', () => {
    vi.mocked(useBoundedDAGRunDetails).mockReturnValue({
      data: dagRun,
      error: null,
      isLoading: false,
      isValidating: false,
      refresh: vi.fn(),
    });

    renderModal();

    fireEvent.click(screen.getByRole('button', { name: 'Inspect step' }));

    expect(screen.getByLabelText('Location')).toHaveTextContent(
      '/dag-runs/example/run-1?remoteNode=edge&step=build'
    );
  });
});
