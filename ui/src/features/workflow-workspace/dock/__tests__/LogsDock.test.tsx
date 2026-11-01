// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useEffect, type ReactNode } from 'react';
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
  RightPanelProvider,
  useRightPanelClaim,
} from '@/contexts/RightPanelContext';
import {
  useWorkspaceEditorBinding,
  WorkspaceEditorProvider,
} from '../../canvas/WorkspaceEditorContext';
import type { ApiDAGDetails, WorkspaceEditorHandle } from '../../contracts';
import { WorkspaceDataContext } from '../../data/context';
import { dagPage, node, run } from '../../data/__tests__/fixtures';
import {
  firstFailedStep,
  nodesByStep,
  waitingCounts,
} from '../../data/runFacts';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import {
  DOCK_HEIGHT_KEY,
  DOCK_OPEN_KEY,
  DockProvider,
  defaultDockHeight,
  maxDockHeight,
  useDock,
} from '../DockContext';
import { LogsDock } from '../LogsDock';

const mounts = vi.hoisted(() => ({ count: 0 }));

// The timeline and the steps navigation have their own tests.
vi.mock('../RunTimeline', () => ({
  RunTimeline: () => <div data-testid="timeline-stub" />,
}));
vi.mock('../RunStepsNav', () => ({
  RunStepsNav: () => <div data-testid="steps-nav-stub" />,
}));

// Stateful bodies, so a test can tell a kept instance from a new one. 'chat'
// has no body, like a tab that is not registered yet.
vi.mock('../tabs', async () => {
  const { useEffect, useState } = await import('react');
  const make = (name: string) =>
    function Body() {
      const [clicks, setClicks] = useState(0);
      useEffect(() => {
        mounts.count += 1;
      }, []);
      return (
        <div data-testid={`body-${name}`}>
          <button type="button" onClick={() => setClicks(clicks + 1)}>
            {`clicks ${clicks}`}
          </button>
          <input aria-label="field" />
        </div>
      );
    };
  return {
    dockTabBodies: Object.fromEntries(
      ['log', 'agent', 'actions', 'timeline', 'outputs'].map((tab) => [
        tab,
        make(tab),
      ])
    ),
  };
});

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

const location = {
  current: '',
  navigation: '',
  navigate: (() => {}) as NavigateFunction,
};

function Location() {
  const loc = useLocation();
  location.current = loc.pathname + loc.search;
  location.navigation = useNavigationType();
  location.navigate = useNavigate();
  return null;
}

function renderDock(
  details: ApiDAGRunDetails | null,
  url = '/dags/demo',
  extra: ReactNode = null
) {
  const dagData = dagPage(details ?? run(''));
  const data = ({ children }: { children: ReactNode }) => (
    <WorkspaceDataContext.Provider
      value={{
        fileName: 'demo',
        dagData,
        workspaceName: null,
        remoteNode: 'local',
        run: fakeRun(details),
        refresh: async () => {},
        registerHistory: () => () => {},
      }}
    >
      {children}
    </WorkspaceDataContext.Provider>
  );
  const Data = data;
  const element = (
    <Data>
      <RightPanelProvider>
        <WorkspaceEditorProvider>
          <DockProvider>
            <Location />
            {extra}
            <LogsDock fileName="demo" dag={dagData.dag as ApiDAGDetails} />
          </DockProvider>
        </WorkspaceEditorProvider>
      </RightPanelProvider>
    </Data>
  );
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/dags/:fileName" element={element} />
        <Route path="/dags/:fileName/:tab" element={element} />
      </Routes>
    </MemoryRouter>
  );
}

const finished = run('done', [node('a'), node('b')], Status.Success);

// Two agents and one approval wait; the run has chat steps too.
const busy = run(
  'busy',
  [
    node('ask1', NodeStatus.Running, { agentSession: { state: 'waiting' } }),
    node('ask2', NodeStatus.Running, { agentSession: { state: 'waiting' } }),
    node('approve', NodeStatus.Waiting, { step: { approval: {} } }),
    node('talk', NodeStatus.Success, {
      step: { executorConfig: { type: 'chat' } },
    }),
  ],
  Status.Waiting
);

function dockSection() {
  return screen.getByTestId('ww-dock');
}

function toggleButton() {
  return within(dockSection()).getByRole('button', {
    name: 'Activity',
  });
}

