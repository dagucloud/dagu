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
  it('renders the step selector and the last finished transcript as before', () => {
    const { container } = render(<ChatHistoryTab dagRun={twoChats()} />);
    expect(container.innerHTML).toBe(BASE_ALL);
  });

  it('renders the empty state as before', () => {
    const { container } = render(
      <ChatHistoryTab dagRun={chatRun([chatNode('shell', 'command')])} />
    );
    expect(container.innerHTML).toBe(BASE_EMPTY);
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

const BASE_ALL =
  '<div class="space-y-2"><div class="flex items-center gap-2 text-xs"><label for="chat-step-select" class="text-muted-foreground">Step:</label><select id="chat-step-select" class="h-6 px-2 text-xs border rounded bg-card focus:outline-none"><option value="ask">ask (Succeeded)</option><option value="plan">plan (Succeeded)</option></select></div><div class="border rounded bg-card"><div class="border-l-2 border-b last:border-b-0 bg-card border-l-violet-500"><button class="w-full flex items-center gap-2 px-2 py-1 hover:bg-accent/50 text-left" type="button"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-chevron-right h-3 w-3 shrink-0 transition-transform" aria-hidden="true"><path d="m9 18 6-6-6-6"></path></svg><span class="text-xs font-mono w-10 shrink-0 text-muted-foreground">USER</span><span class="text-xs text-muted-foreground truncate flex-1 min-w-0">question to plan</span></button></div><div class="border-l-2 border-b last:border-b-0 bg-card border-l-green-500"><button class="w-full flex items-center gap-2 px-2 py-1 hover:bg-accent/50 text-left" type="button"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-chevron-right h-3 w-3 shrink-0 transition-transform rotate-90" aria-hidden="true"><path d="m9 18 6-6-6-6"></path></svg><span class="text-xs font-mono w-10 shrink-0 text-muted-foreground">ASST</span></button><div class="px-2 pb-2 pl-7"><div class="flex gap-4 items-start"><div class="flex-1 min-w-0"><div class="text-xs prose prose-sm prose-slate dark:prose-invert max-w-none break-words prose-p:my-1 prose-p:leading-relaxed prose-pre:my-1 prose-pre:p-2 prose-pre:text-xs prose-pre:bg-muted prose-pre:text-foreground prose-pre:rounded prose-pre:border prose-pre:border-border prose-code:text-xs prose-code:bg-muted prose-code:text-foreground prose-code:px-1 prose-code:py-0.5 prose-code:rounded prose-code:before:content-none prose-code:after:content-none prose-ul:my-1 prose-ul:pl-4 prose-ol:my-1 prose-ol:pl-4 prose-li:my-0.5 prose-headings:my-1 prose-headings:font-semibold prose-h1:text-sm prose-h2:text-sm prose-h3:text-xs prose-blockquote:my-1 prose-blockquote:pl-2 prose-blockquote:border-l-2 prose-table:my-1 prose-table:text-xs prose-a:text-violet-700 dark:prose-a:text-violet-400"><p>answer from plan</p></div></div><div class="shrink-0 flex flex-col items-end gap-1"><button class="p-1 hover:bg-accent rounded" type="button" title="Copy message"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-copy h-3 w-3 text-muted-foreground" aria-hidden="true"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"></rect><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"></path></svg></button></div></div></div></div></div></div>';
const BASE_EMPTY =
  '<div class="text-xs text-muted-foreground p-2">No chat steps in this DAG run</div>';
