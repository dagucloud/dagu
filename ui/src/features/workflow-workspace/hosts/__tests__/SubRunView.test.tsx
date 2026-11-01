// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { SubRunOpenProvider } from '@/features/dags/components/common/SubRunStackModal';
import { SubRunView } from '../SubRunView';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/features/dag-runs/components/common/RunMetaLine', () => ({
  RunMetaLine: () => null,
}));
vi.mock('@/features/dags/components/dag-execution/StepLog', () => ({
  default: ({ stepName }: { stepName: string }) => (
    <div data-testid="step-log">{stepName}</div>
  ),
}));
// One button for each step: a click picks it, a double click opens it.
vi.mock('@/features/visual-editor/DetailsCanvas', () => ({
  default: ({
    dag,
    overlay,
    onSelectStep,
    onOpenStep,
  }: {
    dag: { steps?: Array<{ name: string }> };
    overlay?: Record<string, { status?: NodeStatus }>;
    onSelectStep?(step: string | null): void;
    onOpenStep?(step: string): void;
  }) => (
    <div>
      {dag.steps?.map((step) => (
        <button
          key={step.name}
          type="button"
          data-status={overlay?.[step.name]?.status}
          onClick={() => onSelectStep?.(step.name)}
          onDoubleClick={() => onOpenStep?.(step.name)}
        >
          {step.name}
        </button>
      ))}
    </div>
  ),
}));

function node(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    startedAt: '2026-01-15T02:00:01Z',
    finishedAt: '2026-01-15T02:00:02Z',
    retryCount: 0,
    doneCount: 0,
    log: '',
    ...extra,
  } as Node;
}

const run = {
  name: 'child-dag',
  dagRunId: 'c-1',
  rootDAGRunName: 'demo',
  rootDAGRunId: 'run-1',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  log: '',
  nodes: [
    node('extract'),
    node('call-grandchild', {
      step: { name: 'call-grandchild', call: 'grandchild' },
      subRuns: [{ dagRunId: 'g-1', dagName: 'grandchild', params: '' }],
    }),
  ],
} as unknown as DAGRunDetails;

function renderView() {
  const push = vi.fn();
  render(
    <MemoryRouter>
      <SubRunOpenProvider value={push}>
        <SubRunView run={run} />
      </SubRunOpenProvider>
    </MemoryRouter>
  );
  return push;
}

describe('SubRunView', () => {
  it('draws the steps of the run with their state', async () => {
    renderView();

    expect(
      await screen.findByRole('button', { name: 'extract' })
    ).toHaveAttribute('data-status', String(NodeStatus.Success));
  });

  it('shows the log of a picked step, and closes it', async () => {
    renderView();

    fireEvent.click(await screen.findByRole('button', { name: 'extract' }));
    expect(screen.getByTestId('step-log')).toHaveTextContent('extract');

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('step-log')).not.toBeInTheDocument();
  });

  // A child run of the child run opens on the same stack.
  it('opens the child run of a step on the stack', async () => {
    const push = renderView();

    fireEvent.doubleClick(
      await screen.findByRole('button', { name: 'call-grandchild' })
    );

    expect(push).toHaveBeenCalledWith({
      name: 'grandchild',
      dagRunId: 'g-1',
    });
  });
});
