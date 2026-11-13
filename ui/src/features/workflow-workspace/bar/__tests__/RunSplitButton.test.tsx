// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, type ReactNode } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  RestartPolicy,
  ServiceDAGState,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import {
  UnsavedChangesProvider,
  useUnsavedChanges,
} from '@/contexts/UnsavedChangesContext';
import {
  WorkspaceEditorProvider,
  useWorkspaceEditorBinding,
} from '../../canvas/WorkspaceEditorContext';
import type {
  ApiDAGDetails,
  WorkspaceEditorHandle,
  WorkspaceEditorState,
} from '../../contracts';
import { barControlAvailable } from '../../commands/WorkflowCommands';
import { WorkspaceDataContext } from '../../data/context';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceRunValue,
} from '../../data/types';
import { RunActionsCluster } from '../RunActionsCluster';
import { RunStartProvider } from '../RunStartContext';
import { RunSplitButton } from '../RunSplitButton';

const h = vi.hoisted(() => ({
  runDags: true,
  canExecute: true,
  client: { GET: vi.fn(), POST: vi.fn() },
  showToast: vi.fn(),
  showError: vi.fn(),
  pushRunProgress: vi.fn(),
  serviceDags: [] as { fileName: string; state: ServiceDAGState }[],
  serviceRead: vi.fn(),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ permissions: { runDags: h.runDags } }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => true,
  useCanExecute: () => h.canExecute,
  useCanExecuteForWorkspace: () => true,
  useIsAdmin: () => false,
  useCanManageProfiles: () => false,
  useCanManageWebhooks: () => false,
  useCanManageNotifications: () => false,
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => h.client,
  useQuery: () => ({ data: undefined, isLoading: false }),
}));
// The read of the Workflows list: one request for the list and the header. It
// reads nothing while it is not enabled, as the hook does.
vi.mock('@/features/dag-runs/hooks/useServiceDAGs', async (importOriginal) => ({
  ...(await importOriginal<object>()),
  useServiceDAGs: (...args: unknown[]) => {
    h.serviceRead(...args);
    return args[1] ? h.serviceDags : [];
  },
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: h.showToast }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: h.showError }),
}));
vi.mock('@/features/dags/components/dag-execution/RunProgressStack', () => ({
  pushRunProgress: h.pushRunProgress,
}));
// The dialog itself is covered by RunStartContext.test.
vi.mock('@/features/dags/components/dag-execution/StartDAGModal', () => ({
  default: (p: {
    visible: boolean;
    onSubmit(params: string, id?: string, immediate?: boolean): Promise<void>;
  }) =>
    p.visible ? (
      <div data-testid="start-modal">
        <button type="button" onClick={() => void p.onSubmit('', '', true)}>
          Submit start
        </button>
        <button type="button" onClick={() => void p.onSubmit('', '', false)}>
          Submit enqueue
        </button>
      </div>
    ) : null,
}));

// The dialog itself is covered by its own tests: this stub shows what the
// button gives it and ends the retry as the dialog does.
vi.mock('../EditRetryDialog', () => ({
  EditRetryDialog: (p: {
    open: boolean;
    dagName: string;
    dagRunId: string;
    onClose(): void;
    onRetried(dagRunId: string): void;
  }) =>
    p.open ? (
      <div data-testid="edit-retry" data-run={`${p.dagName}/${p.dagRunId}`}>
        <button type="button" onClick={() => p.onRetried('run-9')}>
          Finish edit retry
        </button>
      </div>
    ) : null,
}));

const FILE = 'etl.yaml';
const dag = { name: 'etl' } as ApiDAGDetails;

function runDetails(
  dagRunId: string,
  status: Status = Status.Success,
  extra: Partial<ApiDAGRunDetails> = {}
): ApiDAGRunDetails {
  return {
    name: 'etl',
    dagRunId,
    status,
    statusLabel: StatusLabel.succeeded,
    autoRetryCount: 0,
    startedAt: '',
    finishedAt: '',
    nodes: [],
    ...extra,
  } as unknown as ApiDAGRunDetails;
}

interface Setup {
  pinned?: string | null;
  details?: ApiDAGRunDetails | null;
  latest?: ApiDAGRunDetails;
  dirtyFlag?: boolean;
  /** An editor offers its save to the unsaved context. */
  canSave?: boolean;
  editor?: { dirty: boolean; buffer: string } | null;
  cluster?: boolean;
  url?: string;
  /** The definition of the workflow; a plain one by default. */
  dag?: ApiDAGDetails;
}

function EditorBinder({ dirty, buffer }: { dirty: boolean; buffer: string }) {
  const { editorHandleRef, onEditorStateChange } = useWorkspaceEditorBinding();
  useEffect(() => {
    editorHandleRef({
      getBuffer: () => buffer,
      isDirty: () => dirty,
    } as unknown as WorkspaceEditorHandle);
    onEditorStateChange({
      dirty,
      editable: true,
      loaded: true,
    } as WorkspaceEditorState);
  }, [buffer, dirty, editorHandleRef, onEditorStateChange]);
  return null;
}

function DirtyFlag({ on }: { on: boolean }) {
  const { setHasUnsavedChanges } = useUnsavedChanges();
  useEffect(() => setHasUnsavedChanges(on), [on, setHasUnsavedChanges]);
  return null;
}

/** Offers a save to the unsaved context, as an editor of a writer does. */
function Saver() {
  const { registerSave } = useUnsavedChanges();
  useEffect(() => registerSave(async () => true), [registerSave]);
  return null;
}

function Where() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {location.pathname + location.search}
    </output>
  );
}

