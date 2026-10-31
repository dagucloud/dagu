// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentProps, ReactNode } from 'react';
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
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type StepLogComponent from '@/features/dags/components/dag-execution/StepLog';
import type { RunOverlay } from '@/features/visual-editor/contracts';
import dayjs from '@/lib/dayjs';
import { WorkspaceDataContext } from '../../data/context';
import { nodesByStep } from '../../data/runFacts';
import type {
  DAGPageData,
  WorkspacePermissions,
  WorkspaceRunValue,
} from '../../data/types';
import { StepRunView, type StepRunTarget } from '../StepRunView';

type Node = components['schemas']['Node'];
type Details = components['schemas']['DAGRunDetails'];
type ForeachItemList = components['schemas']['ForeachItemList'];
type ForeachItem = components['schemas']['ForeachItem'];

const SUB_RUN_PATH = '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}';

const api = vi.hoisted(() => ({
  log: undefined as unknown,
  outputs: undefined as unknown,
  subRun: undefined as unknown,
  foreachList: undefined as unknown,
  foreachItems: {} as Record<string, unknown>,
  calls: [] as Array<[string, { params: { path: Record<string, string> } }]>,
  patch: vi.fn(),
}));
const perms = vi.hoisted(() => ({ value: {} as WorkspacePermissions }));
const dock = vi.hoisted(() => ({ openStep: vi.fn() }));
const foreachLog = vi.hoisted(() => ({ props: null as unknown }));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PATCH: api.patch, POST: vi.fn() }),
  useQuery: (
    path: string,
    init: { params: { path: Record<string, string> } } | null
  ) => {
    let data: unknown;
    if (init) {
      api.calls.push([path, init]);
      if (path === '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}') {
        data = api.subRun;
      } else if (path.endsWith('/outputs')) {
        data = api.outputs;
      } else if (path.endsWith('/foreach')) {
        data = api.foreachList;
      } else if (path.endsWith('/foreach/{item}')) {
        data = api.foreachItems[init.params.path.item ?? ''];
      } else if (path.endsWith('/log')) {
        data = api.log;
      }
    }
    return {
      data,
      error: undefined,
      isLoading: false,
      isValidating: false,
      mutate: () => Promise.resolve(undefined),
    };
  },
}));
vi.mock('@/hooks/useStepLogSSE', () => ({
  useStepLogSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
    error: null,
  }),
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
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ({ openStep: dock.openStep }),
  useDockCommands: () => ({ openStep: dock.openStep }),
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: null, state: null }),
}));
// The real StepLog, wrapped so that a test can count its mounts.
vi.mock(
  '@/features/dags/components/dag-execution/StepLog',
  async (importOriginal) => {
    const actual = await importOriginal<{ default: typeof StepLogComponent }>();
    const Real = actual.default;
    return {
      default: (props: ComponentProps<typeof Real>) => (
        <div
          data-testid="step-log-mount"
          data-dag-run={props.dagRun?.dagRunId}
          data-step={props.stepName}
        >
          <Real {...props} />
        </div>
      ),
    };
  }
);
vi.mock('@/features/dags/components/dag-execution/ForeachBodyStepLog', () => ({
  default: (props: unknown) => {
    foreachLog.props = props;
    return <div data-testid="foreach-body-log" />;
  },
}));

const STARTED = '2026-10-01T09:00:00Z';
const FINISHED = '2026-10-01T09:00:05Z';

function node(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    stdout: '/logs/build.out',
    stderr: '/logs/build.err',
    startedAt: STARTED,
    finishedAt: FINISHED,
    retryCount: 0,
    doneCount: 1,
    ...extra,
  } as Node;
}

function details(nodes: Node[], extra: Partial<Details> = {}): Details {
  return {
    name: 'example',
    dagRunId: 'run-1',
    rootDAGRunName: 'example',
    rootDAGRunId: 'run-1',
    status: Status.Success,
    statusLabel: StatusLabel.succeeded,
    startedAt: STARTED,
    finishedAt: FINISHED,
    autoRetryCount: 0,
    nodes,
    ...extra,
  } as Details;
}

function runValue(d: Details | null): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: d ? { name: d.name, runId: d.dagRunId } : null,
    details: d,
    hasRun: !!d,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {} as RunOverlay,
    nodeByStep: nodesByStep(d),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
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

