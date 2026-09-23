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

const runExecutingNotice =
  'This DAG-run is queued or running. Open tasks become editable once it is waiting.';
const postMock = vi.hoisted(() => vi.fn());
const artifactPreviewMock = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: vi.fn(),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'worker-a',
}));

vi.mock('../../artifacts/ArtifactFilePreview', () => ({
  ArtifactFilePreview: ({
    dagRunName,
    dagRunId,
    path,
    remoteNode,
  }: {
    dagRunName: string;
    dagRunId: string;
    path: string | null;
    remoteNode: string;
  }) => {
    artifactPreviewMock({ dagRunName, dagRunId, path, remoteNode });
    return (
      <div data-testid="artifact-preview">
        {`${dagRunName}:${dagRunId}:${remoteNode}:${path}`}
      </div>
    );
  },
}));

function humanTaskRun(
  form?: Record<string, unknown>,
  artifacts?: string[],
  artifactsAvailable = true
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
    artifactsAvailable,
    log: '',
    nodes: [
      {
        step: {
          id: 'review',
          name: 'Release review',
          humanTask: {
            prompt: 'Confirm the production release.',
            form,
            artifacts,
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
  it('previews a referenced artifact with the current DAG-run context', () => {
    render(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff'])}
        onChanged={vi.fn()}
      />
    );

    expect(screen.getByTestId('artifact-preview')).toHaveTextContent(
      'deploy:run-1:worker-a:changes.diff'
    );
    expect(
      screen.getByRole('button', { name: 'Complete task' })
    ).toBeEnabled();
  });

  it('switches between referenced artifacts', () => {
    render(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff', 'report.html'])}
        onChanged={vi.fn()}
      />
    );

    expect(screen.getByRole('tab', { name: 'changes.diff' })).toBeVisible();
    fireEvent.click(screen.getByRole('tab', { name: 'report.html' }));

    expect(screen.getByTestId('artifact-preview')).toHaveTextContent(
      'deploy:run-1:worker-a:report.html'
    );
    expect(screen.getByRole('tab', { name: 'report.html' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('keeps the selected artifact when refreshed run data has the same paths', () => {
    const { rerender } = render(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff', 'report.html'])}
        onChanged={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('tab', { name: 'report.html' }));
    rerender(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff', 'report.html'])}
        onChanged={vi.fn()}
      />
    );

    expect(screen.getByTestId('artifact-preview')).toHaveTextContent(
      'deploy:run-1:worker-a:report.html'
    );
  });

  it('does not pass a removed artifact to the preview during a refresh', () => {
    const { rerender } = render(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff', 'report.html'])}
        onChanged={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('tab', { name: 'report.html' }));
    artifactPreviewMock.mockClear();
    rerender(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff'])}
        onChanged={vi.fn()}
      />
    );

    expect(artifactPreviewMock).not.toHaveBeenCalledWith(
      expect.objectContaining({ path: 'report.html' })
    );
    expect(screen.getByTestId('artifact-preview')).toHaveTextContent(
      'deploy:run-1:worker-a:changes.diff'
    );
  });

  it('explains that referenced artifacts are unavailable instead of erroring', () => {
    render(
      <HumanTasksTab
        dagRun={humanTaskRun(undefined, ['changes.diff'], false)}
        onChanged={vi.fn()}
      />
    );

    expect(screen.queryByTestId('artifact-preview')).toBeNull();
    expect(
      screen.getByText(
        'Referenced artifacts are not available for this DAG run yet.'
      )
    ).toBeVisible();
  });

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

  it('blocks unsafe integers before submission', async () => {
    render(
      <HumanTasksTab
        dagRun={humanTaskRun({
          type: 'object',
          properties: {
            count: { type: 'integer', title: 'Exact count' },
          },
          required: ['count'],
          additionalProperties: false,
        })}
        onChanged={vi.fn()}
      />
    );

    fireEvent.change(screen.getByLabelText(/Exact count/), {
      target: { value: '9007199254740993' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    expect(
      await screen.findByText(/outside the safe integer range/)
    ).toBeVisible();
    expect(postMock).not.toHaveBeenCalled();
  });

  it('associates labels with the correct task when forms share field names', () => {
    const form = {
      type: 'object',
      properties: {
        comment: { type: 'string', title: 'Comment' },
      },
      additionalProperties: false,
    };
    const dagRun = humanTaskRun(form);
    const firstNode = dagRun.nodes[0];
    if (!firstNode) throw new Error('expected a human task fixture');
    dagRun.nodes.push({
      ...firstNode,
      step: {
        ...firstNode.step,
        id: 'security-review',
        name: 'Security review',
      },
    });

    render(<HumanTasksTab dagRun={dagRun} onChanged={vi.fn()} />);

    const inputs = screen.getAllByRole('textbox', { name: 'Comment' });
    expect(inputs).toHaveLength(2);
    expect(inputs[0]).not.toHaveAttribute('id', inputs[1]?.id);
    const labels = screen.getAllByText('Comment');
    expect(labels[0]).toHaveAttribute('for', inputs[0]?.id);
    expect(labels[1]).toHaveAttribute('for', inputs[1]?.id);
  });

  it('retries a pending resume without resubmitting task input', async () => {
    const onChanged = vi.fn();
    const dagRun = humanTaskRun();
    dagRun.nodes = [];
    dagRun.humanTaskResumePending = true;
    render(<HumanTasksTab dagRun={dagRun} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Retry queue' }));

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

  it('refreshes the run after a completion transport failure', async () => {
    postMock.mockRejectedValueOnce(new Error('Network unavailable'));
    const onChanged = vi.fn();
    render(<HumanTasksTab dagRun={humanTaskRun()} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    expect(await screen.findByText('Network unavailable')).toBeVisible();
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('refreshes the run after a resume transport failure', async () => {
    postMock.mockRejectedValueOnce(new Error('Network unavailable'));
    const onChanged = vi.fn();
    const dagRun = humanTaskRun();
    dagRun.nodes = [];
    dagRun.humanTaskResumePending = true;
    render(<HumanTasksTab dagRun={dagRun} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Retry queue' }));

    expect(await screen.findByText('Network unavailable')).toBeVisible();
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  // Completing another task can resume the run while this one stays open. The
  // draft stays on screen, read-only, until the run is waiting again.
  it.each([
    { name: 'queued', status: Status.Queued },
    { name: 'running', status: Status.Running },
  ])('keeps a read-only draft while the run is $name', async ({ status }) => {
    const waitingRun = humanTaskRun({
      type: 'object',
      properties: {
        count: { type: 'integer', title: 'Replica count' },
      },
      required: ['count'],
      additionalProperties: false,
    });
    const { rerender } = render(
      <HumanTasksTab dagRun={waitingRun} onChanged={vi.fn()} />
    );
    fireEvent.change(screen.getByLabelText(/Replica count/), {
      target: { value: '3' },
    });

    rerender(
      <HumanTasksTab dagRun={{ ...waitingRun, status }} onChanged={vi.fn()} />
    );
    expect(screen.getByLabelText(/Replica count/)).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Complete task' })
    ).toBeDisabled();
    expect(screen.getByText(runExecutingNotice)).toBeVisible();

    rerender(<HumanTasksTab dagRun={waitingRun} onChanged={vi.fn()} />);
    expect(screen.queryByText(runExecutingNotice)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        expect.objectContaining({ body: { count: 3 } })
      )
    );
  });

  it('disables completing a task without a form while the run is running', () => {
    render(
      <HumanTasksTab
        dagRun={{ ...humanTaskRun(), status: Status.Running }}
        onChanged={vi.fn()}
      />
    );

    expect(
      screen.getByRole('button', { name: 'Complete task' })
    ).toBeDisabled();
  });

  // A completion that raced another task's resume is rejected; the stale
  // error must not linger once the task is editable again.
  it('clears a rejected completion once the run is waiting again', async () => {
    postMock.mockResolvedValueOnce({
      error: { message: 'DAG-run deploy is not waiting (status: queued)' },
    });
    const waitingRun = humanTaskRun();
    const { rerender } = render(
      <HumanTasksTab dagRun={waitingRun} onChanged={vi.fn()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));
    expect(await screen.findByText(/is not waiting/)).toBeVisible();

    rerender(
      <HumanTasksTab
        dagRun={{ ...waitingRun, status: Status.Running }}
        onChanged={vi.fn()}
      />
    );
    rerender(<HumanTasksTab dagRun={waitingRun} onChanged={vi.fn()} />);

    expect(screen.queryByText(/is not waiting/)).not.toBeInTheDocument();
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
