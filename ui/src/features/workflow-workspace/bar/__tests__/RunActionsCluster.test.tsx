// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, StatusLabel } from '@/api/v1/schema';
import type { ApiDAGDetails } from '../../contracts';
import { WorkspaceDataContext } from '../../data/context';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceRunValue,
} from '../../data/types';
import { RunActionsCluster } from '../RunActionsCluster';

const h = vi.hoisted(() => ({
  runDags: true,
  canWrite: true,
  // The definition that the run used, and the saved one.
  runSpec: 'name: etl\n',
  savedSpec: 'name: etl\n',
  editor: {
    handle: null as { replaceBuffer: ReturnType<typeof vi.fn> } | null,
    state: null as Record<string, unknown> | null,
  },
  client: { GET: vi.fn(), POST: vi.fn() },
  showToast: vi.fn(),
  showError: vi.fn(),
  setDockTab: vi.fn(),
}));

// The commands of the Run panel that the cluster calls.
vi.mock('../../dock/DockContext', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../dock/DockContext')>()),
  useDockCommands: () => ({ setTab: h.setDockTab }),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: h.runDags } }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => h.canWrite,
  useCanExecute: () => true,
  useCanExecuteForWorkspace: () => true,
  useIsAdmin: () => false,
  useCanManageProfiles: () => false,
  useCanManageWebhooks: () => false,
  useCanManageNotifications: () => false,
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => h.client,
  useQuery: (path: string, init: unknown) => {
    if (init === null) return { data: undefined, isLoading: false };
    if (path === '/dag-runs/{name}/{dagRunId}/spec') {
      return { data: { spec: h.runSpec }, isLoading: false };
    }
    if (path === '/dags/{fileName}/spec') {
      return { data: { spec: h.savedSpec }, isLoading: false };
    }
    return { data: undefined, isLoading: false };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => h.editor,
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: h.showToast }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: h.showError }),
}));

const FILE = 'etl.yaml';
const dag = { name: 'etl' } as ApiDAGDetails;

type RunCase =
  | 'running'
  | 'queued'
  | 'waiting-approval'
  | 'failed-auto-retry'
  | 'failed'
  | 'succeeded';

function details(kind: RunCase): ApiDAGRunDetails {
  const base = {
    name: 'etl',
    dagRunId: 'run-1',
    autoRetryCount: 0,
    startedAt: '',
    finishedAt: '',
    nodes: [],
  };
  switch (kind) {
    case 'running':
      return {
        ...base,
        status: Status.Running,
        statusLabel: StatusLabel.running,
      } as unknown as ApiDAGRunDetails;
    case 'queued':
      return {
        ...base,
        status: Status.Queued,
        statusLabel: StatusLabel.queued,
      } as unknown as ApiDAGRunDetails;
    case 'waiting-approval':
      return {
        ...base,
        status: Status.Waiting,
        statusLabel: StatusLabel.waiting,
        nodes: [
          {
            step: { name: 'approve', approval: {} },
            status: NodeStatus.Waiting,
          },
        ],
      } as unknown as ApiDAGRunDetails;
    case 'failed-auto-retry':
      return {
        ...base,
        status: Status.Failed,
        statusLabel: StatusLabel.failed,
        autoRetryCount: 1,
        autoRetryLimit: 3,
      } as unknown as ApiDAGRunDetails;
    case 'failed':
      return {
        ...base,
        status: Status.Failed,
        statusLabel: StatusLabel.failed,
      } as unknown as ApiDAGRunDetails;
    case 'succeeded':
      return {
        ...base,
        status: Status.Success,
        statusLabel: StatusLabel.succeeded,
      } as unknown as ApiDAGRunDetails;
  }
}

function runValue(selected: ApiDAGRunDetails | null) {
  return {
    selection: { runId: 'run-1' },
    ref: selected ? { name: selected.name, runId: selected.dagRunId } : null,
    details: selected,
    hasRun: Boolean(selected),
    isLatest: false,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => undefined),
  } as unknown as WorkspaceRunValue;
}

function DataProvider({
  run,
  children,
}: {
  run: WorkspaceRunValue;
  children: ReactNode;
}) {
  return (
    <WorkspaceDataContext.Provider
      value={{
        fileName: FILE,
        dagData: { dag } as unknown as DAGPageData,
        workspaceName: null,
        remoteNode: 'local',
        run,
        refresh: vi.fn(async () => undefined),
        registerHistory: () => () => {},
      }}
    >
      {children}
    </WorkspaceDataContext.Provider>
  );
}

function Where() {
  const { pathname, search } = useLocation();
  return <output data-testid="where">{pathname + search}</output>;
}