const originalMatchMedia = window.matchMedia;

function mockWidth(px: number) {
  window.matchMedia = vi.fn((query: string) => {
    const min = Number(/min-width:\s*(\d+)px/.exec(query)?.[1] ?? 0);
    return {
      matches: px >= min,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    } as MediaQueryList;
  });
}

// The Run panel starts collapsed. Most tests open it with the toggle.
beforeEach(() => {
  localStorage.clear();
  mounts.count = 0;
});

afterEach(() => {
  window.matchMedia = originalMatchMedia;
  vi.restoreAllMocks();
});

// parity:log-side-panel
// parity:embed-workflow-overview-summary
describe('LogsDock frame', () => {
  // parity:chg-dock-collapsed-default
  // Collapsed, the panel has no bar: a small tab with its name and its
  // chevron sticks out of the bottom edge, like a binder tab laid flat.
  it('is a collapsed tab by default, named Activity', () => {
    renderDock(finished);
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');
    expect(dockSection()).toHaveClass('h-0');
    expect(screen.getByTestId('ww-dock-bar')).toHaveClass(
      'absolute',
      'bottom-0'
    );
    expect(toggleButton()).toHaveClass('rounded-t-lg', 'border-b-0', 'h-8');
    expect(screen.getByTestId('ww-dock-header-tools')).not.toBeVisible();
    expect(toggleButton()).toHaveAccessibleName('Activity');
    expect(toggleButton()).toHaveTextContent('Activity');
    expect(toggleButton()).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('tabpanel')).toBeNull();
    expect(screen.queryByTestId('body-timeline')).toBeNull();
    expect(screen.queryByRole('separator')).toBeNull();
    // The tabs and the toggle name a panel that exists, also while collapsed.
    const panelId = screen
      .getByRole('tab', { name: 'Log', hidden: true })
      .getAttribute('aria-controls');
    expect(toggleButton()).toHaveAttribute('aria-controls', panelId);
    expect(
      document.getElementById(panelId ?? '')?.closest('[hidden]')
    ).not.toBeNull();
  });

  it('opens on a click, with the timeline above the steps and the tabs', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(toggleButton()).toHaveAccessibleName('Activity');
    expect(toggleButton()).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('timeline-stub')).toBeVisible();
    expect(screen.getByTestId('steps-nav-stub')).toBeVisible();
    expect(screen.getByTestId('body-timeline')).toBeVisible();
    expect(dockSection()).toHaveStyle({ height: `${defaultDockHeight()}px` });
    // The timeline is above the two columns.
    const nav = screen.getByTestId('ww-run-steps-nav');
    expect(
      screen.getByTestId('timeline-stub').compareDocumentPosition(nav) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    // The tabs are on the right of the steps.
    expect(nav.nextElementSibling).toContainElement(
      screen.getByRole('tablist')
    );
  });

  it('is open when the URL has ?dock at load', () => {
    renderDock(finished, '/dags/demo?dock=outputs');
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(toggleButton()).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('body-outputs')).toBeVisible();
  });

  it('is open when the URL has ?run at load', () => {
    renderDock(finished, '/dags/demo?run=done');
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(toggleButton()).toHaveAttribute('aria-expanded', 'true');
    expect(localStorage.getItem(DOCK_OPEN_KEY)).toBeNull();
  });

  it('remembers the toggle under the v2 key', () => {
    const first = renderDock(finished);
    fireEvent.click(toggleButton());
    expect(localStorage.getItem('dagu.workspace.dockOpen.v2')).toBe('true');
    first.unmount();

    renderDock(finished);
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    fireEvent.click(toggleButton());
    expect(localStorage.getItem('dagu.workspace.dockOpen.v2')).toBe('false');
  });

  it('has a heading for the dock', () => {
    renderDock(finished);
    expect(
      within(dockSection()).getByRole('heading', { level: 2, name: 'Logs' })
    ).toBeInTheDocument();
  });

  it('opens at the default height on the timeline body', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(dockSection()).toHaveStyle({ height: `${defaultDockHeight()}px` });
    expect(screen.getByRole('tabpanel')).toBeVisible();
    expect(screen.getByTestId('body-timeline')).toBeInTheDocument();
  });

  it('has no Steps tab and no Spec tab', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    expect(screen.queryByRole('tab', { name: 'Steps' })).toBeNull();
    expect(screen.queryByRole('tab', { name: 'Spec' })).toBeNull();
  });

  it('keeps the body instance when it maximizes and collapses', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    fireEvent.click(screen.getByRole('button', { name: 'clicks 0' }));
    expect(mounts.count).toBe(1);

    fireEvent.click(screen.getByRole('button', { name: 'Maximize' }));
    expect(dockSection()).toHaveAttribute('data-maximized', 'true');
    expect(dockSection()).toHaveStyle({ height: '100%' });
    expect(screen.getByRole('button', { name: 'clicks 1' })).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: 'Restore' }));
    expect(dockSection()).not.toHaveAttribute('data-maximized');

    fireEvent.click(toggleButton());
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');
    expect(screen.getByRole('tabpanel', { hidden: true })).not.toBeVisible();

    fireEvent.click(toggleButton());
    expect(screen.getByRole('button', { name: 'clicks 1' })).toBeVisible();
    expect(mounts.count).toBe(1);
  });

  it('does not collapse on Esc inside the body', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    const field = screen.getByRole('textbox', { name: 'field' });
    field.focus();
    fireEvent.keyDown(field, { key: 'Escape' });
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(screen.getByRole('tabpanel')).toBeVisible();
  });
});

