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
  errorTitle: string;
  errorDescription: string;
};

type DAGRunTerminateCopy = {
  nonRootTooltipText: string;
  stopTooltipText: string;
  cancelTooltipText: string;
  stopConfirmText: string;
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
  if (run?.status === Status.Running) {
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
      errorTitle: 'Failed to stop DAG run',
      errorDescription: copy.stopErrorDescription,
    };
  }

  const action = getDAGRunTerminateAction(run);
  if (action === 'cancel') {
    return {
      action,
      buttonText: 'Cancel',
      tooltipText: copy.cancelTooltipText,
      confirmText: copy.cancelConfirmText,
      errorTitle: 'Failed to cancel DAG run',
      errorDescription: copy.cancelErrorDescription,
    };
  }

  return {
    action,
    buttonText: 'Stop',
    tooltipText: copy.stopTooltipText,
    confirmText: copy.stopConfirmText,
    errorTitle: 'Failed to stop DAG run',
    errorDescription: copy.stopErrorDescription,
  };
}
