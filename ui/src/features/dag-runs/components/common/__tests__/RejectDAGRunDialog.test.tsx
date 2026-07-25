// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RejectDAGRunDialog } from '../RejectDAGRunDialog';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  showError: vi.fn(),
  showToast: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: mocks.post }),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'worker-a',
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: mocks.showToast }),
}));

function renderDialog() {
  const onOpenChange = vi.fn();
  const onSettled = vi.fn();
  render(
    <RejectDAGRunDialog
      open
      onOpenChange={onOpenChange}
      dagName="deploy"
      dagRunId="run-1"
      stepName="review"
      onSettled={onSettled}
    />
  );
  return { onOpenChange, onSettled };
}

describe('RejectDAGRunDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('rejects the DAG run through one waiting approval', async () => {
    mocks.post.mockResolvedValueOnce({ error: undefined });
    const { onOpenChange, onSettled } = renderDialog();

    fireEvent.change(screen.getByLabelText('Rejection reason (optional)'), {
      target: { value: 'Needs revision' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));

    await waitFor(() => expect(onSettled).toHaveBeenCalledTimes(1));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(mocks.post).toHaveBeenCalledOnce();
    expect(mocks.post).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/reject',
      {
        params: {
          path: { name: 'deploy', dagRunId: 'run-1', stepName: 'review' },
          query: { remoteNode: 'worker-a' },
        },
        body: { reason: 'Needs revision' },
      }
    );
    expect(mocks.showToast).toHaveBeenCalledWith('DAG run rejected');
    expect(mocks.showError).not.toHaveBeenCalled();
  });

  it('reports transport failures and refreshes the run', async () => {
    mocks.post.mockRejectedValueOnce(new Error('Network unavailable'));
    const { onSettled } = renderDialog();

    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));

    await waitFor(() => expect(onSettled).toHaveBeenCalledTimes(1));
    expect(mocks.showError).toHaveBeenCalledWith(
      'Failed to reject DAG run',
      'Network unavailable'
    );
    expect(mocks.showToast).not.toHaveBeenCalled();
  });
});
