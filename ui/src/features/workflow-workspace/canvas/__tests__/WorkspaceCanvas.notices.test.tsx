// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import type React from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import type DAGSpec from '@/features/dags/components/dag-editor/DAGSpec';
import type {
  DAGPageData,
  WorkspaceNotice,
  WorkspacePermissions,
  WorkspaceRunValue,
} from '../../contracts';
import type { DockController } from '../../dock/DockContext';
import type { StepRunActions } from '../../step/useStepRunActions';
import { WorkspaceCanvas } from '../WorkspaceCanvas';
import { WorkspaceEditorProvider } from '../WorkspaceEditorContext';

type SpecProps = React.ComponentProps<typeof DAGSpec>;

const host = vi.hoisted(() => ({ props: null as SpecProps | null }));
vi.mock('@/features/dags/components/dag-editor/DAGSpec', () => ({
  default: (props: SpecProps) => {
    host.props = props;
    return <div data-testid="dag-spec-stand-in" />;
  },
}));

const ws = vi.hoisted(() => ({
  run: null as unknown,
  permissions: null as unknown,
  actions: null as unknown,
  dock: null as unknown,
  notices: [] as unknown[],
}));
vi.mock('../../data', () => ({
  useWorkspaceRun: () => ws.run,
  useWorkspacePermissions: () => ws.permissions,
}));
vi.mock('../../step/useStepRunActions', () => ({
  useStepRunActions: () => ws.actions,
}));
vi.mock('../../step/useReuseSource', () => ({
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
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ws.dock,
  useDockCommands: () => ws.dock,
}));
vi.mock('../../notices/useWorkspaceNotices', () => ({
  useWorkspaceNotices: () => ws.notices,
}));

const dagData = {
  dag: { name: 'example' },
  localDags: [],
  editorHints: { customActions: [] },
  latestDAGRun: {},
  suspended: false,
  pinned: false,
} as unknown as DAGPageData;

function makeRun(): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: 'example', runId: 'run-7' },
    details: null,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: { runId: 'run-7', status: Status.Success },
    overlay: {},
    nodeByStep: new Map(),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => undefined),
  };
}

function makePermissions(canExecute: boolean): WorkspacePermissions {
  return {
    canWrite: true,
    canRunDags: true,
    canExecute,
    canExecuteForWorkspace: true,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  };
}

function makeActions(): StepRunActions {
  return {
    can: vi.fn((step: string) => ({
      retry: step === 'a',
      setStatus: step === 'b',
      runStep: true,
      subRuns: step === 'c',
    })),
    retryStep: vi.fn(),
    setStatus: vi.fn(),
    runStep: vi.fn(),
    runUpstream: vi.fn(),
    followed: [],
    clearFollowed: vi.fn(),
    openSubRun: vi.fn(),
    openLogs: vi.fn(),
    dialogs: null,
  };
}

function makeDock(): DockController {
  return {
    open: false,
    tab: 'log',
    height: 0,
    maximized: false,
    setTab: vi.fn(),
    toggle: vi.fn(),
    maximize: vi.fn(),
    openStep: vi.fn(),
    openRunLog: vi.fn(),
  };
}

function renderCanvas() {
  return render(
    <MemoryRouter initialEntries={['/dags/example.yaml']}>
      <Routes>
        <Route
          path="/dags/:fileName"
          element={
            <WorkspaceEditorProvider>
              <WorkspaceCanvas fileName="example.yaml" dagData={dagData} />
            </WorkspaceEditorProvider>
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

let actions: StepRunActions;
let dock: DockController;

beforeEach(() => {
  host.props = null;
  actions = makeActions();
  dock = makeDock();
  ws.run = makeRun();
  ws.permissions = makePermissions(true);
  ws.actions = actions;
  ws.dock = dock;
  ws.notices = [];
});

// parity:overview-summary-agent-bar parity:overview-approval-resume-alert
// parity:run-approval-resume-alert parity:overview-monitoring-offer
describe('WorkspaceCanvas notices', () => {
  // One line for the run notices and the offer. The run chip of the bar
  // says that a newer run is active, so that notice stays out.
  it('gives DAGSpec the run notices and the offer, not newer-run-active', () => {
    const waiting: WorkspaceNotice = {
      id: 'actions-waiting',
      tone: 'info',
      message: '2 actions are waiting.',
      priority: 40,
    };
    const offer: WorkspaceNotice = {
      id: 'monitoring-offer',
      tone: 'info',
      message: 'Get an email if this server stops',
      priority: 10,
    };
    ws.notices = [
      waiting,
      {
        id: 'newer-run-active',
        tone: 'info',
        message: 'A newer run is active',
        priority: 30,
      },
      offer,
    ];
    renderCanvas();
    expect(props().notices).toEqual([waiting, offer]);
  });

  it('gives an empty list when no notice applies', () => {
    renderCanvas();
    expect(props().notices).toEqual([]);
  });

  it('hides the run bar and asks for compact notices', () => {
    renderCanvas();
    expect(props().chrome).toEqual({
      hideRunBar: true,
      compactNotices: true,
    });
  });

  it('binds the step commands to the step run actions', () => {
    renderCanvas();
    props().onStepCommand?.({ kind: 'runStep', step: 'a' });
    props().onStepCommand?.({ kind: 'retryStep', step: 'a' });
    props().onStepCommand?.({ kind: 'setStatus', step: 'b' });
    props().onStepCommand?.({ kind: 'openSubRun', step: 'c' });
    props().onStepCommand?.({ kind: 'viewLogs', step: 'a' });
    expect(actions.runStep).toHaveBeenCalledTimes(1);
    expect(actions.runStep).toHaveBeenCalledWith('a');
    expect(actions.retryStep).toHaveBeenCalledWith('a');
    expect(actions.setStatus).toHaveBeenCalledWith('b');
    expect(actions.openSubRun).toHaveBeenCalledWith('c', { newTab: undefined });
    expect(actions.openLogs).toHaveBeenCalledWith({
      step: 'a',
      stream: undefined,
    });
  });

  it('gates Run step by the permission and the run items by the run', () => {
    ws.permissions = makePermissions(false);
    renderCanvas();
    const gates = props().stepCommandGates!;
    expect(gates.canRunStep).toBe(false);
    expect([gates.canRetry('a'), gates.canSetStatus('b')]).toEqual([
      true,
      true,
    ]);
    expect(gates.hasSubRuns('c')).toBe(true);
    expect(gates.canRetry('live')).toBe(false);
  });
});
