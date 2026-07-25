// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import DAGRunActions from '../DAGRunActions';
import {
  getDAGRunTerminateAction,
  getDAGRunTerminateActionDetails,
  isFailedAutoRetryPendingRun,
} from '../terminateAction';

vi.mock('../../../../../contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: true,
    },
  }),
}));

vi.mock('../../../../../hooks/api', () => ({
  useClient: () => ({
    POST: vi.fn(),
    GET: vi.fn(),
  }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: vi.fn(),
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({
    showToast: vi.fn(),
  }),
}));

describe('DAGRunActions', () => {
  it('treats running runs as stoppable', () => {
    expect(
      getDAGRunTerminateAction({
        status: Status.Running,
        autoRetryCount: 0,
        autoRetryLimit: 3,
      })
    ).toBe('stop');
    expect(
      getDAGRunTerminateActionDetails({
        status: Status.Running,
        autoRetryCount: 0,
        autoRetryLimit: 3,
      })
    ).toMatchObject({
      action: 'stop',
      buttonText: 'Stop',
      tooltipText: 'Stop DAGRun execution',
      errorTitle: 'Failed to stop DAG run',
    });
  });

  it('treats failed runs with remaining auto retries as cancelable', () => {
    expect(
      isFailedAutoRetryPendingRun({
        status: Status.Failed,
        autoRetryCount: 1,
        autoRetryLimit: 3,
      })
    ).toBe(true);
    expect(
      getDAGRunTerminateAction({
        status: Status.Failed,
        autoRetryCount: 1,
        autoRetryLimit: 3,
      })
    ).toBe('cancel');
    expect(
      getDAGRunTerminateActionDetails({
        status: Status.Failed,
        autoRetryCount: 1,
        autoRetryLimit: 3,
      })
    ).toMatchObject({
      action: 'cancel',
      buttonText: 'Cancel',
      tooltipText: 'Cancel auto-retry for this failed DAGRun',
      errorTitle: 'Failed to cancel DAG run',
    });
  });

  it('does not allow cancel once auto retries are exhausted', () => {
    expect(
      isFailedAutoRetryPendingRun({
        status: Status.Failed,
        autoRetryCount: 3,
        autoRetryLimit: 3,
      })
    ).toBe(false);
    expect(
      getDAGRunTerminateAction({
        status: Status.Failed,
        autoRetryCount: 3,
        autoRetryLimit: 3,
      })
    ).toBe('none');
  });

  it('does not allow cancel when no auto retry policy is configured', () => {
    expect(
      getDAGRunTerminateAction({
        status: Status.Failed,
        autoRetryCount: 0,
        autoRetryLimit: 0,
      })
    ).toBe('none');
  });

  it('shows cancel for failed runs with pending auto retries', () => {
    render(
      <DAGRunActions
        dagRun={{
          name: 'retry-dag',
          dagRunId: 'run-1',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          artifactsAvailable: false,
          autoRetryCount: 1,
          autoRetryLimit: 3,
          startedAt: '',
          finishedAt: '',
        }}
        name="retry-dag"
        displayMode="full"
      />
    );

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
  });

  it('disables retry for waiting DAG executions', () => {
    render(
      <DAGRunActions
        dagRun={{
          name: 'waiting-dag',
          dagRunId: 'run-1',
          status: Status.Waiting,
          statusLabel: StatusLabel.waiting,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        name="waiting-dag"
        displayMode="full"
      />
    );

    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
  });

  it('keeps stop disabled with a root-level tooltip for nested runs', () => {
    render(
      <DAGRunActions
        dagRun={{
          name: 'nested-dag',
          dagRunId: 'run-1',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          artifactsAvailable: false,
          autoRetryCount: 1,
          autoRetryLimit: 3,
          startedAt: '',
          finishedAt: '',
        }}
        name="nested-dag"
        displayMode="full"
        isRootLevel={false}
      />
    );

    expect(screen.getByRole('button', { name: 'Stop' })).toBeDisabled();
  });
});
