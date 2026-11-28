// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { subscribeClock } from '@/hooks/useClock';
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom';
import {
  useActiveRightPanel,
  useCloseRightPanel,
} from '@/contexts/RightPanelContext';
import DAGSpec from '@/features/dags/components/dag-editor/DAGSpec';
import type { DAGSpecHostProps } from '@/features/dags/components/dag-editor/hostTypes';
import type {
  FollowedStepRun,
  InputRun,
  LauncherHost,
} from '@/features/visual-editor/contracts';
import { referencedStepNames } from '@/features/visual-editor/references/stepReferences';
import { runInputProvenance } from '@/features/visual-editor/run/inputProvenance';
import {
  stepsWithUpstream,
  upstreamStepNames,
} from '@/features/visual-editor/run/upstreamSteps';
import { cn } from '@/lib/utils';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import {
  PanelColumnProvider,
  PanelPlacementProvider,
} from '@/features/dags/components/step-details/PanelShell';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { WIDE_QUERY } from '@/lib/breakpoints';
import {
  CanvasToolbarHostContext,
  TOOLBAR_ROW_CLASS,
  TOOLBAR_ROW_OVER_PANEL_CLASS,
  TOOLBAR_ZONE_CLASS,
} from '@/features/visual-editor/canvas/toolbarHost';
import { CanvasOverlayInsetContext } from '@/features/visual-editor/canvas/overlayInset';
import type { StepCommandGates } from '@/features/visual-editor/contracts';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceEditorHandle,
} from '../contracts';
import { useWorkspacePermissions, useWorkspaceRun } from '../data';
import { isNeverRun } from '../data/runFacts';
import { useStandaloneRun } from '../data/context';
import { RunSnapshotCanvas } from '../executions/RunSnapshotCanvas';
import { PaneActiveContext, useModePaneActive } from '../layout/paneActive';
import { WorkflowTabsPanel } from '../panel/WorkflowTabsPanel';
import { useSettingsLeaveGuard } from '../settings/useSettingsLeaveGuard';
import { useWorkspaceNotices } from '../notices/useWorkspaceNotices';
import { isRunRedirect, useWorkspaceUrl } from '../state/workspaceUrl';
import { StepPanel } from '../step/StepPanel';
import { StepRunTabs } from '../step/StepRunTabs';
import { composeCarriers } from '@/features/visual-editor/run/reuseSource';
import {
  useCacheScope,
  useOutputCacheEntries,
} from '@/features/visual-editor/run/useOutputCacheEntries';
import { useReuseCheck } from '../step/useReuseCheck';
import { useReuseSource } from '../step/useReuseSource';
import { useStepRunActions } from '../step/useStepRunActions';
import { useCanvasRunSource } from './canvasRunPolicy';
import { useDock, useDockCommands } from '../dock/DockContext';
import { useDockYield } from './useDockYield';
import { useTestRunNotice } from './useTestRunNotice';
import { useEscInStepYaml } from './useEscInStepYaml';
import { useOpenStepDetails } from './useOpenStepDetails';
import { RightColumn } from './RightColumn';
import { RIGHT_COLUMN_MOUNTED } from './rightColumnMounted';
import { useRightColumn, type RightColumnTab } from './useRightColumn';
import { useRightPanelClearance } from './useRightPanelClearance';
import { runStepGates, useStepCommandHost } from './useStepCommandHost';
import { useStepPanel } from './useStepPanel';
import { useDeepLinkReveal, useStepViewKeys } from './useStepViewHost';
import {
  useWorkflowPanelMemory,
  useYamlPanelMemory,
} from './useYamlPanelMemory';
import {
  useWorkspaceEditor,
  useWorkspaceEditorBinding,
} from './WorkspaceEditorContext';

const CHROME: DAGSpecHostProps['chrome'] = {
  hideRunBar: true,
  compactNotices: true,
};
// While the node details view or the step panel is open, Escape on the
// canvas closes it.
const CHROME_NDV_OPEN: DAGSpecHostProps['chrome'] = {
  ...CHROME,
  escapeToHost: true,
};

const NO_STEPS: readonly string[] = [];
const NO_SET: ReadonlySet<string> = new Set();
const BUFFER_POLL_MS = 500;

/**
 * The text of the editor while it holds unsaved changes; undefined
 * otherwise. The editor gives no event for each edit, so the buffer is read
 * at a short interval, only while `active`.
 */
