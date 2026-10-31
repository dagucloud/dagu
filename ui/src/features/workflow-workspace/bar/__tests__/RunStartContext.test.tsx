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
import { useEffect, useState, type ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, StatusLabel } from '@/api/v1/schema';
import {
  UnsavedChangesProvider,
  useUnsavedChanges,
} from '@/contexts/UnsavedChangesContext';
import { DAGContext } from '@/features/dags/contexts/DAGContext';
import { WorkspaceEditorProvider } from '../../canvas/WorkspaceEditorContext';
import type { ApiDAGDetails } from '../../contracts';
import { WorkspaceDataContext } from '../../data/context';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceRunValue,
} from '../../data/types';
import { RunStartProvider, useRunStart } from '../RunStartContext';
import { RunSplitButton } from '../RunSplitButton';

const h = vi.hoisted(() => ({
  runDags: true,
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
  useCanExecute: () => true,
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

const FILE = 'etl.yaml';
const dag = { name: 'etl' } as ApiDAGDetails;
const latest = {
  name: 'etl',
  dagRunId: 'run-3',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  nodes: [],
} as unknown as ApiDAGRunDetails;

/** A zone other than the bar, for example the canvas or the palette. */
function OtherZone() {
  const { canStart, requestStart } = useRunStart();
  return (
    <button
      type="button"
      data-can-start={String(canStart)}
      onClick={requestStart}
    >
      Start from elsewhere
    </button>
  );
}

function DirtyFlag() {
  const { setHasUnsavedChanges } = useUnsavedChanges();
  useEffect(() => setHasUnsavedChanges(true), [setHasUnsavedChanges]);
  return null;
}

function Workspace({
  children,
  dagContext,
}: {
  children: ReactNode;
  dagContext?: { autoOpenStartModal?: boolean };
}) {
  const run = {
    selection: 'latest',
    ref: { name: 'etl', runId: 'run-3' },
    details: latest,
    select: vi.fn(),
    refresh: vi.fn(async () => undefined),
  } as unknown as WorkspaceRunValue;
  // The Start request opens the new run on Executions: it needs a router.
  return (
    <MemoryRouter>
      <UnsavedChangesProvider>
        <DAGContext.Provider
          value={{
            refresh: () => {},
            name: 'etl',
            fileName: FILE,
            ...dagContext,
          }}
        >
          <WorkspaceDataContext.Provider
            value={{
              fileName: FILE,
              dagData: { dag, latestDAGRun: latest } as unknown as DAGPageData,
              workspaceName: null,
              remoteNode: 'local',
              run,
              refresh: vi.fn(async () => undefined),
              registerHistory: () => () => {},
            }}
          >
            <WorkspaceEditorProvider>
              <RunStartProvider>{children}</RunStartProvider>
            </WorkspaceEditorProvider>
          </WorkspaceDataContext.Provider>
        </DAGContext.Provider>
      </UnsavedChangesProvider>
    </MemoryRouter>
  );
}

function renderWorkspace() {
  return render(
    <Workspace>
      <RunSplitButton fileName={FILE} dag={dag} workspaceName={null} />
      <OtherZone />
    </Workspace>
  );
}

async function requestFromOtherZone() {
  fireEvent.click(screen.getByRole('button', { name: 'Start from elsewhere' }));
  return screen.findByRole('dialog');
}

function serveDag(extra: Record<string, unknown> = {}) {
  h.client.GET.mockImplementation(async (path: string) =>
    path === '/dags/{fileName}'
      ? { data: { dag: { name: 'etl', ...extra } } }
      : { data: {} }
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  h.runDags = true;
  serveDag();
  h.client.POST.mockResolvedValue({
    data: { dagRunId: 'new-run' },
    response: { status: 200 },
  });
});

describe('RunStartContext', () => {
  // parity:start-modal-load
  // parity:start-modal-header
  it('opens the Start dialog of the bar from a sibling consumer', async () => {
    renderWorkspace();
    expect(
      screen.getByRole('button', { name: 'Start from elsewhere' })
    ).toHaveAttribute('data-can-start', 'true');

    const dialog = await requestFromOtherZone();
    expect(
      within(dialog).getByRole('heading', { name: 'Start workflow' })
    ).toBeInTheDocument();
    expect(h.client.GET).toHaveBeenCalledWith('/dags/{fileName}', {
      params: { path: { fileName: FILE }, query: { remoteNode: 'local' } },
    });

    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Maximize dialog' })
    );
    expect(
      within(dialog).getByRole('button', { name: 'Restore dialog' })
    ).toHaveAttribute('aria-pressed', 'true');
  });

  it('opens the dialog again on the next request', async () => {
    renderWorkspace();

    let dialog = await requestFromOtherZone();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    dialog = await requestFromOtherZone();
    expect(
      within(dialog).getByRole('heading', { name: 'Start workflow' })
    ).toBeInTheDocument();
  });

  // parity:shell-unsaved-guard
  it('asks before the dialog when a request comes with unsaved changes', async () => {
    render(
      <Workspace>
        <DirtyFlag />
        <RunSplitButton fileName={FILE} dag={dag} workspaceName={null} />
        <OtherZone />
      </Workspace>
    );

    let guard = await requestFromOtherZone();
    expect(
      within(guard).getByRole('heading', { name: 'Unsaved Changes' })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Start workflow' })
    ).toBeNull();

    fireEvent.click(within(guard).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(h.client.GET).not.toHaveBeenCalledWith(
      '/dags/{fileName}',
      expect.anything()
    );

    guard = await requestFromOtherZone();
    fireEvent.click(within(guard).getByRole('button', { name: 'Run anyway' }));
    expect(
      await screen.findByRole('heading', { name: 'Start workflow' })
    ).toBeInTheDocument();
  });

  it('cannot start without runDags', () => {
    h.runDags = false;
    renderWorkspace();

    const other = screen.getByRole('button', { name: 'Start from elsewhere' });
    expect(other).toHaveAttribute('data-can-start', 'false');
    fireEvent.click(other);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(h.client.GET).not.toHaveBeenCalled();
  });

  it('keeps no request while the bar is not mounted', async () => {
    function Toggle() {
      const [bar, setBar] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setBar(true)}>
            Mount bar
          </button>
          {bar && (
            <RunSplitButton fileName={FILE} dag={dag} workspaceName={null} />
          )}
        </>
      );
    }
    render(
      <Workspace>
        <Toggle />
        <OtherZone />
      </Workspace>
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Start from elsewhere' })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Mount bar' }));
    await act(async () => {});
    expect(screen.getByRole('button', { name: 'Start' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps the auto-open request of the page', async () => {
    render(
      <Workspace dagContext={{ autoOpenStartModal: true }}>
        <RunSplitButton fileName={FILE} dag={dag} workspaceName={null} />
      </Workspace>
    );

    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  // parity:start-modal-run-id
  // parity:start-modal-keyboard
  // parity:start-modal-submit
  // parity:embed-run-progress
  it('starts with a custom run ID on Enter and shows the progress', async () => {
    renderWorkspace();

    const dialog = await requestFromOtherZone();
    const runId = await within(dialog).findByRole('textbox', {
      name: 'Run ID (optional)',
    });
    fireEvent.change(runId, { target: { value: 'my-run' } });
    runId.focus();
    fireEvent.keyDown(document, { key: 'Enter' });

    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith('/dags/{fileName}/start', {
        params: { path: { fileName: FILE }, query: { remoteNode: 'local' } },
        body: { params: '', dagRunId: 'my-run' },
      })
    );
    await waitFor(() =>
      expect(h.pushRunProgress).toHaveBeenCalledWith({
        dagName: 'etl',
        fileName: 'etl.yaml',
        dagRunId: 'new-run',
        remoteNode: 'local',
      })
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  // parity:start-modal-enqueue
  it('enqueues the run when Enqueue is checked', async () => {
    renderWorkspace();

    const dialog = await requestFromOtherZone();
    await userEvent.click(
      await within(dialog).findByRole('checkbox', { name: 'Enqueue' })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Enqueue' })
    );

    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith('/dags/{fileName}/enqueue', {
        params: { path: { fileName: FILE }, query: { remoteNode: 'local' } },
        body: { params: '' },
      })
    );
    expect(h.showToast).toHaveBeenCalledWith('DAG run enqueued');
  });

  // parity:start-modal-no-reuse
  it('can disable reuse for a build workflow', async () => {
    serveDag({ type: 'build' });
    renderWorkspace();

    const dialog = await requestFromOtherZone();
    await userEvent.click(
      await within(dialog).findByRole('checkbox', {
        name: /disable reuse for this run/i,
      })
    );
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Start' })
    );

    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith('/dags/{fileName}/start', {
        params: { path: { fileName: FILE }, query: { remoteNode: 'local' } },
        body: { params: '', noReuse: true },
      })
    );
  });

  // parity:start-modal-restrictions
  it('notes the run restrictions of the workflow', async () => {
    serveDag({ runConfig: { disableRunIdEdit: true } });
    renderWorkspace();

    const dialog = await requestFromOtherZone();
    expect(
      await within(dialog).findByText('This DAG has restrictions:')
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText('Custom run IDs are disabled.')
    ).toBeInTheDocument();
  });

  // parity:start-modal-params
  // parity:start-modal-validation
  it('validates and submits the typed parameters', async () => {
    serveDag({
      paramDefs: [{ name: 'region', type: 'string', required: true }],
    });
    renderWorkspace();

    const dialog = await requestFromOtherZone();
    const region = await within(dialog).findByLabelText(/region/i);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start' }));
    expect(
      await within(dialog).findByText(
        'Fix the highlighted parameter errors before submitting.'
      )
    ).toBeInTheDocument();
    expect(h.client.POST).not.toHaveBeenCalled();

    fireEvent.change(region, { target: { value: 'eu' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Start' }));
    await waitFor(() =>
      expect(h.client.POST).toHaveBeenCalledWith('/dags/{fileName}/start', {
        params: { path: { fileName: FILE }, query: { remoteNode: 'local' } },
        body: { params: '[{"region":"eu"}]' },
      })
    );
  });

  // parity:crit-canvas-run-vs-header-start
  it('opens the Start dialog on a request from another zone', async () => {
    renderWorkspace();

    const dialog = await requestFromOtherZone();
    expect(
      within(dialog).getByRole('heading', { name: 'Start workflow' })
    ).toBeInTheDocument();
  });
});
