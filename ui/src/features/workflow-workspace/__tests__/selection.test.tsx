// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React, { useImperativeHandle, useState, type ReactNode } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useNavigationType,
  type NavigateFunction,
} from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  RightPanelProvider,
  useRightPanelClaim,
} from '@/contexts/RightPanelContext';
import type DAGSpec from '@/features/dags/components/dag-editor/DAGSpec';
import { WorkspaceCanvas } from '../canvas/WorkspaceCanvas';
import { WorkspaceEditorProvider } from '../canvas/WorkspaceEditorContext';
import type {
  ApiDAGDetails,
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceEditorHandle,
  WorkspaceRunValue,
} from '../contracts';
import { WorkspaceDataContext } from '../data/context';
import { runHandlers } from '../data/handlerMapping';
import { firstFailedStep, nodesByStep, waitingCounts } from '../data/runFacts';
import { node, run as runFixture } from '../data/__tests__/fixtures';
import { DOCK_OPEN_KEY, DockProvider } from '../dock/DockContext';
import { LogsDock } from '../dock/LogsDock';

type SpecProps = React.ComponentProps<typeof DAGSpec>;

const host = vi.hoisted(() => ({
  props: null as SpecProps | null,
  handle: null as unknown,
  /** Gives the handle, as DAGSpec does once the spec has loaded. */
  load: null as null | (() => void),
}));

// DAGSpec stands in: it gives the handle once loaded, and its node details
// view holds the 'ndv' panel while ?ndv is set, as NodeDetailsFrame does.
vi.mock('@/features/dags/components/dag-editor/DAGSpec', () => {
  function StandIn(props: SpecProps) {
    host.props = props;
    const [loaded, setLoaded] = useState(false);
    host.load = () => setLoaded(true);
    useImperativeHandle(
      loaded ? props.editorHandleRef : undefined,
      () => host.handle as WorkspaceEditorHandle,
      []
    );
    useRightPanelClaim('ndv', props.ndv?.state != null, () =>
      props.ndv?.onStateChange(null)
    );
    return (
      <div
        data-testid="dag-spec-stand-in"
        data-selected-step={props.selectedStepName ?? ''}
        data-ndv={props.ndv?.state ?? ''}
      />
    );
  }
  return { default: StandIn };
});

// The steps navigation is the real one; the tab bodies and the timeline have
// their own tests.
vi.mock('../dock/tabs', () => ({ dockTabBodies: { log: () => null } }));
vi.mock('../dock/RunTimeline', () => ({ RunTimeline: () => null }));
vi.mock('../step/StepRunView', () => ({
  StepRunView: ({ target }: { target: { step: string } }) => (
    <div data-testid="step-run-view" data-step={target.step} />
  ),
}));
vi.mock('../step/useStepRunActions', () => ({
  useStepRunActions: () => ({
    can: () => ({ retry: false, setStatus: false, runStep: false }),
    openSubRun: vi.fn(),
    setStatus: vi.fn(),
    retryStep: vi.fn(),
    runStep: vi.fn(),
    runUpstream: vi.fn(),
    followed: [],
    clearFollowed: vi.fn(),
    openLogs: vi.fn(),
    dialogs: null,
  }),
}));
vi.mock('../step/useReuseSource', () => ({
  useReuseSource: () => ({
    source: null,
    waitingFor: null,
    stale: [],
    missing: [],
    headChanged: false,
    paramsDiffer: false,
    loading: false,
    options: [],
    selectedOptionId: null,
    choose: () => undefined,
    cached: [],
    cachedRuns: {},
    gone: [],
    stepRuns: {},
    cacheCapturedAt: null,
    cacheTimes: {},
  }),
}));
vi.mock('../notices/useWorkspaceNotices', () => ({
  useWorkspaceNotices: () => [],
}));
vi.mock('../data/useWorkspacePermissions', () => ({
  useWorkspacePermissions: () => ({
    canWrite: true,
    canRunDags: true,
    canExecute: true,
    canExecuteForWorkspace: true,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  }),
}));

const DAG = {
  name: 'demo',
  steps: [{ name: 'fetch' }, { name: 'store', depends: ['fetch'] }],
} as unknown as ApiDAGDetails;

function runValue(details: ApiDAGRunDetails): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: details.name, runId: details.dagRunId },
    details,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    overlay: {},
    nodeByStep: nodesByStep(details),
    handlers: runHandlers(details),
    waiting: waitingCounts(details),
    failedStep: firstFailedStep(details),
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => {}),
  } as unknown as WorkspaceRunValue;
}

const nav = {
  location: '',
  type: '',
  navigate: (() => {}) as NavigateFunction,
};

function Probe() {
  const location = useLocation();
  nav.location = location.pathname + location.search;
  nav.type = useNavigationType();
  nav.navigate = useNavigate();
  return null;
}

/** A Radix menu of the workspace, such as the workflow menu. */
function Menu() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger>Workflow menu</DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem>Rename</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

let runIds = 0;

