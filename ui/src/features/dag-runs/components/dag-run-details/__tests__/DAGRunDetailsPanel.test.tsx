// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useBoundedDAGRunDetails } from '../../../hooks/useBoundedDAGRunDetails';
import DAGRunDetailsPanel from '../DAGRunDetailsPanel';

vi.mock('../../../hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(),
}));

vi.mock('../DAGRunDetailsContent', () => ({
  default: ({ dagRun }: { dagRun: { dagRunId: string } }) => (
    <div>run {dagRun.dagRunId}</div>
  ),
}));

const appBarValue = {
  title: 'Runs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const useBoundedDAGRunDetailsMock = useBoundedDAGRunDetails as unknown as {
  mockReturnValue: (value: unknown) => void;
  mockClear: () => void;
  mock: {
    calls: Array<[unknown]>;
  };
};

function renderPanel() {
  return render(
    <MemoryRouter>
      <AppBarContext.Provider value={appBarValue}>
        <DAGRunDetailsPanel
          name="child-dag"
          dagRunId="child-run"
          onClose={vi.fn()}
        />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

afterEach(() => {
  vi.clearAllMocks();
  window.history.pushState({}, '', '/');
});

describe('DAGRunDetailsPanel', () => {
  it('enables the regular dag-run details target when no sub-dag params exist', () => {
    useBoundedDAGRunDetailsMock.mockReturnValue({
      data: { dagRunId: 'child-run', name: 'child-dag' },
      error: null,
      refresh: vi.fn(),
    });

    renderPanel();

    expect(
      useBoundedDAGRunDetailsMock.mock.calls[0]?.[0]
    ).toEqual(
      expect.objectContaining({
        target: {
          remoteNode: 'local',
          name: 'child-dag',
          dagRunId: 'child-run',
        },
        enabled: true,
        pollIntervalMs: 2000,
      })
    );
    expect(screen.getByText('run child-run')).toBeInTheDocument();
  });

  it('enables the sub-dag details target when sub-dag params exist', () => {
    window.history.pushState(
      {},
      '',
      '/?subDAGRunId=sub-run&dagRunId=root-run&dagRunName=root-dag'
    );
    useBoundedDAGRunDetailsMock.mockReturnValue({
      data: { dagRunId: 'sub-run' },
      error: null,
      refresh: vi.fn(),
    });

    renderPanel();

    expect(
      useBoundedDAGRunDetailsMock.mock.calls[0]?.[0]
    ).toEqual(
      expect.objectContaining({
        target: {
          remoteNode: 'local',
          name: 'child-dag',
          dagRunId: 'child-run',
          parentName: 'root-dag',
          parentDAGRunId: 'root-run',
          subDAGRunId: 'sub-run',
        },
        enabled: true,
        pollIntervalMs: 2000,
      })
    );
    expect(screen.getByText('run sub-run')).toBeInTheDocument();
  });
});
