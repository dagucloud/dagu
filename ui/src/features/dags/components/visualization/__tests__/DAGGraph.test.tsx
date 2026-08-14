// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { components, Status, StatusLabel } from '@/api/v1/schema';
import DAGGraph from '../DAGGraph';

type TimelineProps = {
  onOpenSubRun?: (entry: { name: string; dagRunId: string }) => void;
};

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: false } }),
}));

vi.mock('react-cookie', () => ({
  useCookies: () => [{}, vi.fn()],
}));

vi.mock('../index', () => ({
  Graph: () => <div>Graph</div>,
  TimelineChart: ({ onOpenSubRun }: TimelineProps) => (
    <button
      type="button"
      onClick={() =>
        onOpenSubRun?.({ name: 'child-dag', dagRunId: 'child-run' })
      }
    >
      Open child run
    </button>
  ),
}));

const dagRun: components['schemas']['DAGRunDetails'] = {
  dagRunId: 'root-run',
  name: 'root-dag',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  autoRetryCount: 0,
  startedAt: '2026-01-01T00:00:00Z',
  finishedAt: '2026-01-01T00:01:00Z',
  artifactsAvailable: false,
  rootDAGRunName: 'root-dag',
  rootDAGRunId: 'root-run',
  log: '',
  nodes: [],
};

describe('DAGGraph', () => {
  it('forwards timeline child-run opens to its caller', async () => {
    const onOpenSubRun = vi.fn();

    render(<DAGGraph dagRun={dagRun} onOpenSubRun={onOpenSubRun} />);
    await userEvent.click(screen.getByRole('button', { name: 'Timeline' }));
    await userEvent.click(
      screen.getByRole('button', { name: 'Open child run' })
    );

    expect(onOpenSubRun).toHaveBeenCalledWith({
      name: 'child-dag',
      dagRunId: 'child-run',
    });
  });
});
