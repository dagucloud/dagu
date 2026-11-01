// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
} from 'react';
import {
  UNSAFE_NavigationContext,
  useLocation,
  useNavigate,
  useParams,
} from 'react-router-dom';

/** The old Executions tab is the workspace; /dags/:f/executions redirects (legacyLinks.ts). */
export type WorkspaceMode = 'workspace' | 'popout';
/** Tabs of the Workflow panel. */
export type WorkflowPanelTab =
  | 'overview'
  | 'wiki'
  | 'incidents'
  | 'notifications';
/** A right panel that a link opens once: the Workflow panel or a launcher. */
export type OpenPanel = 'workflow' | 'cron' | 'webhook';
/** The tabs of the Run panel. ?dock=steps and ?dock=spec are no tabs: they read as no tab. */
export type DockTab =
  | 'timeline'
  | 'outputs'
  | 'artifacts'
  | 'actions'
  | 'agent'
  | 'chat'
  | 'tasks'
  | 'continuous'
  | 'log';
export type LogStream = 'stdout' | 'stderr';

export interface WorkspaceUrlState {
  mode: WorkspaceMode;
  /** ?panel: opens the Workflow panel or a launcher panel once; the editor then clears it. */
  panel: OpenPanel | null;
  /** ?wtab: the tab of the Workflow panel; null = overview. */
  wtab: WorkflowPanelTab | null;
  popout: {
    kind: 'step' | 'run';
    dagRunId?: string;
    dagRunName?: string;
    subDAGRunId?: string;
  } | null;
  /** ?run ; null = latest */
  run: string | null;
  /**
   * ?subRun: a sub-run of the run that a link opens on the sub-run stack,
   * once. A ?step next to it is a step of that sub-run, so `step` is null.
   */
  subRun: { id: string; step: string | null } | null;
  /** ?def=run: the canvas shows the definition that the run used. null = the current definition. */
  def: 'run' | null;
  /** ?step (server step name) */
  step: string | null;
  ndv: 'docked' | 'expanded' | null;
  dock: DockTab | null;
  stream: LogStream | null;
  /** ?item: foreach item path (ForeachLogTarget.item) */
  item: string | null;
  /** ?body: foreach body step name */
  body: string | null;
  /** ?sub: subDAGRunId of the step shown in the dock log */
  sub: string | null;
  carry: {
    remoteNode?: string;
    workspace?: string;
  };
}

export const DOCK_TABS: readonly DockTab[] = [
  'timeline',
  'outputs',
  'artifacts',
  'actions',
  'agent',
  'chat',
  'tasks',
  'continuous',
  'log',
];

export const WORKFLOW_PANEL_TABS: readonly WorkflowPanelTab[] = [
  'overview',
  'wiki',
  'incidents',
  'notifications',
];
const OPEN_PANELS: readonly OpenPanel[] = ['workflow', 'cron', 'webhook'];

// Query keys in the order that buildWorkspaceUrl writes them. A key outside
// this list belongs to someone else and a URL update keeps it.
const STATE_KEYS = [
  'run',
  'def',
  'step',
  'ndv',
  'dock',
  'stream',
  'item',
  'body',
  'sub',
  'panel',
  'wtab',
] as const;
const POPOUT_KEYS = ['dagRunId', 'dagRunName', 'subDAGRunId'] as const;
const CARRY_KEYS = ['remoteNode', 'workspace'] as const;
const OWNED_KEYS: ReadonlySet<string> = new Set<string>([
  ...STATE_KEYS,
  'subRun',
  // ?view (List or Grid) is gone: a URL write drops it.
  'view',
  // ?yaml (the legacy /spec link) is gone once the editor has opened the YAML.
  'yaml',
  ...POPOUT_KEYS,
  ...CARRY_KEYS,
]);

/**
 * The router state of the ?run that the page adds by itself (the redirect to
 * the latest run). A reader that must tell it from a link that the user
 * opened, like the Run panel at mount, checks it with isRunRedirect.
 */
export const RUN_REDIRECT_STATE = { wwRunRedirect: true } as const;