function renderView(
  d: Details | null,
  views: ReactNode,
  url = '/dags/example.yaml?dock=log&step=build'
) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <UserPreferencesProvider>
        <RemoteNodeProvider remoteNode="edge">
          <WorkspaceDataContext.Provider
            value={{
              fileName: 'example.yaml',
              dagData: { dag: { name: 'example' } } as DAGPageData,
              workspaceName: null,
              remoteNode: 'edge',
              run: runValue(d),
              refresh: vi.fn(),
              registerHistory: () => () => undefined,
            }}
          >
            <Routes>
              <Route
                path="/dags/:fileName"
                element={
                  <>
                    {views}
                    <LocationProbe />
                  </>
                }
              />
            </Routes>
          </WorkspaceDataContext.Provider>
        </RemoteNodeProvider>
      </UserPreferencesProvider>
    </MemoryRouter>
  );
}

function facts(target: StepRunTarget, onOpenLogs?: (t: StepRunTarget) => void) {
  return (
    <StepRunView target={target} variant="facts" onOpenLogs={onOpenLogs} />
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
  api.log = {
    content: 'first output',
    totalLines: 1,
    lineCount: 1,
    hasMore: false,
  };
  api.outputs = undefined;
  api.subRun = undefined;
  api.foreachList = undefined;
  api.foreachItems = {};
  api.calls = [];
  api.patch.mockReset().mockResolvedValue({ error: undefined });
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
  dock.openStep.mockReset();
  foreachLog.props = null;
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// parity:test-node-status-row parity:test-step-details parity:history-step-drawer
// parity:step-run-panel parity:dup-6
describe('StepRunView facts', () => {
  // parity:steps-status-chip parity:run-step-status-cell
  it('makes the status chip update the status only when the gate passes', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));

    const chip = screen.getByTitle('Click to update status');
    expect(chip).toHaveTextContent('Succeeded');
    fireEvent.click(chip);

    expect(screen.getByText('Update status')).toBeVisible();
  });

  it.each([
    ['the run is active', Status.Running, true],
    ['runDags is off', Status.Success, false],
  ])('keeps the status chip still when %s', (_, status, runDags) => {
    perms.value = { ...perms.value, canRunDags: runDags };
    renderView(details([node('build')], { status }), facts({ step: 'build' }));

    expect(
      screen.queryByTitle('Click to update status')
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Succeeded'));
    expect(screen.queryByText('Update status')).not.toBeInTheDocument();
  });

  // parity:drawer-runtime-facts parity:run-step-timing-manual
  it('shows Started, Finished, Duration, Retries and Completions', () => {
    renderView(
      details([node('build', { retryCount: 2, doneCount: 3 })]),
      facts({ step: 'build' })
    );

    const panel = within(screen.getByTestId('ww-step-facts'));
    const value = (label: string) =>
      panel.getByText(label, { selector: 'dt' }).nextElementSibling;
    expect(value('Started')).toHaveTextContent(
      dayjs(STARTED).format('YYYY-MM-DD HH:mm:ss')
    );
    expect(value('Finished')).toHaveTextContent(
      dayjs(FINISHED).format('YYYY-MM-DD HH:mm:ss')
    );
    expect(value('Duration')).toHaveTextContent('5s');
    expect(value('Retries')).toHaveTextContent('2');
    expect(value('Completions')).toHaveTextContent('3');
    expect(panel.getByLabelText('Retried 2×')).toBeVisible();
  });

  it('counts the duration of a running step every second', () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] });
    vi.setSystemTime(new Date('2026-10-01T09:00:05Z'));
    renderView(
      details(
        [
          node('build', {
            status: NodeStatus.Running,
            statusLabel: NodeStatusLabel.running,
            finishedAt: '',
          }),
        ],
        { status: Status.Running }
      ),
      facts({ step: 'build' })
    );

    const duration = () =>
      screen.getByText('Duration', { selector: 'dt' }).nextElementSibling;
    expect(duration()).toHaveTextContent('5s');
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(duration()).toHaveTextContent('7s');
  });

  it('links the producer run of a reused build with the remote node', async () => {
    const user = userEvent.setup();
    renderView(
      details([
        node('build', {
          build: {
            decision: 'reuse',
            phase: 'complete',
            reason: 'matched',
            producerRun: { name: 'producer', id: 'run-2' },
          },
        } as Partial<Node>),
      ]),
      facts({ step: 'build' })
    );

    await user.hover(screen.getByText('reused'));
    const links = await screen.findAllByRole('link', {
      name: 'Produced by producer:run-2',
    });
    for (const link of links) {
      expect(link).toHaveAttribute(
        'href',
        '/dags/producer?remoteNode=edge&run=run-2'
      );
    }
  });

  // parity:steps-repeat-policy
  it('shows the repeat policy badge, its summary line and the run count', () => {
    renderView(
      details([
        node('build', {
          doneCount: 3,
          step: {
            name: 'build',
            repeatPolicy: {
              repeat: 'until',
              interval: 30,
              limit: 5,
              condition: { condition: '$STATUS', expected: 'done' },
            },
          } as Node['step'],
        }),
      ]),
      facts({ step: 'build' })
    );

    const block = screen.getByTestId('ww-repeat');
    expect(within(block).getByTestId('repeat-badge')).toHaveTextContent('×3');
    expect(within(block).getByTestId('ww-repeat-summary')).toHaveTextContent(
      'repeat until "$STATUS = done" · every 30s · max 5'
    );
  });

  it('shows no repeat block for a step without a repeat policy', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));
    expect(screen.queryByTestId('ww-repeat')).toBeNull();
  });

  // parity:steps-manual-action-details parity:common-manual-action-bits
  it('shows the manual audit', () => {
    renderView(
      details([
        node('approve', {
          approvedBy: 'alice',
          approvedAt: FINISHED,
          approvalInputs: { env: 'prod' },
          pushBackHistory: [
            { iteration: 1, by: 'bob', inputs: { note: 'again' } },
          ],
          rejectedBy: 'carol',
          rejectedAt: FINISHED,
          rejectionReason: 'Not ready',
          humanTaskCompletedBy: 'dave',
        }),
      ]),
      facts({ step: 'approve' })
    );

    // The first match is the audit line; the push-back history repeats 'Inputs:'.
    const line = (label: string) =>
      screen.getAllByText(label)[0]!.parentElement!;
    expect(line('Approved by:')).toHaveTextContent('alice');
    expect(line('Inputs:')).toHaveTextContent('{"env":"prod"}');
    expect(line('Rejected by:')).toHaveTextContent('carol');
    expect(line('Reason:')).toHaveTextContent('Not ready');
    expect(line('Completed by:')).toHaveTextContent('dave');
    expect(screen.getByText('Push-back History')).toBeVisible();
    expect(screen.getByText('bob')).toBeVisible();
  });

  // parity:steps-error-preconditions parity:dup-9
  it('shows the full step error and the unmet precondition cards', () => {
    const error = 'line one\nline two\nline three\nline four';
    renderView(
      details([
        node('process', {
          error,
          status: NodeStatus.Failed,
          statusLabel: NodeStatusLabel.failed,
          step: {
            name: 'process',
            preconditions: [
              {
                condition: '${MODE}',
                expectedAny: ['full', 'minimal'],
                error: 'condition was not met',
              },
            ],
          } as Node['step'],
        }),
      ]),
      facts({ step: 'process' })
    );

    expect(screen.getByRole('alert').textContent).toBe(error);
    expect(screen.getByText('Precondition unmet')).toBeVisible();
    expect(screen.getByText(/Condition: \$\{MODE\}/)).toBeVisible();
    expect(screen.getByText(/full \| minimal/)).toBeVisible();
    expect(screen.getByText(/condition was not met/)).toBeVisible();
  });

  // parity:steps-execution-cell parity:run-step-execution-col
  it('shows the command and opens the script dialog', () => {
    renderView(
      details([
        node('build', {
          step: {
            name: 'build',
            commands: [{ command: 'make', args: ['all'] }],
            script: 'echo one\necho two',
          } as Node['step'],
        }),
      ]),
      facts({ step: 'build' })
    );

    const execution = within(screen.getByRole('region', { name: 'Execution' }));
    expect(execution.getByText('Command:')).toBeVisible();
    expect(execution.getByText('make all')).toBeVisible();
    fireEvent.click(execution.getByTitle('View script'));

    expect(screen.getByText('Script: build')).toBeVisible();
    expect(screen.getByText('echo two')).toBeVisible();
  });

  it('shows the written message of a log step', () => {
    api.log = { content: 'Deploying production\n' };
    renderView(
      details([
        node('announce', {
          step: {
            name: 'announce',
            executorConfig: {
              type: 'log',
              config: { message: 'Deploying ${ENVIRONMENT}' },
            },
          } as Node['step'],
        }),
      ]),
      facts({ step: 'announce' })
    );

    expect(screen.getByText('Message:')).toBeVisible();
    expect(
      screen.getByLabelText('Log message: Deploying production')
    ).toBeVisible();
    expect(
      screen.queryByText('Deploying ${ENVIRONMENT}')
    ).not.toBeInTheDocument();
  });

  it('shows the harness summary of a harness step', () => {
    renderView(
      details([
        node('review', {
          step: {
            name: 'review',
            commands: [{ command: 'Review', args: ['this'] }],
            executorConfig: {
              type: 'harness',
              config: { provider: 'claude', model: 'sonnet' },
            },
          } as Node['step'],
        }),
      ]),
      facts({ step: 'review' })
    );

    expect(screen.getByText('Execution:')).toBeVisible();
    expect(screen.getByText('Harness')).toBeVisible();
    expect(screen.getByText('model=sonnet')).toBeVisible();
  });

  it('shows a JSON output prettified, with a button that copies it', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    api.outputs = {
      outputs: [
        { name: 'RESULT', value: '{"count":3}', truncated: false, size: 11 },
      ],
      outputVariable: {
        name: 'OUT',
        value: 'plain text',
        truncated: false,
        size: 10,
      },
    };
    renderView(details([node('build')]), facts({ step: 'build' }));

    const outputs = within(screen.getByRole('region', { name: 'Outputs' }));
    expect(outputs.getByText('"count"')).toBeVisible();
    expect(outputs.getByText('plain text')).toBeVisible();
    fireEvent.click(outputs.getAllByRole('button', { name: 'Copy value' })[1]!);

    expect(writeText).toHaveBeenCalledWith('{\n  "count": 3\n}');
    expect(
      api.calls.find(([path]) => path.endsWith('/outputs'))?.[1].params.path
    ).toEqual({ name: 'example', dagRunId: 'run-1', stepName: 'build' });
  });

  // parity:drawer-log-buttons parity:steps-log-links parity:run-step-log-links
  it('opens stdout and stderr in the dock', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));

    fireEvent.click(screen.getByRole('link', { name: 'View stdout' }));
    fireEvent.click(screen.getByRole('link', { name: 'View stderr' }));

    expect(dock.openStep.mock.calls).toEqual([
      [{ step: 'build', stream: 'stdout' }],
      [{ step: 'build', stream: 'stderr' }],
    ]);
    // The view stays: nothing closes it.
    expect(screen.getByTestId('ww-step-facts')).toBeVisible();
  });

  it('leaves a Cmd or Ctrl+click on a log button to the log page link', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));

    const stdout = screen.getByRole('link', { name: 'View stdout' });
    const stderr = screen.getByRole('link', { name: 'View stderr' });
    expect(stdout).toHaveAttribute(
      'href',
      '/dags/example.yaml/log?remoteNode=edge&dagRunId=run-1&step=build'
    );
    expect(stderr).toHaveAttribute(
      'href',
      '/dags/example.yaml/log?remoteNode=edge&dagRunId=run-1&step=build&stream=stderr'
    );
    // fireEvent returns false when the handler prevented the default.
    expect(fireEvent.click(stdout, { metaKey: true })).toBe(true);
    expect(fireEvent.click(stderr, { ctrlKey: true })).toBe(true);
    expect(dock.openStep).not.toHaveBeenCalled();
    expect(fireEvent.click(stdout)).toBe(false);
    expect(dock.openStep).toHaveBeenCalledTimes(1);
  });

  it('gives the log buttons to onOpenLogs when the host passes it', () => {
    const onOpenLogs = vi.fn();
    renderView(details([node('build')]), facts({ step: 'build' }, onOpenLogs));

    fireEvent.click(screen.getByRole('link', { name: 'View stderr' }));

    expect(onOpenLogs).toHaveBeenCalledWith({
      step: 'build',
      stream: 'stderr',
    });
    expect(dock.openStep).not.toHaveBeenCalled();
  });

  it('opens the retry dialog from Retry step', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));

    fireEvent.click(screen.getByRole('button', { name: 'Retry step' }));
    expect(screen.getByText('Retry this step?')).toBeVisible();
  });

  it('opens the status update from Change status', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));

    fireEvent.click(screen.getByRole('button', { name: 'Change status' }));
    expect(screen.getByText('Update status')).toBeVisible();
  });

  // parity:run-single-step
  // The node view header owns the run action ('Run step'); the facts keep
  // no second button for it. The confirmation is tested with the actions.
  it('has no second Run step button next to the header one', () => {
    renderView(details([node('build')]), facts({ step: 'build' }));

    expect(
      screen.queryByRole('button', { name: 'Run step' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry step' })).toBeVisible();
  });

  it('offers retry while the run is active, but no status change', () => {
    renderView(
      details([node('build')], { status: Status.Running }),
      facts({ step: 'build' })
    );

    expect(screen.getByRole('button', { name: 'Retry step' })).toHaveAttribute(
      'title',
      'Retry unavailable while the DAG run is running.'
    );
    expect(
      screen.queryByRole('button', { name: 'Change status' })
    ).not.toBeInTheDocument();
  });

  it('offers no step action without permissions', () => {
    perms.value = { ...perms.value, canRunDags: false, canExecute: false };
    renderView(details([node('build')]), facts({ step: 'build' }));

    expect(
      screen.queryByRole('button', { name: 'Retry step' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Change status' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Run step' })
    ).not.toBeInTheDocument();
  });

  it('says when the step did not run or no run is selected', () => {
    const { unmount } = renderView(details([]), facts({ step: 'build' }));
    expect(screen.getByText('Not run in this run')).toBeVisible();
    unmount();

    renderView(null, facts({ step: 'build' }));
    expect(screen.getByText('No run selected')).toBeVisible();
  });

  // parity:drawer-foreach-items parity:run-step-drawer-foreach parity:api-foreach
  // parity:steplog-foreach-body
  it('lists foreach items and opens a body step log in the dock', () => {
    const summary = {
      item: '1',
      index: 1,
      key: 'b',
      status: NodeStatus.Failed,
      statusLabel: NodeStatusLabel.failed,
      startedAt: STARTED,
      finishedAt: FINISHED,
    };
    const item: ForeachItem = {
      ...summary,
      steps: [
        {
          name: 'body',
          status: NodeStatus.Failed,
          statusLabel: NodeStatusLabel.failed,
          hasStdout: true,
          hasStderr: false,
        },
      ],
    };
    api.foreachItems = { '1': item };
    api.foreachList = {
      total: 1,
      counts: {
        notStarted: 0,
        running: 0,
        succeeded: 0,
        failed: 1,
        aborted: 0,
      },
      items: [summary],
    } satisfies ForeachItemList;
    renderView(
      details([
        node('each', {
          status: NodeStatus.Failed,
          statusLabel: NodeStatusLabel.failed,
          step: {
            name: 'each',
            executorConfig: { type: 'foreach', config: {} },
          } as Node['step'],
        }),
      ]),
      facts({ step: 'each' })
    );

    fireEvent.click(screen.getByRole('button', { name: 'Item 1' }));
    fireEvent.click(
      screen.getByRole('button', { name: 'Open body in log viewer' })
    );

    expect(dock.openStep).toHaveBeenCalledWith({
      step: 'each',
      stream: 'stdout',
      foreach: { item: '1', bodyStep: 'body' },
    });
    expect(screen.queryByTestId('step-log-mount')).not.toBeInTheDocument();
  });
});

// parity:log-side-modal parity:run-log-step parity:test-execution-log
// parity:test-log-viewer-modal parity:api-step-log parity:common-step-log-foreach-hooks
describe('StepRunView logs', () => {
  // parity:steps-inline-log parity:run-step-inline-log
  it('mounts one StepLog when facts and logs show the same step', () => {
    renderView(
      details([node('build')]),
      <>
        {facts({ step: 'build' })}
        <StepRunView target={{ step: 'build' }} variant="logs" />
      </>
    );

    expect(screen.getAllByTestId('step-log-mount')).toHaveLength(1);
  });

  // parity:step-log-stream-modes parity:step-log-search parity:step-log-download-reload
  // parity:step-log-page-nav parity:log-view-mode parity:log-page-size parity:log-pagination
  // parity:log-line-count parity:log-reload parity:steplog-search parity:steplog-download
  // parity:log-chunk-render
  it('mounts StepLog with its stream, order, search and download controls', () => {
    renderView(
      details([node('build')]),
      <StepRunView target={{ step: 'build' }} variant="logs" />
    );

    expect(screen.getByTestId('step-log-mount')).toHaveAttribute(
      'data-dag-run',
      'run-1'
    );
    expect(screen.getByText('first output')).toBeVisible();
    expect(screen.getByRole('group', { name: 'Stream' })).toBeVisible();
    // The log of the workspace shows the latest lines only: no Page View.
    expect(screen.queryByRole('group', { name: 'Lines shown' })).toBeNull();
    expect(screen.queryByText('Page View')).toBeNull();
    expect(screen.getByRole('button', { name: 'Old to new' })).toBeVisible();
    expect(
      screen.getByPlaceholderText('Search in loaded lines...')
    ).toBeVisible();
    expect(screen.getByTitle('Download full log')).toBeVisible();
    expect(screen.getByTitle('Reload logs')).toBeVisible();
  });

  // The controls of the log sit inside the pane, not on its edges.
  it('insets the controls of the log from the edges of the pane', () => {
    renderView(
      details([node('build')]),
      <StepRunView target={{ step: 'build' }} variant="logs" />
    );
    const mount = screen.getByTestId('step-log-mount');
    expect(mount.closest('[data-testid="ww-step-log"]')).not.toBeNull();
    const slot = mount.parentElement!;
    expect(slot).toHaveClass('px-3', 'pb-2');
  });

  // parity:steplog-stream
  it('writes the stream switch to ?stream with replace', () => {
    renderView(
      details([node('build')]),
      <StepRunView target={{ step: 'build' }} variant="logs" />
    );
    fireEvent.click(
      within(screen.getByRole('group', { name: 'Stream' })).getByText('stderr')
    );

    expect(new URLSearchParams(where.search).get('stream')).toBe('stderr');
    expect(where.type).toBe('REPLACE');
  });

  // parity:steplog-live parity:step-log-live-follow parity:runlog-follow
  it('follows a running step live', () => {
    renderView(
      details(
        [
          node('build', {
            status: NodeStatus.Running,
            statusLabel: NodeStatusLabel.running,
            finishedAt: '',
          }),
        ],
        { status: Status.Running }
      ),
      <StepRunView target={{ step: 'build' }} variant="logs" />
    );

    expect(screen.getByRole('button', { name: 'LIVE' })).toBeVisible();
  });

  // parity:foreach-body-step-log
  it('mounts ForeachBodyStepLog for a foreach body step', () => {
    renderView(
      details([node('each')]),
      <StepRunView
        target={{
          step: 'each',
          stream: 'stderr',
          foreach: { item: '1', bodyStep: 'body' },
        }}
        variant="logs"
      />
    );

    expect(screen.getByTestId('foreach-body-log')).toBeInTheDocument();
    expect(foreachLog.props).toMatchObject({
      dagName: 'example',
      dagRunId: 'run-1',
      bodyStepName: 'body',
      foreach: { stepName: 'each', item: '1' },
      stream: 'stderr',
    });
    expect(screen.queryByTestId('step-log-mount')).not.toBeInTheDocument();
  });

  it('reads the step of a sub run through the sub-run query', () => {
    api.subRun = {
      dagRunDetails: details([node('inner')], {
        name: 'child',
        dagRunId: 'sub-1',
        rootDAGRunName: 'example',
        rootDAGRunId: 'run-1',
      }),
    };
    renderView(
      details([node('build')]),
      <StepRunView
        target={{
          step: 'inner',
          subRun: { dagName: 'child', subDAGRunId: 'sub-1' },
        }}
        variant="logs"
      />
    );

    expect(
      api.calls.find(([path]) => path === SUB_RUN_PATH)?.[1].params.path
    ).toEqual({ name: 'example', dagRunId: 'run-1', subDAGRunId: 'sub-1' });
    const mount = screen.getByTestId('step-log-mount');
    expect(mount).toHaveAttribute('data-dag-run', 'sub-1');
    expect(mount).toHaveAttribute('data-step', 'inner');
  });
});