function tree(run: WorkspaceRunValue, url: string) {
  const cluster = (
    <DataProvider run={run}>
      <Where />
      <RunActionsCluster fileName={FILE} dag={dag} workspaceName={null} />
    </DataProvider>
  );
  return (
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/dags/:fileName/:tab?" element={cluster} />
      </Routes>
    </MemoryRouter>
  );
}

function setup(
  selected: ApiDAGRunDetails | null,
  url = '/dags/etl.yaml?run=run-1'
) {
  const run = runValue(selected);
  render(tree(run, url));
  return run;
}

// The name of a button: the label of an icon, else its (visually hidden) text.
function buttonNames(): string[] {
  return screen
    .queryAllByRole('button')
    .map((b) => b.getAttribute('aria-label') ?? b.textContent ?? '');
}

const COPY = "Revert to this run's YAML";

beforeEach(() => {
  vi.clearAllMocks();
  h.runDags = true;
  h.canWrite = true;
  h.runSpec = 'name: etl\n';
  h.savedSpec = 'name: etl\n';
  h.editor = {
    handle: { replaceBuffer: vi.fn() },
    state: { dirty: false, editable: true, loaded: true },
  };
  h.client.GET.mockResolvedValue({ data: {} });
  h.client.POST.mockResolvedValue({ data: {} });
});