export function isRunRedirect(state: unknown): boolean {
  return (
    typeof state === 'object' &&
    state !== null &&
    (state as { wwRunRedirect?: unknown }).wwRunRedirect === true
  );
}

const PUSH_KEYS = ['mode', 'run', 'def', 'popout'] as const;

function text(search: URLSearchParams, key: string): string | null {
  const value = search.get(key);
  return value ? value : null;
}

function oneOf<T extends string>(
  value: string | null,
  allowed: readonly T[]
): T | null {
  return value !== null && (allowed as readonly string[]).includes(value)
    ? (value as T)
    : null;
}

export function parseWorkspaceUrl(
  tab: string | undefined,
  search: URLSearchParams
): WorkspaceUrlState {
  let mode: WorkspaceMode = 'workspace';
  let popout: WorkspaceUrlState['popout'] = null;
  // The legacy tab 'executions' reads as the workspace: the redirect of
  // legacyLinks.ts drops it from the URL.
  if (tab === 'log' || tab === 'dagRun-log') {
    mode = 'popout';
    popout = { kind: tab === 'log' ? 'step' : 'run' };
    for (const key of POPOUT_KEYS) {
      const value = text(search, key);
      if (value !== null) popout[key] = value;
    }
  }

  const carry: WorkspaceUrlState['carry'] = {};
  const remoteNode = text(search, 'remoteNode');
  if (remoteNode !== null) carry.remoteNode = remoteNode;
  const workspace = text(search, 'workspace');
  if (workspace !== null) carry.workspace = workspace;
  const subRunId = text(search, 'subRun');
  const subRun = subRunId ? { id: subRunId, step: text(search, 'step') } : null;

  return {
    mode,
    popout,
    panel: oneOf(text(search, 'panel'), OPEN_PANELS),
    wtab: oneOf(text(search, 'wtab'), WORKFLOW_PANEL_TABS),
    run: text(search, 'run'),
    subRun,
    def: oneOf(text(search, 'def'), ['run'] as const),
    step: subRun ? null : text(search, 'step'),
    ndv: oneOf(text(search, 'ndv'), ['docked', 'expanded'] as const),
    // 'steps' and 'spec' were tabs: a link to them shows the default tab.
    dock: oneOf(text(search, 'dock'), DOCK_TABS),
    stream: oneOf(text(search, 'stream'), ['stdout', 'stderr'] as const),
    item: text(search, 'item'),
    body: text(search, 'body'),
    sub: text(search, 'sub'),
    carry,
  };
}

function tabOf(s: WorkspaceUrlState): string | undefined {
  if (s.mode === 'popout') {
    return s.popout?.kind === 'run' ? 'dagRun-log' : 'log';
  }
  return undefined;
}

function buildPath(fileName: string, s: WorkspaceUrlState): string {
  const base = `/dags/${encodeURIComponent(fileName)}`;
  const tab = tabOf(s);
  return tab ? `${base}/${tab}` : base;
}

function buildQuery(s: WorkspaceUrlState): URLSearchParams {
  const query = new URLSearchParams();
  if (s.mode === 'popout' && s.popout) {
    for (const key of POPOUT_KEYS) {
      const value = s.popout[key];
      if (value) query.set(key, value);
    }
  }
  for (const key of STATE_KEYS) {
    const value = s[key];
    if (value) query.set(key, value);
  }
  // A step of the run itself drops a sub-run link that has not opened yet.
  if (s.subRun && s.step === null) {
    query.set('subRun', s.subRun.id);
    if (s.subRun.step) query.set('step', s.subRun.step);
  }
  for (const key of CARRY_KEYS) {
    const value = s.carry[key];
    if (value) query.set(key, value);
  }
  return query;
}

function joinUrl(path: string, query: URLSearchParams): string {
  const q = query.toString();
  return q ? `${path}?${q}` : path;
}

export function buildWorkspaceUrl(
  fileName: string,
  s: WorkspaceUrlState
): string {
  return joinUrl(buildPath(fileName, s), buildQuery(s));
}

