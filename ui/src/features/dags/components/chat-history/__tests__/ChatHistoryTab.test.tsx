// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// parity:api-manual-actions
import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  ChatMessageRole,
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { ChatHistoryTab } from '../ChatHistoryTab';

type Details = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];

const queries = vi.hoisted(
  () => [] as Array<{ path: string; stepName: string }>
);

vi.mock('@/hooks/api', () => ({
  useQuery: (
    path: string,
    init: { params: { path: { stepName: string } } } | null
  ) => {
    if (!init) return { data: undefined, isLoading: false };
    const stepName = init.params.path.stepName;
    queries.push({ path, stepName });
    return {
      data: {
        messages: [
          { role: ChatMessageRole.user, content: `question to ${stepName}` },
          {
            role: ChatMessageRole.assistant,
            content: `answer from ${stepName}`,
          },
        ],
        toolDefinitions: [],
      },
      isLoading: false,
    };
  },
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'worker-a',
}));

function chatNode(
  name: string,
  type: string,
  status: NodeStatus = NodeStatus.Success
): Node {
  return {
    step: { name, executorConfig: { type, config: {} } },
    stdout: '',
    stderr: '',
    startedAt: '',
    finishedAt: '',
    status,
    statusLabel:
      status === NodeStatus.Success
        ? NodeStatusLabel.succeeded
        : NodeStatusLabel.running,
    retryCount: 0,
    doneCount: 1,
  } as Node;
}

function chatRun(nodes: Node[]): Details {
  return {
    name: 'assistant',
    dagRunId: 'run-1',
    rootDAGRunName: '',
    rootDAGRunId: '',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    autoRetryCount: 0,
    startedAt: '',
    finishedAt: '',
    log: '',
    nodes,
  } as Details;
}

const twoChats = () =>
  chatRun([
    chatNode('ask', 'chat'),
    chatNode('shell', 'command'),
    chatNode('plan', 'agent'),
  ]);

/** The distinct message queries, as a render can repeat them. */
function queried() {
  const seen = new Map<string, { path: string; stepName: string }>();
  for (const q of queries) seen.set(`${q.path}|${q.stepName}`, q);
  return [...seen.values()];
}

afterEach(() => {
  queries.length = 0;
  vi.clearAllMocks();
});

describe('ChatHistoryTab', () => {
  it('selects the last finished chat step and shows its transcript', () => {
    render(<ChatHistoryTab dagRun={twoChats()} />);

    const select = screen.getByLabelText('Step:');
    expect(
      screen.getAllByRole('option').map((option) => option.textContent)
    ).toEqual(['ask (Succeeded)', 'plan (Succeeded)']);
    expect(select).toHaveValue('plan');
    expect(screen.getByText('question to plan')).toBeVisible();
    expect(screen.getByText('answer from plan')).toBeVisible();
  });

  it('says when the run has no chat steps', () => {
    render(<ChatHistoryTab dagRun={chatRun([chatNode('shell', 'command')])} />);

    expect(screen.getByText('No chat steps in this DAG run')).toBeVisible();
    expect(screen.queryByLabelText('Step:')).toBeNull();
  });

  it('shows only the transcript of the step filter, without the selector', () => {
    render(<ChatHistoryTab dagRun={twoChats()} stepName="ask" />);

    expect(screen.queryByLabelText('Step:')).toBeNull();
    expect(screen.getByText('question to ask')).toBeVisible();
    expect(screen.queryByText('question to plan')).toBeNull();
    expect(queried()).toEqual([
      {
        path: '/dag-runs/{name}/{dagRunId}/steps/{stepName}/messages',
        stepName: 'ask',
      },
    ]);
  });

  it('reads the transcript of a sub run step through the root run', () => {
    render(
      <ChatHistoryTab
        dagRun={{
          ...twoChats(),
          dagRunId: 'sub-1',
          rootDAGRunName: 'parent',
          rootDAGRunId: 'run-0',
        }}
        stepName="plan"
      />
    );

    expect(queried()).toEqual([
      {
        path: '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/messages',
        stepName: 'plan',
      },
    ]);
  });

  it('renders nothing for a step filter without a transcript', () => {
    const { container } = render(
      <ChatHistoryTab dagRun={twoChats()} stepName="shell" />
    );

    expect(container).toBeEmptyDOMElement();
    expect(queried()).toEqual([]);
  });
});
