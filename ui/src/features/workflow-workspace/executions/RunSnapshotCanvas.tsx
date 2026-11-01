// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ArrowLeft } from 'lucide-react';
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import {
  RightPanelProvider,
  useActiveRightPanel,
} from '@/contexts/RightPanelContext';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { useStepViewForm } from '@/features/dags/components/dag-editor/stepViewMode';
import { useYamlSection } from '@/features/dags/components/dag-editor/useYamlSection';
import {
  ReadOnlyMarker,
  YamlFilePanel,
} from '@/features/dags/components/dag-editor/YamlFilePanel';
import { PanelPlacementProvider } from '@/features/dags/components/step-details/PanelShell';
import {
  CanvasToolbarHostContext,
  TOOLBAR_ROW_CLASS,
  TOOLBAR_ROW_OVER_PANEL_CLASS,
  TOOLBAR_ZONE_CLASS,
} from '@/features/visual-editor/canvas/toolbarHost';
import { cn } from '@/lib/utils';
import { CanvasOverlayInsetContext } from '@/features/visual-editor/canvas/overlayInset';
import type { CodeEditorLike } from '@/features/visual-editor/useEditSession';
import type { VisualEditorValidation } from '@/features/visual-editor/VisualEditor';
import type {
  ApiStep,
  EditorHostProps,
  HandlerEvent,
  RunSource,
  StepCommand,
  StepCommandGates,
  VisualEditorHandle,
} from '@/features/visual-editor/contracts';
import { useI18n } from '@/i18n/I18nProvider';
import { useRightPanelClearance } from '../canvas/useRightPanelClearance';
import { escFree } from '../canvas/escFree';
import { useDockYield } from '../canvas/useDockYield';
import { runStepGates, useStepCommandHost } from '../canvas/useStepCommandHost';
import { useDeepLinkReveal, useStepViewKeys } from '../canvas/useStepViewHost';
import type { ApiDAGDetails, ApiDAGRunDetails } from '../contracts';
import { useWorkspacePermissions, useWorkspaceRun } from '../data';
import { useWorkspaceData } from '../data/context';
import { useModePaneActive } from '../layout/paneActive';
import {
  useWorkspaceUrl,
  type SetWorkspaceUrl,
  type WorkspaceUrlState,
} from '../state/workspaceUrl';
import { StepRunTabs } from '../step/StepRunTabs';
import { useStepRunActions } from '../step/useStepRunActions';
import { useRunReferenceScope } from './useRunReferenceScope';
import { useRunSpec, type RunSpec } from './useRunSpec';

const VisualEditor = lazy(() => import('@/features/visual-editor'));

const CHROME: EditorHostProps['chrome'] = {
  hideRunBar: true,
  striped: true,
  // The lock in the bar is the one read-only marker.
  hideReadOnlyChip: true,
};
// While the step view is open, Escape on the canvas closes it.
const CHROME_NDV_OPEN: EditorHostProps['chrome'] = {
  ...CHROME,
  escapeToHost: true,
};
const NO_ACTIONS: never[] = [];
const ignoreText = () => undefined;

// The step that stands for the agent in the runs of an agent DAG.
const AGENT_RUN_STEP = '__agent__';

const RUN_HANDLERS: readonly [keyof ApiDAGRunDetails, HandlerEvent][] = [
  ['onInit', 'init'],
  ['onWait', 'wait'],
  ['onSuccess', 'success'],
  ['onFailure', 'failure'],
  ['onAbort', 'abort'],
  ['onExit', 'exit'],
];

/**
 * The DAG as the run saw it, for the names of the steps: the canvas then
 * gives unnamed steps the names of the run.
 */