function setup({
  pinned = null,
  details,
  latest = runDetails('run-3'),
  dirtyFlag = false,
  canSave = false,
  editor = null,
  cluster = false,
  url = `/dags/${FILE}`,
  dag: dagDef = dag,
}: Setup = {}) {
  const selected = details === undefined ? latest : details;
  const run = {
    selection: pinned ? { runId: pinned } : 'latest',
    ref: selected ? { name: selected.name, runId: selected.dagRunId } : null,
    details: selected,
    hasRun: Boolean(selected),
    isLatest: !pinned,
    isActive: selected?.status === Status.Running,
    notFound: false,
    latestRef: null,
    overlay: {},
    nodeByStep: new Map(),
    handlers: [],
    waiting: {
      approvals: 0,
      humanTasks: 0,
      agent: 0,
      firstWaitingStep: null,
    },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => undefined),
  } as unknown as WorkspaceRunValue;
  const refresh = vi.fn(async () => undefined);
  const dagData = {
    dag: dagDef,
    latestDAGRun: latest,
    localDags: [],
    suspended: false,
    pinned: false,
  } as unknown as DAGPageData;

  function Tree({ children }: { children: ReactNode }) {
    return (
      <UnsavedChangesProvider>
        <WorkspaceDataContext.Provider
          value={{
            fileName: FILE,
            dagData,
            workspaceName: null,
            remoteNode: 'local',
            run,
            refresh,
            registerHistory: () => () => {},
          }}
        >
          <WorkspaceEditorProvider>
            <RunStartProvider>
              <DirtyFlag on={dirtyFlag} />
              {canSave && <Saver />}
              {editor && <EditorBinder {...editor} />}
              {children}
            </RunStartProvider>
          </WorkspaceEditorProvider>
        </WorkspaceDataContext.Provider>
      </UnsavedChangesProvider>
    );
  }

  const page = (
    <Tree>
      {cluster && (
        <RunActionsCluster fileName={FILE} dag={dagDef} workspaceName={null} />
      )}
      <RunSplitButton fileName={FILE} dag={dagDef} workspaceName={null} />
    </Tree>
  );
  const view = render(
    <MemoryRouter initialEntries={[url]}>
      <Where />
      <Routes>
        <Route path="/dags/:fileName" element={page} />
        <Route path="/dags/:fileName/:tab" element={page} />
      </Routes>
    </MemoryRouter>
  );
  return { ...view, run, refresh };
}

async function openMenu() {
  await userEvent.click(
    screen.getByRole('button', { name: 'More run options' })
  );
  return screen.findByRole('menu');
}

// jsdom has no stylesheet: a control under a 'hidden' class (display: none)
// still has a role. Start is mounted and hidden while Stop or Retry has its
// slot, so the queries of a shown control skip it.
function shown(buttons: HTMLElement[]): HTMLElement[] {
  return buttons.filter((b) => !b.closest('.hidden'));
}

function shownButtons(scope: HTMLElement = document.body): HTMLElement[] {
  return shown(within(scope).queryAllByRole('button'));
}

function shownByName(name: string, scope: HTMLElement = document.body) {
  return shown(within(scope).queryAllByRole('button', { name }));
}

const failedRun = (id = 'run-3') =>
  runDetails(id, Status.Failed, { statusLabel: StatusLabel.failed });

beforeEach(() => {
  vi.clearAllMocks();
  h.runDags = true;
  h.canExecute = true;
  h.serviceDags = [];
  h.client.GET.mockResolvedValue({ data: { dag: { name: 'etl' } } });
  h.client.POST.mockResolvedValue({
    data: { dagRunId: 'new-run' },
    response: { status: 200 },
  });
});

