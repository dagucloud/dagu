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

const feedbackForm = {
  type: 'object',
  properties: {
    feedback: { type: 'string', title: 'Feedback' },
  },
  required: ['feedback'],
  additionalProperties: false,
};

// A null form builds a push-back that accepts no feedback.
function pushBackRun(
  form: Record<string, unknown> | null = feedbackForm,
  iteration?: number
): components['schemas']['DAGRunDetails'] {
  const dagRun = humanTaskRun();
  const node = dagRun.nodes[0];
  if (!node?.step.humanTask) throw new Error('expected a human task fixture');
  node.step.humanTask.pushBack = {
    rewindTo: 'implement',
    form: form ?? undefined,
  };
  node.approvalIteration = iteration;
  return dagRun;
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
    expect(screen.getByRole('button', { name: 'Complete task' })).toBeEnabled();
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

  // The dock pads its tabs: the host drops the frame of the card.
  it('drops the frame of the card for a padded host', () => {
    const { container } = render(
      <HumanTasksTab dagRun={humanTaskRun()} onChanged={vi.fn()} bare />
    );

    expect(screen.getByRole('button', { name: 'Complete task' })).toBeVisible();
    expect(container.querySelector('.bg-surface')).toBeNull();
    expect(container.querySelector('.rounded-lg')).toBeNull();
  });

  it('keeps the frame of the card by default', () => {
    const { container } = render(
      <HumanTasksTab dagRun={humanTaskRun()} onChanged={vi.fn()} />
    );

    expect(container.querySelector('.bg-surface.rounded-lg')).not.toBeNull();
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

describe('HumanTasksTab push-back', () => {
  it('offers push-back only when the task declares it', () => {
    render(<HumanTasksTab dagRun={humanTaskRun()} onChanged={vi.fn()} />);

    expect(
      screen.queryByRole('button', { name: 'Request changes' })
    ).not.toBeInTheDocument();
  });

  it('submits feedback with the reviewed iteration', async () => {
    const onChanged = vi.fn();
    render(<HumanTasksTab dagRun={pushBackRun()} onChanged={onChanged} />);

    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));
    expect(
      screen.getByText(
        'implement and the steps that depend on it run again with your feedback. This task reopens afterward.'
      )
    ).toBeVisible();
    expect(
      screen.queryByRole('button', { name: 'Complete task' })
    ).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Feedback/), {
      target: { value: 'Add tests' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/push-back',
        {
          params: {
            path: { name: 'deploy', dagRunId: 'run-1', stepId: 'review' },
            query: { remoteNode: 'worker-a', expectedIteration: 0 },
          },
          body: { feedback: 'Add tests' },
        }
      )
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('requires the feedback form before posting', async () => {
    render(<HumanTasksTab dagRun={pushBackRun()} onChanged={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));
    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));

    expect(
      await screen.findByText(
        'Fix the highlighted form errors before requesting changes.'
      )
    ).toBeVisible();
    expect(postMock).not.toHaveBeenCalled();
  });

  it('pushes back without a feedback form using an empty object', async () => {
    render(<HumanTasksTab dagRun={pushBackRun(null, 2)} onChanged={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));
    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/push-back',
        expect.objectContaining({
          params: expect.objectContaining({
            query: { remoteNode: 'worker-a', expectedIteration: 2 },
          }),
          body: {},
        })
      )
    );
  });

  it('returns to completion without posting when cancelled', () => {
    render(<HumanTasksTab dagRun={pushBackRun()} onChanged={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(screen.getByRole('button', { name: 'Complete task' })).toBeEnabled();
    expect(screen.queryByLabelText(/Feedback/)).not.toBeInTheDocument();
    expect(postMock).not.toHaveBeenCalled();
  });

  it('shows a rejected push-back and keeps the feedback', async () => {
    postMock.mockResolvedValueOnce({
      error: {
        message: 'human task step "review" is at push-back iteration 1, not 0',
      },
    });
    render(<HumanTasksTab dagRun={pushBackRun()} onChanged={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));
    fireEvent.change(screen.getByLabelText(/Feedback/), {
      target: { value: 'Add tests' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));

    expect(
      await screen.findByText(/is at push-back iteration 1, not 0/)
    ).toBeVisible();
    expect(screen.getByLabelText(/Feedback/)).toHaveValue('Add tests');
  });

  it('shows the iteration and previous push-backs of a reopened task', () => {
    const dagRun = pushBackRun(feedbackForm, 1);
    const node = dagRun.nodes[0];
    if (!node) throw new Error('expected a human task fixture');
    node.pushBackHistory = [
      {
        iteration: 1,
        by: 'alice',
        at: '2026-07-21T01:02:03Z',
        inputs: { feedback: 'Add tests' },
      },
    ];
    render(<HumanTasksTab dagRun={dagRun} onChanged={vi.fn()} />);

    expect(screen.getByText('Previous Push-backs')).toBeVisible();
    expect(screen.getByText('feedback="Add tests"')).toBeVisible();
    expect(screen.getAllByText(/Iteration/).length).toBeGreaterThan(0);
  });

  // A reopened task is a new review, so the previous mode and draft must not
  // carry over from the earlier iteration.
  it('starts a fresh review when the task reopens at a new iteration', () => {
    const { rerender } = render(
      <HumanTasksTab dagRun={pushBackRun()} onChanged={vi.fn()} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Request changes' }));
    fireEvent.change(screen.getByLabelText(/Feedback/), {
      target: { value: 'Add tests' },
    });

    rerender(
      <HumanTasksTab
        dagRun={pushBackRun(feedbackForm, 1)}
        onChanged={vi.fn()}
      />
    );

    expect(screen.getByRole('button', { name: 'Complete task' })).toBeEnabled();
    expect(screen.queryByLabelText(/Feedback/)).not.toBeInTheDocument();
  });

  it.each([
    {
      name: 'without execute permission',
      canExecute: false,
      status: Status.Waiting,
    },
    {
      name: 'while the run is running',
      canExecute: true,
      status: Status.Running,
    },
  ])('disables push-back $name', ({ canExecute, status }) => {
    vi.mocked(useCanExecuteForWorkspace).mockReturnValue(canExecute);
    render(
      <HumanTasksTab
        dagRun={{ ...pushBackRun(), status }}
        onChanged={vi.fn()}
      />
    );

    expect(
      screen.getByRole('button', { name: 'Request changes' })
    ).toBeDisabled();
  });
});

// parity:test-human-tasks-approval
function twoTaskRun(): components['schemas']['DAGRunDetails'] {
  const dagRun = pushBackRun(feedbackForm, 1);
  const first = dagRun.nodes[0];
  if (!first?.step.humanTask) throw new Error('expected a human task fixture');
  const second = {
    ...first,
    step: {
      id: 'signoff',
      name: 'Security sign-off',
      humanTask: { prompt: 'Confirm the security review.' },
    },
    approvalIteration: undefined,
  };
  return {
    ...dagRun,
    humanTaskResumePending: true,
    nodes: [first, second],
  };
}

describe('HumanTasksTab output without a step filter', () => {
  it('renders the resume alert and every waiting task as before', () => {
    const { container } = render(
      <HumanTasksTab dagRun={twoTaskRun()} onChanged={vi.fn()} />
    );
    expect(container.innerHTML).toBe(BASE_ALL);
  });

  it('renders the running notice as before', () => {
    const { container } = render(
      <HumanTasksTab
        dagRun={{ ...humanTaskRun(), status: Status.Running }}
        onChanged={vi.fn()}
      />
    );
    expect(container.innerHTML).toBe(BASE_RUNNING);
  });

  it('renders the empty state as before', () => {
    const { container } = render(
      <HumanTasksTab
        dagRun={{ ...humanTaskRun(), nodes: [] }}
        onChanged={vi.fn()}
      />
    );
    expect(container.innerHTML).toBe(BASE_EMPTY);
  });
});

describe('HumanTasksTab step filter', () => {
  it('shows only the task of the step, without the resume alert', () => {
    render(
      <HumanTasksTab
        dagRun={twoTaskRun()}
        onChanged={vi.fn()}
        stepName="Security sign-off"
      />
    );

    expect(screen.getByText('Confirm the security review.')).toBeVisible();
    expect(screen.queryByText('Confirm the production release.')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Retry queue' })).toBeNull();
    expect(
      screen.getAllByRole('button', { name: 'Complete task' })
    ).toHaveLength(1);
  });

  it('completes the task of the step', async () => {
    const onChanged = vi.fn();
    render(
      <HumanTasksTab
        dagRun={twoTaskRun()}
        onChanged={onChanged}
        stepName="Security sign-off"
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Complete task' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        {
          params: {
            path: { name: 'deploy', dagRunId: 'run-1', stepId: 'signoff' },
            query: { remoteNode: 'worker-a' },
          },
          body: {},
        }
      )
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it('keeps the workspace execute gate', () => {
    vi.mocked(useCanExecuteForWorkspace).mockReturnValue(false);
    render(
      <HumanTasksTab
        dagRun={twoTaskRun()}
        onChanged={vi.fn()}
        stepName="Release review"
      />
    );

    expect(useCanExecuteForWorkspace).toHaveBeenCalledWith('production');
    expect(
      screen.getByRole('button', { name: 'Complete task' })
    ).toBeDisabled();
    expect(
      screen.getByRole('button', { name: 'Request changes' })
    ).toBeDisabled();
  });

  it('shows the empty state for a step without a waiting task', () => {
    render(
      <HumanTasksTab
        dagRun={twoTaskRun()}
        onChanged={vi.fn()}
        stepName="build"
      />
    );

    expect(screen.getByText('No human tasks are waiting.')).toBeVisible();
  });
});

const BASE_ALL =
  '<div class="space-y-4"><div role="alert" class="relative w-full rounded-md border p-4 [&amp;>svg~*]:pl-7 [&amp;>svg+div]:translate-y-[-3px] [&amp;>svg]:absolute [&amp;>svg]:left-4 [&amp;>svg]:top-4 bg-warning/10 border-warning/20 text-warning [&amp;>svg]:text-warning dark:bg-warning/15 dark:border-warning/30"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-triangle-alert h-4 w-4" aria-hidden="true"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"></path><path d="M12 9v4"></path><path d="M12 17h.01"></path></svg><div class="text-sm [&amp;_p]:leading-relaxed flex flex-wrap items-center justify-between gap-3"><span>Task input is safely stored, but the DAG-run still needs to be queued for resume.</span><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-input bg-card text-foreground shadow-sm hover:border-border-strong hover:bg-muted h-8 px-3 text-xs" type="button"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-refresh-ccw h-4 w-4" aria-hidden="true"><path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"></path><path d="M3 3v5h5"></path><path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"></path><path d="M16 16h5v5"></path></svg>Retry queue</button></div></div><div class="space-y-4 rounded-lg border border-border bg-surface p-4"><div class="space-y-1"><div class="flex items-center gap-2"><span class="text-sm font-semibold">Release review</span><span class="rounded bg-muted px-1.5 py-0.5 text-xs font-normal text-muted-foreground">Iteration 1</span></div><div class="whitespace-pre-wrap text-base">Confirm the production release.</div></div><div class="flex justify-end gap-2"><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border text-sm font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-input bg-card text-foreground shadow-sm hover:border-border-strong hover:bg-muted h-9 px-4" type="button"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-rotate-ccw h-4 w-4" aria-hidden="true"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"></path><path d="M3 3v5h5"></path></svg>Request changes</button><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border text-sm font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-primary-fill bg-primary-fill text-primary-foreground shadow-sm hover:border-primary-fill-hover hover:bg-primary-fill-hover h-9 px-4" type="button"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-check h-4 w-4" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>Complete task</button></div></div><div class="space-y-4 rounded-lg border border-border bg-surface p-4"><div class="space-y-1"><div class="flex items-center gap-2"><span class="text-sm font-semibold">Security sign-off</span></div><div class="whitespace-pre-wrap text-base">Confirm the security review.</div></div><div class="flex justify-end gap-2"><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border text-sm font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-primary-fill bg-primary-fill text-primary-foreground shadow-sm hover:border-primary-fill-hover hover:bg-primary-fill-hover h-9 px-4" type="button"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-check h-4 w-4" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>Complete task</button></div></div></div>';
const BASE_RUNNING =
  '<div class="space-y-4"><div role="alert" class="relative w-full rounded-md border p-4 [&amp;>svg~*]:pl-7 [&amp;>svg+div]:translate-y-[-3px] [&amp;>svg]:absolute [&amp;>svg]:left-4 [&amp;>svg]:top-4 bg-info/10 border-info/20 text-info [&amp;>svg]:text-info dark:bg-info/15 dark:border-info/30"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-info h-4 w-4" aria-hidden="true"><circle cx="12" cy="12" r="10"></circle><path d="M12 16v-4"></path><path d="M12 8h.01"></path></svg><div class="text-sm [&amp;_p]:leading-relaxed">This DAG-run is queued or running. Open tasks become editable once it is waiting.</div></div><div class="space-y-4 rounded-lg border border-border bg-surface p-4"><div class="space-y-1"><div class="flex items-center gap-2"><span class="text-sm font-semibold">Release review</span></div><div class="whitespace-pre-wrap text-base">Confirm the production release.</div></div><div class="flex justify-end gap-2"><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border text-sm font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-primary-fill bg-primary-fill text-primary-foreground shadow-sm hover:border-primary-fill-hover hover:bg-primary-fill-hover h-9 px-4" type="button" disabled=""><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-check h-4 w-4" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>Complete task</button></div></div></div>';
const BASE_EMPTY =
  '<div class="py-8 text-center text-sm text-muted-foreground">No human tasks are waiting.</div>';