function sameValue(a: unknown, b: unknown): boolean {
  return a === b || JSON.stringify(a) === JSON.stringify(b);
}

export type WorkspaceUrlPatch = Partial<WorkspaceUrlState>;
export type SetWorkspaceUrl = (
  patch: WorkspaceUrlPatch,
  opts?: { replace?: boolean }
) => void;

interface UrlSnapshot {
  /** Key of the router location that the snapshot starts from. */
  key: string;
  state: WorkspaceUrlState;
  search: string;
}

interface RouterStore {
  /** The location that the router rendered last. */
  committed: UrlSnapshot | null;
  /** The URL of the last write that the router has not rendered yet. */
  pending: UrlSnapshot | null;
  writers: number;
}

// One store for each router (its navigator object), shared by every hook
// instance: two updates before the next render must compose, so the second
// one starts from the URL that the first one wrote, not from the rendered
// location. A write made by a stale closure starts from the last rendered
// location too. Keyed by router, so that routers of different tests never
// share it.
const STORES = new WeakMap<object, RouterStore>();

function storeOf(navigator: object): RouterStore {
  let store = STORES.get(navigator);
  if (!store) {
    store = { committed: null, pending: null, writers: 0 };
    STORES.set(navigator, store);
  }
  return store;
}

/** push for mode, run, def and popout; replace for step, subRun, ndv, dock, stream, item, body, sub, panel, wtab. */
export function useWorkspaceUrl(): [WorkspaceUrlState, SetWorkspaceUrl] {
  const { fileName = '', tab } = useParams<{
    fileName: string;
    tab?: string;
  }>();
  const location = useLocation();
  const navigate = useNavigate();
  const { navigator } = useContext(UNSAFE_NavigationContext);
  const store = storeOf(navigator);
  const state = useMemo(
    () => parseWorkspaceUrl(tab, new URLSearchParams(location.search)),
    [tab, location.search]
  );

  // The router has committed a new location: the written URL is no longer
  // pending. A location key can come back (history Back), so a stale entry
  // must not outlive the commit that replaced it.
  useLayoutEffect(() => {
    store.committed = { key: location.key, state, search: location.search };
    if (store.pending && store.pending.key !== location.key) {
      store.pending = null;
    }
  }, [store, location.key, location.search, state]);

  useEffect(() => {
    store.writers += 1;
    return () => {
      store.writers -= 1;
      if (store.writers === 0) {
        store.pending = null;
        store.committed = null;
      }
    };
  }, [store]);

  const setUrl = useCallback<SetWorkspaceUrl>(
    (patch, opts) => {
      const rendered = store.committed ?? {
        key: location.key,
        state,
        search: location.search,
      };
      const base =
        store.pending?.key === rendered.key ? store.pending : rendered;
      const withForeignKeys = (s: WorkspaceUrlState) => {
        const query = buildQuery(s);
        for (const [key, value] of new URLSearchParams(base.search)) {
          if (!OWNED_KEYS.has(key)) query.append(key, value);
        }
        return query;
      };
      const nextState: WorkspaceUrlState = { ...base.state, ...patch };
      const query = withForeignKeys(nextState);
      const next = joinUrl(buildPath(fileName, nextState), query);
      // Compared in canonical form, so a different query order is no change.
      const current = joinUrl(
        buildPath(fileName, base.state),
        withForeignKeys(base.state)
      );
      if (next === current) return;
      const push =
        !opts?.replace &&
        PUSH_KEYS.some(
          (key) => key in patch && !sameValue(patch[key], base.state[key])
        );
      const search = query.toString();
      store.pending = {
        key: rendered.key,
        state: parseWorkspaceUrl(tabOf(nextState), query),
        search: search ? `?${search}` : '',
      };
      // A write that keeps the ?run keeps the mark of the redirect too.
      const keepRedirect =
        !push &&
        isRunRedirect(location.state) &&
        nextState.run === base.state.run;
      navigate(next, {
        replace: !push,
        ...(keepRedirect ? { state: location.state } : {}),
      });
    },
    [fileName, location, navigate, state, store]
  );

  return [state, setUrl];
}
