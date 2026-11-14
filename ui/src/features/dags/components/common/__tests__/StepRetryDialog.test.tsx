// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { Status, StatusLabel } from '@/api/v1/schema';
import { ToastProvider } from '@/components/ui/simple-toast';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { DAGContext } from '../../../contexts/DAGContext';
import { StepRetryDialog } from '../StepRetryDialog';

const configMock = vi.hoisted(() => ({ runDags: true }));
const postMock = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: postMock }),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: configMock.runDags } }),
}));

type DAGRunDetails = components['schemas']['DAGRunDetails'];

const rootRun = {
  name: 'example',
  dagRunId: 'run-1',
  status: Status.Failed,
  statusLabel: StatusLabel.failed,
  startedAt: '',
  finishedAt: '',
  autoRetryCount: 0,
} as DAGRunDetails;

const childRun = {
  ...rootRun,
  name: 'child',
  dagRunId: 'child-run',
  rootDAGRunName: 'root',
  rootDAGRunId: 'root-run',
} as DAGRunDetails;

function Harness({
  dagRun = rootRun,
  disabled,
  onRetried,
}: {
  dagRun?: DAGRunDetails;
  disabled?: boolean;
  onRetried?: () => void;
}) {
  const [open, setOpen] = React.useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open retry
      </button>
      <StepRetryDialog
        open={open}
        onOpenChange={setOpen}
        dagRun={dagRun}
        stepName="build"
        disabled={disabled}
        onRetried={onRetried}
      />
    </>
  );
}

function renderDialog(
  props: React.ComponentProps<typeof Harness> = {},
  refresh = vi.fn()
) {
  render(
    <ToastProvider>
      <RemoteNodeProvider remoteNode="node-a">
        <DAGContext.Provider
          value={{ refresh, name: 'example', fileName: 'example.yaml' }}
        >
          <Harness {...props} />
        </DAGContext.Provider>
      </RemoteNodeProvider>
    </ToastProvider>
  );
  return { refresh, user: userEvent.setup() };
}

async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Open retry' }));
  return screen.getByRole('dialog');
}

