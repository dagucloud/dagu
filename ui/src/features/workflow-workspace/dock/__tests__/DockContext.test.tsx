// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, renderHook } from '@testing-library/react';
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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { WorkspaceDataContext } from '../../data/context';
import { dagPage, node, run } from '../../data/__tests__/fixtures';
import {
  firstFailedStep,
  nodesByStep,
  waitingCounts,
} from '../../data/runFacts';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import {
  DOCK_FAILURE_HEIGHT,
  DOCK_HEIGHT_KEY,
  DOCK_OPEN_KEY,
  defaultDockHeight,
  DockProvider,
  useDock,
  useDockCommands,
  useDockFrame,
  type DockCommands,
  type DockController,
  type DockFrame,
} from '../DockContext';

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

const finished = run('r1', [node('a'), node('b')], Status.Success);
// A run whose step 'ask' has the agent tab.
const waitingAgent = run(
  'r2',
  [node('ask', NodeStatus.Waiting, { agentSession: { state: 'waiting' } })],
  Status.Waiting
);

interface Probe {
  dock: DockController;
  commands: DockCommands;
  frame: DockFrame;
  location: string;
  navigation: string;
  navigate: NavigateFunction;
  setDetails(d: ApiDAGRunDetails | null): void;
}

function renderDock(
  url:
    | string
    | { pathname: string; search: string; state?: unknown } = '/dags/demo',
  details: ApiDAGRunDetails | null = finished
) {
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

  function Read() {
    probe.dock = useDock();
    probe.commands = useDockCommands();
    probe.frame = useDockFrame();
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
                <Read />
              </DockProvider>
            </Data>
          }
        />
      </Routes>
    </MemoryRouter>
  );
  return probe;
}

function query(probe: Probe): URLSearchParams {
  return new URLSearchParams(probe.location.split('?')[1] ?? '');
}

// Most tests need an open panel: the user left it open (stored). The tests of
// the default clear the storage.
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem(DOCK_OPEN_KEY, 'true');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('DockProvider commands', () => {
  // A component that only calls the commands must not render again when the
  // panel opens, changes tab or is maximized.
  it('keeps the identity of the commands when the state changes', () => {
    localStorage.clear();
    const probe = renderDock();
    const before = probe.commands;
    act(() => probe.commands.toggle());
    expect(probe.dock.open).toBe(true);
    act(() => probe.commands.setTab('log', { user: false }));
    act(() => probe.commands.maximize(true));
    act(() => probe.commands.openStep('a'));
    expect(probe.dock.maximized).toBe(true);
    expect(probe.commands).toBe(before);
  });

  it('keeps the frame while only the state changes', () => {
    localStorage.clear();
    const probe = renderDock();
    const frame = probe.frame;
    act(() => probe.commands.toggle());
    act(() => probe.commands.maximize(true));
    expect(probe.frame).toBe(frame);
  });

  it('reads the tab of the last render in openStep with keepTab', () => {
    const probe = renderDock('/dags/demo?dock=timeline');
    const open = probe.commands.openStep;
    act(() => open('a', { keepTab: true }));
    expect(probe.dock.tab).toBe('log');
    expect(probe.commands.openStep).toBe(open);
  });

  it('keeps the tab in openStep with keepTab when the step has it', () => {
    const probe = renderDock('/dags/demo?dock=agent', waitingAgent);
    act(() => probe.commands.openStep('ask', { keepTab: true }));
    expect(probe.dock.tab).toBe('agent');
    expect(query(probe).get('dock')).toBe('agent');
    expect(query(probe).get('step')).toBe('ask');
  });
});

