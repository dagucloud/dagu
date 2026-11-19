// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import type React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ServiceDAGState, Status } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import ActivityBars from '../ActivityBars';
import { LastRunCell } from '../LastRunCell';

// Reads the locale from stored preferences, as the app does.
function Wrapper({ children }: { children: React.ReactNode }) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>{children}</I18nProvider>
    </UserPreferencesProvider>
  );
}

const display = vi.hoisted(() => ({
  current: undefined as { healthy: boolean } | undefined,
}));

vi.mock('@/features/dag-runs/hooks/useListRunDisplay', () => ({
  useListRunDisplay: () => display.current,
}));

function dag(startedAt: string | undefined, status = Status.Success) {
  return {
    fileName: 'alpha.yaml',
    dag: { name: 'alpha' },
    latestDAGRun: { dagRunId: 'r1', status, startedAt },
  } as never;
}

function ago(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

describe('LastRunCell', () => {
  beforeEach(() => {
    display.current = undefined;
  });

  it('reads the compact time of the workflow header, behind the status dot', () => {
    render(<LastRunCell dag={dag(ago(3 * 86_400_000))} />, {
      wrapper: Wrapper,
    });
    expect(screen.getByTestId('dag-last-run')).toHaveTextContent('3d ago');
    expect(screen.getByRole('img', { name: 'Succeeded' })).toBeInTheDocument();
  });

  it('shows a dash when the workflow never ran', () => {
    render(<LastRunCell dag={dag(undefined)} />, { wrapper: Wrapper });
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('shows a heartbeat that runs well as Healthy, with no time', () => {
    display.current = { healthy: true };
    render(<LastRunCell dag={dag(ago(1000 * 86_400_000), Status.Running)} />, {
      wrapper: Wrapper,
    });
    const cell = screen.getByTestId('dag-last-run');
    expect(cell).toHaveAttribute('data-healthy', 'true');
    expect(cell).toHaveTextContent('Healthy');
    expect(cell).not.toHaveTextContent('ago');
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
    display.current = undefined;
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

  it('reads Healthy, with the chip of the service, for a heartbeat', () => {
    display.current = { healthy: true };
    render(
      <LastRunCell
        dag={dag(ago(1000 * 86_400_000), Status.Running)}
        serviceState={ServiceDAGState.running}
      />,
      { wrapper: Wrapper }
    );
    const cell = screen.getByTestId('dag-last-run');
    expect(cell).toHaveTextContent('Healthy');
    expect(cell).toHaveAttribute('data-service-state', 'running');
    expect(cell).not.toHaveAttribute('data-healthy');
  });

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

describe('ActivityBars of a heartbeat', () => {
  const run = (status: Status, startedAt: string) => ({
    dagRunId: `r-${status}`,
    status,
    startedAt,
  });

  it('draws the running run as a band to the end of the strip, not as a bar', () => {
    const { container } = render(
      <ActivityBars
        healthy
        activity={{
          runs: [run(Status.Running, ago(3_600_000))],
          truncated: false,
        }}
      />,
      { wrapper: Wrapper }
    );
    expect(container.querySelector('[data-band="true"]')).not.toBeNull();
  });

  it('keeps the bars of the other workflows', () => {
    const { container } = render(
      <ActivityBars
        activity={{
          runs: [run(Status.Running, ago(3_600_000))],
          truncated: false,
        }}
      />,
      { wrapper: Wrapper }
    );
    expect(container.querySelector('[data-band="true"]')).toBeNull();
    expect(container.querySelector('[data-run-id]')).not.toBeNull();
  });
});
