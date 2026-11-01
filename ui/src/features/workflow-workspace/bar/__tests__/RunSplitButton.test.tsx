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
import { Status, StatusLabel } from '@/api/v1/schema';
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
  editor?: { dirty: boolean; buffer: string } | null;
  cluster?: boolean;
  url?: string;
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
  editor = null,
  cluster = false,
  url = `/dags/${FILE}`,
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
    dag,
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
        <RunActionsCluster fileName={FILE} dag={dag} workspaceName={null} />
      )}
      <RunSplitButton fileName={FILE} dag={dag} workspaceName={null} />
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

beforeEach(() => {
  vi.clearAllMocks();
  h.runDags = true;
  h.canExecute = true;
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
    setup({ latest: runDetails('run-3', Status.Failed) });

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
  it('shows Start as an icon button with its name and a tooltip', () => {
    setup({ latest: runDetails('run-3', Status.Failed) });

    const start = screen.getByRole('button', { name: 'Start' });
    expect(start).toHaveAttribute('aria-label', 'Start');
    expect(start.querySelector('svg')).not.toBeNull();
    expect(within(start).getByText('Start')).toHaveClass('sr-only');
  });

  // The palette commands Run…, Stop and Retry click the bar controls that
  // carry data-ww-action.
  it('marks Start, Stop and Retry for the command palette', () => {
    const running = runDetails('run-3', Status.Running);
    const first = setup({ latest: running, cluster: true });
    const start = document.querySelector<HTMLElement>(
      '[data-ww-action="start"]'
    );
    expect(start).toHaveTextContent('Start');
    expect(barControlAvailable('start')).toBe(true);
    expect(barControlAvailable('stop')).toBe(true);
    expect(
      document.querySelector('[data-ww-action="stop"]')
    ).toHaveAccessibleName('Stop');
    expect(barControlAvailable('retry')).toBe(false);
    // The palette clicks the control as a script does.
    act(() => start!.click());
    expect(screen.getByTestId('start-modal')).toBeInTheDocument();
    first.unmount();

    setup({ latest: runDetails('run-3', Status.Failed), cluster: true });
    expect(barControlAvailable('stop')).toBe(false);
    expect(barControlAvailable('retry')).toBe(true);
    expect(
      document.querySelector('[data-ww-action="retry"]')
    ).toHaveAccessibleName('Retry');
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
  // draft becomes dirty: it is disabled, not removed.
  it('keeps the caret slot while the editor is clean, disabled', () => {
    setup({ editor: { dirty: false, buffer: 'steps: []\n' } });

    const caret = screen.getByRole('button', { name: 'More run options' });
    expect(caret).toBeDisabled();
    expect(screen.queryByRole('menuitem', { name: 'Run now' })).toBeNull();
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
  it('has one Stop with the cluster, and it stops the selected run', async () => {
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

  // parity:retry-modal
  // parity:retry-modal-reschedule
  it('retries the selected run from the cluster, with Reschedule', async () => {
    setup({
      cluster: true,
      pinned: 'run-1',
      details: runDetails('run-1', Status.Failed, {
        statusLabel: StatusLabel.failed,
      }),
    });

    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    const dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByRole('heading', { name: 'Retry this run?' })
    ).toBeInTheDocument();
    expect(within(dialog).getByText('run-1')).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole('checkbox', { name: 'Reschedule as a new run' })
    );
    expect(
      within(dialog).getByRole('heading', { name: 'Reschedule as a new run?' })
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Reschedule' }));

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
});
