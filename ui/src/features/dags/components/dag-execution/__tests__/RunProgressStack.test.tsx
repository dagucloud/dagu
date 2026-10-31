// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import RunProgressStack, { pushRunProgress } from '../RunProgressStack';

const { runStatuses } = vi.hoisted(() => ({
  runStatuses: new Map<string, Status>(),
}));

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: vi.fn(({ target }) => {
    const status = target
      ? (runStatuses.get(target.dagRunId) ?? Status.Running)
      : Status.Running;
    return {
      data: target
        ? {
            name: target.name,
            dagRunId: target.dagRunId,
            status,
            statusLabel:
              status === Status.Success
                ? StatusLabel.succeeded
                : StatusLabel.running,
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
    };
  }),
}));

beforeEach(() => {
  runStatuses.clear();
  vi.mocked(useBoundedDAGRunDetails).mockClear();
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

afterEach(() => {
  vi.useRealTimers();
});

function Where() {
  const location = useLocation();
  return (
    <output data-testid="where">{location.pathname + location.search}</output>
  );
}

function AddRunButton({
  dagName,
  dagRunId,
  fileName,
}: {
  dagName: string;
  dagRunId: string;
  fileName?: string;
}) {
  return (
    <button
      type="button"
      onClick={() =>
        pushRunProgress({ dagName, fileName, dagRunId, remoteNode: 'edge' })
      }
    >
      Add {dagRunId}
    </button>
  );
}

describe('RunProgressStack', () => {
  it('stacks submitted runs', () => {
    render(
      <MemoryRouter>
        <AddRunButton dagName="first" dagRunId="run-1" />
        <AddRunButton dagName="second" dagRunId="run-2" />
        <RunProgressStack />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add run-2' }));

    expect(
      screen.getByRole('button', { name: 'Open run progress for run-1' })
    ).toHaveTextContent('first');
    expect(
      screen.getByRole('button', { name: 'Open run progress for run-2' })
    ).toHaveTextContent('second');
  });

  // A click leaves the notification and shows the run on the workflow page.
  it('goes to the run page and removes the card when a stacked run is clicked', () => {
    render(
      <MemoryRouter>
        <AddRunButton
          dagName="Example name"
          dagRunId="run-1"
          fileName="example"
        />
        <RunProgressStack />
        <Where />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Open run progress for run-1' })
    );

    expect(screen.getByTestId('where')).toHaveTextContent(
      '/dags/example?remoteNode=edge&run=run-1'
    );
    expect(
      screen.queryByRole('button', { name: 'Open run progress for run-1' })
    ).toBeNull();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('falls back on the DAG name when no file name is given', () => {
    render(
      <MemoryRouter>
        <AddRunButton dagName="example" dagRunId="run-1" />
        <RunProgressStack />
        <Where />
      </MemoryRouter>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Open run progress for run-1' })
    );
    expect(screen.getByTestId('where')).toHaveTextContent('/dags/example?');
  });

  it('caps the stack to the latest five runs', () => {
    render(
      <MemoryRouter>
        <RunProgressStack />
      </MemoryRouter>
    );

    act(() => {
      for (let index = 1; index <= 6; index += 1) {
        pushRunProgress({
          dagName: `dag-${index}`,
          dagRunId: `run-${index}`,
          remoteNode: 'edge',
        });
      }
    });

    expect(
      screen.queryByRole('button', { name: 'Open run progress for run-1' })
    ).toBeNull();
    expect(
      screen.getByRole('button', { name: 'Open run progress for run-6' })
    ).toBeVisible();
  });

  it('auto-dismisses successful runs', async () => {
    vi.useFakeTimers();
    runStatuses.set('run-1', Status.Success);
    render(
      <MemoryRouter>
        <AddRunButton dagName="example" dagRunId="run-1" />
        <RunProgressStack />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    expect(
      screen.getByRole('button', { name: 'Open run progress for run-1' })
    ).toBeVisible();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(
      screen.queryByRole('button', { name: 'Open run progress for run-1' })
    ).toBeNull();
  });

  it('dismisses a stacked run without leaving the page', () => {
    render(
      <MemoryRouter>
        <AddRunButton dagName="example" dagRunId="run-1" />
        <RunProgressStack />
        <Where />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add run-1' }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Dismiss run progress' })
    );

    expect(
      screen.queryByRole('button', { name: 'Open run progress for run-1' })
    ).toBeNull();
    expect(screen.getByTestId('where').textContent).toBe('/');
  });
});