describe('DockProvider default', () => {
  beforeEach(() => localStorage.clear());

  // parity:chg-dock-collapsed-default
  it('starts collapsed, with nothing stored', () => {
    const probe = renderDock();
    expect(probe.dock.open).toBe(false);
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBeNull();
  });

  it('stores the open state under the v2 key', () => {
    expect(DOCK_OPEN_KEY).toBe('dagu.workspace.dockOpen.v2');
    const probe = renderDock();
    act(() => probe.dock.toggle());
    expect(localStorage.getItem('dagu.workspace.dockOpen.v2')).toBe('true');
    expect(renderDock().dock.open).toBe(true);
  });

  it('ignores the open state that the first version stored', () => {
    localStorage.setItem('dagu.workspace.dockOpen', 'true');
    expect(renderDock().dock.open).toBe(false);
  });

  it('starts open when ?dock is in the URL at load', () => {
    const probe = renderDock('/dags/demo?dock=timeline');
    expect(probe.dock.open).toBe(true);
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBeNull();
  });

  it('starts open when ?run is in the URL at load', () => {
    const probe = renderDock('/dags/demo?run=done');
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.tab).toBe('timeline');
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBeNull();
  });

  // The page adds ?run=<latest> by itself (history replace, marked in the
  // router state): that is no link to a run, also after a reload or a Back.
  // parity:chg-run-url-latest
  it('stays closed for the ?run of the redirect to the latest run', () => {
    const probe = renderDock({
      pathname: '/dags/demo',
      search: '?run=done',
      state: { wwRunRedirect: true },
    });
    expect(probe.dock.open).toBe(false);
  });

  it('does not open when ?run appears after load', () => {
    const probe = renderDock();
    act(() => {
      void probe.navigate('/dags/demo?run=done');
    });
    expect(probe.dock.open).toBe(false);
  });

  it('records the runs that the user started in this tab', () => {
    const probe = renderDock();
    expect(probe.frame.startedRuns.size).toBe(0);
    act(() => probe.dock.noteUserRun!('run-1'));
    act(() => probe.dock.noteUserRun!('run-1'));
    expect([...probe.frame.startedRuns]).toEqual(['run-1']);
  });
});

