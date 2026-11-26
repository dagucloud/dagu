// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import type React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ServiceDAGState, Status } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { LastRunCell } from '../LastRunCell';

// Reads the locale from stored preferences, as the app does.
function Wrapper({ children }: { children: React.ReactNode }) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>{children}</I18nProvider>
    </UserPreferencesProvider>
  );
}

function dag(
  startedAt: string | undefined,
  status = Status.Success,
  dagRunId = 'r1'
) {
  return {
    fileName: 'alpha.yaml',
    dag: { name: 'alpha' },
    latestDAGRun: { dagRunId, status, startedAt },
  } as never;
}

function ago(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

describe('LastRunCell', () => {
  it('reads the compact time of the workflow header, behind the status dot', () => {
    render(<LastRunCell dag={dag(ago(3 * 86_400_000))} />, {
      wrapper: Wrapper,
    });
    expect(screen.getByTestId('dag-last-run')).toHaveTextContent('3d ago');
    expect(screen.getByRole('img', { name: 'Succeeded' })).toBeInTheDocument();
  });

  it('shows a dash when the workflow never ran', () => {
    render(<LastRunCell dag={dag(undefined, Status.NotStarted, '')} />, {
      wrapper: Wrapper,
    });
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  // A queued run has no start time until it starts.
  it('names the status of a run that has not started', () => {
    render(<LastRunCell dag={dag(undefined, Status.Queued)} />, {
      wrapper: Wrapper,
    });
    const cell = screen.getByTestId('dag-last-run');
    expect(cell).toHaveTextContent('Queued');
    expect(cell.querySelector('[data-status]')).toHaveAttribute(
      'data-status',
      String(Status.Queued)
    );
  });

  // Spec 100: only a service DAG reads Healthy; any other workflow keeps the
  // time of its run, also a run that repeats for ever.
  it('keeps the time of a running workflow', () => {
    render(<LastRunCell dag={dag(ago(3 * 86_400_000), Status.Running)} />, {
      wrapper: Wrapper,
    });
    expect(screen.getByTestId('dag-last-run')).toHaveTextContent('3d ago');
    expect(screen.getByRole('img', { name: 'Running' })).toBeInTheDocument();
  });
});

describe('LastRunCell of a service DAG', () => {
  beforeEach(() => {
    // The chip of a running DAG animates its text unless the user prefers less
    // motion.
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      configurable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    });
  });

  it.each([
    [ServiceDAGState.pending, 'Pending', Status.Queued],
    [ServiceDAGState.running, 'Healthy', Status.Success],
    [ServiceDAGState.restarting, 'Healthy', Status.Success],
    [ServiceDAGState.failing, 'Failing', Status.Failed],
    [ServiceDAGState.suspended, 'Suspended', Status.Waiting],
    [ServiceDAGState.stopped, 'Stopped', Status.Aborted],
  ])(
    'shows the state %s as the chip of the Runs page',
    (state, label, status) => {
      render(<LastRunCell dag={dag(ago(11_000))} serviceState={state} />, {
        wrapper: Wrapper,
      });
      const cell = screen.getByTestId('dag-last-run');
      expect(cell).toHaveAttribute('data-service-state', state);
      expect(cell).toHaveTextContent(label);
      expect(cell).not.toHaveTextContent('ago');
      expect(cell.querySelector('[data-status]')).toHaveAttribute(
        'data-status',
        String(status)
      );
    }
  );

  it('shows the state of a service DAG that never ran', () => {
    render(
      <LastRunCell
        dag={dag(undefined)}
        serviceState={ServiceDAGState.pending}
      />,
      { wrapper: Wrapper }
    );
    expect(screen.getByTestId('dag-last-run')).toHaveTextContent('Pending');
  });

  it('falls back to the plain cell when the state is not known', () => {
    render(<LastRunCell dag={dag(ago(11_000))} />, { wrapper: Wrapper });
    const cell = screen.getByTestId('dag-last-run');
    expect(cell).toHaveTextContent('11s ago');
    expect(cell).not.toHaveAttribute('data-service-state');
  });
});
