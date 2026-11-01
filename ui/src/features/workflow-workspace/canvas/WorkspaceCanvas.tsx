// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { subscribeClock } from '@/hooks/useClock';
import { useSearchParams } from 'react-router-dom';
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
import { PanelPlacementProvider } from '@/features/dags/components/step-details/PanelShell';
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
import { RunSnapshotCanvas } from '../executions/RunSnapshotCanvas';
import { PaneActiveContext, useModePaneActive } from '../layout/paneActive';
import { WorkflowTabsPanel } from '../panel/WorkflowTabsPanel';
import { useSettingsLeaveGuard } from '../settings/useSettingsLeaveGuard';
import { useWorkspaceNotices } from '../notices/useWorkspaceNotices';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { StepPanel } from '../step/StepPanel';
import { StepRunTabs } from '../step/StepRunTabs';
import { useReuseSource } from '../step/useReuseSource';
import { useStepRunActions } from '../step/useStepRunActions';
import { useCanvasRunSource } from './canvasRunPolicy';
import { useDock } from '../dock/DockContext';
import { useDockYield } from './useDockYield';
import { useTestRunNotice } from './useTestRunNotice';
import { useEscInStepYaml } from './useEscInStepYaml';
import { useOpenStepDetails } from './useOpenStepDetails';
import { useRightPanelClearance } from './useRightPanelClearance';
import { runStepGates, useStepCommandHost } from './useStepCommandHost';
import { useStepPanel } from './useStepPanel';
import { useDeepLinkReveal, useStepViewKeys } from './useStepViewHost';
import { useYamlPanelMemory } from './useYamlPanelMemory';
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
  const { source, waitingFor, stale, missing } = reuse;
  const { cached, cacheCapturedAt, cacheTimes, cachedRuns } = reuse;
  const staleSet = useMemo(() => new Set(stale), [stale]);
  const missingSet = useMemo(() => new Set(missing), [missing]);
  // The steps that the browser cache covers, with the run that each value
  // was read from: the INPUT column names them.
  const cache = useMemo(
    () => ({
      cached: new Set(cached),
      capturedAt: cacheCapturedAt,
      times: cacheTimes,
      runs: Object.fromEntries(
        Object.entries(cachedRuns).map(([name, from]) => [name, from.runId])
      ),
    }),
    [cached, cacheCapturedAt, cacheTimes, cachedRuns]
  );
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
 * Closes the right panel when Activity opens. It is a component of its own:
 * the state of the dock changes with each toggle, and the canvas must not
 * render again for it.
 */
