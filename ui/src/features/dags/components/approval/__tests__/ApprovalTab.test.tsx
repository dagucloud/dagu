// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// parity:test-approval-tab-untested parity:api-manual-actions
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  components,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { useClient } from '@/hooks/api';
import { ApprovalTab } from '../ApprovalTab';

type Details = components['schemas']['DAGRunDetails'];
type Node = components['schemas']['Node'];

const postMock = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
  useQuery: (_path: string, init: unknown) => ({
    data: init
      ? { content: 'ready for review', totalLines: 1, lineCount: 1 }
      : undefined,
    isLoading: false,
  }),
}));

vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'worker-a',
}));

function approvalNode(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: {
      name,
      description: `Check ${name}`,
      approval: {
        prompt: `Approve ${name}?`,
        input: ['reason'],
        required: ['reason'],
      },
    },
    stdout: `/logs/${name}.out`,
    stderr: '',
    startedAt: '',
    finishedAt: '',
    status: NodeStatus.Waiting,
    statusLabel: NodeStatusLabel.waiting,
    retryCount: 0,
    doneCount: 0,
    ...extra,
  } as Node;
}

function approvalRun(extra: Partial<Details> = {}): Details {
  return {
    name: 'release',
    dagRunId: 'run-1',
    rootDAGRunName: '',
    rootDAGRunId: '',
    status: Status.Waiting,
    statusLabel: StatusLabel.waiting,
    autoRetryCount: 0,
    startedAt: '',
    finishedAt: '',
    log: '',
    nodes: [
      approvalNode('gate-a'),
      approvalNode('gate-b', {
        approvalIteration: 2,
        pushBackHistory: [
          {
            iteration: 1,
            by: 'alice',
            inputs: { reason: 'tests are red' },
          },
        ],
      }),
      {
        ...approvalNode('build'),
        step: { name: 'build' },
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
      },
    ],
    ...extra,
  } as Details;
}

beforeEach(() => {
  postMock.mockResolvedValue({ error: undefined });
  vi.mocked(useClient).mockReturnValue({
    POST: postMock,
  } as unknown as ReturnType<typeof useClient>);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('ApprovalTab', () => {
  it('renders every waiting approval as before without a step filter', () => {
    const { container } = render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" />
    );
    expect(container.innerHTML).toBe(BASE_ALL);
  });

  it('renders the empty state as before without a step filter', () => {
    const { container } = render(
      <ApprovalTab dagRun={approvalRun({ nodes: [] })} dagName="release" />
    );
    expect(container.innerHTML).toBe(BASE_EMPTY);
  });

  it('shows only the card of the step filter', () => {
    render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" stepName="gate-b" />
    );

    expect(screen.getByText('Approve gate-b?')).toBeVisible();
    expect(screen.queryByText('Approve gate-a?')).toBeNull();
    expect(screen.getAllByRole('button', { name: 'Approve' })).toHaveLength(1);
  });

  // The dock pads its tabs: the host drops the frame of the cards.
  it('drops the frame of the cards for a padded host', () => {
    const { container } = render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" bare />
    );

    expect(screen.getAllByRole('button', { name: 'Approve' })).toHaveLength(2);
    expect(container.querySelector('.bg-surface')).toBeNull();
    expect(container.querySelector('.rounded-lg')).toBeNull();
  });

  it('shows the empty state for a step filter that waits for nothing', () => {
    render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" stepName="build" />
    );

    expect(screen.getByText('No steps awaiting approval.')).toBeVisible();
  });

  // The server refuses an approval without its required inputs, so the
  // approve dialog asks for them and sends them.
  it('approves the step through the review dialog', async () => {
    render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" stepName="gate-a" />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Approve: gate-a');
    const submit = within(dialog).getByRole('button', { name: 'Approve' });
    expect(submit).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/reason/), {
      target: { value: 'checked' },
    });
    fireEvent.click(submit);

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/approve',
        {
          params: {
            path: { name: 'release', dagRunId: 'run-1', stepName: 'gate-a' },
            query: { remoteNode: 'worker-a' },
          },
          body: { inputs: { reason: 'checked' } },
        }
      )
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('pushes the step back with the required inputs', async () => {
    render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" stepName="gate-b" />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Previous Push-backs')).toBeVisible();
    const submit = within(dialog).getByRole('button', { name: 'Retry' });
    expect(submit).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText(/reason/), {
      target: { value: 'flaky test fixed' },
    });
    fireEvent.click(submit);

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/push-back',
        {
          params: {
            path: { name: 'release', dagRunId: 'run-1', stepName: 'gate-b' },
            query: { remoteNode: 'worker-a' },
          },
          body: { inputs: { reason: 'flaky test fixed' } },
        }
      )
    );
  });

  it('addresses the root run for a step of a sub run', async () => {
    render(
      <ApprovalTab
        dagRun={approvalRun({
          name: 'child',
          dagRunId: 'sub-1',
          rootDAGRunName: 'release',
          rootDAGRunId: 'run-1',
        })}
        dagName="child"
        stepName="gate-a"
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/reason/), {
      target: { value: 'checked' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));

    await waitFor(() =>
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/approve',
        {
          params: {
            path: {
              name: 'release',
              dagRunId: 'run-1',
              stepName: 'gate-a',
              subDAGRunId: 'sub-1',
            },
            query: { remoteNode: 'worker-a' },
          },
          body: { inputs: { reason: 'checked' } },
        }
      )
    );
  });

  it('keeps the dialog open and shows a refused approval', async () => {
    postMock.mockResolvedValue({ error: { message: 'not waiting' } });
    render(
      <ApprovalTab dagRun={approvalRun()} dagName="release" stepName="gate-a" />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/reason/), {
      target: { value: 'checked' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));

    expect(await within(dialog).findByText('not waiting')).toBeVisible();
  });
});