/** A run id that useDockAutoOpen has not handled in this test file. */
function freshRun(status: Status = Status.Success): ApiDAGRunDetails {
  runIds += 1;
  const failed = status === Status.Failed;
  return runFixture(
    `run-${runIds}`,
    [
      node('fetch', failed ? NodeStatus.Failed : NodeStatus.Success),
      node('store', NodeStatus.NotStarted),
    ],
    status
  );
}

function renderWorkspace(
  url: string,
  details: ApiDAGRunDetails = freshRun(),
  extra: ReactNode = null
) {
  const dagData = {
    dag: DAG,
    latestDAGRun: details,
    localDags: [],
    editorHints: {},
  } as unknown as DAGPageData;
  const value = {
    fileName: 'f',
    dagData,
    workspaceName: null,
    remoteNode: 'local',
    run: runValue(details),
    refresh: async () => {},
    registerHistory: () => () => {},
  };
  return render(
    <MemoryRouter initialEntries={['/dags/other', url]} initialIndex={1}>
      <Routes>
        <Route path="/dags/other" element={<Probe />} />
        <Route
          path="/dags/:fileName"
          element={
            <WorkspaceDataContext.Provider value={value}>
              <RightPanelProvider>
                <WorkspaceEditorProvider>
                  <DockProvider>
                    <Probe />
                    {extra}
                    <WorkspaceCanvas fileName="f" dagData={dagData} />
                    <LogsDock fileName="f" dag={DAG} />
                  </DockProvider>
                </WorkspaceEditorProvider>
              </RightPanelProvider>
            </WorkspaceDataContext.Provider>
          }
        />
      </Routes>
    </MemoryRouter>
  );
}

function props(): SpecProps {
  if (!host.props) throw new Error('DAGSpec did not render');
  return host.props;
}

function handle(): WorkspaceEditorHandle {
  return host.handle as WorkspaceEditorHandle;
}

function loadSpec() {
  act(() => host.load?.());
}

function dock() {
  return screen.getByTestId('ww-dock');
}

function stepRow(name: string): HTMLElement {
  const list = screen.getByRole('list', { name: 'Run steps' });
  const row = list.querySelector<HTMLElement>(`li[data-step="${name}"]`);
  if (!row) throw new Error(`no row ${name}`);
  return row;
}

function summaryRow(): HTMLElement {
  const row = document.querySelector<HTMLElement>('li[data-summary]');
  if (!row) throw new Error('no summary row');
  return row;
}

// Most tests use the steps of the dock: the user left the panel open. The
// tests of the default clear the storage.
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem(DOCK_OPEN_KEY, 'true');
  host.props = null;
  host.load = null;
  host.handle = {
    revealStep: vi.fn(() => true),
    openForm: vi.fn(() => true),
  };
});

afterEach(() => {
  vi.clearAllMocks();
});

