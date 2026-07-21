// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useClient } from '@/hooks/api';
import { HumanTasksTab } from '../HumanTasksTab';

const postMock = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: vi.fn(),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'worker-a',
}));

function humanTaskRun(
  form?: Record<string, unknown>
): components['schemas']['DAGRunDetails'] {
  return {
    name: 'deploy',
    dagRunId: 'run-1',
    rootDAGRunName: '',
    rootDAGRunId: '',
    workspace: 'production',
    status: Status.Waiting,
    statusLabel: StatusLabel.waiting,
    autoRetryCount: 0,
    startedAt: '',
    finishedAt: '',
    artifactsAvailable: false,
    log: '',
    nodes: [
      {
        step: {
          id: 'review',
          name: 'Release review',
          humanTask: {
            prompt: 'Confirm the production release.',
            form,
          },
        },
        stdout: '',
        stderr: '',
        startedAt: '',
        finishedAt: '',
        status: NodeStatus.Waiting,
        statusLabel: NodeStatusLabel.waiting,
        retryCount: 0,
        doneCount: 1,
      },
    ],
  };
}

beforeEach(() => {
  vi.mocked(useCanExecuteForWorkspace).mockReturnValue(true);
  vi.mocked(useClient).mockReturnValue({
    POST: postMock.mockResolvedValue({ error: undefined }),
  } as unknown as ReturnType<typeof useClient>);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('HumanTasksTab', () => {
  it('completes a task without a form using an empty object', async () => {
    const onChanged = vi.fn();
    render(<HumanTasksTab dagRun={humanTaskRun()} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        {
          params: {
            path: {
              name: 'deploy',
              dagRunId: 'run-1',
              stepId: 'review',
            },
            query: { remoteNode: 'worker-a' },
          },
          body: {},
        }
      )
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('treats a null form as a form-less task', async () => {
    const onChanged = vi.fn();
    const dagRun = humanTaskRun();
    const humanTask = dagRun.nodes[0]?.step.humanTask;
    if (!humanTask) throw new Error('expected a human task fixture');
    humanTask.form = null as unknown as Record<string, unknown>;
    render(<HumanTasksTab dagRun={dagRun} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        expect.objectContaining({ body: {} })
      )
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('submits typed form data directly to the completion endpoint', async () => {
    const onChanged = vi.fn();
    render(
      <HumanTasksTab
        dagRun={humanTaskRun({
          type: 'object',
          properties: {
            count: { type: 'integer', title: 'Replica count' },
          },
          required: ['count'],
          additionalProperties: false,
        })}
        onChanged={onChanged}
      />
    );

    fireEvent.change(screen.getByLabelText(/Replica count/), {
      target: { value: '3' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        expect.objectContaining({ body: { count: 3 } })
      )
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('retries a pending resume without resubmitting task input', async () => {
    const onChanged = vi.fn();
    const dagRun = humanTaskRun();
    dagRun.nodes = [];
    dagRun.humanTaskResumePending = true;
    render(<HumanTasksTab dagRun={dagRun} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Retry resume' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/resume',
        {
          params: {
            path: { name: 'deploy', dagRunId: 'run-1' },
            query: { remoteNode: 'worker-a' },
          },
        }
      )
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('disables mutations without workspace execute permission', () => {
    vi.mocked(useCanExecuteForWorkspace).mockReturnValue(false);
    render(<HumanTasksTab dagRun={humanTaskRun()} onChanged={vi.fn()} />);

    expect(
      screen.getByRole('button', { name: 'Complete task' })
    ).toBeDisabled();
    expect(
      screen.getByText('Execute permission is required to complete this task.')
    ).toBeVisible();
  });
});