describe('RunSplitButton', () => {
  // parity:actions-start
  // parity:actions-split-copies
  // parity:test-start-modal
  it("opens the Start dialog from 'Run…', the start-only DAGActions", async () => {
    setup({ latest: runDetails('run-3', Status.Success) });

    // The only DAGActions copy is start only: no Stop, Retry or Reject. The
    // second button is the caret of the draft menu, an icon without text.
    const split = screen.getByTestId('ww-run-split');
    expect(
      within(split)
        .getAllByRole('button')
        .map((b) => b.textContent)
    ).toEqual(['Start', '']);
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect(await screen.findByTestId('start-modal')).toBeInTheDocument();
    expect(screen.queryByText('Unsaved Changes')).toBeNull();
  });

  // The bar has no room for a word: Start is an icon, named for the reader.
  // Like Retry, Start says its name on a wide bar (container query) and is
  // an icon below.
  it('shows Start with its name, as text on a wide bar only', () => {
    setup({ latest: runDetails('run-3', Status.Success) });

    const start = screen.getByRole('button', { name: 'Start' });
    expect(start).toHaveAttribute('aria-label', 'Start');
    expect(start.querySelector('svg')).not.toBeNull();
    expect(within(start).getByText('Start').closest('.sr-only')).toHaveClass(
      '@7xl:not-sr-only'
    );
  });

  // The palette commands Run…, Stop and Retry click the bar controls that
  // carry data-ww-action. Stop or Retry has the slot of Start while the run is
  // active or failed; the hidden Start stays, so that Run… still opens its
  // dialog.
  it('marks Start, Stop and Retry for the command palette', () => {
    const startControl = () =>
      document.querySelector<HTMLElement>('[data-ww-action="start"]');

    const running = setup({ latest: runDetails('run-3', Status.Running) });
    expect(startControl()!.closest('.hidden')).not.toBeNull();
    expect(barControlAvailable('stop')).toBe(true);
    expect(
      document.querySelector('[data-ww-action="stop"]')
    ).toHaveAccessibleName('Stop');
    expect(document.querySelector('[data-ww-action="retry"]')).toBeNull();
    expect(barControlAvailable('retry')).toBe(false);
    running.unmount();

    const failed = setup({ latest: failedRun() });
    expect(startControl()!.closest('.hidden')).not.toBeNull();
    expect(barControlAvailable('stop')).toBe(false);
    expect(barControlAvailable('retry')).toBe(true);
    expect(
      document.querySelector('[data-ww-action="retry"]')
    ).toHaveAccessibleName('Retry');
    // The palette clicks the hidden Start as a script does.
    expect(barControlAvailable('start')).toBe(true);
    act(() => startControl()!.click());
    expect(screen.getByTestId('start-modal')).toBeInTheDocument();
    failed.unmount();

    setup({ latest: runDetails('run-3', Status.Success) });
    expect(startControl()).toHaveTextContent('Start');
    expect(startControl()!.closest('.hidden')).toBeNull();
    expect(barControlAvailable('start')).toBe(true);
    expect(barControlAvailable('stop')).toBe(false);
    expect(barControlAvailable('retry')).toBe(false);
    act(() => startControl()!.click());
    expect(screen.getByTestId('start-modal')).toBeInTheDocument();
  });

  // The displayed run is active: Stop sits in the slot of Start, which is hidden.
  it('puts Stop in the slot of Start while the displayed run runs', () => {
    setup({
      cluster: true,
      latest: runDetails('run-3', Status.Running, {
        statusLabel: StatusLabel.running,
      }),
    });

    const slot = screen.getByTestId('ww-run-start-slot');
    expect(within(slot).getByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(shownByName('Start')).toHaveLength(0);
    expect(shownByName('Retry')).toHaveLength(0);
    // Stop is not in the cluster: it has the one Stop button of the bar.
    expect(screen.getAllByRole('button', { name: 'Stop' })).toHaveLength(1);
  });

  // The displayed run is a past one: Start is back and Stop is gone, even
  // while the latest run is active.
  it('puts Start back in its slot for a finished run', () => {
    setup({
      cluster: true,
      pinned: 'run-1',
      details: runDetails('run-1', Status.Success),
      latest: runDetails('run-3', Status.Running),
    });

    const slot = screen.getByTestId('ww-run-start-slot');
    expect(within(slot).getByRole('button', { name: 'Start' })).toBeEnabled();
    expect(shownByName('Start', slot)).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });

  // A queued run has Dequeue in the slot of Start, not in the cluster.
  describe('Dequeue in the slot of Start', () => {
    const queued = () =>
      runDetails('run-3', Status.Queued, { statusLabel: StatusLabel.queued });

    it('replaces Start for a queued run, not in the cluster', () => {
      setup({ cluster: true, latest: queued() });

      const slot = screen.getByTestId('ww-run-start-slot');
      expect(
        within(slot).getByRole('button', { name: 'Dequeue' })
      ).toBeEnabled();
      expect(screen.getAllByRole('button', { name: 'Dequeue' })).toHaveLength(
        1
      );
      expect(shownByName('Start')).toHaveLength(0);
      expect(shownByName('Retry')).toHaveLength(0);
      expect(shownByName('Stop')).toHaveLength(0);
      // Start stays mounted and hidden.
      expect(
        within(slot)
          .getAllByRole('button', { name: 'Start' })
          .filter((b) => b.closest('.hidden'))
      ).toHaveLength(1);
      expect(
        shownButtons(screen.getByTestId('ww-run-split')).map(
          (b) => b.getAttribute('aria-label') ?? b.textContent
        )
      ).toEqual(['Dequeue', 'More run options']);
    });

    it('dequeues the displayed run after a confirmation', async () => {
      const view = setup({ latest: queued() });

      fireEvent.click(screen.getByRole('button', { name: 'Dequeue' }));
      fireEvent.click(
        within(screen.getByRole('dialog')).getByRole('button', {
          name: 'Dequeue',
        })
      );
      await waitFor(() => expect(view.run.refresh).toHaveBeenCalled());
      expect(h.client.GET).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/dequeue',
        expect.objectContaining({
          params: expect.objectContaining({
            path: { name: 'etl', dagRunId: 'run-3' },
          }),
        })
      );
    });

    // The caret menu has the Start item only while Retry has the slot.
    it('has no Start item in the caret menu', async () => {
      setup({ latest: queued() });

      const menu = await openMenu();
      expect(
        within(menu).queryByRole('menuitem', { name: 'Start' })
      ).toBeNull();
    });

    // Only a queued run is dequeued: any other state has its own button.
    it.each([
      ['succeeded', Status.Success],
      ['aborted', Status.Aborted],
      ['partially succeeded', Status.PartialSuccess],
      ['failed', Status.Failed],
      ['running', Status.Running],
    ])('has no Dequeue for a %s run', (_label, status) => {
      setup({ cluster: true, latest: runDetails('run-3', status) });
      expect(screen.queryByRole('button', { name: 'Dequeue' })).toBeNull();
    });
  });

  // Retry is for a failed run only, and it replaces Start in its slot.
  describe('Retry in the slot of Start', () => {
    it('replaces Start for a failed run, not in the cluster', () => {
      setup({ cluster: true, latest: failedRun() });

      const slot = screen.getByTestId('ww-run-start-slot');
      expect(within(slot).getByRole('button', { name: 'Retry' })).toBeEnabled();
      expect(shownByName('Start')).toHaveLength(0);
      expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
      // The cluster has no Retry: the one Retry button is in the slot.
      expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
      // The shown buttons of the group: Retry and the caret.
      expect(
        shownButtons(screen.getByTestId('ww-run-split')).map(
          (b) => b.getAttribute('aria-label') ?? b.textContent
        )
      ).toEqual(['Retry', 'More run options']);
    });

    // Start is mounted under a 'hidden' class, in the slot.
    it('keeps Start mounted and hidden', () => {
      setup({ latest: failedRun() });

      const slot = screen.getByTestId('ww-run-start-slot');
      const hidden = within(slot)
        .getAllByRole('button', { name: 'Start' })
        .filter((b) => b.closest('.hidden'));
      expect(hidden).toHaveLength(1);
    });

    it('is for the displayed run, a past one too', () => {
      setup({
        pinned: 'run-1',
        details: failedRun('run-1'),
        latest: runDetails('run-3', Status.Success),
      });

      expect(shownByName('Retry')).toHaveLength(1);
      expect(shownByName('Start')).toHaveLength(0);
    });

    it('is absent while the displayed run is a success after a failed one', () => {
      setup({
        pinned: 'run-1',
        details: runDetails('run-1', Status.Success),
        latest: failedRun(),
      });

      expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
      expect(shownByName('Start')).toHaveLength(1);
    });

    // Only a failed run can be retried from the bar.
    it.each([
      ['succeeded', Status.Success],
      ['aborted', Status.Aborted],
      ['partially succeeded', Status.PartialSuccess],
      ['queued', Status.Queued],
      ['running', Status.Running],
      ['waiting', Status.Waiting],
    ])('has no Retry for a %s run', (_label, status) => {
      setup({ cluster: true, latest: runDetails('run-3', status) });

      expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
      expect(document.querySelector('[data-ww-action="retry"]')).toBeNull();
      expect(barControlAvailable('retry')).toBe(false);
    });

    it.each([
      ['succeeded', Status.Success],
      ['aborted', Status.Aborted],
      ['partially succeeded', Status.PartialSuccess],
    ])('keeps Start in the slot for a %s run', (_label, status) => {
      setup({ latest: runDetails('run-3', status) });

      const slot = screen.getByTestId('ww-run-start-slot');
      expect(shownByName('Start', slot)).toHaveLength(1);
      expect(within(slot).getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    // A failed run that waits for its auto-retry has Cancel in the slot.
    it('gives the slot to Cancel, not Retry, for a pending auto-retry', () => {
      setup({
        latest: runDetails('run-3', Status.Failed, {
          statusLabel: StatusLabel.failed,
          autoRetryCount: 1,
          autoRetryLimit: 3,
        }),
      });

      expect(shownByName('Cancel')).toHaveLength(1);
      expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    });

    // parity:retry-modal
    // parity:retry-modal-reschedule
    it('retries the selected run, with Reschedule', async () => {
      setup({
        cluster: true,
        pinned: 'run-1',
        details: failedRun('run-1'),
      });

      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      const dialog = screen.getByRole('dialog');
      expect(
        within(dialog).getByRole('heading', { name: 'Retry this run?' })
      ).toBeInTheDocument();
      expect(within(dialog).getByText('run-1')).toBeInTheDocument();

      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Reschedule as a new run',
        })
      );
      expect(
        within(dialog).getByRole('heading', {
          name: 'Reschedule as a new run?',
        })
      ).toBeInTheDocument();
      fireEvent.click(
        within(dialog).getByRole('button', { name: 'Reschedule' })
      );

      await waitFor(() =>
        expect(h.client.POST).toHaveBeenCalledWith(
          '/dag-runs/{name}/{dagRunId}/reschedule',
          expect.objectContaining({
            params: {
              path: { name: 'etl', dagRunId: 'run-1' },
              query: { remoteNode: 'local' },
            },
          })
        )
      );
    });

    it('offers Reschedule in the Retry confirm', () => {
      setup({ latest: failedRun() });

      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      const dialog = screen.getByRole('dialog');
      expect(
        within(dialog).getByRole('checkbox', {
          name: 'Reschedule as a new run',
        })
      ).toBeInTheDocument();
    });

    it('shows the new run after a reschedule', async () => {
      h.client.POST.mockResolvedValue({ data: { dagRunId: 'run-9' } });
      setup({ latest: failedRun('run-1'), pinned: 'run-1' });
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Reschedule as a new run',
        })
      );
      fireEvent.click(
        within(dialog).getByRole('button', { name: 'Reschedule' })
      );
      await waitFor(() =>
        expect(screen.getByTestId('location')).toHaveTextContent(
          '/dags/etl.yaml?run=run-9'
        )
      );
    });

    // A run started with --name lives under a name that only the URL holds,
    // and a reschedule keeps the name.
    it('names the new run of a reschedule as the run it comes from', async () => {
      h.client.POST.mockResolvedValue({ data: { dagRunId: 'run-9' } });
      setup({
        latest: failedRun('run-1'),
        pinned: 'run-1',
        url: '/dags/nightly?run=run-1&dagRunName=etl',
      });
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Reschedule as a new run',
        })
      );
      fireEvent.click(
        within(dialog).getByRole('button', { name: 'Reschedule' })
      );
      await waitFor(() =>
        expect(screen.getByTestId('location')).toHaveTextContent(
          '/dags/nightly?run=run-9&dagRunName=etl'
        )
      );
    });

    // A name override makes a run of another DAG, which this workflow does not
    // list: the page stays on the run that it shows.
    it('stays on the run after a reschedule under another DAG name', async () => {
      h.client.POST.mockResolvedValue({ data: { dagRunId: 'run-9' } });
      setup({
        latest: failedRun('run-1'),
        pinned: 'run-1',
        url: '/dags/etl.yaml?run=run-1',
      });
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      const dialog = screen.getByRole('dialog');
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Reschedule as a new run',
        })
      );
      fireEvent.change(
        within(dialog).getByLabelText('DAG Name Override (optional)'),
        { target: { value: 'etl-copy' } }
      );
      fireEvent.click(
        within(dialog).getByRole('button', { name: 'Reschedule' })
      );
      await waitFor(() =>
        expect(h.showToast).toHaveBeenCalledWith(
          expect.stringContaining('run-9')
        )
      );
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/dags/etl.yaml?run=run-1'
      );
    });

    // The edited retry opens from the Retry dialog, and its new run is shown.
    it('opens the edited retry from the Retry dialog and shows its run', async () => {
      setup({ latest: failedRun('run-1'), pinned: 'run-1' });
      expect(screen.queryByTestId('edit-retry')).toBeNull();

      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      fireEvent.click(
        within(screen.getByRole('dialog')).getByRole('button', {
          name: 'Edit the definition and choose the steps to run',
        })
      );

      const edit = screen.getByTestId('edit-retry');
      expect(edit).toHaveAttribute('data-run', 'etl/run-1');
      expect(screen.queryByRole('dialog')).toBeNull();
      fireEvent.click(
        within(edit).getByRole('button', { name: 'Finish edit retry' })
      );
      await waitFor(() =>
        expect(screen.getByTestId('location')).toHaveTextContent(
          '/dags/etl.yaml?run=run-9'
        )
      );
    });

    // Retry replaces Start, so the caret menu keeps a way to the Start dialog.
    describe('caret menu', () => {
      it("has 'Start' first, and it opens the Start dialog", async () => {
        setup({ latest: failedRun(), editor: { dirty: true, buffer: 'a: b' } });

        const menu = await openMenu();
        expect(
          within(menu)
            .getAllByRole('menuitem')
            .map((item) => item.textContent)
        ).toEqual(['Start', 'Run unsaved draft']);

        await userEvent.click(
          within(menu).getByRole('menuitem', { name: 'Start' })
        );
        expect(await screen.findByTestId('start-modal')).toBeInTheDocument();
        expect(h.client.POST).not.toHaveBeenCalled();
      });

      it('asks before the Start dialog while the definition is unsaved', async () => {
        setup({ latest: failedRun(), dirtyFlag: true });

        const menu = await openMenu();
        await userEvent.click(
          within(menu).getByRole('menuitem', { name: 'Start' })
        );
        const guard = await screen.findByRole('dialog');
        expect(
          within(guard).getByRole('heading', { name: 'Unsaved Changes' })
        ).toBeInTheDocument();
        expect(screen.queryByTestId('start-modal')).toBeNull();
      });

      it.each([
        ['succeeded', Status.Success],
        ['aborted', Status.Aborted],
        ['running', Status.Running],
      ])('has no Start item for a %s run', async (_label, status) => {
        setup({ latest: runDetails('run-3', status) });

        const menu = await openMenu();
        expect(
          within(menu).queryByRole('menuitem', { name: 'Start' })
        ).toBeNull();
        expect(within(menu).getAllByRole('menuitem')).toHaveLength(1);
      });
    });
  });

  // parity:shell-unsaved-guard
  // parity:spec-unsaved-context
  it('asks before the Start dialog while the definition is unsaved', () => {
    setup({ dirtyFlag: true });

    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    const guard = screen.getByRole('dialog');
    expect(
      within(guard).getByRole('heading', { name: 'Unsaved Changes' })
    ).toBeInTheDocument();
    expect(screen.queryByTestId('start-modal')).toBeNull();

    fireEvent.click(within(guard).getByRole('button', { name: 'Run anyway' }));
    expect(screen.getByTestId('start-modal')).toBeInTheDocument();
  });

  // parity:run-progress-stack
  // parity:test-run-progress
  // parity:api-start-enqueue
  // parity:common-toasts-errors
  // parity:run-dag-button
  // parity:crit-canvas-run-vs-header-start
  it.each([
    ['start', '/dags/{fileName}/start', 'DAG run started'],
    ['enqueue', '/dags/{fileName}/enqueue', 'DAG run enqueued'],
  ])(
    'submits a %s from the dialog and shows its progress',
    async (action, path, toast) => {
      const { refresh } = setup();

      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      fireEvent.click(screen.getByRole('button', { name: `Submit ${action}` }));

      await waitFor(() =>
        expect(h.pushRunProgress).toHaveBeenCalledWith({
          dagName: 'etl',
          fileName: 'etl.yaml',
          dagRunId: 'new-run',
          remoteNode: 'local',
        })
      );
      expect(h.client.POST).toHaveBeenCalledWith(path, {
        params: { path: { fileName: FILE }, query: { remoteNode: 'local' } },
        body: { params: '' },
      });
      expect(h.showToast).toHaveBeenCalledWith(toast);
      expect(refresh).toHaveBeenCalled();
    }
  );

  // 'Run now' did what Start does once its dialog is confirmed, so the menu
  // keeps only the choice that Start lacks: the unsaved draft. The caret keeps
  // its slot while there is no draft, so that Start does not move when the
  // draft becomes dirty; its menu says why the draft cannot run.
  it('says that a clean editor has no draft to run', async () => {
    setup({ editor: { dirty: false, buffer: 'steps: []\n' } });

    const menu = await openMenu();
    const item = within(menu).getByRole('menuitem', {
      name: /Run unsaved draft/,
    });
    expect(item).toHaveAttribute('aria-disabled', 'true');
    expect(item).toHaveTextContent('No unsaved changes');
    expect(
      within(menu).queryByRole('menuitem', { name: 'Run now' })
    ).toBeNull();
  });

  // Start is the first child and the caret the second, clean and dirty: the
  // right-aligned group never gains a button, so Start keeps its x position.
  it('has the same buttons, in the same order, clean and dirty', () => {
    const names = () =>
      within(screen.getByTestId('ww-run-split'))
        .getAllByRole('button')
        .map((b) => b.getAttribute('aria-label'));
    const clean = setup({ editor: { dirty: false, buffer: 'steps: []\n' } });
    const before = names();
    clean.unmount();

    setup({ editor: { dirty: true, buffer: 'steps: []\n' } });
    expect(names()).toEqual(before);
    expect(before).toEqual(['Start', 'More run options']);
    expect(
      screen.getByRole('button', { name: 'More run options' })
    ).toBeEnabled();
  });

  // parity:run-draft-button
  it('runs the editor buffer while the editor is dirty', async () => {
    setup({
      pinned: 'run-1',
      details: runDetails('run-1'),
      editor: { dirty: true, buffer: 'steps:\n  - echo draft\n' },
    });

    const menu = await openMenu();
    expect(
      within(menu)
        .getAllByRole('menuitem')
        .map((item) => item.textContent)
    ).toEqual(['Run unsaved draft']);

    await userEvent.click(
      within(menu).getByRole('menuitem', { name: 'Run unsaved draft' })
    );

    // The new run is selected, whatever run was selected.
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(
        `/dags/${FILE}?run=new-run`
      )
    );
    expect(h.client.POST).toHaveBeenCalledTimes(1);
    expect(h.client.POST).toHaveBeenCalledWith('/dag-runs', {
      params: { query: { remoteNode: 'local' } },
      body: {
        spec: 'steps:\n  - echo draft\n',
        name: 'etl',
        labels: ['dagu.source=editor'],
      },
    });
  });

  // A run started from the bar stays on the step and its node view.
  it('keeps ?step and ?ndv when a draft run starts', async () => {
    setup({
      url: `/dags/${FILE}?run=old-run&step=merge&ndv=expanded`,
      pinned: 'old-run',
      details: runDetails('old-run'),
      editor: { dirty: true, buffer: 'steps:\n  - echo draft\n' },
    });

    const menu = await openMenu();
    await userEvent.click(
      within(menu).getByRole('menuitem', { name: 'Run unsaved draft' })
    );

    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(
        `/dags/${FILE}?run=new-run&step=merge&ndv=expanded`
      )
    );
  });

  it('refreshes the workflow after a draft run', async () => {
    const { refresh } = setup({
      editor: { dirty: true, buffer: 'steps: []\n' },
    });

    const menu = await openMenu();
    await userEvent.click(
      within(menu).getByRole('menuitem', { name: 'Run unsaved draft' })
    );

    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });

  it('offers no draft run without canExecute', () => {
    h.canExecute = false;
    setup({ editor: { dirty: true, buffer: 'steps: []\n' } });

    expect(
      screen.queryByRole('button', { name: 'More run options' })
    ).toBeNull();
  });

  it('keeps only the draft menu without runDags', async () => {
    h.runDags = false;
    setup({ editor: { dirty: true, buffer: 'steps: []\n' } });

    expect(screen.queryByRole('button', { name: 'Start' })).toBeNull();
    const menu = await openMenu();
    expect(
      within(menu).getByRole('menuitem', { name: 'Run unsaved draft' })
    ).not.toHaveAttribute('aria-disabled');
  });

  const menuItems = async () =>
    within(await openMenu())
      .getAllByRole('menuitem')
      .map((item) => item.textContent);

  it('offers to save and run to a user who can save and run', async () => {
    setup({
      canSave: true,
      dirtyFlag: true,
      editor: { dirty: true, buffer: 'steps: []\n' },
    });

    expect(await menuItems()).toEqual(['Save & Run', 'Run unsaved draft']);
  });

  it('offers no save and run while there is nothing to save', async () => {
    setup({ canSave: true });

    expect(await menuItems()).toEqual(['Run unsaved draftNo unsaved changes']);
  });

  it('offers no save and run without runDags', async () => {
    h.runDags = false;
    setup({
      canSave: true,
      dirtyFlag: true,
      editor: { dirty: true, buffer: 'steps: []\n' },
    });

    expect(await menuItems()).toEqual(['Run unsaved draft']);
  });

  it('offers only save and run without canExecute', async () => {
    h.canExecute = false;
    setup({
      canSave: true,
      dirtyFlag: true,
      editor: { dirty: true, buffer: 'steps: []\n' },
    });

    expect(await menuItems()).toEqual(['Save & Run']);
  });

  it('shows nothing without runDags and without a draft', () => {
    h.runDags = false;
    setup();

    expect(screen.queryByTestId('ww-run-split')).toBeNull();
  });

  it('is absent with neither runDags nor canExecute, like the cluster', () => {
    h.runDags = false;
    h.canExecute = false;
    setup({ cluster: true, latest: runDetails('run-3', Status.Running) });

    expect(screen.queryByTestId('ww-run-split')).toBeNull();
    expect(screen.queryByTestId('ww-run-actions')).toBeNull();
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });

  // parity:common-toasts-errors
  it('shows why the draft run could not start', async () => {
    h.client.POST.mockResolvedValue({
      error: { message: 'worker unavailable' },
      response: { status: 503 },
    });
    setup({ editor: { dirty: true, buffer: 'steps: []\n' } });

    const menu = await openMenu();
    await userEvent.click(
      within(menu).getByRole('menuitem', { name: 'Run unsaved draft' })
    );

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'worker unavailable'
    );
    // No run started: the page stays on the editor.
    expect(screen.getByTestId('location')).toHaveTextContent(`/dags/${FILE}`);
    expect(screen.getByTestId('location')).not.toHaveTextContent('executions');
  });

  // The feedback of a start sits in the Start button: its size does not
  // change in idle, busy and error, and no text appears next to it.
  it('keeps the Start button size while a draft start is busy or failed', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let fail: (value: unknown) => void = () => undefined;
      h.client.POST.mockReturnValue(
        new Promise((resolve) => {
          fail = resolve;
        })
      );
      setup({ editor: { dirty: true, buffer: 'steps: []\n' } });
      const sizeClasses = () =>
        screen.getByRole('button', { name: 'Start' }).className;
      const idle = sizeClasses();
      const slot = screen.getByTestId('ww-run-split');
      expect(within(slot).queryByRole('status')).toBeNull();

      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      await user.click(
        screen.getByRole('button', { name: 'More run options' })
      );
      await user.click(
        await screen.findByRole('menuitem', { name: 'Run unsaved draft' })
      );

      // Busy: a spinner in the slot of the Play icon.
      const spinner = await within(slot).findByRole('status', {
        name: 'Starting run',
      });
      expect(spinner).toBeInTheDocument();
      expect(sizeClasses()).toBe(idle);
      expect(screen.getByTestId('ww-run-start-slot')).toContainElement(spinner);

      // Error: an alert icon in the same slot, the message as its tooltip.
      await act(async () => {
        fail({
          error: { message: 'worker unavailable' },
          response: { status: 503 },
        });
      });
      const alert = await within(slot).findByRole('alert');
      expect(alert).toHaveTextContent('worker unavailable');
      expect(screen.getByTestId('ww-run-start-slot')).toHaveAttribute(
        'title',
        'worker unavailable'
      );
      expect(within(slot).queryByRole('status')).toBeNull();
      expect(sizeClasses()).toBe(idle);
      // The message is not text of the bar: only the tooltip and a hidden label.
      expect(alert.querySelector('.sr-only')).not.toBeNull();

      // After 5 s the Play icon is back.
      act(() => {
        vi.advanceTimersByTime(5000);
      });
      await waitFor(() => expect(within(slot).queryByRole('alert')).toBeNull());
      expect(screen.getByTestId('ww-run-start-slot')).not.toHaveAttribute(
        'title'
      );
      expect(sizeClasses()).toBe(idle);
    } finally {
      vi.useRealTimers();
    }
  });

  // parity:dup-13
  // parity:common-run-state-helpers
  it('has one Stop, in the slot of Start, and it stops the selected run', async () => {
    setup({
      cluster: true,
      pinned: 'run-1',
      details: runDetails('run-1', Status.Running, {
        statusLabel: StatusLabel.running,
      }),
      latest: runDetails('run-3', Status.Running, {
        statusLabel: StatusLabel.running,
      }),
    });

    const stops = screen.getAllByRole('button', { name: 'Stop' });
    expect(stops).toHaveLength(1);
    fireEvent.click(stops[0]!);
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));

    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/stop',
        {
          params: {
            query: { remoteNode: 'local' },
            path: { name: 'etl', dagRunId: 'run-1' },
          },
        }
      )
    );
  });

  // parity:actions-stop
  // parity:api-stop
  it('stops every running instance from the Stop in the slot of Start', async () => {
    const { run } = setup({
      latest: runDetails('run-3', Status.Running, {
        statusLabel: StatusLabel.running,
      }),
    });

    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(
      within(dialog).getByRole('checkbox', {
        name: 'Stop all running instances',
      })
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop' }));
    await waitFor(() => expect(run.refresh).toHaveBeenCalledTimes(1));
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
  it('has Cancel in the slot of Start for a run with a pending auto-retry', async () => {
    setup({
      cluster: true,
      latest: runDetails('run-3', Status.Failed, {
        statusLabel: StatusLabel.failed,
        autoRetryCount: 1,
        autoRetryLimit: 3,
      }),
    });

    const slot = screen.getByTestId('ww-run-start-slot');
    expect(shownByName('Start')).toHaveLength(0);
    fireEvent.click(within(slot).getByRole('button', { name: 'Cancel' }));
    const dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByRole('heading', { name: 'Cancel auto-retry?' })
    ).toBeInTheDocument();
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
  it('has Reject in the slot of Start for a run that waits for approval', async () => {
    const { run } = setup({
      cluster: true,
      latest: runDetails('run-3', Status.Waiting, {
        statusLabel: StatusLabel.waiting,
        nodes: [
          {
            step: { name: 'approve', approval: {} },
            status: NodeStatus.Waiting,
          },
        ],
      } as unknown as Partial<ApiDAGRunDetails>),
    });

    const slot = screen.getByTestId('ww-run-start-slot');
    expect(shownByName('Start')).toHaveLength(0);
    fireEvent.click(within(slot).getByRole('button', { name: 'Reject' }));
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
          path: { name: 'etl', dagRunId: 'run-3', stepName: 'approve' },
          query: { remoteNode: 'local' },
        },
        body: { reason: 'not now' },
      }
    );
  });

  // A service workflow has one run at a time: the bar offers Stop while a run
  // is active, and Retry or Start and the draft run when none is.
  describe('service workflow', () => {
    const service = (restartPolicy: RestartPolicy) =>
      ({
        name: 'etl',
        restartPolicy,
        restartWaitSec: 5,
      }) as ApiDAGDetails;
    const running = runDetails('run-3', Status.Running, {
      statusLabel: StatusLabel.running,
    });
    const dirty = { dirty: true, buffer: 'steps: []\n' };

    it.each([RestartPolicy.always, RestartPolicy.unless_stopped])(
      'has Stop in the slot of Start and no Retry while a run is active (%s)',
      (policy) => {
        setup({
          dag: service(policy),
          latest: running,
          cluster: true,
          editor: dirty,
        });

        expect(
          within(screen.getByTestId('ww-run-start-slot')).getByRole('button', {
            name: 'Stop',
          })
        ).toBeEnabled();
        expect(shownByName('Start')).toHaveLength(0);
        expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
        expect(
          screen.queryByRole('button', { name: 'More run options' })
        ).toBeNull();
        // The palette commands Run… and Retry find nothing to click.
        expect(barControlAvailable('start')).toBe(false);
        expect(barControlAvailable('retry')).toBe(false);
        expect(barControlAvailable('stop')).toBe(true);
      }
    );

    // A service workflow has one run at a time: the failed past run keeps
    // its Retry, disabled, while the latest run is active.
    it('has a disabled Retry for a past failed run while the latest run is active', () => {
      setup({
        dag: service(RestartPolicy.always),
        latest: running,
        cluster: true,
        pinned: 'run-1',
        details: failedRun('run-1'),
      });

      expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
      expect(shownByName('Start')).toHaveLength(0);
    });

    it('has Retry in the slot of Start, the Start item and the draft menu, and no Stop, without an active run', async () => {
      setup({
        dag: service(RestartPolicy.always),
        latest: failedRun(),
        cluster: true,
        editor: dirty,
      });

      expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
      expect(shownByName('Start')).toHaveLength(0);
      expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
      expect(barControlAvailable('start')).toBe(true);
      const menu = await openMenu();
      expect(
        within(menu)
          .getAllByRole('menuitem')
          .map((item) => item.textContent)
      ).toEqual(['Start', 'Run unsaved draft']);
      expect(
        within(menu).getByRole('menuitem', { name: 'Run unsaved draft' })
      ).not.toHaveAttribute('aria-disabled');
    });

    it('has Start, and no Retry, for a succeeded run without an active run', () => {
      setup({
        dag: service(RestartPolicy.always),
        latest: runDetails('run-3', Status.Success),
        cluster: true,
        editor: dirty,
      });

      expect(shownByName('Start')).toHaveLength(1);
      expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
    });

    // The scheduler starts the next run of a service DAG that restarts or
    // fails: the slot keeps a disabled Retry and has no draft menu.
    describe.each([ServiceDAGState.restarting, ServiceDAGState.failing])(
      'with no run alive and the state %s',
      (state) => {
        it('has a disabled Retry and no draft menu', () => {
          h.serviceDags = [{ fileName: FILE, state }];
          setup({
            dag: service(RestartPolicy.always),
            latest: runDetails('run-3', Status.Failed, {
              statusLabel: StatusLabel.failed,
            }),
            cluster: true,
            editor: dirty,
          });

          expect(shownByName('Start')).toHaveLength(0);
          expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
          expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
          expect(
            screen.queryByRole('button', { name: 'More run options' })
          ).toBeNull();
          expect(barControlAvailable('start')).toBe(false);
          expect(barControlAvailable('retry')).toBe(false);
          // The header reads the list of the page, for this node and scope.
          expect(h.serviceRead).toHaveBeenCalledWith(
            { remoteNode: 'local', workspace: 'all' },
            true
          );
        });

        it('has no Run button when the user may only execute', () => {
          h.runDags = false;
          h.serviceDags = [{ fileName: FILE, state }];
          setup({
            dag: service(RestartPolicy.always),
            latest: runDetails('run-3', Status.Failed),
            cluster: true,
            editor: dirty,
          });

          expect(
            screen.queryByRole('button', { name: 'More run options' })
          ).toBeNull();
          expect(screen.queryByTestId('ww-run-split')).toBeNull();
        });

        it('has only Stop while a run is alive', () => {
          h.serviceDags = [{ fileName: FILE, state }];
          setup({
            dag: service(RestartPolicy.always),
            latest: running,
            cluster: true,
            editor: dirty,
          });

          expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
          expect(shownByName('Start')).toHaveLength(0);
          expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
        });
      }
    );

    it.each([
      ServiceDAGState.pending,
      ServiceDAGState.suspended,
      ServiceDAGState.stopped,
    ])(
      'keeps Retry, the Start item and the draft menu in the state %s',
      async (state) => {
        h.serviceDags = [{ fileName: FILE, state }];
        setup({
          dag: service(RestartPolicy.always),
          latest: failedRun(),
          cluster: true,
          editor: dirty,
        });

        expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
        expect(barControlAvailable('start')).toBe(true);
        expect(
          screen.getByRole('button', { name: 'More run options' })
        ).toBeInTheDocument();
      }
    );

    it('keeps Retry when the list does not have the DAG', () => {
      h.serviceDags = [
        { fileName: 'other.yaml', state: ServiceDAGState.failing },
      ];
      setup({
        dag: service(RestartPolicy.always),
        latest: failedRun(),
        cluster: true,
      });

      expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
      expect(barControlAvailable('start')).toBe(true);
    });

    it('keeps Retry for a plain workflow and does not read the list', () => {
      h.serviceDags = [{ fileName: FILE, state: ServiceDAGState.failing }];
      setup({ latest: failedRun(), cluster: true });

      expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
      expect(barControlAvailable('start')).toBe(true);
      expect(h.serviceRead.mock.calls.every((args) => args[1] === false)).toBe(
        true
      );
    });

    // Stop has the slot of Start; the caret of the draft menu stays.
    it('keeps Stop in the slot of Start and the draft menu for a plain workflow', () => {
      setup({ latest: running, cluster: true, editor: dirty });

      expect(shownByName('Start')).toHaveLength(0);
      expect(
        within(screen.getByTestId('ww-run-start-slot')).getByRole('button', {
          name: 'Stop',
        })
      ).toBeEnabled();
      expect(
        screen.getByRole('button', { name: 'More run options' })
      ).toBeInTheDocument();
    });
  });
});