describe('RunActionsCluster', () => {
  // parity:actions-state-matrix-workflow
  // parity:run-actions-visibility
  // parity:test-dag-actions
  // parity:test-run-header-meta
  // Stop, Cancel, Reject, Retry, Dequeue and Start are in the slot of Start
  // (RunSplitButton.test). The cluster keeps the Action required button and
  // the revert to the YAML of the run.
  it.each<[RunCase, string[]]>([
    ['running', []],
    ['queued', []],
    ['waiting-approval', ['Action required']],
    ['failed-auto-retry', []],
    ['failed', []],
    ['succeeded', []],
  ])('with runDags, a %s run shows %j', (kind, names) => {
    setup(details(kind));
    expect(buttonNames()).toEqual(names);
    for (const name of names) {
      expect(screen.getByRole('button', { name })).toBeEnabled();
    }
  });

  // parity:actions-permission-gate
  it.each<RunCase>([
    'running',
    'queued',
    'waiting-approval',
    'failed-auto-retry',
    'failed',
    'succeeded',
  ])('without runDags, a %s run shows nothing', (kind) => {
    h.runDags = false;
    setup(details(kind));
    expect(screen.queryByTestId('ww-run-actions')).toBeNull();
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });

  // The approvals and the human tasks of the run are in the Actions tab.
  it('opens the Actions tab of a run that waits for a person', () => {
    setup(details('waiting-approval'));
    fireEvent.click(screen.getByRole('button', { name: 'Action required' }));
    expect(h.setDockTab).toHaveBeenCalledWith('actions', { user: true });
  });

  // The Actions tab lists the actions of the runs that the steps called too.
  it('leads a run that waits in a sub-run to the Actions tab', () => {
    setup({
      ...details('running'),
      status: Status.Waiting,
      statusLabel: StatusLabel.waiting,
      nodes: [
        {
          step: { name: 'notify', call: 'notify' },
          status: NodeStatus.Waiting,
          subRuns: [{ dagRunId: 'sub-1', params: '' }],
        },
      ],
    } as unknown as ApiDAGRunDetails);
    fireEvent.click(screen.getByRole('button', { name: 'Action required' }));
    expect(h.setDockTab).toHaveBeenCalledWith('actions', { user: true });
  });

  // No task waits once a resume of the human tasks failed; its retry does.
  it('leads a run whose resume failed to the Actions tab', () => {
    setup({
      ...details('running'),
      status: Status.Waiting,
      statusLabel: StatusLabel.waiting,
      humanTaskResumePending: true,
      nodes: [],
    } as unknown as ApiDAGRunDetails);
    fireEvent.click(screen.getByRole('button', { name: 'Action required' }));
    expect(h.setDockTab).toHaveBeenCalledWith('actions', { user: true });
  });

  it('is absent for a workflow that never ran', () => {
    setup(null);
    expect(screen.queryByTestId('ww-run-actions')).toBeNull();
  });

  // Stop, Cancel, Reject, Retry and Dequeue have the slot of Start, not a
  // place in the cluster, whatever the status of the run.
  it.each<RunCase>([
    'running',
    'queued',
    'waiting-approval',
    'failed-auto-retry',
    'failed',
    'succeeded',
  ])('has no Stop, Cancel, Reject, Retry or Dequeue for a %s run', (kind) => {
    setup(details(kind));
    expect(screen.queryByTestId('ww-run-actions-slot')).toBeNull();
    for (const name of ['Stop', 'Cancel', 'Reject', 'Retry', 'Dequeue']) {
      expect(screen.queryByRole('button', { name })).toBeNull();
    }
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  // The selected run can be a past one or the active one: the cluster is the
  // same for the selection, and the bar shows it on the one tab.
  it('shows no icon of a past run that finished', () => {
    setup(details('succeeded'), '/dags/etl.yaml?run=run-1');
    expect(buttonNames()).toEqual([]);
  });

  it('shows no icon of the active run', () => {
    setup(details('running'), '/dags/etl.yaml');
    expect(buttonNames()).toEqual([]);
  });

  // parity:test-run-header-meta
  it('flattens the actions: no menu, no run page link, no Open in editor', () => {
    setup(details('waiting-approval'));
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Open in editor' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'More actions' })).toBeNull();
  });

  // parity:chg-no-edit-spec-retry
  it('has no Edit spec and retry button', () => {
    setup(details('failed'));
    expect(
      screen.queryByRole('button', { name: 'Edit spec and retry' })
    ).toBeNull();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  describe("Revert to this run's YAML", () => {
    beforeEach(() => {
      h.runSpec = 'name: etl\nsteps: [a]\n';
    });

    // The icon follows Action required, when the run waits for a person.
    // parity:test-run-header-meta
    it('shows after Action required while the run is stale', () => {
      setup(details('waiting-approval'));
      expect(buttonNames()).toEqual(['Action required', COPY]);
    });

    it('is the only icon of a stale run that needs no action', () => {
      setup(details('failed'));
      expect(buttonNames()).toEqual([COPY]);
    });

    it('is the only icon of a stale queued run', () => {
      setup(details('queued'));
      expect(buttonNames()).toEqual([COPY]);
    });

    it('is absent while the run used the saved definition', () => {
      h.runSpec = h.savedSpec;
      setup(details('failed'));
      expect(screen.queryByRole('button', { name: COPY })).toBeNull();
    });

    it('is absent without write access', () => {
      h.canWrite = false;
      setup(details('failed'));
      expect(screen.queryByRole('button', { name: COPY })).toBeNull();
      expect(buttonNames()).toEqual([]);
    });

    it('is the only icon of a writer without runDags', () => {
      h.runDags = false;
      setup(details('failed'));
      expect(buttonNames()).toEqual([COPY]);
    });

    it('puts the run definition in a clean editor and stays on the run', () => {
      setup(details('failed'));
      fireEvent.click(screen.getByRole('button', { name: COPY }));
      expect(screen.queryByRole('dialog')).toBeNull();
      expect(h.editor.handle!.replaceBuffer).toHaveBeenCalledWith(
        'name: etl\nsteps: [a]\n'
      );
      expect(screen.getByTestId('where')).toHaveTextContent(
        '/dags/etl.yaml?run=run-1'
      );
    });

    it('ends the run definition view after the copy', () => {
      setup(details('failed'), '/dags/etl.yaml?run=run-1&def=run');
      fireEvent.click(screen.getByRole('button', { name: COPY }));
      expect(screen.getByTestId('where')).toHaveTextContent(
        '/dags/etl.yaml?run=run-1'
      );
      expect(screen.getByTestId('where')).not.toHaveTextContent('def=run');
    });

    it('asks before it replaces unsaved changes', async () => {
      h.editor.state = { dirty: true, editable: true, loaded: true };
      setup(details('failed'));
      fireEvent.click(screen.getByRole('button', { name: COPY }));
      const dialog = await screen.findByRole('dialog');
      expect(dialog).toHaveTextContent(
        'The editor has unsaved changes. The definition of this run replaces them.'
      );
      expect(h.editor.handle!.replaceBuffer).not.toHaveBeenCalled();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Replace' }));
      expect(h.editor.handle!.replaceBuffer).toHaveBeenCalledTimes(1);
    });

    // The parser can freeze the editor while the question is open.
    it('replaces nothing when the editor froze during the question', async () => {
      h.editor.state = { dirty: true, editable: true, loaded: true };
      const run = runValue(details('failed'));
      const url = '/dags/etl.yaml?run=run-1';
      const view = render(tree(run, url));
      fireEvent.click(screen.getByRole('button', { name: COPY }));
      const dialog = await screen.findByRole('dialog');

      h.editor = { ...h.editor, state: { ...h.editor.state, frozen: true } };
      view.rerender(tree(run, url));
      fireEvent.click(within(dialog).getByRole('button', { name: 'Replace' }));

      expect(h.editor.handle!.replaceBuffer).not.toHaveBeenCalled();
    });

    it.each([
      ['cannot be edited', { editable: false }],
      ['is frozen', { frozen: true }],
    ])('is disabled while the editor %s', (_label, patch) => {
      h.editor.state = { dirty: false, editable: true, ...patch };
      setup(details('failed'));
      expect(screen.getByRole('button', { name: COPY })).toBeDisabled();
    });
  });
});