describe('DockProvider', () => {
  it('opens on the timeline tab at about 40 percent of the window', () => {
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(1000);
    const probe = renderDock();
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.maximized).toBe(false);
    expect(probe.dock.tab).toBe('timeline');
    expect(probe.dock.height).toBe(400);
    expect(defaultDockHeight()).toBe(400);
  });

  it('keeps the default height above its floor in a short window', () => {
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(700);
    expect(defaultDockHeight()).toBe(300);
  });

  // The log of a step is what a selected step or a failed run shows.
  it('starts on the log tab for a selected step or a failed run', () => {
    expect(renderDock('/dags/demo?step=a').dock.tab).toBe('log');
    const failed = run('r3', [node('a', NodeStatus.Failed)], Status.Failed);
    expect(renderDock('/dags/demo', failed).dock.tab).toBe('log');
  });

  it('keeps a tab that the user chose when a step is selected', () => {
    const probe = renderDock('/dags/demo?dock=timeline&step=a');
    expect(probe.dock.tab).toBe('timeline');
  });

  it('reads ?dock=steps and ?dock=spec as no tab', () => {
    expect(renderDock('/dags/demo?dock=steps').dock.tab).toBe('timeline');
    expect(renderDock('/dags/demo?dock=spec').dock.tab).toBe('timeline');
  });

  // A failed step needs room for the banner, the log toolbar and a log.
  it('opens a failed step with room for its banner and log, unstored', () => {
    const probe = renderDock();
    act(() => probe.frame.showAuto('log', { step: 'b', stream: 'stderr' }));
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.height).toBe(DOCK_FAILURE_HEIGHT);
    expect(localStorage.getItem(DOCK_HEIGHT_KEY)).toBeNull();
  });

  it('keeps a taller dock, with or without a step', () => {
    localStorage.setItem(DOCK_HEIGHT_KEY, '400');
    const probe = renderDock();
    act(() => probe.frame.showAuto('log', { step: 'b' }));
    expect(probe.dock.height).toBe(400);
    const other = renderDock();
    act(() => other.frame.showAuto('agent', null));
    expect(other.dock.height).toBe(400);
  });

  it('writes ?dock with replace for a user tab choice only', () => {
    const probe = renderDock();
    act(() => probe.dock.setTab('timeline', { user: false }));
    expect(probe.dock.tab).toBe('timeline');
    expect(probe.dock.open).toBe(true);
    expect(probe.location).toBe('/dags/demo');

    act(() => probe.dock.setTab('outputs', { user: true }));
    expect(probe.dock.tab).toBe('outputs');
    expect(probe.location).toBe('/dags/demo?dock=outputs');
    expect(probe.navigation).toBe('REPLACE');
  });

  // The form of a waiting action sits below the fold of the default height.
  it('opens the Actions tab at 60 percent of the window, without storing it', () => {
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(1000);
    const probe = renderDock();
    act(() => probe.dock.setTab('actions', { user: true }));
    expect(probe.dock.height).toBe(600);
    expect(localStorage.getItem(DOCK_HEIGHT_KEY)).toBeNull();

    act(() => probe.dock.setTab('outputs', { user: true }));
    expect(probe.dock.height).toBe(600);
  });

  it('keeps a taller dock when Actions opens', () => {
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(1000);
    localStorage.setItem(DOCK_HEIGHT_KEY, '620');
    const probe = renderDock();
    act(() => probe.dock.setTab('actions', { user: true }));
    expect(probe.dock.height).toBe(620);
  });

  it('opens a step log and writes the target with replace', () => {
    const probe = renderDock();
    act(() =>
      probe.dock.openStep({
        step: 'b',
        stream: 'stderr',
        foreach: { item: 'x', bodyStep: 'y' },
        subRun: { dagName: 'child', subDAGRunId: 's1' },
      })
    );
    expect(probe.dock.tab).toBe('log');
    expect(probe.dock.open).toBe(true);
    expect(Object.fromEntries(query(probe))).toEqual({
      step: 'b',
      dock: 'log',
      stream: 'stderr',
      item: 'x',
      body: 'y',
      sub: 's1',
    });
    expect(probe.navigation).toBe('REPLACE');

    act(() => probe.dock.openStep('a', { maximize: true }));
    expect(Object.fromEntries(query(probe))).toEqual({
      step: 'a',
      dock: 'log',
    });
    expect(probe.dock.maximized).toBe(true);
  });

  it('opens the run log without a step', () => {
    const probe = renderDock('/dags/demo?step=a&stream=stdout');
    act(() => probe.dock.openRunLog());
    expect(probe.dock.tab).toBe('log');
    expect(probe.dock.open).toBe(true);
    expect(probe.location).toBe('/dags/demo?dock=log');
    expect(probe.navigation).toBe('REPLACE');
  });

  it('toggles and maximizes', () => {
    const probe = renderDock();
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(false);
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBe('false');
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(true);
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBe('true');

    act(() => probe.dock.maximize(true));
    expect(probe.dock.maximized).toBe(true);
    act(() => probe.dock.maximize(false));
    expect(probe.dock.maximized).toBe(false);
    expect(probe.dock.open).toBe(true);

    act(() => probe.dock.maximize(true));
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.maximized).toBe(false);
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBe('false');
  });

  it('maximize opens a collapsed dock', () => {
    localStorage.setItem(DOCK_OPEN_KEY, 'false');
    const probe = renderDock();
    expect(probe.dock.open).toBe(false);
    act(() => probe.dock.maximize(true));
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.maximized).toBe(true);
  });

  it('reads the stored open state and height', () => {
    localStorage.setItem(DOCK_OPEN_KEY, 'false');
    localStorage.setItem(DOCK_HEIGHT_KEY, '200');
    const probe = renderDock();
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.height).toBe(200);
  });

  it('falls back to the defaults when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const probe = renderDock();
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.height).toBe(defaultDockHeight());
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(true);
  });

  // parity:overview-tab-url-sync
  it('follows ?dock', () => {
    const probe = renderDock('/dags/demo?dock=timeline');
    expect(probe.dock.tab).toBe('timeline');
  });

  it('opens on the tab of a ?dock link at load and writes no URL', () => {
    localStorage.setItem(DOCK_OPEN_KEY, 'false');
    const probe = renderDock('/dags/demo?step=a&ndv=docked&dock=log');
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.tab).toBe('log');
    expect(probe.location).toBe('/dags/demo?step=a&ndv=docked&dock=log');
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBe('false');
  });

  it('closes a dock that a ?dock link opened, and it stays closed', () => {
    const probe = renderDock('/dags/demo?dock=log');
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(false);
    expect(probe.location).toBe('/dags/demo?dock=log');
    act(() => probe.setDetails(run('r2', [node('a')], Status.Success)));
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.tab).toBe('log');
  });

  it('opens a closed dock when ?dock changes, such as Back or a link', () => {
    const probe = renderDock('/dags/demo?dock=outputs');
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(false);

    act(() => {
      void probe.navigate('/dags/demo?dock=timeline');
    });
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.tab).toBe('timeline');

    act(() => probe.dock.toggle());
    act(() => {
      void probe.navigate(-1);
    });
    expect(probe.location).toBe('/dags/demo?dock=outputs');
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.tab).toBe('outputs');

    // A URL without ?dock leaves the open state as it is.
    act(() => probe.dock.toggle());
    act(() => {
      void probe.navigate('/dags/demo');
    });
    expect(probe.dock.open).toBe(false);
  });

  // parity:overview-tab-unavailable-fallback
  it('falls back to log when the tab disappears and writes no URL', () => {
    const probe = renderDock('/dags/demo?dock=agent', waitingAgent);
    expect(probe.dock.tab).toBe('agent');

    act(() => probe.setDetails(finished));
    expect(probe.dock.tab).toBe('log');
    expect(probe.location).toBe('/dags/demo?dock=agent');

    act(() => probe.setDetails(waitingAgent));
    expect(probe.dock.tab).toBe('agent');
  });

  it('has only the log tab for a workflow that never ran', () => {
    const probe = renderDock('/dags/demo?dock=timeline', null);
    expect(probe.dock.tab).toBe('log');
    expect(probe.frame.tabs).toEqual(['log']);
  });
});

