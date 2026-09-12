// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { components, NodeStatus, Status } from '@/api/v1/schema';
import RunOutput from '../RunOutput';

vi.mock('../StepLog', () => ({
  default: ({
    stepName,
    stream,
    onFollowTailChange,
    onSettled,
  }: {
    stepName: string;
    stream: string;
    onFollowTailChange: (following: boolean) => void;
    onSettled: (name: string) => void;
  }) => (
    <div>
      <p>
        {stepName} {stream} output
      </p>
      <button onClick={() => onFollowTailChange(false)}>
        Read older lines
      </button>
      <button onClick={() => onSettled(stepName)}>Final output received</button>
    </div>
  ),
}));

function run(statuses: NodeStatus[], id = 'run-1', status = Status.Running) {
  return {
    name: 'parallel',
    dagRunId: id,
    status,
    nodes: statuses.map((nodeStatus, index) => ({
      step: { name: `step-${index + 1}` },
      status: nodeStatus,
      statusLabel: NodeStatus[nodeStatus].toLowerCase(),
    })),
  } as components['schemas']['DAGRunDetails'];
}

const inspect = vi.fn();

describe('RunOutput', () => {
  it('keeps the active step while parallel steps change', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    view.rerender(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Failed])}
        onInspect={inspect}
      />
    );
    expect(screen.getByText('step-1 stdout output')).toBeVisible();
    expect(screen.getByText('1 failed')).toBeVisible();
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'step-1running',
      'step-2failed',
    ]);
  });

  it('advances after the selected step delivers its final output', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    view.rerender(
      <RunOutput
        dagRun={run([NodeStatus.Success, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    expect(screen.getByText('step-1 stdout output')).toBeVisible();
    fireEvent.click(
      screen.getByRole('button', { name: 'Final output received' })
    );
    expect(screen.getByText('step-2 stdout output')).toBeVisible();
  });

  it('keeps a manually selected parallel step after completion', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    fireEvent.click(screen.getByRole('tab', { name: /step-2/ }));
    view.rerender(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Success])}
        onInspect={inspect}
      />
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Final output received' })
    );
    expect(screen.getByText('step-2 stdout output')).toBeVisible();
  });

  it('pauses automatic selection while reading and resumes explicitly', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Read older lines' }));
    view.rerender(
      <RunOutput
        dagRun={run([NodeStatus.Success, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Final output received' })
    );
    expect(screen.getByText('step-1 stdout output')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Back to live' }));
    expect(screen.getByText('step-2 stdout output')).toBeVisible();
  });

  it.each([NodeStatus.Failed, NodeStatus.Aborted])(
    'keeps an unsuccessful step while siblings continue (%s)',
    (status) => {
      const view = render(
        <RunOutput
          dagRun={run([NodeStatus.Running, NodeStatus.Running])}
          onInspect={inspect}
        />
      );
      view.rerender(
        <RunOutput
          dagRun={run([status, NodeStatus.Running])}
          onInspect={inspect}
        />
      );
      fireEvent.click(
        screen.getByRole('button', { name: 'Final output received' })
      );
      fireEvent.click(screen.getByRole('button', { name: 'stderr' }));
      expect(screen.getByText('step-1 stderr output')).toBeVisible();
    }
  );

  it('starts displaying output when a queued run becomes active', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.NotStarted], 'run-1', Status.Queued)}
        onInspect={inspect}
      />
    );
    expect(screen.getByRole('tabpanel')).toHaveTextContent(
      'Waiting for execution...'
    );
    view.rerender(
      <RunOutput dagRun={run([NodeStatus.Running])} onInspect={inspect} />
    );
    expect(screen.getByText('step-1 stdout output')).toBeVisible();
  });

  it('resets selection when opening a different run', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    fireEvent.click(screen.getByRole('tab', { name: /step-2/ }));
    view.rerender(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.NotStarted], 'run-2')}
        onInspect={inspect}
      />
    );
    expect(screen.getByText('step-1 stdout output')).toBeVisible();
  });

  it('supports keyboard selection without moving focus on live updates', () => {
    const view = render(
      <RunOutput
        dagRun={run([NodeStatus.Running, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    fireEvent.keyDown(screen.getByRole('tab', { name: /step-1/ }), {
      key: 'ArrowDown',
    });
    const selected = screen.getByRole('tab', { name: /step-2/ });
    expect(selected).toHaveFocus();
    expect(screen.getByText('step-2 stdout output')).toBeVisible();
    view.rerender(
      <RunOutput
        dagRun={run([NodeStatus.Failed, NodeStatus.Running])}
        onInspect={inspect}
      />
    );
    expect(selected).toHaveFocus();
  });
});