// parity:steps-retry-dialog
describe('StepRetryDialog', () => {
  beforeEach(() => {
    configMock.runDags = true;
    postMock.mockReset();
  });

  // parity:crit-retry-downstream-hint
  it('asks to re-execute the step and explains both scopes', async () => {
    const { user } = renderDialog();
    const dialog = await openDialog(user);

    expect(within(dialog).getByText('Retry this step?')).toBeVisible();
    expect(dialog).toHaveTextContent(
      'This will re-execute build. Are you sure?'
    );
    expect(within(dialog).getByText('build').tagName).toBe('B');
    expect(
      within(dialog).getByText('Downstream steps keep their current status.')
    ).toBeVisible();
    expect(
      within(dialog).getByRole('radio', { name: /This step only/ })
    ).toBeChecked();
    expect(
      within(dialog).getByRole('radio', {
        name: /This step and all downstream steps/,
      })
    ).not.toBeChecked();
  });

  // D27: the server retries with the stored definition of the run.
  it('names the run whose definition the retry uses', async () => {
    const { user } = renderDialog({ dagRun: childRun });
    const dialog = await openDialog(user);
    expect(dialog).toHaveTextContent(
      'Retries with the definition of run root-run'
    );
  });

  // The label stays Retry while busy: only the icon becomes a spinner.
  it('keeps the Retry label and shows a spinner while it retries', async () => {
    let finish: (value: object) => void = () => undefined;
    postMock.mockReturnValueOnce(new Promise((resolve) => (finish = resolve)));
    const { user } = renderDialog();
    const dialog = await openDialog(user);

    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));
    const retry = within(dialog).getByRole('button', { name: 'Retry' });
    expect(retry).toBeDisabled();
    expect(retry.querySelector('svg.animate-spin')).not.toBeNull();
    finish({});
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.queryByText('Step retry started')).toBeNull();
  });

  // parity:api-retry
  it('retries the step only by default and refreshes the DAG', async () => {
    postMock.mockResolvedValueOnce({});
    const { user, refresh } = renderDialog();
    const dialog = await openDialog(user);

    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));

    expect(postMock).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}/retry', {
      params: {
        path: { name: 'example', dagRunId: 'run-1' },
        query: { remoteNode: 'node-a' },
      },
      body: { dagRunId: 'run-1', stepName: 'build' },
    });
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
    // A success shows no notification in the middle of the screen.
    expect(screen.queryByText('Step retry started')).toBeNull();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('sends the downstream scope and the precondition bypass', async () => {
    postMock.mockResolvedValueOnce({});
    const { user } = renderDialog();
    const dialog = await openDialog(user);

    await user.click(
      within(dialog).getByRole('radio', {
        name: /This step and all downstream steps/,
      })
    );
    await user.click(
      within(dialog).getByRole('checkbox', {
        name: /Bypass step preconditions/,
      })
    );
    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));

    expect(postMock).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/retry',
      expect.objectContaining({
        body: {
          dagRunId: 'run-1',
          stepName: 'build',
          includeDownstream: true,
          bypassPreconditions: true,
        },
      })
    );
  });

  it('retries a child step through its root run with subDAGRunId', async () => {
    postMock.mockResolvedValueOnce({});
    const { user } = renderDialog({ dagRun: childRun });
    const dialog = await openDialog(user);

    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));

    expect(postMock).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}/retry', {
      params: {
        path: { name: 'root', dagRunId: 'root-run' },
        query: { remoteNode: 'node-a' },
      },
      body: {
        dagRunId: 'root-run',
        stepName: 'build',
        subDAGRunId: 'child-run',
      },
    });
  });

  it('keeps the dialog open with the error inline when the API rejects', async () => {
    postMock.mockResolvedValueOnce({
      error: { message: 'The DAG run cannot be retried' },
    });
    const { user, refresh } = renderDialog();
    const dialog = await openDialog(user);

    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));

    expect(
      await within(dialog).findByText('The DAG run cannot be retried')
    ).toBeVisible();
    expect(screen.getByRole('dialog')).toBeVisible();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('resets the options and the error each time it opens', async () => {
    postMock.mockResolvedValueOnce({ error: { message: 'Rejected' } });
    const { user } = renderDialog();
    let dialog = await openDialog(user);

    await user.click(
      within(dialog).getByRole('radio', {
        name: /This step and all downstream steps/,
      })
    );
    await user.click(
      within(dialog).getByRole('checkbox', {
        name: /Bypass step preconditions/,
      })
    );
    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));
    expect(await within(dialog).findByText('Rejected')).toBeVisible();
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    dialog = await openDialog(user);
    expect(
      within(dialog).getByRole('checkbox', {
        name: /Bypass step preconditions/,
      })
    ).not.toBeChecked();
    expect(
      within(dialog).getByRole('radio', { name: /This step only/ })
    ).toBeChecked();
    expect(within(dialog).queryByText('Rejected')).not.toBeInTheDocument();
  });

  it('calls onRetried in place of the DAG refresh', async () => {
    postMock.mockResolvedValueOnce({});
    const onRetried = vi.fn();
    const { user, refresh } = renderDialog({ onRetried });
    const dialog = await openDialog(user);

    await user.click(within(dialog).getByRole('button', { name: 'Retry' }));

    await waitFor(() => expect(onRetried).toHaveBeenCalledTimes(1));
    expect(screen.queryByText('Step retry started')).toBeNull();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('disables Retry when the host says so', async () => {
    const { user } = renderDialog({ disabled: true });
    const dialog = await openDialog(user);

    expect(
      within(dialog).getByRole('button', { name: 'Retry' })
    ).toBeDisabled();
  });

  it('sends nothing without the runDags permission', async () => {
    configMock.runDags = false;
    const { user } = renderDialog();
    const dialog = await openDialog(user);
    const retry = within(dialog).getByRole('button', { name: 'Retry' });
    expect(retry).toBeDisabled();

    await user.click(retry);

    expect(postMock).not.toHaveBeenCalled();
  });
});