export function runDagDetails(
  details: ApiDAGRunDetails,
  dagName: string
): ApiDAGDetails {
  const handlerOn: Partial<Record<HandlerEvent, ApiStep>> = {};
  for (const [field, event] of RUN_HANDLERS) {
    const node = details[field] as { step: ApiStep } | undefined;
    if (node) handlerOn[event] = node.step;
  }
  return {
    name: details.name || dagName,
    steps: (details.nodes ?? [])
      .map((node) => node.step)
      .filter((step) => step.name !== AGENT_RUN_STEP),
    handlerOn,
    tasks: details.agentTasks,
  } as ApiDAGDetails;
}

export interface RunSnapshotCanvasProps {
  /** Defaults to the workspace file. */
  fileName?: string;
  /** Defaults to the workspace DAG. */
  dag?: ApiDAGDetails;
}

interface RunCanvasBodyProps {
  fileName: string;
  dagName: string;
  text: string;
  /** The Monaco model of the definition of the run: one for each run. */
  modelUri: string;
  /** The id of the run, for the header of the YAML panel. */
  runId: string;
  validation: VisualEditorValidation | null;
  runSource: RunSource;
  details: ApiDAGRunDetails | null;
  url: WorkspaceUrlState;
  setUrl: SetWorkspaceUrl;
  onSelectedStepNameChange(name: string | null): void;
  renderStepOutput: NonNullable<EditorHostProps['renderStepOutput']>;
  onStepCommand(cmd: StepCommand): void;
  stepCommandGates: StepCommandGates;
  onRunStep(step: string): void;
}

/**
 * The read-only canvas and the step view of the run, in the graph zone: the
 * step view is the node view of the editor (INPUT | parameters | OUTPUT) with
 * the data of the run, over the canvas and never beyond the zone.
 */