function useDraftText(
  handle: Pick<WorkspaceEditorHandle, 'getBuffer'> | null,
  active: boolean
): string | undefined {
  const [text, setText] = useState<string | undefined>(undefined);
  useEffect(() => {
    if (!handle || !active) {
      setText(undefined);
      return;
    }
    const read = () => setText(handle.getBuffer() || undefined);
    read();
    return subscribeClock(BUFFER_POLL_MS, read);
  }, [handle, active]);
  return text;
}

/**
 * The run that feeds the open step in the INPUT column: the reuse source of
 * the step run dialog (D12), so that the column and the dialog name the same
 * run, with the previous steps that changed since it or hold no output in it
 * (D14). The previous steps come from the saved definition.
 *
 * When the selected run is itself a step run that reused the previous steps
 * of the open step (its labels say so), the column shows that run and the
 * source of each input, as it did while the run was followed: a reload or a
 * link to the run gives the same view.
 */
function useInputRun(p: {
  fileName: string;
  dagData: DAGPageData;
  step: string | null;
  followed: readonly FollowedStepRun[];
  /** The selected run, which can hold the record of the inputs that it used. */
  selected: ApiDAGRunDetails | null | undefined;
  liveText: string | undefined;
  onSelectRun(runId: string): void;
}): InputRun | undefined {
  const { dagData, step, followed, selected, onSelectRun } = p;
  const steps = dagData.dag?.steps;
  const { reads, previous } = useMemo(() => {
    if (step === null) return { reads: NO_STEPS, previous: NO_STEPS };
    const saved = steps?.find((s) => s.name === step);
    const read = referencedStepNames(
      steps,
      step,
      saved ? JSON.stringify(saved) : ''
    );
    return {
      reads: read,
      previous: stepsWithUpstream(
        steps,
        [...upstreamStepNames(steps, step), ...read],
        step
      ),
    };
  }, [steps, step]);
  const followedUpstreamRunId =
    followed.find((f) => f.kind === 'upstream' && f.step === step)?.runId ??
    null;
  const reuse = useReuseSource({
    fileName: p.fileName,
    step: step ?? '',
    previous,
    reads,
    followedUpstreamRunId,
    ...(p.liveText !== undefined ? { liveText: p.liveText } : {}),
  });
  const { source, waitingFor } = reuse;
  // The answer of the server for this step run, when the step run dialog
  // asked for it: the column never asks, since the server evaluates the
  // parameters to check.
  const entries = useOutputCacheEntries(useCacheScope(p.fileName));
  const entryRuns = useMemo(
    () =>
      Object.fromEntries(
        previous.flatMap((name) => {
          const entry = entries[name];
          return name !== step && entry ? [[name, entry.runId]] : [];
        })
      ),
    [entries, previous, step]
  );
  const check = useReuseCheck(
    {
      fileName: p.fileName,
      dagName: dagData.dag?.name ?? p.fileName,
      step: step ?? '',
      sourceRunId: source?.dagRunId ?? null,
      entries: entryRuns,
      draft: p.liveText ?? null,
    },
    { enabled: step !== null && !reuse.loading, ask: false }
  );
  const verified = useMemo(
    () =>
      check.state === 'ready'
        ? composeCarriers({
            previous,
            fromSource: source ? check.fromSource : {},
            fromCache: check.fromCache,
          })
        : null,
    [check.fromCache, check.fromSource, check.state, previous, source]
  );
  const stale = verified ? verified.changed : reuse.stale;
  const missing = verified ? verified.missing : reuse.missing;
  const staleSet = useMemo(() => new Set(stale), [stale]);
  const missingSet = useMemo(() => new Set(missing), [missing]);
  // A cached step that changed is marked against its own run.
  const staleRuns = useMemo(
    () =>
      new Map(
        Object.entries(verified?.carriers ?? {}).flatMap(([name, carrier]) =>
          carrier.from === 'cache' && carrier.changes.length > 0
            ? [[name, carrier.runId] as const]
            : []
        )
      ),
    [verified]
  );
  // The steps that the browser cache covers, with the run that each value
  // was read from: the INPUT column names them.
  const { cached, cacheCapturedAt, cacheTimes, cachedRuns } = reuse;
  const cache = useMemo(() => {
    if (!verified) {
      return {
        cached: new Set(cached),
        capturedAt: cacheCapturedAt,
        times: cacheTimes,
        runs: Object.fromEntries(
          Object.entries(cachedRuns).map(([name, from]) => [name, from.runId])
        ),
      };
    }
    const fromCache = Object.entries(verified.carriers).filter(
      ([, carrier]) => carrier.from === 'cache'
    );
    const times = Object.fromEntries(
      fromCache.map(([name]) => [name, entries[name]?.capturedAt ?? 0])
    );
    const oldest = Object.values(times);
    return {
      cached: new Set(fromCache.map(([name]) => name)),
      capturedAt: oldest.length > 0 ? Math.min(...oldest) : null,
      times,
      runs: Object.fromEntries(
        fromCache.map(([name, carrier]) => [name, carrier.runId])
      ),
    };
  }, [verified, cached, cacheCapturedAt, cacheTimes, cachedRuns, entries]);
  // The choice of the run that the next step run takes its inputs from.
  const { options, selectedOptionId, choose } = reuse;
  const picker = useMemo(
    () => ({ options, selectedId: selectedOptionId, onSelect: choose }),
    [options, selectedOptionId, choose]
  );
  // The steps that the selected run reused, when it reused some previous
  // step of the open step.
  const selectedReused = useMemo(() => {
    const found = runInputProvenance(selected);
    return previous.some((name) => found.has(name)) ? found : null;
  }, [selected, previous]);
  const useSelected = selected && selectedReused !== null ? selected : null;
  const startedAt = (useSelected ?? (source as { startedAt?: string } | null))
    ?.startedAt;
  const status = (useSelected ?? source)?.status;
  const runId = (useSelected ?? source)?.dagRunId ?? null;
  // The steps that the shown run itself reused, and the runs that the
  // history can select: labels are in lower case, ids are not.
  const sourceReused = useMemo(() => runInputProvenance(source), [source]);
  const provenance = selectedReused ?? sourceReused;
  const resolveRun = reuse.resolveRun as InputRun['resolveRun'];
  return useMemo(
    () =>
      step === null
        ? undefined
        : {
            runId,
            // The reuse source and the cache are about the next run.
            stale: useSelected ? NO_SET : staleSet,
            missing: useSelected ? NO_SET : missingSet,
            ...(useSelected ? {} : { staleRuns, checked: verified !== null }),
            waitingFor: useSelected ? null : waitingFor,
            ...(useSelected ? {} : { cache, picker }),
            provenance,
            ...(resolveRun ? { resolveRun } : {}),
            ...(status !== undefined ? { status } : {}),
            ...(startedAt !== undefined ? { startedAt } : {}),
            onSelectRun,
          },
    [
      step,
      runId,
      useSelected,
      staleSet,
      missingSet,
      staleRuns,
      verified,
      cache,
      picker,
      provenance,
      resolveRun,
      waitingFor,
      status,
      startedAt,
      onSelectRun,
    ]
  );
}

