// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import RunProgressStack, { pushRunProgress } from '../RunProgressStack';

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(({ target }) => ({
    data: target
      ? {
          name: target.name,
          dagRunId: target.dagRunId,
          status: Status.Running,
          statusLabel: StatusLabel.running,
          startedAt: '',
          finishedAt: '',
          artifactsAvailable: false,
          nodes: [],
        }
      : null,
    error: null,
    isLoading: false,
    isValidating: false,
    refresh: vi.fn(),
  })),
}));

vi.mock('../RunProgressModal', () => ({
  default: ({
    dagName,
    dagRunId,
    visible,
  }: {
    dagName: string;
    dagRunId: string;
    visible: boolean;
  }) =>
    visible ? (
      <div role="dialog" aria-label="Run progress">
        {dagName} {dagRunId}
      </div>
    ) : null,
}));

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

function AddRunButton({
  dagName,
  dagRunId,
}: {
  dagName: string;
  dagRunId: string;
}) {
  return (
    <button
      type="button"
      onClick={() => pushRunProgress({ dagName, dagRunId, remoteNode: 'edge' })}
    >
      Add {dagRunId}
    </button>
  );
}

describe('RunProgressStack', () => {
  it('stacks submitted runs without opening the modal', () => {
    render(
      <MemoryRouter>
        <AddRunButton dagName="first" dagRunId="run-1" />
        <AddRunButton dagName="second" dagRunId="run-2" />
        <RunProgressStack />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add run-2' }));

    expect(screen.queryByRole('dialog', { name: 'Run progress' })).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Open run progress for run-1' })
    ).toHaveTextContent('first');
    expect(
      screen.getByRole('button', { name: 'Open run progress for run-2' })
    ).toHaveTextContent('second');
  });

  it('opens the modal when a stacked run is clicked', async () => {
    render(
      <MemoryRouter>
        <AddRunButton dagName="example" dagRunId="run-1" />
        <RunProgressStack />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Open run progress for run-1' })
    );

    expect(
      await screen.findByRole('dialog', { name: 'Run progress' })
    ).toHaveTextContent('example run-1');
  });

  it('dismisses a stacked run without opening the modal', () => {
    render(
      <MemoryRouter>
        <AddRunButton dagName="example" dagRunId="run-1" />
        <RunProgressStack />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss run progress' }));

    expect(
      screen.queryByRole('button', { name: 'Open run progress for run-1' })
    ).toBeNull();
    expect(screen.queryByRole('dialog', { name: 'Run progress' })).toBeNull();
  });
});