function RunCanvasBody({
  fileName,
  dagName,
  text,
  modelUri,
  runId,
  validation,
  runSource,
  details,
  url,
  setUrl,
  onSelectedStepNameChange,
  renderStepOutput,
  onStepCommand,
  stepCommandGates,
  onRunStep,
}: RunCanvasBodyProps) {
  const paneActive = useModePaneActive();
  const activePanel = useActiveRightPanel();
  const zoneRef = useRef<HTMLDivElement>(null);
  const clearance = useRightPanelClearance(zoneRef, activePanel);
  const inset = useMemo(
    () => ({ right: clearance.overlap }),
    [clearance.overlap]
  );

  // ?ndv=docked is the YAML panel of the whole file, as in the editor. The
  // step view needs a step: ?ndv=expanded alone shows nothing.
  const ndvState: WorkspaceUrlState['ndv'] =
    url.ndv === 'docked' ? 'docked' : url.step !== null ? url.ndv : null;

  // The step view needs the room of the Run panel, as in the editor.
  useDockYield(paneActive && ndvState === 'expanded');

  // The YAML of the step view is the read-only editor of the edit mode, on
  // the one step of the definition that the run used.
  const yamlEditorRef = useRef<CodeEditorLike | null>(null);
  const [, setEditorEpoch] = useState(0);
  const [yamlStepPath, setYamlStepPath] = useState<string[] | null>(null);
  const [drawerForm, setDrawerForm] = useStepViewForm(
    paneActive && ndvState === 'expanded' && url.step !== null
  );
  const [bodyRoot, setBodyRoot] = useState<HTMLDivElement | null>(null);
  // The step opens on its definition; the bar of the editor, which the frame
  // holds, has the Form button.
  const [toolbarRoot, setToolbarRoot] = useState<HTMLDivElement | null>(null);
  // The row of the canvas toolbars: the editor renders them in it.
  const [toolbarRow, setToolbarRow] = useState<HTMLDivElement | null>(null);
  const [inputRoot, setInputRoot] = useState<HTMLDivElement | null>(null);
  const [outputRoot, setOutputRoot] = useState<HTMLDivElement | null>(null);
  const [handle, setHandle] = useState<VisualEditorHandle | null>(null);
  const scope = useRunReferenceScope(text, fileName, ndvState !== null);
  const withScope = useMemo<VisualEditorValidation | null>(
    () => (validation ? { ...validation, referenceScope: scope } : null),
    [validation, scope]
  );

  const closeNdv = useCallback(() => {
    setUrl({ ndv: null }, { replace: true });
    setDrawerForm(false);
  }, [setUrl]);
  // The YAML panel and the step view follow the URL flag, by the rule of the
  // editor.
  const host = useMemo(
    () => ({
      state: ndvState,
      onStateChange: (state: 'docked' | 'expanded' | null) => {
        if (state === null) closeNdv();
        else setUrl({ ndv: state }, { replace: true });
      },
      active: paneActive,
    }),
    [ndvState, closeNdv, setUrl, paneActive]
  );
  const { yamlKind, setYamlKind, setSectionOpen } = useYamlSection(host);

  // A link with ?step&ndv opens the view once the editor has loaded.
  useDeepLinkReveal(
    handle,
    url.step && url.ndv === 'expanded'
      ? { step: url.step, ndv: url.ndv }
      : null,
    false
  );

  // Esc closes the step view when no dialog or menu is above it. With no
  // panel or step view open, it clears the selected step and ?step.
  useStepViewKeys({
    zoneRef,
    active: paneActive,
    hasStep: url.step !== null,
    viewOpen: ndvState !== null,
    panelOpen: activePanel !== null,
    closeView: closeNdv,
    clearStep: () => setUrl({ step: null, ndv: null }, { replace: true }),
  });

  const step = url.step;
  const runStep =
    stepCommandGates.canRunStep && step !== null
      ? () => onRunStep(step)
      : undefined;

  return (
    // The graph zone of the run view: the step view sits inside this box
    // (absolute) and never beyond it, under the row of the canvas toolbars.
    <div
      ref={zoneRef}
      data-testid="ww-run-zone"
      className={cn(
        'relative flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden',
        TOOLBAR_ZONE_CLASS
      )}
    >
      <div
        ref={setToolbarRow}
        data-testid="ww-run-zone-toolbar"
        className={cn(
          TOOLBAR_ROW_CLASS,
          clearance.free !== null && TOOLBAR_ROW_OVER_PANEL_CLASS
        )}
      />
      <CanvasOverlayInsetContext.Provider value={inset}>
        <PanelPlacementProvider value="zone">
          <CanvasToolbarHostContext.Provider value={toolbarRow}>
            <Suspense fallback={null}>
              <VisualEditor
                fileName={fileName}
                dagName={dagName}
                text={text}
                onTextChange={ignoreText}
                validation={withScope}
                editable={false}
                isDirty={false}
                customActions={NO_ACTIONS}
                runSource={runSource}
                selectedStepName={url.step}
                onSelectedStepNameChange={onSelectedStepNameChange}
                openNdvOn="open"
                renderStepOutput={renderStepOutput}
                onStepCommand={onStepCommand}
                stepCommandGates={stepCommandGates}
                chrome={ndvState !== null ? CHROME_NDV_OPEN : CHROME}
                yamlVisible={ndvState !== null}
                onYamlVisibleChange={setSectionOpen}
                onYamlModeChange={setYamlKind}
                initialYamlMode={yamlKind}
                yamlToolbarRoot={toolbarRoot}
                drawerBodyRoot={bodyRoot}
                formVisible={drawerForm}
                onFormVisibleChange={setDrawerForm}
                inputColumnRoot={inputRoot}
                outputColumnRoot={outputRoot}
                yamlEditorRef={yamlEditorRef}
                onYamlStepPathChange={setYamlStepPath}
                handleRef={setHandle}
              />
            </Suspense>
          </CanvasToolbarHostContext.Provider>
          <YamlFilePanel
            // The definition of the run is read-only: no tools, no edit. The
            // header has the same slots as the editor: the step title, the
            // lock and the run identity.
            text={text}
            readOnly
            runRef={{ id: runId }}
            title={url.step ?? undefined}
            hasRunFacts
            modelUri={modelUri}
            scopePath={yamlStepPath}
            onEditorMount={(editor) => {
              yamlEditorRef.current = editor;
              // The lazy editor mounts after the views that read it.
              setEditorEpoch((epoch) => epoch + 1);
            }}
            state={ndvState}
            scope={yamlKind}
            onClose={closeNdv}
            stepName={url.step}
            dag={validation?.dag}
            runDetails={details}
            onSelectStep={onSelectedStepNameChange}
            onRunStep={runStep}
            formMode={drawerForm}
            formRootRef={setBodyRoot}
            canForm={url.step !== null}
            onFormModeChange={setDrawerForm}
            toolbarRef={setToolbarRoot}
            inputRef={setInputRoot}
            outputRef={setOutputRoot}
          />
        </PanelPlacementProvider>
      </CanvasOverlayInsetContext.Provider>
    </div>
  );
}

