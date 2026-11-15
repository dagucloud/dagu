// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';

type AutoRetryCancelableRun = {
  status?: Status | null;
  autoRetryCount?: number | null;
  autoRetryLimit?: number | null;
};

export type DAGRunTerminateAction = 'none' | 'stop' | 'cancel';
export type DAGRunTerminateActionDetails = {
  action: DAGRunTerminateAction;
  buttonText: 'Stop' | 'Cancel';
  tooltipText: string;
  confirmText: string;
  successText: string;
  errorTitle: string;
  errorDescription: string;
  // Stop-all reaches running instances only, so a waiting run is stopped alone.
  offersStopAll: boolean;
};

type DAGRunTerminateCopy = {
  nonRootTooltipText: string;
  stopTooltipText: string;
  cancelTooltipText: string;
  stopConfirmText: string;
  waitingStopConfirmText: string;
  cancelConfirmText: string;
  stopErrorDescription: string;
  cancelErrorDescription: string;
};

type DAGRunTerminateActionOptions = {
  isRootLevel?: boolean;
  copy?: Partial<DAGRunTerminateCopy>;
};

const defaultTerminateCopy: DAGRunTerminateCopy = {
  nonRootTooltipText: 'Stop is only available on the top-level run',
  stopTooltipText: 'Stop this run',
  cancelTooltipText: 'Cancel auto-retry for this run',
  stopConfirmText: 'Running steps are canceled.',
  waitingStopConfirmText:
    'Waiting steps are canceled and the remaining steps do not run.',
  cancelConfirmText: 'The failed run will not be retried automatically.',
  stopErrorDescription:
    'The DAG run may have already completed or the worker is unavailable.',
  cancelErrorDescription:
    'The DAG run may have already changed state. Refresh and try again.',
};

export function isFailedAutoRetryPendingRun(
  run?: AutoRetryCancelableRun | null
): boolean {
  const limit = run?.autoRetryLimit ?? 0;
  const count = Math.max(run?.autoRetryCount ?? 0, 0);

  return run?.status === Status.Failed && limit > 0 && count < limit;
}

export function getDAGRunTerminateAction(
  run?: AutoRetryCancelableRun | null
): DAGRunTerminateAction {
  if (run?.status === Status.Running || run?.status === Status.Waiting) {
    return 'stop';
  }
  if (isFailedAutoRetryPendingRun(run)) {
    return 'cancel';
  }
  return 'none';
}

export function getDAGRunTerminateActionDetails(
  run?: AutoRetryCancelableRun | null,
  options: DAGRunTerminateActionOptions = {}
): DAGRunTerminateActionDetails {
  const copy = {
    ...defaultTerminateCopy,
    ...options.copy,
  };

  if (options.isRootLevel === false) {
    return {
      action: 'none',
      buttonText: 'Stop',
      tooltipText: copy.nonRootTooltipText,
      confirmText: copy.stopConfirmText,
      successText: 'Stop signal sent',
      errorTitle: 'Failed to stop DAG run',
      errorDescription: copy.stopErrorDescription,
      offersStopAll: false,
    };
  }

  const action = getDAGRunTerminateAction(run);
  if (action === 'cancel') {
    return {
      action,
      buttonText: 'Cancel',
      tooltipText: copy.cancelTooltipText,
      confirmText: copy.cancelConfirmText,
      successText: 'Stop signal sent',
      errorTitle: 'Failed to cancel DAG run',
      errorDescription: copy.cancelErrorDescription,
      offersStopAll: false,
    };
  }

  // A waiting run holds no process, so the stop ends it before the reply.
  const waiting = run?.status === Status.Waiting;
  return {
    action,
    buttonText: 'Stop',
    tooltipText: copy.stopTooltipText,
    confirmText: waiting ? copy.waitingStopConfirmText : copy.stopConfirmText,
    successText: waiting ? 'Run stopped' : 'Stop signal sent',
    errorTitle: 'Failed to stop DAG run',
    errorDescription: copy.stopErrorDescription,
    offersStopAll: action === 'stop' && !waiting,
  };
}
