// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useLocation } from 'react-router-dom';
import { Status } from '@/api/v1/schema';
import type { RunViewFacts } from '@/features/dags/lib/runViewTabs';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import { useWorkspaceData } from '../data/context';
import type { StepRunTarget } from '../step/StepRunView';
import {
  isRunRedirect,
  useWorkspaceUrl,
  type DockTab,
  type WorkspaceUrlPatch,
} from '../state/workspaceUrl';
import {
  dockFacts,
  dockTabBadges,
  defaultDockTab,
  dockTabsFor,
  fallbackDockTab,
} from './dockTabs';
import { stepDockTabs } from './tabs/stepScope';

export interface DockController {
  open: boolean;
  tab: DockTab;
  height: number;
  maximized: boolean;
  /** user=true writes ?dock (replace). */
  setTab(t: DockTab, o: { user: boolean }): void;
  toggle(): void;
  maximize(on: boolean): void;
  /**
   * Opens tab 'log' on the target; writes ?dock=log&step&stream[&item&body&sub] with replace. A string is { step }.
   * keepTab keeps the current tab when the target step has it.
   */
  openStep(
    target: StepRunTarget | string,
    opts?: { maximize?: boolean; keepTab?: boolean }
  ): void;
  /** Tab 'log' without step: the run summary and the scheduler log (ExecutionLog). */
  openRunLog(): void;
  /**
   * Gives the room of the panel to a view that needs it (the node view of a
   * step). on=true shows the panel as collapsed to its bar, with no stored
   * change. on=false brings it back, at the same height, unless the user
   * used the panel in between (toggle, tab, step) or restore was false.
   * Restore false leaves the panel collapsed when the view closes.
   * Optional so that a test double of the dock may leave it out.
   */
  yieldToView?(on: boolean, opts?: { restore?: boolean }): void;
  /**
   * The step that the dock auto-open chose for the selected run. It is not in
   * the URL; ?step wins over it. null when there is none.
   */
  autoStep?: StepRunTarget | null;
  /**
   * Drops the step that the auto-open chose, so that the selected run shows
   * its run view. Optional so that a test double of the dock may leave it
   * out.
   */
  dropAutoStep?(): void;
  /**
   * Records a run that the user started in this tab. The auto-open rules open
   * the panel when such a run fails. Optional so that a test double of the
   * dock may leave it out.
   */
  noteUserRun?(runId: string): void;
}

/** Frame state of the dock, for LogsDock and the auto-open rules only. */
export interface DockFrame {
  /** Dock tabs of the selected run (dockTabsFor), before the body registry. */
  tabs: DockTab[];
  badges: Partial<Record<DockTab, number>>;
  /** null when the workflow has never run. */
  facts: RunViewFacts | null;
  /** Sets the open height; null restores the default. Stored. */
  resize(height: number | null): void;
  /** IDs of the runs that the user started in this tab (noteUserRun). */
  startedRuns: ReadonlySet<string>;
  /** Opens the dock on a tab without a URL write, as the auto-open rules do. */
  showAuto(tab: DockTab, step: StepRunTarget | null): void;
}

export const DOCK_BAR_HEIGHT = 32;
// The Run panel opens at about 40% of the window, and never under the floor
// that holds the bar, the timeline and a few steps.
export const DOCK_DEFAULT_RATIO = 0.4;
export const DOCK_DEFAULT_MIN_HEIGHT = 300;
export const DOCK_MIN_HEIGHT = 120;
export const DOCK_HEIGHT_KEY = 'dagu.workspace.dockHeight';
// v2: the first version stored a panel that starts open. A stored value of
// that version does not apply to a panel that starts collapsed.
export const DOCK_OPEN_KEY = 'dagu.workspace.dockOpen.v2';

// The window keeps this much above an open dock: the bar and the floor of
// the main zone (MAIN_ZONE_FLOOR_CLASS).
const CANVAS_RESERVE = 360;

// A failed step shows its banner, the log toolbar and a log: the auto-open
// gives it this much, unless the dock is already taller. Not stored.
export const DOCK_FAILURE_HEIGHT = 320;

export function maxDockHeight(): number {
  return Math.max(DOCK_MIN_HEIGHT, window.innerHeight - CANVAS_RESERVE);
}

/** The open height of the Run panel when none is stored. */
export function defaultDockHeight(): number {
  return clampDockHeight(
    Math.max(DOCK_DEFAULT_MIN_HEIGHT, window.innerHeight * DOCK_DEFAULT_RATIO)
  );
}

export function clampDockHeight(value: number): number {
  return Math.round(
    Math.min(maxDockHeight(), Math.max(DOCK_MIN_HEIGHT, value))
  );
}

function readStored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
    return null;
  }
}

function readHeight(): number {
  const stored = readStored(DOCK_HEIGHT_KEY);
  const value = stored === null ? NaN : Number(stored);
  return Number.isFinite(value) ? clampDockHeight(value) : defaultDockHeight();
}