function CloseRightPanelOnActivity({ paneActive }: { paneActive: boolean }) {
  const dock = useDock();
  const closeRightPanel = useCloseRightPanel();
  const activePanel = useActiveRightPanel();
  const panelRef = useRef(activePanel);
  panelRef.current = activePanel;
  const dockWasOpen = useRef(dock.open);
  useEffect(() => {
    const opened = dock.open && !dockWasOpen.current;
    dockWasOpen.current = dock.open;
    if (opened && paneActive && panelRef.current !== null) closeRightPanel();
  }, [dock.open, paneActive, closeRightPanel]);
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
  const { guard, setSecretVisible } = useSettingsLeaveGuard();
  // The runs of the step view start and show without leaving the editor.
  const actions = useStepRunActions({ stay: true });
  const [url, setUrl] = useWorkspaceUrl();
  const { editorHandleRef, onEditorStateChange } = useWorkspaceEditorBinding();
  const { handle, state: editorState } = useWorkspaceEditor();
  const runSource = useCanvasRunSource(
    run,
    handle,
    editorState?.dirty ?? false
  );
  // One line for the run notices and the offer. The run chip of the bar
  // already says that a newer run is active.
  const allNotices = useWorkspaceNotices();
  // A test run stays on the line while it is shown.
  const testRunNotice = useTestRunNotice(run.details);
  const notices = useMemo(
    () => [
      ...allNotices.filter((notice) => notice.id !== 'newer-run-active'),
      ...(testRunNotice ? [testRunNotice] : []),
    ],
    [allNotices, testRunNotice]
  );
  // The snapshot takes the place of the editor: the hidden editor is not
  // the active pane.
  const snapshot = url.def === 'run';
  const paneActive = useModePaneActive() && !snapshot;
  const activePanel = useActiveRightPanel();
  // A right panel covers part of the zone: the canvas fits in the part that
  // stays free.
  const zoneRef = useRef<HTMLDivElement>(null);
  // The row of the canvas toolbars: the editor renders them in it.
  const [toolbarRow, setToolbarRow] = useState<HTMLDivElement | null>(null);
  const clearance = useRightPanelClearance(zoneRef, activePanel);
  const inset = useMemo(
    () => ({ right: clearance.overlap }),
    [clearance.overlap]
  );

  // A run that the bar starts becomes the selected run. The node view and
  // the step stay as they are.
  const onRunStarted = useCallback(
    (runId: string) => setUrl({ run: runId }),
    [setUrl]
  );

  const onSelectedStepNameChange = useCallback(
    (name: string | null) => {
      setUrl(name === null ? { step: null, ndv: null } : { step: name }, {
        replace: true,
      });
    },
    [setUrl]
  );

  const ndvState = url.ndv;
  const ndv = useMemo<DAGSpecHostProps['ndv']>(
    () => ({
      state: ndvState,
      onStateChange: (state) => setUrl({ ndv: state }, { replace: true }),
      active: paneActive,
    }),
    [ndvState, paneActive, setUrl]
  );

  // A deep link (?step, ?ndv) applies once the editor has loaded the spec.
  useDeepLinkReveal(
    handle,
    url.step ? { step: url.step, ndv: url.ndv } : null,
    true
  );

  // A legacy /spec link (?yaml=1) opens the YAML once the editor has loaded.
  // One URL write swaps ?yaml for ?ndv=docked: setUrl drops ?yaml.
  const [searchParams] = useSearchParams();
  const wantsYaml = searchParams.has('yaml');
  useEffect(() => {
    if (!handle || !wantsYaml) return;
    handle.toggleYaml(true);
    setUrl({ ndv: 'docked' }, { replace: true });
  }, [handle, wantsYaml, setUrl]);

  // The YAML panel shows as the user left it, unless a link opens a view.
  const openYamlPanel = useMemo(
    () => (handle ? () => handle.toggleYaml(true) : null),
    [handle]
  );
  useYamlPanelMemory({
    fileName,
    ndv: url.ndv,
    restorable:
      url.ndv === null &&
      url.step === null &&
      url.panel === null &&
      !snapshot &&
      !wantsYaml,
    openPanel: openYamlPanel,
  });

  // A link with ?panel opens the Workflow panel or a launcher panel once
  // the editor has loaded; the name leaves the URL, ?wtab stays.
  const wantedPanel = url.panel;
  useEffect(() => {
    if (!handle || !wantedPanel) return;
    if (wantedPanel === 'workflow') handle.openWorkflowPanel(true);
    else handle.openLauncher(wantedPanel);
    setUrl({ panel: null }, { replace: true });
  }, [handle, wantedPanel, setUrl]);

  // The panels guard their close, as a draft or a one-time secret is at risk.
  const launcherHost = useMemo<LauncherHost>(
    () => ({ guard, setSecretVisible }),
    [guard, setSecretVisible]
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
  const covered = url.ndv !== null || activePanel !== null || snapshot;
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
  const openStepDetails = useOpenStepDetails();

  // Esc closes the node details view when no dialog or menu is above it.
  // With no panel or node view open, it clears the selected step and ?step.
  const ndvOpen = url.ndv !== null || activePanel === 'ndv';
  useStepViewKeys({
    zoneRef,
    active: paneActive,
    hasStep: url.step !== null,
    viewOpen: ndvOpen,
    panelOpen: activePanel !== null || stepPanel.shown,
    closeView: () => setUrl({ ndv: null }, { replace: true }),
    clearStep: () => setUrl({ step: null, ndv: null }, { replace: true }),
  });

  // The step view holds the room of the Run panel while it is expanded.
  // The right panel (a launcher, the Workflow, the YAML, the node view) and the
  // Activity panel take turns. A panel that opens collapses Activity, which
  // comes back when the panel closes. Activity that opens closes the panel.
  useDockYield(paneActive && (activePanel !== null || url.ndv === 'expanded'));
  // Esc in the YAML of the step view closes the view too.
  useEscInStepYaml({
    active: paneActive,
    viewOpen: url.ndv === 'expanded',
    closeView: () => setUrl({ ndv: null }, { replace: true }),
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
    // The graph zone: every right panel and the expanded node view sit
    // inside this box (absolute) and never beyond it, under the row of the
    // canvas toolbars, which a panel never moves or covers.
    <div
      ref={zoneRef}
      data-testid="ww-canvas"
      className={cn(
        'relative flex h-full min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden',
        TOOLBAR_ZONE_CLASS
      )}
    >
      <CloseRightPanelOnActivity paneActive={paneActive} />
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
        <PanelPlacementProvider value="zone">
          <CanvasToolbarHostContext.Provider value={toolbarRow}>
            <div
              data-testid="ww-canvas-graph"
              hidden={snapshot}
              inert={snapshot}
              className="flex min-h-0 w-full min-w-0 flex-1 flex-col"
            >
              <PaneActiveContext.Provider value={paneActive}>
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
                  stepCommandGates={stepCommandGates}
                  followedRuns={actions.followed}
                  inputRun={inputRun}
                  chrome={ndvOpen || stepPanel.shown ? CHROME_NDV_OPEN : CHROME}
                  notices={notices}
                  editorHandleRef={editorHandleRef}
                  onEditorStateChange={onEditorStateChange}
                  launcherHost={launcherHost}
                  renderWorkflowPanel={renderWorkflowPanel}
                />
              </PaneActiveContext.Provider>
            </div>
          </CanvasToolbarHostContext.Provider>
        </PanelPlacementProvider>
      </CanvasOverlayInsetContext.Provider>
      {panelStep !== null && (
        <StepPanel
          step={panelStep}
          openedAt={stepPanel.openedAt}
          onClose={stepPanel.close}
          onStepCommand={onStepCommand}
          gates={stepCommandGates}
          onOpenStep={() => openStepDetails(panelStep)}
        />
      )}
      {snapshot && <RunSnapshotCanvas />}
      {actions.dialogs}
    </div>
  );
}
