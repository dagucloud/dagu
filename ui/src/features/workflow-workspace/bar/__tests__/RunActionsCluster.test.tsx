// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
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
    .getAllByRole('button')
    .map((b) => b.getAttribute('aria-label') ?? b.textContent ?? '');
}

const COPY = 'Copy to editor';

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
  it.each<[RunCase, string[]]>([
    ['running', ['Stop']],
    ['queued', ['Dequeue']],
    ['waiting-approval', ['Reject']],
    ['failed-auto-retry', ['Cancel', 'Retry']],
    ['failed', ['Retry']],
    ['succeeded', ['Retry']],
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

  it('is absent for a workflow that never ran', () => {
    setup(null);
    expect(screen.queryByTestId('ww-run-actions')).toBeNull();
  });

  // parity:actions-stop
  // parity:api-stop
  it('stops the selected run, or every running instance', async () => {
    const run = setup(details('running'));

    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    let dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
    await waitFor(() => expect(run.refresh).toHaveBeenCalledTimes(1));
    expect(h.client.POST).toHaveBeenLastCalledWith(
      '/dag-runs/{name}/{dagRunId}/stop',
      {
        params: {
          query: { remoteNode: 'local' },
          path: { name: 'etl', dagRunId: 'run-1' },
        },
      }
    );

    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    dialog = screen.getByRole('dialog');
    fireEvent.click(
      within(dialog).getByRole('checkbox', {
        name: 'Stop all running instances',
      })
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
    await waitFor(() => expect(run.refresh).toHaveBeenCalledTimes(2));
    expect(h.client.POST).toHaveBeenLastCalledWith(
      '/dags/{fileName}/stop-all',
      {
        params: {
          path: { fileName: FILE },
          query: { remoteNode: 'local' },
        },
      }
    );
  });

  // parity:actions-cancel-auto-retry
  it('cancels the pending auto-retry of the selected run', async () => {
    setup(details('failed-auto-retry'));

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    const dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByRole('heading', { name: 'Cancel auto-retry?' })
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole('checkbox')).toBeNull();
    const [, submit] = within(dialog).getAllByRole('button', {
      name: 'Cancel',
    });
    fireEvent.click(submit!);
    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/stop',
        expect.anything()
      )
    );
  });

  // parity:actions-reject
  // parity:run-action-reject
  it('rejects the waiting approval of the selected run', async () => {
    const run = setup(details('waiting-approval'));

    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByRole('textbox'), {
      target: { value: 'not now' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reject' }));
    await waitFor(() => expect(run.refresh).toHaveBeenCalled());
    expect(h.client.POST).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/reject',
      {
        params: {
          path: { name: 'etl', dagRunId: 'run-1', stepName: 'approve' },
          query: { remoteNode: 'local' },
        },
        body: { reason: 'not now' },
      }
    );
  });

  it('offers Reschedule in the Retry confirm', () => {
    setup(details('succeeded'));

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    const dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByRole('checkbox', { name: 'Reschedule as a new run' })
    ).toBeInTheDocument();
  });

  // The removed run must not stay in the URL as a broken link.
  it('selects the latest run after a dequeue', async () => {
    const run = setup(details('queued'));
    fireEvent.click(screen.getByRole('button', { name: 'Dequeue' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Dequeue' }));
    await waitFor(() =>
      expect(run.select).toHaveBeenCalledWith('latest', { replace: true })
    );
  });

  it('shows the new run after a reschedule', async () => {
    h.client.POST.mockResolvedValue({ data: { dagRunId: 'run-9' } });
    setup(details('failed'));
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(
      within(dialog).getByRole('checkbox', { name: 'Reschedule as a new run' })
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reschedule' }));
    await waitFor(() =>
      expect(screen.getByTestId('where')).toHaveTextContent(
        '/dags/etl.yaml?run=run-9'
      )
    );
  });

  // A name override makes a run of another DAG, which this workflow does not
  // list: the page stays on the run that it shows.
  it('stays on the run after a reschedule under another DAG name', async () => {
    h.client.POST.mockResolvedValue({ data: { dagRunId: 'run-9' } });
    setup(details('failed'));
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(
      within(dialog).getByRole('checkbox', { name: 'Reschedule as a new run' })
    );
    fireEvent.change(
      within(dialog).getByLabelText('DAG Name Override (optional)'),
      { target: { value: 'etl-copy' } }
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reschedule' }));
    await waitFor(() =>
      expect(h.showToast).toHaveBeenCalledWith(expect.stringContaining('run-9'))
    );
    expect(screen.getByTestId('where')).toHaveTextContent(
      '/dags/etl.yaml?run=run-1'
    );
  });

  it('dequeues only a queued run', async () => {
    const run = setup(details('queued'));

    fireEvent.click(screen.getByRole('button', { name: 'Dequeue' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Dequeue' }));
    await waitFor(() => expect(run.refresh).toHaveBeenCalled());
    expect(h.client.GET).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/dequeue',
      {
        params: {
          path: { name: 'etl', dagRunId: 'run-1' },
          query: { remoteNode: 'local' },
        },
      }
    );
  });

  // The selected run can be a past one or the active one: the icons are the
  // same for the selection, and the bar shows them on the one tab.
  it('shows the icons of a past run: Retry', () => {
    setup(details('succeeded'), '/dags/etl.yaml?run=run-1');
    expect(buttonNames()).toEqual(['Retry']);
  });

  it('shows the icons of the active run: Stop', () => {
    setup(details('running'), '/dags/etl.yaml');
    expect(buttonNames()).toEqual(['Stop']);
  });

  // parity:test-run-header-meta
  it('flattens the actions: no menu, no run page link, no Open in editor', () => {
    setup(details('failed'));
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

  describe('Copy to editor', () => {
    beforeEach(() => {
      h.runSpec = 'name: etl\nsteps: [a]\n';
    });

    // The icon follows the run actions, with the editor icon.
    // parity:test-run-header-meta
    it('shows after the run actions while the run is stale', () => {
      setup(details('failed'));
      expect(buttonNames()).toEqual(['Retry', COPY]);
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
      expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible();
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