function removeStored(key: string): void {
  try {
    window.localStorage.removeItem(key);
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
}

const NO_STEP: WorkspaceUrlPatch = {
  step: null,
  stream: null,
  item: null,
  body: null,
  sub: null,
};

function targetPatch(target: StepRunTarget): WorkspaceUrlPatch {
  return {
    step: target.step,
    stream: target.stream ?? null,
    item: target.foreach?.item ?? null,
    body: target.foreach?.bodyStep ?? null,
    sub: target.subRun?.subDAGRunId ?? null,
  };
}

/**
 * The commands of the dock. Their identity never changes, so a component
 * that only calls them (useDockCommands) does not render again when the
 * panel opens, closes or changes tab.
 */
export type DockCommands = Pick<
  DockController,
  | 'setTab'
  | 'toggle'
  | 'maximize'
  | 'openStep'
  | 'openRunLog'
  | 'yieldToView'
  | 'noteUserRun'
  | 'dropAutoStep'
>;

const DockContext = createContext<DockController | null>(null);
const DockCommandsContext = createContext<DockCommands | null>(null);
const DockFrameContext = createContext<DockFrame | null>(null);

const NO_DOCK: DockController = {
  open: false,
  tab: 'log',
  height: 0,
  maximized: false,
  setTab() {},
  toggle() {},
  maximize() {},
  openStep() {},
  openRunLog() {},
  yieldToView() {},
  autoStep: null,
  dropAutoStep() {},
  noteUserRun() {},
};

/**
 * Holds the state of the Run panel. It starts collapsed. It starts open when
 * the URL has ?dock or a ?run that a link made at load, or when the user left it open (stored
 * under DOCK_OPEN_KEY). The open state that the user toggles and the height
 * are stored. A ?dock link selects its tab, at load and when ?dock changes. Without a chosen tab, the Log tab shows when
 * a step is selected or the run failed, else the Timeline tab. A tab that
 * the selected run does not have shows 'log'.
 */
export function DockProvider({ children }: { children: ReactNode }) {
  const { run, dagData } = useWorkspaceData('DockProvider');
  const [url, setUrlNow] = useWorkspaceUrl();
  // The URL writer changes with the location. The commands write through
  // this stable function, so that their identity does not change with it.
  const setUrlRef = useRef(setUrlNow);
  setUrlRef.current = setUrlNow;
  const setUrl = useCallback<typeof setUrlNow>(
    (patch, opts) => setUrlRef.current(patch, opts),
    []
  );
  const location = useLocation();
  // The ?run that the page adds itself (the redirect to the latest run) is
  // no request to open the panel: a reload or a Back to it keeps it closed.
  const runLinked = url.run !== null && !isRunRedirect(location.state);
  const [openState, setOpenState] = useState(
    () => url.dock !== null || runLinked || readStored(DOCK_OPEN_KEY) === 'true'
  );
  const [startedRuns, setStartedRuns] = useState<ReadonlySet<string>>(
    () => new Set()
  );
  const noteUserRun = useCallback((id: string) => {
    setStartedRuns((current) =>
      current.has(id) ? current : new Set(current).add(id)
    );
  }, []);
  // A view that needs the room holds the panel collapsed. The open state
  // under it stays, so that the panel comes back as it was.
  const [yielded, setYieldedState] = useState<{ restore: boolean } | null>(
    null
  );
  const yieldedRef = useRef(yielded);
  const setYielded = useCallback((next: { restore: boolean } | null) => {
    yieldedRef.current = next;
    setYieldedState(next);
  }, []);
  const open = openState && yielded === null;
  const setOpen = useCallback(
    (value: boolean) => {
      setOpenState(value);
      // The user, or a rule, uses the panel: the view no longer holds it.
      setYielded(null);
    },
    [setYielded]
  );
  const [height, setHeight] = useState(readHeight);
  const [maximizedState, setMaximized] = useState(false);
  const maximized = maximizedState && yielded === null;
  const [chosenTab, setChosenTab] = useState<DockTab | null>(url.dock);
  const [seenDock, setSeenDock] = useState(url.dock);
  const [auto, setAuto] = useState<{
    runId: string;
    step: StepRunTarget;
  } | null>(null);

  // A new ?dock (link, back, forward) selects its tab and opens the dock. A
  // tab set without a URL write stays until the URL names another one. A
  // toggle does not change ?dock, so a closed dock stays closed.
  if (url.dock !== seenDock) {
    setSeenDock(url.dock);
    if (url.dock) {
      setChosenTab(url.dock);
      setOpen(true);
    }
  }

  const artifactEnabled = !!dagData.dag?.artifacts?.enabled;
  const details = run.hasRun ? run.details : null;
  const facts = useMemo(
    () => (details ? dockFacts(details, { artifactEnabled }) : null),
    [details, artifactEnabled]
  );
  const loading = run.hasRun && !details;
  const tabs = useMemo(() => dockTabsFor(facts, { loading }), [facts, loading]);
  const badges = useMemo(() => dockTabBadges(facts), [facts]);

  const runId = run.ref?.runId ?? null;
  const autoStep = auto && auto.runId === runId ? auto.step : null;
  const tab = fallbackDockTab(
    chosenTab ??
      defaultDockTab({
        stepShown: url.step !== null || autoStep !== null,
        failed: details?.status === Status.Failed,
      }),
    tabs
  );

  const setTab = useCallback(
    (t: DockTab, o: { user: boolean }) => {
      setChosenTab(t);
      setOpen(true);
      // A form and its buttons need more room than the default height gives.
      // The stored height is not changed.
      if (t === 'actions' && o.user) {
        setHeight((h) =>
          Math.max(h, clampDockHeight(Math.round(window.innerHeight * 0.6)))
        );
      }
      if (o.user) setUrl({ dock: t }, { replace: true });
    },
    [setUrl, setOpen]
  );

  // The commands read the state of the last render through refs: their
  // identity does not change with the state, so a memoized consumer that
  // only calls them does not render again.
  const live = useRef({ open, tab, details });
  live.current = { open, tab, details };

  const toggle = useCallback(() => {
    const next = !live.current.open;
    setOpen(next);
    setMaximized(false);
    writeLocalStorage(DOCK_OPEN_KEY, String(next));
  }, [setOpen]);

  const maximize = useCallback(
    (on: boolean) => {
      setMaximized(on);
      if (on) setOpen(true);
    },
    [setOpen]
  );

  const show = useCallback(
    (t: DockTab, patch: WorkspaceUrlPatch, max?: boolean) => {
      setChosenTab(t);
      setOpen(true);
      setAuto(null);
      if (max) setMaximized(true);
      setUrl({ dock: t, ...patch }, { replace: true });
    },
    [setUrl, setOpen]
  );

  const openStep = useCallback(
    (
      target: StepRunTarget | string,
      opts?: { maximize?: boolean; keepTab?: boolean }
    ) => {
      const t = typeof target === 'string' ? { step: target } : target;
      const { details: current, tab: currentTab } = live.current;
      const stepTabs = current ? stepDockTabs(current, t.step) : [];
      show(
        opts?.keepTab && stepTabs.includes(currentTab) ? currentTab : 'log',
        targetPatch(t),
        opts?.maximize
      );
    },
    [show]
  );
  const openRunLog = useCallback(() => show('log', NO_STEP), [show]);
  const dropAutoStep = useCallback(() => setAuto(null), []);

  const yieldToView = useCallback(
    (on: boolean, opts?: { restore?: boolean }) => {
      const current = yieldedRef.current;
      if (on) {
        if (!current) setYielded({ restore: opts?.restore ?? true });
        return;
      }
      setYielded(null);
      if (current && !current.restore) setOpenState(false);
    },
    [setYielded]
  );

  const resize = useCallback((next: number | null) => {
    if (next === null) {
      setHeight(defaultDockHeight());
      removeStored(DOCK_HEIGHT_KEY);
      return;
    }
    const value = clampDockHeight(next);
    setHeight(value);
    writeLocalStorage(DOCK_HEIGHT_KEY, String(value));
  }, []);

  const showAuto = useCallback(
    (t: DockTab, step: StepRunTarget | null) => {
      setChosenTab(t);
      setOpen(true);
      setAuto(step && runId ? { runId, step } : null);
      if (step) {
        setHeight((h) => Math.max(h, clampDockHeight(DOCK_FAILURE_HEIGHT)));
      }
    },
    [runId, setOpen]
  );

  const commands = useMemo<DockCommands>(
    () => ({
      setTab,
      toggle,
      maximize,
      openStep,
      openRunLog,
      yieldToView,
      noteUserRun,
      dropAutoStep,
    }),
    [
      setTab,
      toggle,
      maximize,
      openStep,
      openRunLog,
      yieldToView,
      noteUserRun,
      dropAutoStep,
    ]
  );

  const value = useMemo<DockController>(
    () => ({ open, tab, height, maximized, autoStep, ...commands }),
    [open, tab, height, maximized, autoStep, commands]
  );

  const frame = useMemo<DockFrame>(
    () => ({ tabs, badges, facts, startedRuns, resize, showAuto }),
    [tabs, badges, facts, startedRuns, resize, showAuto]
  );

  return (
    <DockCommandsContext.Provider value={commands}>
      <DockFrameContext.Provider value={frame}>
        <DockContext.Provider value={value}>{children}</DockContext.Provider>
      </DockFrameContext.Provider>
    </DockCommandsContext.Provider>
  );
}

/** The dock of the workspace. Outside DockProvider it is closed and its commands do nothing. */
export function useDock(): DockController {
  return useContext(DockContext) ?? NO_DOCK;
}

/**
 * The commands of the dock, without its state. Use it where the state is not
 * read: the component does not render again for a toggle. Outside
 * DockProvider the commands do nothing.
 */
export function useDockCommands(): DockCommands {
  return useContext(DockCommandsContext) ?? NO_DOCK;
}

/** Frame state for LogsDock and useDockAutoOpen. Needs DockProvider. */
export function useDockFrame(): DockFrame {
  const value = useContext(DockFrameContext);
  if (!value) throw new Error('useDockFrame must be used within DockProvider');
  return value;
}
