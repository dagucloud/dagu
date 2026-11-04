// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import {
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest';
import {
  AgentInteractionKind,
  AgentInteractionStatus,
  AgentSessionState,
  ChatMessageRole,
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { RunOverlay } from '@/features/visual-editor/contracts';
import { WorkspaceDataContext } from '../../data/context';
import { nodesByStep } from '../../data/runFacts';
import type {
  DAGPageData,
  WorkspacePermissions,
  WorkspaceRunValue,
} from '../../data/types';
import { DockProvider } from '../../dock/DockContext';
import { StepRunView, type StepRunTarget } from '../StepRunView';

type Node = components['schemas']['Node'];
type Details = components['schemas']['DAGRunDetails'];

const api = vi.hoisted(() => ({
  post: vi.fn(),
  messages: [] as string[],
  /** Reads the sub run again. */
  rereadSubRun: vi.fn(),
  /** Sub runs by ID, for a test that nests them; the others read subRun(). */
  subRuns: {} as Record<string, unknown>,
  /** The sub runs that a run called, by the ID of a parent sub run ('' for the root). */
  subRunLists: {} as Record<string, unknown[]>,
}));
const perms = vi.hoisted(() => ({ value: {} as WorkspacePermissions }));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PATCH: vi.fn(), POST: api.post }),
  useQuery: (
    path: string,
    init: { params: { path: Record<string, string> } } | null
  ) => {
    let data: unknown;
    let mutate = () => Promise.resolve(undefined);
    if (init && path.endsWith('/sub-dag-runs/{subDAGRunId}')) {
      const id = init.params.path.subDAGRunId ?? '';
      data = { dagRunDetails: api.subRuns[id] ?? subRun() };
      mutate = api.rereadSubRun;
    } else if (init && path.endsWith('/sub-dag-runs')) {
      const query = (init.params as { query?: { parentSubDAGRunId?: string } })
        .query;
      data = { subRuns: api.subRunLists[query?.parentSubDAGRunId ?? ''] ?? [] };
    } else if (init && path.endsWith('/messages')) {
      const step = init.params.path.stepName ?? '';
      api.messages.push(step);
      data = {
        messages: [
          { role: ChatMessageRole.user, content: `question to ${step}` },
          { role: ChatMessageRole.assistant, content: `answer from ${step}` },
        ],
        toolDefinitions: [],
      };
    } else if (init && path.endsWith('/log')) {
      data = { content: 'waiting output', totalLines: 1, lineCount: 1 };
    }
    return {
      data,
      error: undefined,
      isLoading: false,
      isValidating: false,
      mutate,
    };
  },
}));
vi.mock('@/contexts/AuthContext', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useCanExecuteForWorkspace: vi.fn(),
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1', permissions: { runDags: true } }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('../../data/useWorkspacePermissions', () => ({
  useWorkspacePermissions: () => perms.value,
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: null, state: null }),
}));

function node(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    stdout: '',
    stderr: '',
    startedAt: '',
    finishedAt: '',
    retryCount: 0,
    doneCount: 1,
    ...extra,
  } as Node;
}

const waiting = {
  status: NodeStatus.Waiting,
  statusLabel: NodeStatusLabel.waiting,
};

function gate(name: string): Node {
  return node(name, {
    ...waiting,
    stdout: `/logs/${name}.out`,
    step: { name, approval: { prompt: `Approve ${name}?` } },
  });
}

function waitingRun(): Details {
  return {
    name: 'example',
    dagRunId: 'run-1',
    rootDAGRunName: 'example',
    rootDAGRunId: 'run-1',
    workspace: 'production',
    status: Status.Waiting,
    statusLabel: StatusLabel.waiting,
    startedAt: '',
    finishedAt: '',
    autoRetryCount: 0,
    nodes: [
      node('build'),
      gate('gate-a'),
      gate('gate-b'),
      node('review', {
        ...waiting,
        step: {
          id: 'review',
          name: 'review',
          humanTask: { prompt: 'Confirm the production release.' },
        },
      }),
      node('coder', {
        status: NodeStatus.Running,
        statusLabel: NodeStatusLabel.running,
        agentSession: {
          provider: 'opencode',
          state: AgentSessionState.waiting,
          interactions: [
            {
              id: 'q-1',
              kind: AgentInteractionKind.question,
              status: AgentInteractionStatus.pending,
            },
          ],
        },
      }),
      node('asker', {
        status: NodeStatus.Running,
        statusLabel: NodeStatusLabel.running,
        agentSession: {
          provider: 'opencode',
          state: AgentSessionState.running,
          interactions: [
            {
              id: 'p-1',
              kind: AgentInteractionKind.permission,
              status: AgentInteractionStatus.pending,
            },
          ],
        },
      }),
      node('stalled', {
        ...waiting,
        agentSession: {
          provider: 'opencode',
          state: AgentSessionState.unavailable,
        },
      }),
      node('busy', {
        status: NodeStatus.Running,
        statusLabel: NodeStatusLabel.running,
        agentSession: {
          provider: 'opencode',
          state: AgentSessionState.running,
          interactions: [
            {
              id: 'q-0',
              kind: AgentInteractionKind.question,
              status: AgentInteractionStatus.answered,
            },
          ],
        },
      }),
      node('ask', {
        step: { name: 'ask', executorConfig: { type: 'chat', config: {} } },
      }),
      node('later-chat', {
        status: NodeStatus.NotStarted,
        statusLabel: NodeStatusLabel.not_started,
        step: {
          name: 'later-chat',
          executorConfig: { type: 'chat', config: {} },
        },
      }),
    ],
  } as Details;
}