const BASE_ALL =
  '<div class="space-y-4"><div class="bg-surface border border-border rounded-lg p-4 space-y-3"><div class="flex items-start justify-between gap-4"><div class="space-y-1"><div class="flex items-center gap-2"><span class="text-sm font-semibold">gate-a</span></div><div class="text-base whitespace-pre-wrap">Approve gate-a?</div></div><div class="flex shrink-0 gap-2"><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-input bg-card text-foreground shadow-sm hover:border-border-strong hover:bg-muted h-8 px-3 text-xs"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-rotate-ccw h-4 w-4" aria-hidden="true"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"></path><path d="M3 3v5h5"></path></svg>Retry</button><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-primary-fill bg-primary-fill text-primary-foreground shadow-sm hover:border-primary-fill-hover hover:bg-primary-fill-hover h-8 px-3 text-xs"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-check h-4 w-4" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>Approve</button></div></div><div><div class="text-xs font-medium text-muted-foreground mb-1">Step Output</div><div class="max-h-[400px] overflow-y-auto rounded border border-border"><div class="bg-muted rounded overflow-hidden border border-border"><div class="overflow-x-auto max-h-[400px] overflow-y-auto"><pre class="font-mono text-xs text-foreground p-2"><div class="flex px-1 py-0.5"><span class="text-muted-foreground mr-3 select-none w-12 text-right flex-shrink-0">1</span><span class="whitespace-pre-wrap break-all flex-grow"><span>ready for review</span></span></div></pre></div></div></div></div></div><div class="bg-surface border border-border rounded-lg p-4 space-y-3"><div class="flex items-start justify-between gap-4"><div class="space-y-1"><div class="flex items-center gap-2"><span class="text-sm font-semibold">gate-b</span><span class="text-xs font-normal text-muted-foreground bg-muted px-1.5 py-0.5 rounded">Iteration 2</span></div><div class="text-base whitespace-pre-wrap">Approve gate-b?</div></div><div class="flex shrink-0 gap-2"><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-input bg-card text-foreground shadow-sm hover:border-border-strong hover:bg-muted h-8 px-3 text-xs"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-rotate-ccw h-4 w-4" aria-hidden="true"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"></path><path d="M3 3v5h5"></path></svg>Retry</button><button data-slot="button" class="inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md border font-medium outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&amp;_svg]:pointer-events-none [&amp;_svg]:size-4 [&amp;_svg]:shrink-0 border-primary-fill bg-primary-fill text-primary-foreground shadow-sm hover:border-primary-fill-hover hover:bg-primary-fill-hover h-8 px-3 text-xs"><svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-check h-4 w-4" aria-hidden="true"><path d="M20 6 9 17l-5-5"></path></svg>Approve</button></div></div><div><div class="text-xs font-medium text-muted-foreground mb-1.5">Push-back History</div><div class="space-y-2"><div class="rounded-md border border-border bg-muted/30 px-3 py-2"><div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs"><span class="font-medium text-foreground/90">Iteration 1</span><span class="text-muted-foreground">by <span class="text-foreground/80">alice</span></span></div><div class="mt-1 text-xs text-muted-foreground"><span class="font-medium">Inputs:</span> <span class="font-mono text-foreground/80 break-all">reason="tests are red"</span></div></div></div></div><div><div class="text-xs font-medium text-muted-foreground mb-1">Step Output</div><div class="max-h-[400px] overflow-y-auto rounded border border-border"><div class="bg-muted rounded overflow-hidden border border-border"><div class="overflow-x-auto max-h-[400px] overflow-y-auto"><pre class="font-mono text-xs text-foreground p-2"><div class="flex px-1 py-0.5"><span class="text-muted-foreground mr-3 select-none w-12 text-right flex-shrink-0">1</span><span class="whitespace-pre-wrap break-all flex-grow"><span>ready for review</span></span></div></pre></div></div></div></div></div></div>';
const BASE_EMPTY =
  '<div class="text-center text-muted-foreground py-8 text-sm">No steps awaiting approval.</div>';