/**
 * The definition that the snapshot shows. While the definition of the next run
 * loads, the last one stays, so that the step view is not unmounted; the model
 * of a definition stays while the text is the same.
 */
function useKeptSpec(
  spec: RunSpec,
  modelUri: string
): RunSpec & { modelUri: string } {
  const kept = useRef<{ text: string; modelUri: string } | null>(null);
  if (spec.text !== null) {
    if (kept.current?.text !== spec.text) {
      kept.current = { text: spec.text, modelUri };
    }
    return { ...spec, modelUri: kept.current.modelUri };
  }
  if (spec.isLoading && spec.error === null && kept.current !== null) {
    return {
      ...spec,
      text: kept.current.text,
      modelUri: kept.current.modelUri,
    };
  }
  kept.current = null;
  return { ...spec, modelUri };
}

/**
 * Esc leaves the snapshot when no layer above it is open: no dialog or menu,
 * no right panel or step view, no selected step (the step takes the first
 * Esc). It stays mounted while the definition loads.
 */
function useEscLeavesSnapshot(back: () => void, hasStep: boolean) {
  const paneActive = useModePaneActive();
  const activePanel = useActiveRightPanel();
  const backRef = useRef(back);
  backRef.current = back;
  const free = paneActive && activePanel === null && !hasStep;
  useEffect(() => {
    if (!free) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!escFree(event)) return;
      event.preventDefault();
      backRef.current();
    };
    // Capture phase: the canvas and its toolbar take Escape for themselves
    // when they have the focus, and the key would reach the document marked
    // as handled.
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [free]);
}

/** Mounted inside the RightPanelProvider of the snapshot. */
function SnapshotKeys({
  back,
  hasStep,
}: {
  back: () => void;
  hasStep: boolean;
}) {
  useEscLeavesSnapshot(back, hasStep);
  return null;
}

/**
 * The definition that the selected run used, on request (?def=run): a
 * read-only canvas with the state of the steps of the run, in place of the
 * editor. The step view is the node view of the editor with the data of the
 * run. Back (button or Esc) clears ?def.
 */
export function RunSnapshotCanvas(props: RunSnapshotCanvasProps) {
  const [url] = useWorkspaceUrl();
  if (url.def !== 'run') return null;
  return <RunSnapshot {...props} />;
}