/**
 * Mounts the editor once Activity has made room for the expanded step view
 * of the first render, such as a link that opens a step: a graph that fit
 * the zone above the open Activity fit again, and jumped, when Activity
 * collapsed. Once mounted, the editor stays. It
 * is a component of its own, as the canvas must not render again for the
 * state of the dock.
 */
function MountAfterDockYield({
  waits,
  children,
}: {
  waits: boolean;
  children: ReactNode;
}) {
  const { open } = useDock();
  const [mounted, setMounted] = useState(false);
  const holds = !mounted && waits && open;
  if (!mounted && !holds) setMounted(true);
  return holds ? null : children;
}

/**
 * Closes the right panel and the step panel when Activity opens. A right
 * panel with a draft or a one-time secret at risk stays instead, and
 * Activity folds back until it closes: its close would ask first, and
 * Activity can open with no user action. It is a component of its own: the
 * state of the dock changes with each toggle, and the canvas must not render
 * again for it.
 */
function CloseRightPanelOnActivity({
  paneActive,
  atRisk,
  stepView,
  closeStepView,
  closeStepPanel,
}: {
  paneActive: boolean;
  /** A draft or a one-time secret would be lost (useSettingsLeaveGuard). */
  atRisk: boolean;
  /** The step view is open (?ndv=expanded). */
  stepView: boolean;
  /** Closes the step view and gives no YAML panel back under it. */
  closeStepView: () => void;
  closeStepPanel: () => void;
}) {
  const dock = useDock();
  const { yieldToView } = useDockCommands();
  const closeRightPanel = useCloseRightPanel();
  const activePanel = useActiveRightPanel();
  const panelRef = useRef(activePanel);
  panelRef.current = activePanel;
  const dockWasOpen = useRef(dock.open);
  useEffect(() => {
    const opened = dock.open && !dockWasOpen.current;
    dockWasOpen.current = dock.open;
    if (!opened || !paneActive) return;
    if (panelRef.current !== null) {
      if (atRisk) {
        yieldToView?.(true, { restore: true });
        return;
      }
      // The close of a step view opened from the YAML gives the YAML back,
      // which would then show beside Activity with no claim.
      if (stepView) closeStepView();
      else closeRightPanel();
    }
    closeStepPanel();
  }, [
    dock.open,
    paneActive,
    atRisk,
    stepView,
    yieldToView,
    closeRightPanel,
    closeStepView,
    closeStepPanel,
  ]);
  return null;
}