// parity:graph-resize-handle
// parity:run-graph-resize
describe('LogsDock resize', () => {
  it('resizes with the arrow keys, Home and End, and stores the height', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    const separator = screen.getByRole('separator', { name: 'Resize logs' });
    const base = defaultDockHeight();
    expect(separator).toHaveAttribute('aria-valuenow', String(base));

    fireEvent.keyDown(separator, { key: 'ArrowUp' });
    expect(separator).toHaveAttribute('aria-valuenow', String(base + 40));
    expect(dockSection()).toHaveStyle({ height: `${base + 40}px` });
    expect(localStorage.getItem(DOCK_HEIGHT_KEY)).toBe(String(base + 40));

    fireEvent.keyDown(separator, { key: 'ArrowDown' });
    fireEvent.keyDown(separator, { key: 'ArrowDown' });
    expect(separator).toHaveAttribute('aria-valuenow', String(base - 40));

    fireEvent.keyDown(separator, { key: 'Home' });
    expect(separator).toHaveAttribute('aria-valuenow', '120');

    fireEvent.keyDown(separator, { key: 'End' });
    expect(separator).toHaveAttribute('aria-valuenow', String(maxDockHeight()));
  });

  // The window keeps the bar and the floor of the main zone, so that the dock
  // reaches about two thirds of a 900px window (it stopped at 41%).
  it('lets the dock take two thirds of a 900px window', () => {
    const height = window.innerHeight;
    Object.defineProperty(window, 'innerHeight', {
      configurable: true,
      value: 900,
    });
    try {
      expect(maxDockHeight() / 900).toBeGreaterThanOrEqual(0.6);
    } finally {
      Object.defineProperty(window, 'innerHeight', {
        configurable: true,
        value: height,
      });
    }
  });

  // The graph keeps the larger part of the window: the panel opens at about
  // 40% of it.
  it('opens at about 40 percent of a 900px window', () => {
    const height = window.innerHeight;
    Object.defineProperty(window, 'innerHeight', {
      configurable: true,
      value: 900,
    });
    try {
      expect(defaultDockHeight()).toBe(360);
    } finally {
      Object.defineProperty(window, 'innerHeight', {
        configurable: true,
        value: height,
      });
    }
  });

  it('restores the default height on double-click', () => {
    localStorage.setItem(DOCK_HEIGHT_KEY, '200');
    renderDock(finished);
    fireEvent.click(toggleButton());
    const separator = screen.getByRole('separator');
    expect(separator).toHaveAttribute('aria-valuenow', '200');
    fireEvent.doubleClick(separator);
    expect(separator).toHaveAttribute(
      'aria-valuenow',
      String(defaultDockHeight())
    );
    expect(localStorage.getItem(DOCK_HEIGHT_KEY)).toBeNull();
  });

  it('resizes by drag', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    const separator = screen.getByRole('separator');
    fireEvent.pointerDown(separator, { pointerId: 1, button: 0, clientY: 500 });
    const base = defaultDockHeight();
    fireEvent.pointerMove(separator, { pointerId: 1, clientY: 450 });
    expect(dockSection()).toHaveStyle({ height: `${base + 50}px` });
    fireEvent.pointerUp(separator, { pointerId: 1, clientY: 440 });
    expect(separator).toHaveAttribute('aria-valuenow', String(base + 60));
    expect(localStorage.getItem(DOCK_HEIGHT_KEY)).toBe(String(base + 60));
  });

  it('has no separator while maximized', () => {
    renderDock(finished);
    fireEvent.click(toggleButton());
    fireEvent.click(screen.getByRole('button', { name: 'Maximize' }));
    expect(screen.queryByRole('separator')).toBeNull();
  });
});

