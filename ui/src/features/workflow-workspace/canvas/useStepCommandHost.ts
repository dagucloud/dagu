// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import type {
  StepCommand,
  StepCommandGates,
} from '@/features/visual-editor/contracts';
import type { StepRunActions } from '../step/useStepRunActions';

/**
 * The gates of a view that shows no run: no step has a run to retry, to
 * change or to open. A step can still start a run.
 */
export const NO_RUN_GATES = {
  canRetry: () => false,
  canSetStatus: () => false,
  hasSubRuns: () => false,
} as const;

/** The gates of a view that shows a run: the actions say what each step can do. */
export function runStepGates(
  run: Pick<StepRunActions, 'can'>,
  perms: { canExecute: boolean }
): StepCommandGates {
  return {
    canRunStep: perms.canExecute,
    canRetry: (step) => run.can(step).retry,
    canSetStatus: (step) => run.can(step).setStatus,
    hasSubRuns: (step) => run.can(step).subRuns,
  };
}

type HostActions = Pick<
  StepRunActions,
  | 'openSubRun'
  | 'setStatus'
  | 'retryStep'
  | 'runStep'
  | 'runUpstream'
  | 'openLogs'
>;

/**
 * The step commands of a canvas host: every command of the node and of the
 * step view reaches the step actions of the workspace. The gates decide which
 * commands the editor offers and which the host accepts: a retry, a status
 * change or a sub-run that its gate refuses does nothing.
 */
export function useStepCommandHost(
  actions: HostActions,
  gates: StepCommandGates
): {
  onStepCommand(cmd: StepCommand): void;
  stepCommandGates: StepCommandGates;
} {
  const { openSubRun, setStatus, retryStep, runStep, runUpstream, openLogs } =
    actions;
  const onStepCommand = useCallback(
    (cmd: StepCommand) => {
      switch (cmd.kind) {
        case 'openSubRun':
          if (gates.hasSubRuns(cmd.step)) {
            openSubRun(cmd.step, { newTab: cmd.newTab });
          }
          return;
        case 'setStatus':
          if (gates.canSetStatus(cmd.step)) setStatus(cmd.step);
          return;
        case 'retryStep':
          if (gates.canRetry(cmd.step)) retryStep(cmd.step);
          return;
        case 'runStep':
          runStep(cmd.step);
          return;
        case 'runUpstream':
          runUpstream(cmd.step);
          return;
        case 'viewLogs':
          openLogs({ step: cmd.step, stream: cmd.stream });
          return;
      }
    },
    [gates, openSubRun, setStatus, retryStep, runStep, runUpstream, openLogs]
  );
  return { onStepCommand, stepCommandGates: gates };
}