/**
 * The editor pane: DAGSpec in host mode at full height, on the live file with
 * the selected run overlaid on it by step name (canvasRunPolicy). The URL
 * selection and the step actions of the workspace drive it, and it gives its
 * handle and state to WorkspaceEditorContext. A run started from it becomes
 * the selected run. The runs of the step view stay on the editor.
 *
 * With ?def=run the snapshot of the selected run takes the place of the
 * editor. The editor stays mounted, hidden and inert, so that the draft, the
 * undo history and the panels are kept. It takes no key while hidden.
 */
export function WorkspaceCanvas({
  fileName,
  dagData,
  workspaceName = null,
}: {
  fileName: string;
  dagData: DAGPageData;
  workspaceName?: string | null;
}) {
  const permissions = useWorkspacePermissions();
  const run = useWorkspaceRun();
  // A run without its workflow file shows its own definition only: there is
  // no file to edit.
  const standaloneRun = useStandaloneRun();
  const { guard, setSecretVisible, atRisk } = useSettingsLeaveGuard();
  // The runs of the step view start and show without leaving the editor.
  const actions = useStepRunActions({ stay: true });
  const [url, setUrl, readUrl] = useWorkspaceUrl();
  const { editorHandleRef, onEditorStateChange } = useWorkspaceEditorBinding();
  const { handle, state: editorState } = useWorkspaceEditor();
  const runSource = useCanvasRunSource(
    run,
    handle,
    editorState?.dirty ?? false
  );
  // One line for the run notices and the offer.
  const allNotices = useWorkspaceNotices();
  // A test run stays on the line while it is shown.
  const testRunNotice = useTestRunNotice(run.details);
  const notices = useMemo(
    () => [...allNotices, ...(testRunNotice ? [testRunNotice] : [])],
    [allNotices, testRunNotice]
  );
  // The snapshot takes the place of the editor: the hidden editor is not
  // the active pane.
  const snapshot = url.def === 'run';
  const paneActive = useModePaneActive() && !snapshot;
  const activePanel = useActiveRightPanel();
  // On a wide window the Workflow panel and the YAML of the file can be the
  // tabs of a right column beside the graph zone, when the app mounts it
  // (RIGHT_COLUMN_MOUNTED). Otherwise they are docks over the zone.
  const wide = useMediaQuery(WIDE_QUERY, true);
  const columnMode = RIGHT_COLUMN_MOUNTED && wide;
  const [columnTab, setColumnTab] = useRightColumn();
  const columnTabRef = useRef(columnTab);
  columnTabRef.current = columnTab;
  // A maximized column covers the graph zone and takes the room of
  // Activity. A collapse ends it: the column opens beside the graph again.
  const [columnMaximized, setColumnMaximized] = useState(false);
  const [workflowRoot, setWorkflowRoot] = useState<HTMLDivElement | null>(null);
  const [yamlRoot, setYamlRoot] = useState<HTMLDivElement | null>(null);
  const [launcherRoot, setLauncherRoot] = useState<HTMLDivElement | null>(null);
  const [layerRoot, setLayerRoot] = useState<HTMLDivElement | null>(null);
  const panelColumn = useMemo(
    () =>
      columnMode
        ? {
            workflow: workflowRoot,
            yaml: yamlRoot,
            launcher: launcherRoot,
            layer: layerRoot,
          }
        : null,
    [columnMode, workflowRoot, yamlRoot, launcherRoot, layerRoot]
  );
  // A right panel covers part of the zone: the canvas fits in the part that
  // stays free.
  const zoneRef = useRef<HTMLDivElement>(null);
  // The row of the canvas toolbars: the editor renders them in it.
  const [toolbarRow, setToolbarRow] = useState<HTMLDivElement | null>(null);

  // A run that the bar starts becomes the selected run. The node view and
  // the step stay as they are.
  const onRunStarted = useCallback(
    (runId: string) => setUrl({ run: runId, runName: null }),
    [setUrl]
  );

  // A step view needs a step: it closes with the selection. The YAML panel
  // stays, and shows the whole file.
  const onSelectedStepNameChange = useCallback(
    (name: string | null) => {
      if (name !== null) {
        setUrl({ step: name }, { replace: true });
        return;
      }
      const stepView = readUrl().ndv === 'expanded';
      setUrl(
        { step: null, ...(stepView ? { ndv: null } : {}) },
        {
          replace: true,
        }
      );
    },
    [readUrl, setUrl]
  );

  // The YAML panel shows as the user left it, unless a link opens a view.
  // A legacy link, such as /history?idx, has a path that redirects once the
  // run history loads; opening the panel first would rewrite the URL without
  // it.
  const [searchParams] = useSearchParams();
  const wantsYaml = searchParams.has('yaml');
  const { tab: legacyTab } = useParams<{ tab?: string }>();
  const restoreYamlPanel = useCallback(
    () => setUrl({ ndv: 'docked' }, { replace: true }),
    [setUrl]
  );
  // A link that names a step, a panel, a run or a tab of the Run panel opens
  // that view instead. The redirect to the latest run is no link.
  const { state: routeState } = useLocation();
  const restorable =
    url.ndv === null &&
    url.step === null &&
    url.panel === null &&
    url.dock === null &&
    (url.run === null || isRunRedirect(routeState)) &&
    legacyTab === undefined &&
    !snapshot &&
    !wantsYaml &&
    !columnMode;
  const ndvState = useYamlPanelMemory({
    fileName,
    ndv: url.ndv,
    restorable,
    restore: restoreYamlPanel,
  });
  // The Workflow panel shows as the user left it too, when the YAML panel
  // does not.
  const workflowPanel = useWorkflowPanelMemory({
    fileName,
    activePanel,
    restorable: restorable && ndvState === null,
  });
  // In the column the YAML of the file shows while its tab does; ?ndv keeps
  // only the view of a step, which opens over the zone.
  const shownNdv = columnMode
    ? url.ndv === 'expanded'
      ? 'expanded'
      : columnTab === 'yaml'
        ? 'docked'
        : null
    : ndvState;
  const ndv = useMemo<DAGSpecHostProps['ndv']>(
    () => ({
      state: shownNdv,
      onStateChange: (state) => {
        if (!columnMode) {
          setUrl({ ndv: state }, { replace: true });
          return;
        }
        if (state === 'expanded') {
          setUrl({ ndv: 'expanded' }, { replace: true });
          return;
        }
        // The view of a step closes and leaves the column as it is.
        const stepView = readUrl().ndv !== null;
        if (stepView) setUrl({ ndv: null }, { replace: true });
        if (state === 'docked') setColumnTab('yaml');
        else if (!stepView && columnTabRef.current === 'yaml') {
          setColumnTab(null);
        }
      },
      active: paneActive,
    }),
    [shownNdv, columnMode, paneActive, setUrl, readUrl, setColumnTab]
  );
  // A link to the YAML panel (?ndv=docked) opens the YAML tab of the column.
  // The run definition keeps it: there it is the panel of the run's YAML.
  useEffect(() => {
    if (!columnMode || snapshot || url.ndv !== 'docked') return;
    setColumnTab('yaml');
    setUrl({ ndv: null }, { replace: true });
  }, [columnMode, snapshot, url.ndv, setColumnTab, setUrl]);
  // The Workflow tab shows the Workflow panel of the editor: it opens once
  // the editor has loaded, and stays mounted while another tab shows.
  useEffect(() => {
    if (columnMode && columnTab === 'workflow') handle?.openWorkflowPanel(true);
  }, [columnMode, columnTab, handle]);
  const onWorkflowOpenChange = useCallback(
    (open: boolean) => {
      if (columnMode && open) setColumnTab('workflow');
    },
    [columnMode, setColumnTab]
  );

  // A deep link (?step, ?ndv) applies once the editor has loaded the spec.
  useDeepLinkReveal(
    handle,
    url.step ? { step: url.step, ndv: url.ndv } : null,
    true
  );

  // A legacy /spec link (?yaml=1) opens the YAML once the editor has loaded.
  // One URL write swaps ?yaml for ?ndv=docked: setUrl drops ?yaml.
  useEffect(() => {
    if (!handle || !wantsYaml) return;
    handle.toggleYaml(true);
    setUrl({ ndv: 'docked' }, { replace: true });
  }, [handle, wantsYaml, setUrl]);

  // A step that calls a workflow of another file opens that workflow.
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();
  const openWorkflow = useCallback(
    (name: string) => navigate(buildDAGPageURL({ fileName: name, remoteNode })),
    [navigate, remoteNode]
  );

  // A link with ?panel opens the Workflow panel or a launcher panel once
  // the editor has loaded; the name leaves the URL, ?wtab stays.
  const wantedPanel = url.panel;
  useEffect(() => {
    if (!handle || !wantedPanel) return;
    if (wantedPanel === 'workflow') handle.openWorkflowPanel(true);
    else handle.openLauncher(wantedPanel);
    setUrl({ panel: null }, { replace: true });
  }, [handle, wantedPanel, setUrl]);

  const latestRun = isNeverRun(dagData.latestDAGRun)
    ? null
    : dagData.latestDAGRun;
  // The panels guard their close, as a draft or a one-time secret is at risk.
  const launcherHost = useMemo<LauncherHost>(
    () => ({
      guard,
      setSecretVisible,
      suspended: dagData.suspended,
      latestRun: latestRun
        ? { status: latestRun.status, finishedAt: latestRun.finishedAt }
        : null,
    }),
    [
      guard,
      setSecretVisible,
      dagData.suspended,
      latestRun?.status,
      latestRun?.finishedAt,
    ]
  );
  const renderWorkflowPanel = useCallback<
    NonNullable<DAGSpecHostProps['renderWorkflowPanel']>
  >(
    (overview, dag) => (
      <WorkflowTabsPanel
        overview={overview}
        dag={dag}
        fileName={fileName}
        workspaceName={workspaceName}
      />
    ),
    [fileName, workspaceName]
  );

  // A click on a step opens its panel over the canvas; another right panel,
  // the node view or the snapshot takes its place.
  const stepViewOpen = columnMode ? url.ndv === 'expanded' : url.ndv !== null;
  // In the column the step is a tab: a panel over the zone does not cover it.
  const covered =
    stepViewOpen || (!columnMode && activePanel !== null) || snapshot;
  const stepPanel = useStepPanel({
    zoneRef,
    step: url.step,
    covered,
    // A link to a step that another view covers keeps the panel shut.
    linked:
      url.step !== null && url.ndv === null && url.dock === null && !covered,
    active: paneActive,
  });
  const panelStep = stepPanel.shown ? url.step : null;
  // The column is open on a tab of its own or on a temporary one: the
  // details of a step or the panel of a trigger.
  const columnOpen =
    columnTab !== null ||
    panelStep !== null ||
    (editorState?.launcher ?? null) !== null;
  if (columnMaximized && !columnOpen) setColumnMaximized(false);
  const columnFills = columnMode && columnMaximized && !snapshot;
  const openStepDetails = useOpenStepDetails();

  // The step panel is a right panel too, without a claim of its own: it takes
  // turns with Activity (useDockYield) and yields to the other right panels
  // (covered). Each click on a step can hold off its mark, so each one watches
  // again.
  const clearance = useRightPanelClearance(
    zoneRef,
    activePanel ??
      (stepPanel.shown && !columnMode ? (stepPanel.openedAt ?? 'step') : null)
  );
  const inset = useMemo(
    () => ({ right: clearance.overlap, covered: columnFills }),
    [clearance.overlap, columnFills]
  );

  const ndvOpen = stepViewOpen || activePanel === 'ndv';
  // A step view that opened over the YAML panel gives the YAML back when Esc
  // closes it, as its close button does (useYamlSection).
  const yamlUnderView = useRef(false);
  const lastNdv = useRef(url.ndv);
  useEffect(() => {
    if (url.ndv === 'expanded') {
      if (lastNdv.current === 'docked') yamlUnderView.current = true;
    } else {
      yamlUnderView.current = false;
    }
    lastNdv.current = url.ndv;
  }, [url.ndv]);
  const closeStepView = () => {
    if (!columnMode && yamlUnderView.current && handle) handle.toggleYaml(true);
    else setUrl({ ndv: null }, { replace: true });
  };
  const closeStepViewAlone = useCallback(
    () => setUrl({ ndv: null }, { replace: true }),
    [setUrl]
  );
  // Esc closes the expanded node view when no dialog or menu is above it.
  // The YAML panel stays: it is a panel that the page keeps, not a view to
  // leave. With no panel or node view open, Esc clears the step and ?step.
  useStepViewKeys({
    zoneRef,
    active: paneActive,
    hasStep: url.step !== null,
    viewOpen: url.ndv === 'expanded',
    panelOpen: activePanel !== null || stepPanel.shown,
    closeView: closeStepView,
    clearStep: () => setUrl({ step: null, ndv: null }, { replace: true }),
  });

  // The right panel (a launcher, the Workflow, the YAML, the node view, the
  // step panel) and the Activity panel take turns. A panel that opens
  // collapses Activity, which comes back when the panel closes. Activity that
  // opens closes the panel. A maximized column holds the room of the Run
  // panel too. The YAML panel and a Workflow panel that the page restores
  // count from the first render, before the editor loads and claims them, so
  // that Activity does not show and then fold.
  useDockYield(
    paneActive &&
      (activePanel !== null ||
        shownNdv === 'expanded' ||
        columnFills ||
        (!columnMode &&
          (shownNdv === 'docked' || workflowPanel.pending || stepPanel.shown)))
  );
  // Esc in the YAML of the step view closes the view too.
  useEscInStepYaml({
    active: paneActive,
    viewOpen: url.ndv === 'expanded',
    closeView: closeStepView,
  });

  // The INPUT column names the run that the step run dialog copies from.
  const selectRun = useCallback(
    (runId: string) => setUrl({ run: runId }),
    [setUrl]
  );
  const draftText = useDraftText(
    handle,
    url.step !== null && (editorState?.dirty ?? false)
  );
  const inputRun = useInputRun({
    fileName,
    dagData,
    step: url.step,
    followed: actions.followed,
    selected: run.details,
    liveText: draftText,
    onSelectRun: selectRun,
  });

  // The run that the step view follows ends with the step view.
  const { clearFollowed } = actions;
  useEffect(() => {
    if (!ndvOpen) clearFollowed();
  }, [ndvOpen, clearFollowed]);

  // The OUTPUT column of the step view has the tabs of the Run panel.
  const renderStepOutput = useCallback<
    NonNullable<DAGSpecHostProps['renderStepOutput']>
  >(
    (step, { runId, declared }) => (
      <StepRunTabs step={step} runId={runId} declared={declared} />
    ),
    []
  );

  // The steps of the selected run have retry, set status and sub-runs, by
  // the gates of the step actions. A step that the run does not have has
  // none. The gates read the actions when they are called, so that their
  // identity does not change with each event of a live run.
  const canExecute = permissions.canExecute;
  const canRef = useRef(actions.can);
  canRef.current = actions.can;
  const gates = useMemo<StepCommandGates>(
    () => runStepGates({ can: (step) => canRef.current(step) }, { canExecute }),
    [canExecute]
  );
  const { onStepCommand, stepCommandGates } = useStepCommandHost(
    actions,
    gates
  );

  return (
    // The graph zone and, on a wide window, the right column beside it. The
    // row is the box that the column takes its width from.
    <div className="relative flex h-full min-h-0 w-full min-w-0 flex-1 flex-row">
      {/* The graph zone: every right panel and the expanded node view sit
        inside this box (absolute) and never beyond it, under the row of the
        canvas toolbars, which a panel never moves or covers. */}
      <div
        ref={zoneRef}
        data-testid="ww-canvas"
        className={cn(
          'relative flex h-full min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden',
          TOOLBAR_ZONE_CLASS
        )}
      >
        <CloseRightPanelOnActivity
          paneActive={paneActive}
          atRisk={atRisk}
          stepView={url.ndv === 'expanded'}
          closeStepView={closeStepViewAlone}
          closeStepPanel={stepPanel.close}
        />
        <div
          ref={setToolbarRow}
          data-testid="ww-canvas-toolbar"
          hidden={snapshot}
          className={cn(
            TOOLBAR_ROW_CLASS,
            // A docked panel is one box from the top of the zone: the toolbars
            // sit over its top rows.
            clearance.free !== null && TOOLBAR_ROW_OVER_PANEL_CLASS
          )}
        />
        <CanvasOverlayInsetContext.Provider value={inset}>
          <PanelColumnProvider value={panelColumn}>
            <PanelPlacementProvider value="zone">
              <CanvasToolbarHostContext.Provider value={toolbarRow}>
                <div
                  data-testid="ww-canvas-graph"
                  hidden={snapshot}
                  inert={snapshot || columnFills}
                  className={cn(
                    'flex min-h-0 w-full min-w-0 flex-1 flex-col',
                    columnFills && 'invisible'
                  )}
                >
                  <PaneActiveContext.Provider value={paneActive}>
                    {!standaloneRun && (
                      <MountAfterDockYield
                        waits={paneActive && shownNdv === 'expanded'}
                      >
                        <DAGSpec
                          fileName={fileName}
                          localDags={dagData.localDags}
                          editorHints={dagData.editorHints}
                          keyboardActive={paneActive}
                          fillHeight
                          runSource={runSource}
                          onRunStarted={onRunStarted}
                          selectedStepName={url.step}
                          onSelectedStepNameChange={onSelectedStepNameChange}
                          onStepClick={(_step, at) => stepPanel.show(at)}
                          ndv={ndv}
                          openNdvOn="open"
                          renderStepOutput={renderStepOutput}
                          onStepCommand={onStepCommand}
                          onOpenWorkflow={openWorkflow}
                          stepCommandGates={stepCommandGates}
                          followedRuns={actions.followed}
                          inputRun={inputRun}
                          chrome={
                            ndvOpen || stepPanel.shown
                              ? CHROME_NDV_OPEN
                              : CHROME
                          }
                          notices={notices}
                          editorHandleRef={editorHandleRef}
                          onEditorStateChange={onEditorStateChange}
                          launcherHost={launcherHost}
                          renderWorkflowPanel={renderWorkflowPanel}
                          initialWorkflowOpen={
                            columnMode
                              ? columnTab === 'workflow'
                              : workflowPanel.open
                          }
                          onWorkflowOpenChange={onWorkflowOpenChange}
                        />
                      </MountAfterDockYield>
                    )}
                  </PaneActiveContext.Provider>
                </div>
              </CanvasToolbarHostContext.Provider>
            </PanelPlacementProvider>
          </PanelColumnProvider>
        </CanvasOverlayInsetContext.Provider>
        {panelStep !== null && !columnMode && (
          // A panel of the zone: its title and Close button sit in the row.
          <PanelPlacementProvider value="zone">
            <CanvasToolbarHostContext.Provider value={toolbarRow}>
              <StepPanel
                step={panelStep}
                openedAt={stepPanel.openedAt}
                onClose={stepPanel.close}
                onStepCommand={onStepCommand}
                gates={stepCommandGates}
                onOpenStep={() => openStepDetails(panelStep)}
              />
            </CanvasToolbarHostContext.Provider>
          </PanelPlacementProvider>
        )}
        {snapshot && <RunSnapshotCanvas />}
        {actions.dialogs}
      </div>
      {columnMode && (
        <RightColumn
          tab={columnTab}
          onSelect={(tab: RightColumnTab) => {
            stepPanel.close();
            handle?.closeLauncher?.();
            setColumnTab(tab);
          }}
          onCollapse={() => {
            stepPanel.close();
            handle?.closeLauncher?.();
            setColumnTab(null);
          }}
          maximized={columnFills}
          onMaximizedChange={setColumnMaximized}
          workflowRef={setWorkflowRoot}
          yamlRef={setYamlRoot}
          step={panelStep}
          launcher={editorState?.launcher ?? null}
          launcherRef={setLauncherRoot}
          stepDetails={
            panelStep !== null && (
              <StepPanel
                placement="column"
                step={panelStep}
                onClose={stepPanel.close}
                onStepCommand={onStepCommand}
                gates={stepCommandGates}
                onOpenStep={() => openStepDetails(panelStep)}
              />
            )
          }
          hidden={snapshot}
        />
      )}
      {columnMode && (
        // The layer of the YAML frame, over the zone and the column. It takes
        // no pointer itself, and hides with the editor for the snapshot.
        <div
          ref={setLayerRoot}
          data-testid="ww-panel-layer"
          hidden={snapshot}
          inert={snapshot}
          className="pointer-events-none absolute inset-0"
        />
      )}
    </div>
  );
}