/** A sub run of run-1 with its own approval gate, human task and waiting agent. */
function subRun(): Details {
  const root = waitingRun();
  return {
    ...root,
    name: 'child',
    dagRunId: 'sub-1',
    nodes: [
      gate('gate-c'),
      ...root.nodes.filter(
        (n) => n.step.name === 'coder' || n.step.name === 'review'
      ),
    ],
  };
}

function runValue(d: Details): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: d.name, runId: d.dagRunId },
    details: d,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {} as RunOverlay,
    nodeByStep: nodesByStep(d),
    handlers: [],
    waiting: { approvals: 2, humanTasks: 1, agent: 1, firstWaitingStep: null },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
  };
}

const where = { search: '', type: '' };
function LocationProbe() {
  const location = useLocation();
  where.search = location.search;
  where.type = useNavigationType();
  return null;
}

const refresh = vi.fn();

function renderFacts(target: StepRunTarget, d: Details = waitingRun()) {
  return render(
    <MemoryRouter initialEntries={[`/dags/example.yaml?step=${target.step}`]}>
      <UserPreferencesProvider>
        <RemoteNodeProvider remoteNode="edge">
          <WorkspaceDataContext.Provider
            value={{
              fileName: 'example.yaml',
              dagData: { dag: { name: 'example' } } as DAGPageData,
              workspaceName: 'production',
              remoteNode: 'edge',
              run: runValue(d),
              refresh,
              registerHistory: () => () => undefined,
            }}
          >
            <Routes>
              <Route
                path="/dags/:fileName"
                element={
                  <DockProvider>
                    <StepRunView target={target} variant="facts" />
                    <LocationProbe />
                  </DockProvider>
                }
              />
            </Routes>
          </WorkspaceDataContext.Provider>
        </RemoteNodeProvider>
      </UserPreferencesProvider>
    </MemoryRouter>
  );
}

beforeAll(() => {
  // Running chips read the reduced-motion preference.
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  });
});

