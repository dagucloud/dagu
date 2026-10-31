// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  ReusedInputVia,
  Status,
} from '@/api/v1/schema';
import type { ApiDAGRunDetails, ApiNode } from '../../contracts';
import { node, run } from '../../data/__tests__/fixtures';
import { StepFacts } from '../StepFacts';

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: undefined, error: undefined }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
const { useStepOutputsMock } = vi.hoisted(() => ({
  useStepOutputsMock: vi.fn(() => ({ data: undefined, error: undefined })),
}));

vi.mock('@/features/visual-editor/api/useStepOutputs', () => ({
  useStepOutputs: useStepOutputsMock,
}));
vi.mock('../../data/context', () => ({
  useWorkspaceData: () => ({ fileName: 'demo.yaml' }),
}));
vi.mock('../useStepRunActions', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useStepRunActions: () => ({
    can: () => ({
      retry: false,
      setStatus: false,
      runStep: false,
      subRuns: false,
    }),
    retryStep: vi.fn(),
    setStatus: vi.fn(),
    runStep: vi.fn(),
    runUpstream: vi.fn(),
    openSubRun: vi.fn(),
    openLogs: vi.fn(),
    dialogs: null,
  }),
}));

function renderFacts(
  step: ApiNode,
  status = Status.Success,
  extra: Partial<ApiDAGRunDetails> = {}
) {
  return render(
    <MemoryRouter>
      <StepFacts
        target={{ step: step.step.name }}
        holder={run('run-1', [step], status, extra)}
        node={step}
      />
    </MemoryRouter>
  );
}

afterEach(() => {
  vi.useRealTimers();
});

describe('StepFacts colours and durations', () => {
  // The chip takes its colour from lib/statusPalette.ts. parity:dup-23
  it('gives the chip of an aborted step the aborted colour', () => {
    renderFacts(
      node('a', NodeStatus.Aborted, { statusLabel: NodeStatusLabel.aborted }),
      Status.Aborted
    );

    const chip = screen.getByTestId('ww-step-status');
    expect(chip).toHaveTextContent('Aborted');
    expect(chip).toHaveClass(
      'text-[var(--status-aborted)]',
      'bg-[var(--status-aborted)]/10'
    );
    expect(chip.querySelector('[aria-hidden="true"]')).toHaveClass(
      'bg-[var(--status-aborted)]'
    );
  });

  it('colours the retries and the error of a failed step as warning and error', () => {
    renderFacts(
      node('a', NodeStatus.Failed, {
        statusLabel: NodeStatusLabel.failed,
        retryCount: 2,
        error: 'exit status 1',
      }),
      Status.Failed
    );

    expect(screen.getByTestId('ww-step-status')).toHaveClass(
      'text-[var(--status-error)]'
    );
    expect(screen.getByLabelText('Retried 2×')).toHaveClass('bg-warning/10');
    expect(screen.getByRole('alert')).toHaveClass(
      'text-[var(--status-error)]',
      'bg-[var(--status-error)]/10'
    );
  });

  // The status was set to Success by hand: the failure is history, not an alert.
  it('labels the error of a step set to Success as a previous error', () => {
    renderFacts(
      node('a', NodeStatus.Success, {
        statusLabel: NodeStatusLabel.succeeded,
        error: 'exit status 3',
      }),
      Status.Failed
    );

    expect(screen.queryByRole('alert')).toBeNull();
    const previous = screen.getByTestId('ww-step-previous-error');
    expect(previous).toHaveTextContent('Previous error');
    expect(previous).toHaveTextContent('exit status 3');
  });

  // The live time of a running step comes from formatElapsed.
  it('ticks the duration of a running step next to a running dot', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-15T02:01:05Z'));
    renderFacts(
      node('a', NodeStatus.Running, {
        statusLabel: NodeStatusLabel.running,
        finishedAt: '-',
      }),
      Status.Running
    );

    const duration = screen.getByText('1m 5s');
    expect(
      duration.parentElement?.querySelector('[aria-hidden="true"]')
    ).toHaveClass('bg-[var(--status-running)]', 'motion-safe:animate-pulse');
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByText('1m 6s')).toBeInTheDocument();
  });
});

describe('StepFacts outputs', () => {
  const skipped = (extra: Record<string, unknown> = {}) =>
    node('a', NodeStatus.Skipped, {
      statusLabel: NodeStatusLabel.skipped,
      ...extra,
    });

  // The runner gives a step whose precondition failed a finish time: only
  // the record of the run tells a reused step.
  it('reads no outputs of a skipped step that the run does not carry', () => {
    renderFacts(skipped({ doneCount: 0 }));
    expect(useStepOutputsMock).toHaveBeenLastCalledWith(
      expect.any(String),
      null,
      null
    );
  });

  it('reads the outputs of a skipped step that the run carries', () => {
    renderFacts(skipped(), Status.Success, {
      reusedInputs: [{ step: 'a', runId: 'run-0', via: ReusedInputVia.run }],
    });
    expect(useStepOutputsMock).toHaveBeenLastCalledWith(
      expect.any(String),
      'run-1',
      'a'
    );
  });

  // The outputs of a human task come when it completes, under the same key.
  it('reads no outputs of a waiting step', () => {
    renderFacts(
      node('a', NodeStatus.Waiting, { statusLabel: NodeStatusLabel.waiting }),
      Status.Waiting
    );
    expect(useStepOutputsMock).toHaveBeenLastCalledWith(
      expect.any(String),
      null,
      null
    );
  });
});
