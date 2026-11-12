// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useNavigationType,
  type NavigateFunction,
} from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { WorkspaceDataContext } from '../../data/context';
import { dagPage, node, run } from '../../data/__tests__/fixtures';
import {
  firstFailedStep,
  nodesByStep,
  waitingCounts,
} from '../../data/runFacts';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import { DockProvider, useDock, type DockController } from '../DockContext';
import { useDockAutoOpen } from '../useDockAutoOpen';

function fakeRun(details: ApiDAGRunDetails | null): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: details ? { name: details.name, runId: details.dagRunId } : null,
    details,
    hasRun: !!details,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {},
    nodeByStep: nodesByStep(details),
    handlers: [],
    waiting: waitingCounts(details),
    failedStep: firstFailedStep(details),
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => {}),
  };
}

interface Probe {
  dock: DockController;
  location: string;
  navigation: string;
  navigate: NavigateFunction;
  setDetails(d: ApiDAGRunDetails | null): void;
}

function renderAutoOpen(
  details: ApiDAGRunDetails | null,
  url = '/dags/demo'
): Probe {
  const probe = {} as Probe;

  function Data({ children }: { children: ReactNode }) {
    const [current, setCurrent] = useState(details);
    probe.setDetails = setCurrent;
    return (
      <WorkspaceDataContext.Provider
        value={{
          fileName: 'demo',
          dagData: dagPage(current ?? run('')),
          workspaceName: null,
          remoteNode: 'local',
          run: fakeRun(current),
          refresh: async () => {},
          registerHistory: () => () => {},
        }}
      >
        {children}
      </WorkspaceDataContext.Provider>
    );
  }

  function Host() {
    useDockAutoOpen();
    probe.dock = useDock();
    const location = useLocation();
    probe.location = location.pathname + location.search;
    probe.navigation = useNavigationType();
    probe.navigate = useNavigate();
    return null;
  }

  render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route
          path="/dags/:fileName"
          element={
            <Data>
              <DockProvider>
                <Host />
              </DockProvider>
            </Data>
          }
        />
      </Routes>
    </MemoryRouter>
  );
  return probe;
}

const failedRun = (id: string) =>
  run(
    id,
    [node('a'), node('b', NodeStatus.Failed), node('c', NodeStatus.Failed)],
    Status.Failed
  );

const runningRun = (id: string) =>
  run(id, [node('a', NodeStatus.Running)], Status.Running);

const agentWaiting = node('ask', NodeStatus.Running, {
  agentSession: { state: 'waiting' },
});
const approvalWaiting = node('approve', NodeStatus.Waiting, {
  step: { approval: {} },
});

// The Run panel starts collapsed: a collapsed one shows whether the rules
// open it.
beforeEach(() => {
  localStorage.clear();
});

// A run that the user starts in this tab: the dock records it, then the run
// arrives and later fails.
function startRun(probe: Probe, id: string) {
  act(() => probe.dock.noteUserRun!(id));
}

// parity:overview-tab-auto-switch
// parity:run-tabs-auto-switch
// parity:steps-inline-log
describe('useDockAutoOpen', () => {
  it('opens Log on the failed step when a run that the user started fails', () => {
    const probe = renderAutoOpen(runningRun('user-1'));
    startRun(probe, 'user-1');
    expect(probe.dock.open).toBe(false);

    act(() => probe.setDetails(failedRun('user-1')));
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.tab).toBe('log');
    expect(probe.dock.autoStep).toEqual({ step: 'b', stream: 'stderr' });
    expect(probe.location).toBe('/dags/demo');
    expect(probe.navigation).toBe('POP');
  });

  it('does not open for a failed run that is loaded with the page', () => {
    const probe = renderAutoOpen(failedRun('loaded-1'));
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.autoStep).toBeNull();
  });

  it('does not open for a run that the user did not start', () => {
    const probe = renderAutoOpen(runningRun('other-1'));
    act(() => probe.setDetails(failedRun('other-1')));
    expect(probe.dock.open).toBe(false);
  });

  it('does not open when a run starts', () => {
    const probe = renderAutoOpen(null);
    startRun(probe, 'start-1');
    act(() => probe.setDetails(runningRun('start-1')));
    expect(probe.dock.open).toBe(false);
  });

  it('does not open for waiting work', () => {
    const probe = renderAutoOpen(
      run('wait-1', [approvalWaiting, agentWaiting], Status.Waiting)
    );
    startRun(probe, 'wait-1');
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.tab).not.toBe('agent');
  });

  it('acts at most once for each run ID', () => {
    const probe = renderAutoOpen(runningRun('once-1'));
    startRun(probe, 'once-1');
    act(() => probe.setDetails(failedRun('once-1')));
    expect(probe.dock.open).toBe(true);
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(false);

    act(() =>
      probe.setDetails(
        run('once-1', [node('a'), node('z', NodeStatus.Failed)], Status.Failed)
      )
    );
    expect(probe.dock.open).toBe(false);
  });

  it('remembers the runs in memory across mounts', () => {
    const first = renderAutoOpen(runningRun('mount-1'));
    startRun(first, 'mount-1');
    act(() => first.setDetails(failedRun('mount-1')));
    expect(first.dock.open).toBe(true);

    const second = renderAutoOpen(runningRun('mount-1'));
    startRun(second, 'mount-1');
    act(() => second.setDetails(failedRun('mount-1')));
    expect(second.dock.open).toBe(false);
  });

  it('does nothing when ?step is set', () => {
    const url = '/dags/demo?step=a';
    const probe = renderAutoOpen(runningRun('linked-step'), url);
    startRun(probe, 'linked-step');
    act(() => probe.setDetails(failedRun('linked-step')));
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.autoStep).toBeNull();
    expect(probe.location).toBe(url);

    // The run stays handled after the link is cleared.
    act(() => {
      void probe.navigate('/dags/demo');
    });
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.autoStep).toBeNull();
  });

  // The ?dock link opens the dock itself; the rules do not change its tab.
  it('does nothing when ?dock is set', () => {
    const url = '/dags/demo?dock=outputs';
    const probe = renderAutoOpen(runningRun('linked-dock'), url);
    startRun(probe, 'linked-dock');
    act(() => probe.setDetails(failedRun('linked-dock')));
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.tab).toBe('outputs');
    expect(probe.dock.autoStep).toBeNull();
    expect(probe.location).toBe(url);
  });

  it('does nothing for a finished run that did not fail', () => {
    const probe = renderAutoOpen(runningRun('ok-1'));
    startRun(probe, 'ok-1');
    act(() => probe.setDetails(run('ok-1', [node('a')], Status.Success)));
    expect(probe.dock.open).toBe(false);
  });

  it('does nothing for a workflow that never ran', () => {
    const probe = renderAutoOpen(null);
    expect(probe.dock.open).toBe(false);
  });
});
