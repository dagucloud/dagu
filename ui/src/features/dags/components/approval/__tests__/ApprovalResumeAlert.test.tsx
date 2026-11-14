// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { components } from '@/api/v1/schema';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { ApprovalResumeAlert } from '../ApprovalResumeAlert';

vi.mock('@/hooks/api', () => ({ useClient: vi.fn() }));
vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: vi.fn(),
}));
vi.mock('@/contexts/ConfigContext', () => ({ useConfig: vi.fn() }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'worker-a',
}));

const post = vi.fn();
const dagRun = {
  name: 'release',
  dagRunId: 'run-1',
  workspace: 'production',
} as components['schemas']['DAGRunDetails'];

beforeEach(() => {
  post.mockReset();
  vi.mocked(useClient).mockReturnValue({ POST: post } as unknown as ReturnType<
    typeof useClient
  >);
  vi.mocked(useCanExecuteForWorkspace).mockReturnValue(true);
  vi.mocked(useConfig).mockReturnValue({
    permissions: { runDags: true },
  } as ReturnType<typeof useConfig>);
});

describe('ApprovalResumeAlert', () => {
  it('retries without approval inputs on the selected remote node', async () => {
    post.mockResolvedValue({ data: { resumed: true } });
    render(<ApprovalResumeAlert dagRun={dagRun} />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry resume' }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}/resume', {
        params: {
          path: { name: 'release', dagRunId: 'run-1' },
          query: { remoteNode: 'worker-a' },
        },
      })
    );
    expect(useCanExecuteForWorkspace).toHaveBeenCalledWith('production');
  });

  it('keeps failed recovery available after remount', async () => {
    post.mockResolvedValue({
      error: { message: 'Resume temporarily unavailable' },
    });
    const { unmount } = render(<ApprovalResumeAlert dagRun={dagRun} />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry resume' }));
    expect(
      await screen.findByText('Resume temporarily unavailable')
    ).toBeVisible();
    expect(screen.getByRole('button', { name: 'Retry resume' })).toBeEnabled();
    unmount();
    render(<ApprovalResumeAlert dagRun={dagRun} />);
    expect(screen.getByText('Approval saved; resume failed.')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Retry resume' })).toBeEnabled();
  });

  it.each(['workspace', 'server'])(
    'respects %s execution permission',
    (permission) => {
      if (permission === 'workspace')
        vi.mocked(useCanExecuteForWorkspace).mockReturnValue(false);
      else
        vi.mocked(useConfig).mockReturnValue({
          permissions: { runDags: false },
        } as ReturnType<typeof useConfig>);
      render(<ApprovalResumeAlert dagRun={dagRun} />);
      expect(
        screen.getByRole('button', { name: 'Retry resume' })
      ).toBeDisabled();
    }
  );

  it('prevents duplicate submissions while the request is pending', async () => {
    let finish!: (value: object) => void;
    post.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      })
    );
    render(<ApprovalResumeAlert dagRun={dagRun} />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry resume' }));
    expect(screen.getByRole('button', { name: 'Resuming...' })).toBeDisabled();
    finish({ data: { resumed: true } });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Retry resume' })).toBeEnabled()
    );
    expect(post).toHaveBeenCalledTimes(1);
  });
});
