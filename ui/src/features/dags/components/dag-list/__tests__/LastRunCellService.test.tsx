// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import type React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import { LastRunCell } from '../LastRunCell';

// The hook of the list runs for real: it reads the definition of the workflow.
const h = vi.hoisted(() => ({ service: false }));

const forever = { repeatPolicy: { repeat: 'while', interval: 10 } };

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    if (init === null) return { data: undefined };
    if (path === '/dags/{fileName}') {
      return {
        data: {
          dag: {
            steps: [forever],
            restartPolicy: h.service ? 'always' : undefined,
          },
        },
      };
    }
    return {
      data: {
        dagRunDetails: {
          dagRunId: 'r1',
          status: Status.Running,
          nodes: [{ status: NodeStatus.Running, step: forever }],
        },
      },
    };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

function Wrapper({ children }: { children: React.ReactNode }) {
  return (
    <UserPreferencesProvider>
      <I18nProvider>{children}</I18nProvider>
    </UserPreferencesProvider>
  );
}

const running = {
  fileName: 'always-running.yaml',
  dag: { name: 'always-running' },
  latestDAGRun: {
    dagRunId: 'r1',
    status: Status.Running,
    startedAt: new Date(Date.now() - 3 * 86_400_000).toISOString(),
  },
} as never;

describe('LastRunCell of a run with a continuous step', () => {
  beforeEach(() => {
    h.service = false;
  });

  it('shows the normal status and the time for a workflow that is not a service DAG', () => {
    render(<LastRunCell dag={running} />, { wrapper: Wrapper });
    const cell = screen.getByTestId('dag-last-run');
    expect(cell).not.toHaveAttribute('data-healthy');
    expect(cell).not.toHaveTextContent('Healthy');
    expect(cell).toHaveTextContent('3d ago');
    expect(screen.getByRole('img', { name: 'Running' })).toBeInTheDocument();
  });

  it('shows Healthy, with no time, for a service DAG whose state is not known yet', () => {
    h.service = true;
    render(<LastRunCell dag={running} />, { wrapper: Wrapper });
    const cell = screen.getByTestId('dag-last-run');
    expect(cell).toHaveAttribute('data-healthy', 'true');
    expect(cell).toHaveTextContent('Healthy');
    expect(cell).not.toHaveTextContent('ago');
  });
});
