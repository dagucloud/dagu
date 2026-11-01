// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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

const h = vi.hoisted(() => ({
  client: { POST: vi.fn(), GET: vi.fn() },
  showError: vi.fn(),
  showToast: vi.fn(),
}));

vi.mock('../../../../../hooks/api', () => ({
  useClient: () => h.client,
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: h.showError,
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({
    showToast: h.showToast,
  }),
}));

beforeEach(() => {
  vi.clearAllMocks();
  h.client.POST.mockResolvedValue({ data: {} });
  h.client.GET.mockResolvedValue({ data: {} });
});

const runningRun = {
  name: 'etl',
  dagRunId: 'run-7',
  status: Status.Running,
  statusLabel: StatusLabel.running,
  artifactsAvailable: false,
  autoRetryCount: 0,
  startedAt: '',
  finishedAt: '',
};

function openStopDialog() {
  fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
  return screen.getByRole('dialog');
}

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
      tooltipText: 'Stop this run',
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
      tooltipText: 'Cancel auto-retry for this run',
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

  // The run can still show as running after the request, and a success
  // toast shows nothing, so the stop is confirmed with a note.
  it('confirms a sent stop with a note', async () => {
    render(<DAGRunActions dagRun={runningRun} name="etl" displayMode="full" />);

    const dialog = openStopDialog();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));

    await waitFor(() =>
      expect(h.showToast).toHaveBeenCalledWith('Stop signal sent', {
        variant: 'info',
      })
    );
  });

  it('hides actions that do not apply when asked', () => {
    const finishedRun = {
      name: 'nightly',
      dagRunId: 'run-1',
      status: Status.Success,
      statusLabel: StatusLabel.succeeded,
      artifactsAvailable: false,
      autoRetryCount: 0,
      startedAt: '',
      finishedAt: '',
    };
    const { rerender } = render(
      <DAGRunActions
        dagRun={finishedRun}
        name="nightly"
        displayMode="full"
        hideUnavailable
      />
    );

    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Dequeue' })).toBeNull();

    rerender(
      <DAGRunActions
        dagRun={{
          ...finishedRun,
          status: Status.Queued,
          statusLabel: StatusLabel.queued,
        }}
        name="nightly"
        displayMode="full"
        hideUnavailable
      />
    );

    expect(screen.getByRole('button', { name: 'Dequeue' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });

  it('keeps a disabled Retry on sub-runs when hiding unavailable actions', () => {
    render(
      <DAGRunActions
        dagRun={{
          name: 'child',
          dagRunId: 'run-2',
          status: Status.Success,
          statusLabel: StatusLabel.succeeded,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        name="child"
        displayMode="full"
        hideUnavailable
        isRootLevel={false}
      />
    );

    const retry = screen.getByRole('button', { name: 'Retry' });
    expect(retry).toBeDisabled();
    // The disabled button cannot take focus, so its wrapper does.
    expect(retry.parentElement).toHaveAttribute('tabindex', '0');
  });

  // parity:actions-stop-all
  describe('stopAll', () => {
    it('changes nothing when absent', async () => {
      const withOption = render(
        <DAGRunActions
          dagRun={runningRun}
          name="etl"
          displayMode="full"
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );
      const optionHtml = withOption.container.innerHTML;
      withOption.unmount();
      const { container } = render(
        <DAGRunActions dagRun={runningRun} name="etl" displayMode="full" />
      );
      expect(container.innerHTML).toBe(optionHtml);

      const dialog = openStopDialog();
      expect(
        within(dialog).getByRole('heading', { name: 'Stop this run?' })
      ).toBeInTheDocument();
      expect(within(dialog).queryByRole('checkbox')).toBeNull();
      expect(
        within(dialog).queryByText('Stop all running instances')
      ).toBeNull();

      fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
      await waitFor(() => expect(h.client.POST).toHaveBeenCalledTimes(1));
      expect(h.client.POST).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/stop',
        {
          params: {
            query: { remoteNode: 'local' },
            path: { name: 'etl', dagRunId: 'run-7' },
          },
        }
      );
    });

    it('stops every running instance when checked', async () => {
      const refresh = vi.fn();
      render(
        <DAGRunActions
          dagRun={runningRun}
          name="etl"
          displayMode="full"
          refresh={refresh}
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );

      const dialog = openStopDialog();
      const option = within(dialog).getByRole('checkbox', {
        name: 'Stop all running instances',
      });
      expect(option).not.toBeChecked();
      fireEvent.click(option);
      expect(
        within(dialog).getByRole('heading', {
          name: 'Stop all running instances?',
        })
      ).toBeInTheDocument();
      expect(
        within(dialog).getByText(
          'Running steps in every instance of this workflow are canceled.'
        )
      ).toBeInTheDocument();

      fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
      await waitFor(() =>
        expect(h.showToast).toHaveBeenCalledWith(
          'Stop signal sent to all running instances',
          { variant: 'info' }
        )
      );
      expect(h.client.POST).toHaveBeenCalledTimes(1);
      expect(h.client.POST).toHaveBeenCalledWith('/dags/{fileName}/stop-all', {
        params: {
          path: { fileName: 'etl.yaml' },
          query: { remoteNode: 'local' },
        },
      });
      expect(refresh).toHaveBeenCalledTimes(1);
    });

    it('stops only the run when left unchecked', async () => {
      render(
        <DAGRunActions
          dagRun={runningRun}
          name="etl"
          displayMode="full"
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );

      const dialog = openStopDialog();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
      await waitFor(() => expect(h.client.POST).toHaveBeenCalledTimes(1));
      expect(h.client.POST.mock.calls[0]?.[0]).toBe(
        '/dag-runs/{name}/{dagRunId}/stop'
      );
    });

    it('starts unchecked each time the confirm opens', () => {
      render(
        <DAGRunActions
          dagRun={runningRun}
          name="etl"
          displayMode="full"
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );

      let dialog = openStopDialog();
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Stop all running instances',
        })
      );
      fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

      dialog = openStopDialog();
      expect(
        within(dialog).getByRole('checkbox', {
          name: 'Stop all running instances',
        })
      ).not.toBeChecked();
    });

    it('shows the stop-all error in the error modal', async () => {
      h.client.POST.mockResolvedValue({ error: { message: 'busy' } });
      render(
        <DAGRunActions
          dagRun={runningRun}
          name="etl"
          displayMode="full"
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );

      const dialog = openStopDialog();
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Stop all running instances',
        })
      );
      fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
      await waitFor(() =>
        expect(h.showError).toHaveBeenCalledWith(
          'busy',
          'Some instances may have already completed or the worker is unavailable.'
        )
      );
      expect(h.showToast).not.toHaveBeenCalled();
    });

    // The request succeeds with a list of the runs that did not stop.
    it('shows the runs that did not stop and refreshes', async () => {
      h.client.POST.mockResolvedValue({
        data: { errors: ['failed to stop run "run-8": gone'] },
      });
      const refresh = vi.fn();
      render(
        <DAGRunActions
          dagRun={runningRun}
          name="etl"
          displayMode="full"
          refresh={refresh}
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );

      const dialog = openStopDialog();
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Stop all running instances',
        })
      );
      fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
      await waitFor(() =>
        expect(h.showError).toHaveBeenCalledWith(
          'Failed to stop all DAG instances',
          'Some instances may have already completed or the worker is unavailable.',
          undefined,
          ['failed to stop run "run-8": gone']
        )
      );
      expect(h.showToast).not.toHaveBeenCalled();
      expect(refresh).toHaveBeenCalledTimes(1);
    });

    it('is not offered on a cancel of auto-retry', () => {
      render(
        <DAGRunActions
          dagRun={{
            ...runningRun,
            status: Status.Failed,
            statusLabel: StatusLabel.failed,
            autoRetryCount: 1,
            autoRetryLimit: 3,
          }}
          name="etl"
          displayMode="full"
          stopAll={{ fileName: 'etl.yaml' }}
        />
      );

      fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
      const dialog = screen.getByRole('dialog');
      expect(
        within(dialog).getByRole('heading', { name: 'Cancel auto-retry?' })
      ).toBeInTheDocument();
      expect(within(dialog).queryByRole('checkbox')).toBeNull();
    });
  });

  // parity:run-action-stop-cancel
  it('cancels a pending auto-retry through the stop endpoint', async () => {
    render(
      <DAGRunActions
        dagRun={{
          ...runningRun,
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          autoRetryCount: 1,
          autoRetryLimit: 3,
        }}
        name="etl"
        displayMode="full"
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    const dialog = screen.getByRole('dialog');
    // The footer has the dismiss button first, then the submit button, and
    // both read 'Cancel' here.
    const [, submit] = within(dialog).getAllByRole('button', {
      name: 'Cancel',
    });
    fireEvent.click(submit!);
    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/stop',
        {
          params: {
            query: { remoteNode: 'local' },
            path: { name: 'etl', dagRunId: 'run-7' },
          },
        }
      )
    );
  });

  // parity:run-action-retry
  // parity:run-action-reschedule
  it('retries the run, or reschedules it as a new run', async () => {
    const finished = {
      ...runningRun,
      status: Status.Failed,
      statusLabel: StatusLabel.failed,
    };
    render(<DAGRunActions dagRun={finished} name="etl" displayMode="full" />);

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    let dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));
    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/retry',
        {
          params: {
            path: { name: 'etl', dagRunId: 'run-7' },
            query: { remoteNode: 'local' },
          },
          body: { dagRunId: 'run-7' },
        }
      )
    );

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    dialog = screen.getByRole('dialog');
    fireEvent.click(
      within(dialog).getByRole('checkbox', { name: 'Reschedule as a new run' })
    );
    fireEvent.change(within(dialog).getByLabelText('New run ID (optional)'), {
      target: { value: 'run-8' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reschedule' }));
    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/reschedule',
        {
          params: {
            path: { name: 'etl', dagRunId: 'run-7' },
            query: { remoteNode: 'local' },
          },
          body: { dagRunId: 'run-8', useCurrentDagFile: false },
        }
      )
    );
  });

  // parity:runpage-dequeue
  // parity:run-action-dequeue
  it('dequeues a queued run', async () => {
    const refresh = vi.fn();
    render(
      <DAGRunActions
        dagRun={{
          ...runningRun,
          status: Status.Queued,
          statusLabel: StatusLabel.queued,
        }}
        name="etl"
        displayMode="full"
        refresh={refresh}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Dequeue' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Dequeue' }));
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
    expect(h.client.GET).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/dequeue',
      {
        params: {
          path: { name: 'etl', dagRunId: 'run-7' },
          query: { remoteNode: 'local' },
        },
      }
    );
    expect(h.showToast).toHaveBeenCalledWith('DAG run dequeued');
  });
});