beforeEach(() => {
  api.rereadSubRun.mockReset().mockResolvedValue(undefined);
  api.subRuns = {};
  api.subRunLists = {};
  api.post.mockReset().mockResolvedValue({ error: undefined });
  api.messages = [];
  refresh.mockReset().mockResolvedValue(undefined);
  vi.mocked(useCanExecuteForWorkspace).mockReturnValue(true);
  perms.value = {
    canWrite: true,
    canRunDags: true,
    canExecute: true,
    canExecuteForWorkspace: true,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  };
  where.search = '';
  where.type = '';
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('StepManualActions', () => {
  // parity:step-review-modal
  it('shows the approval card of the step and approves through the review dialog', async () => {
    renderFacts({ step: 'gate-a' });

    const card = screen.getByTestId('ww-step-manual-actions');
    expect(within(card).getByText('Approve gate-a?')).toBeVisible();
    expect(within(card).queryByText('Approve gate-b?')).toBeNull();

    fireEvent.click(within(card).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Approve: gate-a');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/approve',
        {
          params: {
            path: { name: 'example', dagRunId: 'run-1', stepName: 'gate-a' },
            query: { remoteNode: 'edge' },
          },
          body: { inputs: undefined },
        }
      )
    );
  });

  // The server refuses an approval without its required inputs (400), so
  // the review dialog asks for them on Approve too.
  it('asks for the required approval inputs and sends them on Approve', async () => {
    const d = waitingRun();
    d.nodes = [
      ...(d.nodes ?? []),
      node('gate-d', {
        ...waiting,
        step: {
          name: 'gate-d',
          approval: {
            prompt: 'Approve gate-d?',
            input: ['FEEDBACK', 'NOTE'],
            required: ['FEEDBACK'],
          },
        },
      }),
    ];
    renderFacts({ step: 'gate-d' }, d);

    const card = screen.getByTestId('ww-step-manual-actions');
    fireEvent.click(within(card).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    const approve = within(dialog).getByRole('button', { name: 'Approve' });
    expect(approve).toBeDisabled();

    fireEvent.change(within(dialog).getByLabelText(/FEEDBACK/), {
      target: { value: 'looks good' },
    });
    expect(within(dialog).getByLabelText(/NOTE/)).toBeVisible();
    expect(approve).toBeEnabled();
    fireEvent.click(approve);

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/approve',
        {
          params: {
            path: { name: 'example', dagRunId: 'run-1', stepName: 'gate-d' },
            query: { remoteNode: 'edge' },
          },
          body: { inputs: { FEEDBACK: 'looks good' } },
        }
      )
    );
  });

  // parity:step-review-modal
  it('opens the push-back dialog from Retry', async () => {
    renderFacts({ step: 'gate-b' });

    const card = screen.getByTestId('ww-step-manual-actions');
    fireEvent.click(within(card).getByRole('button', { name: 'Retry' }));

    expect(await screen.findByRole('dialog')).toHaveTextContent(
      'Retry: gate-b'
    );
  });

  // parity:human-task-card
  it('shows the human task card of the step and completes it', async () => {
    renderFacts({ step: 'review' });

    const card = screen.getByTestId('ww-step-manual-actions');
    expect(
      within(card).getByText('Confirm the production release.')
    ).toBeVisible();
    fireEvent.click(
      within(card).getByRole('button', { name: 'Complete task' })
    );

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        {
          params: {
            path: { name: 'example', dagRunId: 'run-1', stepId: 'review' },
            query: { remoteNode: 'edge' },
          },
          body: {},
        }
      )
    );
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });

  // parity:human-task-card
  it('keeps the human task disabled without workspace execute permission', () => {
    vi.mocked(useCanExecuteForWorkspace).mockReturnValue(false);
    renderFacts({ step: 'review' });

    const card = screen.getByTestId('ww-step-manual-actions');
    expect(useCanExecuteForWorkspace).toHaveBeenCalledWith('production');
    expect(
      within(card).getByRole('button', { name: 'Complete task' })
    ).toBeDisabled();
    expect(
      within(card).getByText(
        'Execute permission is required to complete this task.'
      )
    ).toBeVisible();
  });

  it('links a waiting agent step to the dock Agent tab and keeps ?step', () => {
    renderFacts({ step: 'coder' });

    const card = screen.getByTestId('ww-step-agent-waiting');
    expect(card).toHaveTextContent('An agent is waiting for an answer.');
    fireEvent.click(
      within(card).getByRole('button', { name: 'Answer in the Agent tab' })
    );

    const params = new URLSearchParams(where.search);
    expect(params.get('dock')).toBe('agent');
    expect(params.get('step')).toBe('coder');
    expect(where.type).toBe('REPLACE');
  });

  it.each(['asker', 'stalled'])(
    'links the %s agent step to the dock Agent tab',
    (step) => {
      renderFacts({ step });

      fireEvent.click(
        screen.getByRole('button', { name: 'Answer in the Agent tab' })
      );
      expect(new URLSearchParams(where.search).get('dock')).toBe('agent');
    }
  );

  // A step that calls a workflow waits while its sub-run waits for a
  // person: the action is in the sub-run, which the step leads to.
  it('leads a step whose sub-run waits to that sub-run', () => {
    const run = waitingRun();
    run.nodes.push(
      node('notify', {
        ...waiting,
        step: { name: 'notify', call: 'notify' },
        subRuns: [{ dagRunId: 'sub-1', params: '' }],
      }) as (typeof run.nodes)[number]
    );
    renderFacts({ step: 'notify' }, run);

    const notice = screen.getByTestId('ww-step-sub-run-waiting');
    expect(notice).toHaveTextContent(
      'A sub-run of this step is waiting for an action.'
    );
    expect(
      within(notice).getByRole('button', { name: 'Open sub-run' })
    ).toBeInTheDocument();
  });

  // The runtime shows a call step as waiting while a run below it waits for
  // a person: the approval two calls down is taken from the step itself.
  it('approves a step that waits two runs below the step', async () => {
    const run = waitingRun();
    run.nodes.push(
      node('report', {
        ...waiting,
        step: { name: 'report', call: 'report' },
        subRuns: [{ dagRunId: 'sub-r', dagName: 'report' }],
      }) as (typeof run.nodes)[number]
    );
    const listed = (dagRunId: string, dagName: string) => ({
      dagRunId,
      dagName,
      status: Status.Waiting,
      statusLabel: StatusLabel.waiting,
      startedAt: '',
    });
    api.subRunLists[''] = [listed('sub-r', 'report')];
    api.subRunLists['sub-r'] = [listed('sub-n', 'notify')];
    api.subRuns['sub-r'] = {
      ...waitingRun(),
      name: 'report',
      dagRunId: 'sub-r',
      nodes: [
        node('notify', {
          ...waiting,
          step: { name: 'notify', call: 'notify' },
          subRuns: [{ dagRunId: 'sub-n', dagName: 'notify' }],
        }),
      ],
    };
    api.subRuns['sub-n'] = {
      ...waitingRun(),
      name: 'notify',
      dagRunId: 'sub-n',
      nodes: [gate('send')],
    };
    renderFacts({ step: 'report' }, run);

    const nested = screen.getByRole('region', { name: 'report › notify' });
    fireEvent.click(within(nested).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/approve',
        {
          params: {
            path: {
              name: 'example',
              dagRunId: 'run-1',
              stepName: 'send',
              subDAGRunId: 'sub-n',
            },
            query: { remoteNode: 'edge' },
          },
          body: { inputs: undefined },
        }
      )
    );
  });

  it('rejects a step of a sub run through the sub run endpoint', async () => {
    renderFacts({
      step: 'gate-c',
      subRun: { dagName: 'child', subDAGRunId: 'sub-1' },
    });

    const card = screen.getByTestId('ww-step-manual-actions');
    fireEvent.click(within(card).getByRole('button', { name: 'Reject' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reject' }));

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/reject',
        {
          params: {
            path: {
              name: 'example',
              dagRunId: 'run-1',
              subDAGRunId: 'sub-1',
              stepName: 'gate-c',
            },
            query: { remoteNode: 'edge' },
          },
          body: { reason: undefined },
        }
      )
    );
  });

  // The dock Agent tab shows the selected root run only.
  it('does not link an agent step of a sub run to the dock Agent tab', () => {
    renderFacts({
      step: 'coder',
      subRun: { dagName: 'child', subDAGRunId: 'sub-1' },
    });

    expect(screen.getByTestId('ww-step-facts')).toBeInTheDocument();
    expect(screen.queryByTestId('ww-step-agent-waiting')).toBeNull();
    expect(screen.queryByTestId('ww-step-manual-actions')).toBeNull();
  });

  // parity:step-review-modal
  it('approves a step of a sub run through the sub run endpoint', async () => {
    renderFacts({
      step: 'gate-c',
      subRun: { dagName: 'child', subDAGRunId: 'sub-1' },
    });

    const card = screen.getByTestId('ww-step-manual-actions');
    fireEvent.click(within(card).getByRole('button', { name: 'Approve' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));

    await waitFor(() =>
      expect(api.post).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/approve',
        {
          params: {
            path: {
              name: 'example',
              dagRunId: 'run-1',
              stepName: 'gate-c',
              subDAGRunId: 'sub-1',
            },
            query: { remoteNode: 'edge' },
          },
          body: { inputs: undefined },
        }
      )
    );
    // A finished sub run is not polled: the approval reads it again.
    await waitFor(() => expect(api.rereadSubRun).toHaveBeenCalled());
  });

  // The server completes the human tasks of a root run only: the card of a
  // sub run would send the task to the wrong run.
  it('offers no task card for a human task of a sub run', () => {
    renderFacts({
      step: 'review',
      subRun: { dagName: 'child', subDAGRunId: 'sub-1' },
    });

    expect(screen.getByTestId('ww-step-sub-run-task')).toHaveTextContent(
      'A human task of a sub-run cannot be completed here.'
    );
    expect(
      screen.queryByRole('button', { name: /complete/i })
    ).not.toBeInTheDocument();
  });

  it.each(['build', 'busy', 'ask'])(
    'renders nothing for the %s step',
    (step) => {
      renderFacts({ step });

      expect(screen.getByTestId('ww-step-facts')).toBeInTheDocument();
      expect(screen.queryByTestId('ww-step-manual-actions')).toBeNull();
    }
  );
});

describe('StepRunView chat transcript', () => {
  it('shows the transcript of a chat step', () => {
    renderFacts({ step: 'ask' });

    const chat = screen.getByRole('region', { name: 'Chat' });
    expect(within(chat).getByText('question to ask')).toBeVisible();
    expect(within(chat).queryByLabelText('Step:')).toBeNull();
    expect(new Set(api.messages)).toEqual(new Set(['ask']));
  });

  it('shows no transcript for a chat step that did not start', () => {
    renderFacts({ step: 'later-chat' });
    expect(screen.queryByRole('region', { name: 'Chat' })).toBeNull();
    expect(api.messages).toEqual([]);
  });

  it('shows no transcript for a step that is not a chat step', () => {
    renderFacts({ step: 'build' });
    expect(screen.queryByRole('region', { name: 'Chat' })).toBeNull();
  });
});
