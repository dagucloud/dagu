// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, NodeStatusLabel, Status } from '@/api/v1/schema';
import type { StepCommandGates } from '@/features/visual-editor/contracts';
import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  WorkspaceRunValue,
} from '../../contracts';
import { DAG_NAME, FILE_NAME, node, run } from '../../data/__tests__/fixtures';
import { WorkspaceDataContext } from '../../data/context';
import type { WorkspaceDataValue } from '../../data/context';
import { runHandlers } from '../../data/handlerMapping';
import { nodesByStep } from '../../data/runFacts';
import { StepPanel } from '../StepPanel';

vi.mock('@/features/dag-runs/hooks/useBoundedDAGRunDetails', () => ({
  useBoundedDAGRunDetails: () => ({ data: undefined, error: null }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
// The run tab bodies have their own tests: here only which one shows.
vi.mock('../../dock/tabs', () => {
  const body = (tab: string) =>
    function Body() {
      return <div data-testid={`body-${tab}`} />;
    };
  return {
    dockTabBodies: {
      log: body('log'),
      history: body('history'),
      outputs: body('outputs'),
    },
  };
});
// The facts read the gates of the step actions, which the panel does not use.
vi.mock('../useStepRunActions', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useStepRunActions: () => ({
    can: () => ({
      retry: false,
      setStatus: false,
      runStep: false,
      subRuns: false,
    }),
    openLogs: vi.fn(),
    dialogs: null,
  }),
}));

const DAG = {
  name: DAG_NAME,
  steps: [
    { name: 'fetch' },
    {
      name: 'build',
      depends: ['fetch'],
      commands: [{ command: 'make', args: ['all'] }],
    },
  ],
} as unknown as ApiDAGDetails;

/** The selected run; with `loadingId` alone, a run whose details are not read yet. */
function runValue(
  details: ApiDAGRunDetails | null,
  loadingId?: string
): WorkspaceRunValue {
  const runId = details?.dagRunId ?? loadingId;
  return {
    ref: runId ? { name: DAG_NAME, runId } : null,
    details,
    hasRun: details !== null,
    isActive: false,
    nodeByStep: nodesByStep(details),
    handlers: runHandlers(details),
    refresh: vi.fn(),
  } as unknown as WorkspaceRunValue;
}

function gates(allow: boolean): StepCommandGates {
  return {
    canRunStep: true,
    canRetry: () => allow,
    canSetStatus: () => allow,
    hasSubRuns: () => false,
  };
}

function setup({
  details,
  step = 'build',
  allow = true,
  loadingId,
  openedAt,
}: {
  details: ApiDAGRunDetails | null;
  step?: string;
  allow?: boolean;
  loadingId?: string;
  openedAt?: { clientX: number; clientY: number };
}) {
  const onClose = vi.fn();
  const onStepCommand = vi.fn();
  const onOpenStep = vi.fn();
  const panel = (value: ApiDAGRunDetails | null, name = step) => (
    <MemoryRouter>
      <WorkspaceDataContext.Provider
        value={
          {
            fileName: FILE_NAME,
            dagData: { dag: DAG },
            run: runValue(value, loadingId),
          } as unknown as WorkspaceDataValue
        }
      >
        <StepPanel
          step={name}
          openedAt={openedAt}
          onClose={onClose}
          onStepCommand={onStepCommand}
          gates={gates(allow)}
          onOpenStep={onOpenStep}
        />
      </WorkspaceDataContext.Provider>
    </MemoryRouter>
  );
  // The run and the step that the panel shows now: each helper changes one.
  let shown = { details, step };
  const view = render(panel(details));
  return {
    onClose,
    onStepCommand,
    onOpenStep,
    rerun: (next: ApiDAGRunDetails | null) => {
      shown = { ...shown, details: next };
      view.rerender(panel(shown.details, shown.step));
    },
    showStep: (name: string) => {
      shown = { ...shown, step: name };
      view.rerender(panel(shown.details, shown.step));
    },
  };
}

function failedBuild(): ApiDAGRunDetails {
  return run(
    'r1',
    [
      node('fetch'),
      node('build', NodeStatus.Failed, {
        statusLabel: NodeStatusLabel.failed,
        error: 'exit status 2',
        step: { depends: ['fetch'], commands: [{ command: 'make' }] },
      }),
    ],
    Status.Failed
  );
}

describe('StepPanel', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('is a landmark named by its step, with the workflow name and a close button', () => {
    const { onClose } = setup({ details: failedBuild() });
    const panel = screen.getByRole('complementary', {
      name: 'Details of build',
    });
    expect(within(panel).getByRole('heading', { name: 'build' })).toBeVisible();
    expect(panel).toHaveTextContent(DAG_NAME);

    fireEvent.click(within(panel).getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('sends Retry step and Change status through the host commands', () => {
    const { onStepCommand, onOpenStep } = setup({ details: failedBuild() });

    fireEvent.click(screen.getByRole('button', { name: 'Retry step' }));
    expect(onStepCommand).toHaveBeenLastCalledWith({
      kind: 'retryStep',
      step: 'build',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Change status' }));
    expect(onStepCommand).toHaveBeenLastCalledWith({
      kind: 'setStatus',
      step: 'build',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Open step' }));
    expect(onOpenStep).toHaveBeenCalledTimes(1);
  });

  it('offers no run action that the gates refuse', () => {
    setup({ details: failedBuild(), allow: false });
    expect(screen.queryByRole('button', { name: 'Retry step' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Change status' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Open step' })).toBeVisible();
  });

  // The node menu offers neither action for a handler.
  it('offers no Retry step or Change status for a handler', () => {
    const details = run('r1', [node('fetch')], Status.Failed, {
      onFailure: node('notify', NodeStatus.Failed),
    });
    setup({ details, step: 'notify' });
    expect(screen.queryByRole('button', { name: 'Retry step' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Change status' })).toBeNull();
  });

  it('opens on Details with the run facts and the definition of the run', () => {
    setup({ details: failedBuild() });
    expect(screen.getByRole('tab', { name: 'Details' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    const details = screen.getByTestId('ww-step-panel-details');
    expect(within(details).getByRole('alert')).toHaveTextContent(
      'exit status 2'
    );
    expect(within(details).getByText('Started')).toBeInTheDocument();
    expect(within(details).getByText('Definition')).toBeInTheDocument();
    expect(within(details).getByText('Commands')).toBeInTheDocument();
    expect(within(details).getByText('Depends On')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'Log' }));
    expect(screen.getByTestId('body-log')).toBeInTheDocument();
  });

  it('shows a step of a workflow that never ran as not run, with its definition', () => {
    // The gates of the host refuse the run actions of a step with no run.
    setup({ details: null, allow: false });
    const details = screen.getByTestId('ww-step-panel-details');
    expect(details).toHaveTextContent('Not run yet');
    expect(within(details).getByText('Depends On')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-step-facts-line')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Retry step' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Open step' })).toBeVisible();
  });

  it('opens another step on Details, whatever tab the last one showed', () => {
    const { showStep } = setup({ details: failedBuild() });
    fireEvent.click(screen.getByRole('tab', { name: 'Log' }));

    showStep('fetch');
    expect(screen.getByRole('tab', { name: 'Details' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('says that the run loads while its details are not read', () => {
    setup({ details: null, loadingId: 'r9' });
    expect(screen.getByTestId('ww-step-panel-details')).toHaveTextContent(
      'Loading run...'
    );
  });

  describe('a double click on a step under it', () => {
    // The panel sits over the right of a 1000px wide zone.
    function placePanel() {
      vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue(
        new DOMRect(600, 0, 400, 800)
      );
    }
    const panel = () => screen.getByTestId('ww-step-panel');

    it('reaches the step when the panel opens over the click', () => {
      vi.useFakeTimers();
      placePanel();
      setup({
        details: failedBuild(),
        openedAt: { clientX: 700, clientY: 100 },
      });
      expect(panel()).toHaveClass('pointer-events-none');

      act(() => vi.advanceTimersByTime(500));
      expect(panel()).not.toHaveClass('pointer-events-none');
    });

    it('leaves the panel usable at once when it opens beside the click', () => {
      placePanel();
      setup({
        details: failedBuild(),
        openedAt: { clientX: 300, clientY: 100 },
      });
      expect(panel()).not.toHaveClass('pointer-events-none');
    });

    it('leaves the panel usable at once when a link opens it', () => {
      placePanel();
      setup({ details: failedBuild() });
      expect(panel()).not.toHaveClass('pointer-events-none');
    });
  });

  it('follows the selected run', () => {
    const { rerun } = setup({ details: failedBuild() });
    expect(screen.getByTestId('ww-step-facts-status')).toHaveTextContent(
      'Failed'
    );

    rerun(run('r2', [node('fetch'), node('build')]));
    expect(screen.getByTestId('ww-step-facts-status')).not.toHaveTextContent(
      'Failed'
    );
    expect(screen.queryByRole('alert')).toBeNull();

    // A run that did not hold the step.
    rerun(run('r3', [node('fetch')]));
    expect(screen.getByTestId('ww-step-panel-details')).toHaveTextContent(
      'Not run in this run'
    );
    expect(screen.queryByTestId('ww-step-facts-line')).toBeNull();
  });
});