function RunSnapshot(props: RunSnapshotCanvasProps) {
  const { ts } = useI18n();
  const data = useWorkspaceData('RunSnapshotCanvas');
  const fileName = props.fileName ?? data.fileName;
  const dagName = props.dag?.name ?? data.dagData.dag?.name ?? fileName;
  const run = useWorkspaceRun();
  const permissions = useWorkspacePermissions();
  const actions = useStepRunActions();
  const [url, setUrl] = useWorkspaceUrl();
  const loadedSpec = useRunSpec(run.ref, fileName);
  const remoteNode = useRemoteNode();
  // One model for each run; the model of the edit mode has another URI.
  const runModelUri = `inmemory://dagu/${encodeURIComponent(remoteNode)}/dag-runs/${encodeURIComponent(run.ref?.name ?? fileName)}/${encodeURIComponent(run.ref?.runId ?? '')}.yaml`;
  // A run that starts from the step view becomes the selected run. The view
  // stays as it is: it keeps the last definition while the next one loads,
  // and the same model, so that the cursor and the scroll stay, when the
  // definition is the same text.
  const spec = useKeptSpec(loadedSpec, runModelUri);
  const modelUri = spec.modelUri;

  const details = run.details;
  const runId = run.ref?.runId ?? null;
  const runSource = useMemo<RunSource>(
    () => ({ runId, details }),
    [runId, details]
  );
  const validation = useMemo<VisualEditorValidation | null>(
    () =>
      details ? { dag: runDagDetails(details, dagName), issues: [] } : null,
    [details, dagName]
  );

  const back = useCallback(() => setUrl({ def: null }), [setUrl]);

  // Picking a step of the snapshot selects ?step, which selects the row of
  // the steps navigation. A pick never opens the Run panel.
  const onSelectedStepNameChange = useCallback(
    (name: string | null) => {
      // Without a step the step view has nothing to show.
      setUrl(name === null ? { step: null, ndv: null } : { step: name }, {
        replace: true,
      });
    },
    [setUrl]
  );

  // The OUTPUT column of the step view has the tabs of the Run panel, as in
  // the editor.
  const renderStepOutput = useCallback<
    NonNullable<EditorHostProps['renderStepOutput']>
  >(
    (step, { runId: outputRunId, declared }) => (
      <StepRunTabs step={step} runId={outputRunId} declared={declared} />
    ),
    []
  );

  const canExecute = permissions.canExecute;
  const gates = useMemo(
    () => runStepGates(actions, { canExecute }),
    [actions, canExecute]
  );
  const { onStepCommand, stepCommandGates } = useStepCommandHost(
    actions,
    gates
  );

  const missingNotice = useMemo(
    () =>
      spec.missing
        ? [
            {
              id: 'run-snapshot-missing',
              tone: 'warning' as const,
              message: ts(
                'This run has no stored definition. The current definition is shown.'
              ),
              priority: 1,
            },
          ]
        : [],
    [spec.missing, ts]
  );

  return (
    <div
      data-testid="ww-run-definition"
      data-run-id={run.ref?.runId}
      className="flex h-full min-h-0 w-full min-w-0 flex-1 flex-col"
    >
      <div
        data-testid="ww-run-snapshot-bar"
        className="flex h-9 shrink-0 items-center gap-1 border-b border-border px-2"
      >
        <IconButton
          label={ts('Back to current definition')}
          icon={ArrowLeft}
          data-testid="ww-run-snapshot-back"
          onClick={back}
        />
        <ReadOnlyMarker runId={run.ref?.runId} />
      </div>
      {/* The panels of this canvas never close those of the editor. */}
      <RightPanelProvider>
        <SnapshotKeys back={back} hasStep={url.step !== null} />
        {!run.ref ? (
          <div className="flex min-h-0 flex-1 items-center justify-center text-sm text-muted-foreground">
            {ts('No runs yet')}
          </div>
        ) : (
          <>
            {missingNotice.length > 0 && (
              <NoticeLine
                notices={missingNotice}
                className="shrink-0 px-2 pt-1.5"
              />
            )}
            {spec.error !== null && (
              <div role="alert" className="p-4 text-sm text-destructive">
                {ts('Failed to load DAG spec:')}{' '}
                {spec.error || ts('Unknown error')}
              </div>
            )}
            {spec.text !== null && (
              <RunCanvasBody
                fileName={fileName}
                dagName={dagName}
                text={spec.text}
                modelUri={modelUri}
                runId={run.ref.runId}
                validation={validation}
                runSource={runSource}
                details={details}
                url={url}
                setUrl={setUrl}
                onSelectedStepNameChange={onSelectedStepNameChange}
                renderStepOutput={renderStepOutput}
                onStepCommand={onStepCommand}
                stepCommandGates={stepCommandGates}
                onRunStep={actions.runStep}
              />
            )}
          </>
        )}
      </RightPanelProvider>
      {actions.dialogs}
    </div>
  );
}