// parity:graph-selected-node parity:graph-click-inspect
// parity:steps-row-inspect parity:drawer-url-sync parity:canvas-selection
// parity:run-page-step-deeplink parity:run-graph-click-inspect
// parity:run-step-row-inspect
describe('workspace selection', () => {
  it('writes a canvas selection to ?step with replace and selects the row', () => {
    renderWorkspace('/dags/f');
    loadSpec();
    expect(stepRow('fetch')).not.toHaveAttribute('aria-current');

    act(() => props().onSelectedStepNameChange?.('fetch'));
    expect(nav.location).toBe('/dags/f?step=fetch');
    expect(nav.type).toBe('REPLACE');
    expect(stepRow('fetch')).toHaveAttribute('aria-current', 'true');
    expect(summaryRow()).not.toHaveAttribute('aria-current');
    expect(props().selectedStepName).toBe('fetch');

    // No new history entry: Back leaves the workflow.
    act(() => {
      void nav.navigate(-1);
    });
    expect(nav.location).toBe('/dags/other');
  });

  it('reveals a step on the canvas from a row click', () => {
    renderWorkspace('/dags/f');
    loadSpec();
    fireEvent.click(
      within(stepRow('fetch')).getByRole('button', { name: 'fetch' })
    );
    expect(handle().revealStep).toHaveBeenCalledWith('fetch');
    // A pick in the steps navigation opens the log of the step.
    expect(nav.location).toBe('/dags/f?step=fetch&dock=log');
    expect(nav.type).toBe('REPLACE');
    expect(props().selectedStepName).toBe('fetch');
    expect(stepRow('fetch')).toHaveAttribute('aria-current', 'true');
  });

  it('clears the row selection when ?step goes', () => {
    renderWorkspace('/dags/f?step=fetch');
    expect(stepRow('fetch')).toHaveAttribute('aria-current', 'true');

    act(() => {
      void nav.navigate('/dags/f', { replace: true });
    });
    expect(stepRow('fetch')).not.toHaveAttribute('aria-current');
    expect(summaryRow()).toHaveAttribute('aria-current', 'true');
    expect(props().selectedStepName).toBeNull();

    act(() => props().onSelectedStepNameChange?.('fetch'));
    act(() => props().onSelectedStepNameChange?.(null));
    expect(nav.location).toBe('/dags/f');
    expect(summaryRow()).toHaveAttribute('aria-current', 'true');
  });

  describe('deep link', () => {
    it('leaves the dock collapsed on a failed run that loads with the page', () => {
      localStorage.removeItem(DOCK_OPEN_KEY);
      renderWorkspace('/dags/f', freshRun(Status.Failed));
      expect(dock()).toHaveAttribute('data-state', 'collapsed');
    });

    it('selects the node and the row after the spec loads, with no dock auto-open', () => {
      localStorage.removeItem(DOCK_OPEN_KEY);
      renderWorkspace('/dags/f?step=fetch', freshRun(Status.Failed));
      expect(props().selectedStepName).toBe('fetch');
      expect(handle().revealStep).not.toHaveBeenCalled();
      // ?step does not open the dock.
      expect(dock()).toHaveAttribute('data-state', 'collapsed');

      loadSpec();
      expect(handle().revealStep).toHaveBeenCalledTimes(1);
      expect(handle().revealStep).toHaveBeenCalledWith('fetch');
      expect(handle().openForm).not.toHaveBeenCalled();
      expect(dock()).toHaveAttribute('data-state', 'collapsed');

      fireEvent.click(within(dock()).getByRole('button', { name: 'Activity' }));
      expect(stepRow('fetch')).toHaveAttribute('aria-current', 'true');
      expect(nav.location).toBe('/dags/f?step=fetch');
    });

    it('opens the node details view once the handle is there', () => {
      renderWorkspace('/dags/f?step=fetch&ndv=docked');
      expect(props().ndv?.state).toBe('docked');
      expect(handle().openForm).not.toHaveBeenCalled();

      loadSpec();
      expect(handle().openForm).toHaveBeenCalledTimes(1);
      expect(handle().openForm).toHaveBeenCalledWith('fetch');
      expect(handle().revealStep).not.toHaveBeenCalled();

      act(() => props().onSelectedStepNameChange?.('store'));
      expect(handle().openForm).toHaveBeenCalledTimes(1);
    });
  });

  it('opens the node details view on open only, with a replace write', () => {
    renderWorkspace('/dags/f');
    loadSpec();
    expect(props().openNdvOn).toBe('open');

    // A single click selects only.
    act(() => props().onSelectedStepNameChange?.('fetch'));
    expect(nav.location).toBe('/dags/f?step=fetch');
    expect(props().ndv?.state).toBeNull();

    // Enter or double-click: DAGSpec asks the host to open the view.
    act(() => props().ndv?.onStateChange('docked'));
    expect(nav.location).toBe('/dags/f?step=fetch&ndv=docked');
    expect(nav.type).toBe('REPLACE');
    expect(props().ndv?.state).toBe('docked');
  });

  // The node view and the Activity panel take turns, so they are never two
  // layers at once: the Esc closes the menu, then the node view.
  it('closes the top layer only on each Esc: menu, then NDV', async () => {
    const user = userEvent.setup();
    renderWorkspace('/dags/f?step=fetch&ndv=docked', freshRun(), <Menu />);
    loadSpec();
    await user.click(screen.getByRole('button', { name: 'Workflow menu' }));
    expect(screen.getByRole('menu')).toBeInTheDocument();

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('menu')).toBeNull();
    expect(props().ndv?.state).toBe('docked');

    await user.keyboard('{Escape}');
    expect(props().ndv?.state).toBeNull();
    expect(nav.location).toBe('/dags/f?step=fetch');
    expect(nav.type).toBe('REPLACE');
  });

  it('restores a maximized Activity panel on Esc, then keeps it open', async () => {
    const user = userEvent.setup();
    renderWorkspace('/dags/f?step=fetch', freshRun(), <Menu />);
    loadSpec();
    // A real click puts the focus in the Activity panel, which owns this Esc.
    await user.click(within(dock()).getByRole('button', { name: 'Maximize' }));
    expect(dock()).toHaveAttribute('data-maximized', 'true');

    await user.keyboard('{Escape}');
    expect(dock()).not.toHaveAttribute('data-maximized');
    expect(dock()).toHaveAttribute('data-state', 'open');
    expect(nav.location).toBe('/dags/f?step=fetch');
  });

  it('collapses the Activity panel while the node view is open', () => {
    renderWorkspace('/dags/f?step=fetch&ndv=docked', freshRun());
    loadSpec();
    expect(dock()).toHaveAttribute('data-state', 'collapsed');
  });

  // The canvas keymap must not take Esc ('clear selection') while the NDV
  // is open, so that the workspace closes the NDV first.
  it('gives Esc on the canvas to the workspace while the NDV is open', () => {
    renderWorkspace('/dags/f?step=fetch&ndv=docked', freshRun());
    loadSpec();
    expect(props().chrome?.escapeToHost).toBe(true);

    act(() => props().ndv?.onStateChange(null));
    expect(nav.location).toBe('/dags/f?step=fetch');
    expect(props().chrome?.escapeToHost).toBeFalsy();
  });
});