// The node view of a step needs the room of the Run panel.
describe('yieldToView', () => {
  it('collapses the open panel and brings it back at its height', () => {
    localStorage.setItem(DOCK_HEIGHT_KEY, '350');
    const probe = renderDock();
    expect(probe.dock.open).toBe(true);
    act(() => probe.dock.maximize(true));

    act(() => probe.dock.yieldToView!(true));
    expect(probe.dock.open).toBe(false);
    expect(probe.dock.maximized).toBe(false);
    // Nothing is stored: the user keeps the panel open for the next visit.
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBe('true');

    act(() => probe.dock.yieldToView!(false));
    expect(probe.dock.open).toBe(true);
    expect(probe.dock.height).toBe(350);
    expect(probe.dock.maximized).toBe(true);
  });

  it('leaves a collapsed panel collapsed', () => {
    localStorage.setItem(DOCK_OPEN_KEY, 'false');
    const probe = renderDock();
    act(() => probe.dock.yieldToView!(true));
    act(() => probe.dock.yieldToView!(false));
    expect(probe.dock.open).toBe(false);
  });

  it('does not bring the panel back after the user collapsed it', () => {
    const probe = renderDock();
    act(() => probe.dock.yieldToView!(true));
    // The user opens the panel, then collapses it again.
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(true);
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(false);
    act(() => probe.dock.yieldToView!(false));
    expect(probe.dock.open).toBe(false);
  });

  it('keeps the panel that the user opened while the view is open', () => {
    const probe = renderDock();
    act(() => probe.dock.yieldToView!(true));
    act(() => probe.dock.setTab('log', { user: false }));
    expect(probe.dock.open).toBe(true);
    act(() => probe.dock.yieldToView!(false));
    expect(probe.dock.open).toBe(true);
  });

  it('collapses the panel again for a new view', () => {
    const probe = renderDock();
    act(() => probe.dock.yieldToView!(true));
    act(() => probe.dock.yieldToView!(false));
    act(() => probe.dock.yieldToView!(true));
    expect(probe.dock.open).toBe(false);
  });

  it('stays collapsed with restore off', () => {
    const probe = renderDock();
    act(() => probe.dock.yieldToView!(true, { restore: false }));
    act(() => probe.dock.yieldToView!(false));
    expect(probe.dock.open).toBe(false);
    // The panel opens again with the next click on its bar.
    act(() => probe.dock.toggle());
    expect(probe.dock.open).toBe(true);
  });
});

describe('useDock', () => {
  it('is a closed no-op dock outside the provider', () => {
    const { result } = renderHook(() => useDock());
    expect(result.current.open).toBe(false);
    expect(() => result.current.openStep('a')).not.toThrow();
  });
});