// parity:overview-inner-tab-bar
// parity:run-tabs-bar
describe('LogsDock tab strip', () => {
  // The strip is in the body of the panel: it needs the panel open.
  beforeEach(() => localStorage.setItem(DOCK_OPEN_KEY, 'true'));

  it('shows the run tabs that have a body, with the waiting badges', () => {
    renderDock(busy);
    const strip = screen.getByRole('tablist');
    expect(
      within(strip)
        .getAllByRole('tab')
        .map((tab) => tab.textContent)
    ).toEqual(['Log', 'Agent2', 'Actions1', 'Timeline', 'Outputs']);
    expect(within(strip).queryByRole('tab', { name: /Chat/ })).toBeNull();
    expect(
      within(strip).getByRole('tab', { name: 'Agent, 2 waiting' })
    ).toBeInTheDocument();
    expect(
      within(strip).getByRole('tab', { name: 'Actions, 1 waiting' })
    ).toBeInTheDocument();
    expect(strip).toHaveClass('h-8');
  });

  // The Timeline and the list of all outputs belong to the run view. With a
  // step open, the strip has no Timeline and the outputs tab is Output.
  it('has no Timeline and names Outputs "Output" when a step is open', () => {
    renderDock(finished, '/dags/demo?step=a');
    const strip = screen.getByRole('tablist');
    expect(
      within(strip)
        .getAllByRole('tab')
        .map((tab) => tab.textContent)
    ).toEqual(['Log', 'Output']);
    expect(within(strip).getByRole('tab', { name: 'Output' }).id).toBe(
      'ww-dock-tab-outputs'
    );
  });

  it('shows the Log tab when ?dock names the Timeline of a step view', () => {
    renderDock(finished, '/dags/demo?dock=timeline&step=a');
    expect(screen.getByRole('tab', { name: 'Log' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.queryByRole('tab', { name: 'Timeline' })).toBeNull();
  });

  it('keeps the Timeline and Outputs in the run view', () => {
    renderDock(finished);
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Log',
      'Timeline',
      'Outputs',
    ]);
  });

  // No width has a More menu or a select: the tabs stay in one strip.
  it.each([900, 500])('keeps every tab in the strip at %ipx', async (width) => {
    mockWidth(width);
    const user = userEvent.setup();
    renderDock(busy);
    expect(screen.queryByRole('button', { name: /More/ })).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
    const strip = screen.getByRole('tablist');
    expect(
      within(strip)
        .getAllByRole('tab')
        .map((tab) => tab.id)
    ).toEqual([
      'ww-dock-tab-log',
      'ww-dock-tab-agent',
      'ww-dock-tab-actions',
      'ww-dock-tab-timeline',
      'ww-dock-tab-outputs',
    ]);

    await user.click(within(strip).getByRole('tab', { name: /Outputs/ }));
    expect(location.current).toBe('/dags/demo?dock=outputs');
    expect(screen.getByTestId('body-outputs')).toBeInTheDocument();
  });

  // parity:overview-tab-url-sync
  it('follows ?dock and writes a tab click with replace', () => {
    renderDock(finished, '/dags/demo?dock=timeline');
    // A ?dock link opens the dock on its tab.
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(screen.getByTestId('body-timeline')).toBeVisible();
    expect(screen.getByRole('tab', { name: 'Timeline' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    fireEvent.click(screen.getByRole('tab', { name: 'Outputs' }));
    expect(location.current).toBe('/dags/demo?dock=outputs');
    expect(location.navigation).toBe('REPLACE');
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(screen.getByTestId('body-outputs')).toBeInTheDocument();
  });

  // parity:overview-tab-url-sync
  it('follows a later ?dock change, such as Back or a link', () => {
    renderDock(finished, '/dags/demo?dock=timeline');
    act(() => {
      void location.navigate('/dags/demo?dock=outputs');
    });
    expect(screen.getByRole('tab', { name: 'Outputs' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    act(() => {
      void location.navigate(-1);
    });
    expect(location.current).toBe('/dags/demo?dock=timeline');
    expect(screen.getByRole('tab', { name: 'Timeline' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  // parity:overview-tab-unavailable-fallback
  it('shows log when the tab is gone and keeps the URL', () => {
    renderDock(finished, '/dags/demo?dock=agent');
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(screen.getByRole('tab', { name: 'Log' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByTestId('body-log')).toBeInTheDocument();
    expect(location.current).toBe('/dags/demo?dock=agent');
  });

  it('shows log for a tab without a body', () => {
    renderDock(busy, '/dags/demo?dock=chat');
    expect(screen.getByRole('tab', { name: 'Log' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  // The old links: the Steps and Spec tabs are gone.
  it.each(['steps', 'spec'])('shows the default tab for ?dock=%s', (tab) => {
    renderDock(finished, `/dags/demo?dock=${tab}`);
    expect(screen.getByRole('tab', { name: 'Timeline' })).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  it('opens by itself on a failed run', () => {
    renderDock(
      run('dock-failed', [node('a', NodeStatus.Failed)], Status.Failed),
      '/dags/demo'
    );
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(screen.getByTestId('body-log')).toBeVisible();
    expect(location.current).toBe('/dags/demo');
  });
});

/** Binds a stand-in editor handle, as WorkspaceCanvas does. */
function BindHandle({ handle }: { handle: WorkspaceEditorHandle }) {
  const { editorHandleRef } = useWorkspaceEditorBinding();
  useEffect(() => {
    editorHandleRef(handle);
  }, [editorHandleRef, handle]);
  return null;
}

/** A control outside the dock that opens it. */
function Opener() {
  const dock = useDock();
  return (
    <button type="button" onClick={dock.toggle}>
      Opener
    </button>
  );
}

/** Holds the node details panel while mounted. */
function NdvPanel() {
  useRightPanelClaim('ndv', true, () => {});
  return null;
}

// parity:run-step-mobile
describe('LogsDock below 768px', () => {
  it('is a bottom sheet at 75vh with a drag grip and a Close button', () => {
    mockWidth(500);
    renderDock(finished);
    expect(dockSection()).not.toHaveAttribute('data-sheet');
    fireEvent.click(toggleButton());
    expect(dockSection()).toHaveAttribute('data-sheet', 'true');
    expect(dockSection()).toHaveClass('fixed', 'bottom-0', 'inset-x-0');
    expect(dockSection()).toHaveStyle({ height: '75vh' });
    expect(screen.queryByRole('separator')).toBeNull();
    expect(dockSection()).toHaveFocus();
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
    const handle = screen.getByTestId('ww-sheet-grip');

    fireEvent.pointerDown(handle, { pointerId: 1, button: 0, clientY: 100 });
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 30 });
    fireEvent.click(handle);
    expect(dockSection()).toHaveAttribute('data-maximized', 'true');
    expect(dockSection()).toHaveStyle({ height: '100dvh' });

    fireEvent.pointerDown(handle, { pointerId: 2, button: 0, clientY: 30 });
    fireEvent.pointerUp(handle, { pointerId: 2, clientY: 200 });
    fireEvent.click(handle);
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');
  });

  // A touch drag can end with no click event; the next tap must still act.
  it('closes on a tap after a drag that had no click', () => {
    mockWidth(500);
    renderDock(finished);
    fireEvent.click(toggleButton());
    let handle = screen.getByTestId('ww-sheet-grip');
    fireEvent.pointerDown(handle, { pointerId: 1, button: 0, clientY: 30 });
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 200 });
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');

    fireEvent.click(toggleButton());
    handle = screen.getByTestId('ww-sheet-grip');
    fireEvent.pointerDown(handle, { pointerId: 2, button: 0, clientY: 30 });
    fireEvent.pointerUp(handle, { pointerId: 2, clientY: 30 });
    fireEvent.click(handle);
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');
  });

  it('is no sheet at 768px and more', () => {
    mockWidth(800);
    renderDock(finished);
    fireEvent.click(toggleButton());
    expect(dockSection()).not.toHaveAttribute('data-sheet');
    expect(dockSection()).toHaveClass('relative');
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
    expect(screen.queryByTestId('ww-sheet-grip')).toBeNull();
  });

  it('opens the node details view of the selected row', () => {
    mockWidth(500);
    const handle = {
      openForm: vi.fn(() => true),
      openYaml: vi.fn(() => true),
      revealStep: vi.fn(() => true),
    } as unknown as WorkspaceEditorHandle;
    renderDock(finished, '/dags/demo?step=b', <BindHandle handle={handle} />);
    fireEvent.click(toggleButton());
    // The name says which step opens; the visible text stays 'Open'.
    const open = screen.getByRole('button', { name: 'Open b' });
    expect(open).toHaveTextContent(/^Open$/);
    fireEvent.click(open);
    // The step opens on its YAML until the form is used.
    expect(handle.openYaml).toHaveBeenCalledWith('b');
    expect(location.current).toBe('/dags/demo?step=b&ndv=expanded');
    expect(location.navigation).toBe('REPLACE');
  });

  it('has no Open button without a selected row', () => {
    mockWidth(500);
    renderDock(finished);
    fireEvent.click(toggleButton());
    expect(screen.queryByRole('button', { name: /^Open\b/ })).toBeNull();
  });

  it('gives the focus back to the opener when it closes', async () => {
    mockWidth(500);
    const user = userEvent.setup();
    renderDock(finished, '/dags/demo', <Opener />);
    const opener = screen.getByRole('button', { name: 'Opener' });
    await user.click(opener);
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    expect(dockSection()).toHaveFocus();

    await user.click(screen.getByRole('button', { name: 'Close' }));
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');
    expect(opener).toHaveFocus();
  });

  it('closes on Esc and gives the focus back', async () => {
    mockWidth(500);
    const user = userEvent.setup();
    renderDock(finished, '/dags/demo', <Opener />);
    const opener = screen.getByRole('button', { name: 'Opener' });
    await user.click(opener);
    await user.keyboard('{Escape}');
    expect(dockSection()).toHaveAttribute('data-state', 'collapsed');
    expect(opener).toHaveFocus();
  });
});

describe('LogsDock Esc', () => {
  function openMaximized(extra: ReactNode = null) {
    renderDock(finished, '/dags/demo', extra);
    fireEvent.click(toggleButton());
    fireEvent.click(screen.getByRole('button', { name: 'Maximize' }));
    expect(dockSection()).toHaveAttribute('data-maximized', 'true');
  }

  it('restores a maximized dock', () => {
    openMaximized();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(dockSection()).not.toHaveAttribute('data-maximized');
    expect(dockSection()).toHaveAttribute('data-state', 'open');
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(dockSection()).toHaveAttribute('data-state', 'open');
  });

  it('leaves Esc to a handler that took it', () => {
    openMaximized();
    const event = new KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    });
    event.preventDefault();
    act(() => {
      document.body.dispatchEvent(event);
    });
    expect(dockSection()).toHaveAttribute('data-maximized', 'true');
  });

  it.each([
    ['an open dialog', '<div role="dialog" data-state="open"></div>'],
    ['an open menu', '<div role="menu"></div>'],
  ])('leaves Esc to %s', (_, html) => {
    openMaximized();
    const layer = document.createElement('div');
    layer.innerHTML = html;
    document.body.append(layer);
    try {
      fireEvent.keyDown(document.body, { key: 'Escape' });
      expect(dockSection()).toHaveAttribute('data-maximized', 'true');
    } finally {
      layer.remove();
    }
  });

  it('leaves Esc to the node details view', () => {
    openMaximized(<NdvPanel />);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(dockSection()).toHaveAttribute('data-maximized', 'true');
  });
});

describe('dockTabBodies', () => {
  it('registers a body for History and none for the Steps and Spec tabs', async () => {
    const real = await vi.importActual<typeof import('../tabs')>('../tabs');
    expect(Object.keys(real.dockTabBodies).sort()).toEqual(
      [
        'actions',
        'agent',
        'artifacts',
        'chat',
        'continuous',
        'history',
        'log',
        'outputs',
        'tasks',
        'timeline',
      ].sort()
    );
  });
});
