// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ArrowLeftToLine,
  FileCode,
  Redo2,
  SlidersHorizontal,
  Undo2,
} from 'lucide-react';
import { createPortal } from 'react-dom';
import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
  type RefObject,
} from 'react';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { IconButton } from '@/components/ui/icon-button';
import { withLocalHarnesses } from '@/features/dags/components/dag-editor/customActionSchema';
import { NoticeLine } from '@/features/dags/components/dag-editor/NoticeLine';
import { useSchema } from '@/contexts/SchemaContext';
import { useI18n } from '@/i18n/I18nProvider';
import { copyText } from '@/lib/clipboard';
import { cn } from '@/lib/utils';
import { buildCanvasGraph, buildDetailsGraph, VisualCanvas } from './canvas';
import { AgentRunContext } from './canvas/agentRunContext';
import { CanvasToolbar } from './canvas/CanvasToolbar';
import { useCanvasToolbarHost } from './canvas/toolbarHost';
import { ghostLookOf } from './canvas/ghost';
import { useCanvasDirection } from './canvas/useCanvasDirection';
import { canvasShortcut, isTextEntryTarget } from './canvas/useCanvasKeymap';
import type {
  ActionCatalogEntry,
  ApiDAGDetails,
  ApiStep,
  CanvasCommand,
  CanvasViewHandle,
  DagDocModel,
  DagEditOp,
  EditorCustomActionHint,
  EditorHostProps,
  EditResult,
  ReferenceScope,
  HandlerEvent,
  PaletteTarget,
  StepKey,
  StepNameMap,
  StepView,
  ValidationIssue,
  VisualEditorHandle,
  VisualEditorState,
} from './contracts';
import {
  groupIssues,
  parseDagDoc,
  parseDagDocLenient,
  readSchedule,
  resolveStepNames,
} from './doc';
import { LauncherDataProbe, type LauncherData } from './launchers/LauncherData';
import {
  CronLauncherPanel,
  ManualLauncherPanel,
  WebhookLauncherPanel,
} from './launchers/LauncherPanel';
import type { LauncherId } from './launchers/model';
import {
  copyStepsYaml,
  duplicateStepsOp,
  pasteStepsOp,
} from './doc/ops/clipboard';
import { canConvertForEdit, editErrorMessage } from './editMessages';
import {
  effectivePanelWidth,
  usePanelPlacement,
} from '@/features/dags/components/step-details/PanelShell';
import {
  useDrawerWidth,
  StepSideDrawer,
} from '@/features/dags/components/step-details/StepSideDrawer';
import { buildActionCatalog, StepInspector } from './inspector';
import {
  buildReferenceCandidates,
  DeclaredOutputsPanel,
  UpstreamDataPanel,
} from './references';
import { insertIntoField, useLastFocusedField } from './insertIntoField';
import { useZoneWidth } from './useZoneWidth';
import {
  RunBar,
  StepRunPanel,
  useEditorRunActions,
  useRunOverlay,
} from './run';
import { buildAgentRunInfo } from './run/agentRun';
import { buildRunOverlay } from './run/runOverlay';
import { upstreamStepNames } from './run/upstreamSteps';
import { AddStepPanel } from './AddStepPanel';
import {
  paletteAddOp,
  paletteTargetOp,
  routeTargetsOf,
} from './canvas/gestures';
import { AGENT_NODE_KEY, type AgentRunInfo } from './canvas/types';
import {
  readRecentActions,
  withRecentAction,
  writeRecentActions,
} from './recentActions';
import { useDocModel } from './useDocModel';
import { savedStepText } from './stepBaseline';
import { useYamlEditorSync } from './useYamlEditorSync';
import { WorkflowDrawer } from './WorkflowDrawer';
import { useEditSession, type CodeEditorLike } from './useEditSession';

/** Result of the server validation of the text (or of the saved spec). */
export interface VisualEditorValidation {
  dag?: ApiDAGDetails;
  issues: ValidationIssue[];
  referenceScope?: ReferenceScope;
}

export interface VisualEditorProps extends EditorHostProps {
  fileName: string;
  dagName: string;
  /** The DAG YAML: the only state of the editor. */
  text: string;
  onTextChange(text: string): void;
  validation: VisualEditorValidation | null;
  /** False when the user may not write the DAG. */
  editable: boolean;
  /** True while the text differs from the saved spec. */
  isDirty: boolean;
  /**
   * The saved text. The step view marks the lines of its step that differ
   * from it, with bars in the margin.
   */
  savedText?: string;
  /**
   * A card over the top centre of the canvas, such as the offer of a draft.
   * It floats over the canvas and does not change its size.
   */
  canvasOverlay?: ReactNode;
  /** Inherited and local custom actions, for the palette and the forms. */
  customActions: EditorCustomActionHint[];
  /** Custom harnesses of the document, which the provider fields suggest. */
  harnesses?: string[];
  /** The YAML editor beside the canvas; edits go through it while it shows. */
  yamlEditorRef?: RefObject<CodeEditorLike | null>;
  yamlVisible?: boolean;
  /**
   * `source` is 'panel' when the user opens the YAML as a panel (the toolbar
   * button, the handle): a host can open it docked, as the other panels.
   */
  onYamlVisibleChange?(visible: boolean, source?: 'panel'): void;
  /** The pointer or the focus is on the YAML button: the host can load the code editor. */
  onYamlIntent?(): void;
  /**
   * Called with what the YAML shows: 'file' for the whole file (the YAML
   * button), 'step' for the YAML or the form of one step (the node view).
   * Called at mount and at each change.
   */
  onYamlModeChange?(mode: 'file' | 'step'): void;
  /** What the YAML shows when the editor mounts with it visible. Default 'step'. */
  initialYamlMode?: 'file' | 'step';
  /** Controls before the run controls in the toolbar of the canvas. */
  toolbarLead?: ReactNode;
  /**
   * Restores the text of the last save; the 'Revert to the last save' button
   * shows only with it, and is on only while the text is dirty. The host asks
   * for a confirmation when needed.
   */
  onRevertToSave?(): void;
  /** Controls after the editor controls, such as save. */
  toolbarTrail?: ReactNode;
  /** What the Workflow panel shows; the panel has no button without it. */
  workflowPanel?: ReactNode;
  /**
   * The bar of the YAML drawer, where the editor puts the choice between the
   * selected step and the whole file.
   */
  yamlToolbarRoot?: HTMLElement | null;
  /**
   * Called with the YAML path of the step that the YAML drawer shows alone
   * (the step scope), and with null when it shows more. The schema docs
   * follow this path while the cursor sits in a hidden line.
   */
  onYamlStepPathChange?(path: string[] | null): void;
  /**
   * The body of the same drawer, for the form of a step. With it, the YAML
   * and the form are two modes of one drawer; without it, the form has a
   * drawer of its own.
   */
  drawerBodyRoot?: HTMLElement | null;
  /** The drawer shows the form of the step, not its YAML. */
  formVisible?: boolean;
  onFormVisibleChange?(visible: boolean): void;
}

interface EditNotice {
  kind: 'edit-refused' | 'insert-hint';
  message: string;
  convertible: boolean;
}

const NOTHING_TO_EDIT: EditResult = {
  ok: false,
  code: 'noop',
  message: 'There is nothing to edit.',
};

function own<T>(record: Record<string, T>, key: string): T | undefined {
  return Object.prototype.hasOwnProperty.call(record, key)
    ? record[key]
    : undefined;
}

function sameState(a: VisualEditorState, b: VisualEditorState): boolean {
  return (
    a.frozen === b.frozen &&
    a.unparsedLine === b.unparsedLine &&
    a.canUndo === b.canUndo &&
    a.canRedo === b.canRedo &&
    a.selectedStepName === b.selectedStepName &&
    a.drawer === b.drawer &&
    a.direction === b.direction
  );
}

// chrome.striped: diagonal stripes behind the canvas of a read-only view.
// The dark theme draws the stripes at 40% of the border colour, so that they
// stay under the nodes.
const STRIPED_CANVAS =
  'bg-[repeating-linear-gradient(135deg,transparent_0,transparent_8px,var(--border)_8px,var(--border)_10px)] dark:bg-[repeating-linear-gradient(135deg,transparent_0,transparent_8px,color-mix(in_srgb,var(--border)_40%,transparent)_8px,color-mix(in_srgb,var(--border)_40%,transparent)_10px)]';

const HANDLER_PREFIX = 'handler_on.';
const NO_AGENT_RUN: AgentRunInfo = { iterations: 0, calls: {}, tasks: {} };
const NO_HARNESSES: string[] = [];

// Host mode: below WIDE the drawers cover the canvas; below NARROW the
// toolbar becomes a 'Tools' sheet.
const WIDE_VIEWPORT = 1000;
const NARROW_VIEWPORT = 768;
// In host mode the canvas shrinks beside a drawer only if it keeps this width.
const MIN_CANVAS_BESIDE_DRAWER = 480;
// A drawer that covers the canvas leaves a free strip at the left. The full
// toolbar needs this width; with less, the toolbar becomes the 'Tools' sheet,
// which sits in the strip, so that Workflow, YAML and Add step stay in reach.
const MIN_FREE_FOR_TOOLBAR = 560;
// Hides the toolbar of VisualCanvas (its top-left panel) for the compact one.
const HIDE_CANVAS_TOOLBAR = '[&_[data-flow-overlay].left-2.top-2]:hidden';

type ViewportBand = 'narrow' | 'medium' | 'wide';

function viewportBand(): ViewportBand {
  const width = window.innerWidth;
  if (width < NARROW_VIEWPORT) return 'narrow';
  return width < WIDE_VIEWPORT ? 'medium' : 'wide';
}

/** The width band of the window, followed only when `enabled`; else 'wide'. */
function useViewportBand(enabled: boolean): ViewportBand {
  const [band, setBand] = useState<ViewportBand>(viewportBand);
  useEffect(() => {
    if (!enabled) return;
    const update = () => setBand(viewportBand());
    update();
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, [enabled]);
  return enabled ? band : 'wide';
}

/**
 * Undo or redo for a key event, or null. Text fields and Monaco keep their
 * own undo, so the keys stay theirs there.
 */
export function historyKeyAction(
  event: Pick<
    KeyboardEvent,
    'key' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'altKey' | 'target'
  >
): 'undo' | 'redo' | null {
  if (!(event.ctrlKey || event.metaKey) || event.altKey) {
    return null;
  }
  if (isTextEntryTarget(event.target)) {
    return null;
  }
  const key = event.key.toLowerCase();
  if (key === 'z') {
    return event.shiftKey ? 'redo' : 'undo';
  }
  return key === 'y' && !event.shiftKey ? 'redo' : null;
}

/** The step of the server DAG for a step key, by its server name. */
function findServerStep(
  details: ApiDAGDetails | undefined,
  names: StepNameMap,
  key: StepKey
): ApiStep | undefined {
  if (!details) {
    return undefined;
  }
  if (key.startsWith(HANDLER_PREFIX)) {
    const event = key.slice(HANDLER_PREFIX.length) as HandlerEvent;
    const handlers = details.handlerOn as
      | Partial<Record<HandlerEvent, ApiStep>>
      | undefined;
    return handlers?.[event];
  }
  const name = names.byKey[key];
  return name === undefined
    ? undefined
    : details.steps?.find((step) => step.name === name);
}

/**
 * Visual editor of a DAG: a canvas of the steps with a step inspector. It
 * edits the YAML text through document operations and never re-prints the
 * file.
 */
export function VisualEditor(props: VisualEditorProps): ReactElement {
  const {
    fileName,
    dagName,
    text,
    onTextChange,
    validation,
    editable,
    isDirty,
    savedText,
    canvasOverlay,
    customActions,
    harnesses = NO_HARNESSES,
    yamlEditorRef,
    yamlVisible = false,
    onYamlVisibleChange,
    onYamlIntent,
    onYamlModeChange,
    initialYamlMode = 'step',
    toolbarLead,
    toolbarTrail,
    onRevertToSave,
    workflowPanel,
    workflowPanelBare = false,
    launcherHost,
    yamlToolbarRoot,
    onYamlStepPathChange,
    drawerBodyRoot,
    formVisible,
    onFormVisibleChange,
    runSource,
    onRunStarted,
    selectedStepName,
    onSelectedStepNameChange,
    openNdvOn = 'select',
    renderStepRun,
    renderStepOutput,
    inputColumnRoot,
    outputColumnRoot,
    onStepCommand,
    stepCommandGates,
    followedRuns,
    inputRun,
    chrome,
    onNotice,
    onStateChange,
    handleRef,
  } = props;
  const { ts } = useI18n();
  const { schema } = useSchema();
  const { model, frozen, sync } = useDocModel(text);

  const details = validation?.dag;
  const names = useMemo(
    () => resolveStepNames(model, details),
    [model, details]
  );
  const issues = useMemo(
    () => groupIssues(model, validation?.issues ?? [], names),
    [model, validation?.issues, names]
  );
  const catalog = useMemo(
    () => buildActionCatalog(schema ?? {}, customActions),
    [schema, customActions]
  );
  const formSchema = useMemo(
    () => withLocalHarnesses(schema ?? {}, harnesses),
    [schema, harnesses]
  );
  // 'latest' is the newest run at the last read; RunBar can choose another
  // one. A host that gives the run makes the editor read no run itself.
  const [runId, setRunId] = useState<string | null>('latest');
  const hosted = runSource !== undefined;
  const ownRun = useRunOverlay({ dagName, runId: hosted ? null : runId });
  const hostDetails = runSource?.details;
  const hostOverlay = useMemo(
    () => (hosted ? buildRunOverlay(hostDetails ?? undefined) : null),
    [hosted, hostDetails]
  );
  const overlay = hostOverlay ?? ownRun.overlay;
  const isAgentDag = model.dagType === 'agent';
  const hostAgentRun = useMemo(
    () => (hosted ? buildAgentRunInfo(hostDetails, isAgentDag) : null),
    [hosted, hostDetails, isAgentDag]
  );
  // A run of an agent DAG without task and without decision has no agent
  // fields: the hub still shows its run, with no call.
  const agentRun = hosted
    ? hostAgentRun
    : (ownRun.agent ??
      (isAgentDag && ownRun.runId && !ownRun.isLoading ? NO_AGENT_RUN : null));
  const resolvedRunId = runSource ? runSource.runId : ownRun.runId;
  const followRun = useCallback(
    (id: string | null) => {
      setRunId(id);
      if (id !== null) onRunStarted?.(id);
    },
    [onRunStarted]
  );
  const actions = useEditorRunActions({
    fileName,
    dagName,
    spec: text,
    isDirty,
  });

  // Until a text parses, the canvas shows the steps that the server read,
  // and the banner names the line of the first YAML error.
  const serverDag = model.ok ? undefined : details;
  const unparsedLine = serverDag ? model.parseErrors[0]?.line : undefined;
  const graph = useMemo(
    () =>
      serverDag
        ? buildDetailsGraph(serverDag)
        : buildCanvasGraph({ model, names, details, issues, overlay }),
    [serverDag, model, names, details, issues, overlay]
  );

  const [selected, setSelected] = useState<StepKey | null>(null);
  const canvasView = useRef<CanvasViewHandle>(null);

  // The last server name of each key: a draft that does not validate has no
  // DAG for the step, and the step keeps its name and its columns.
  const knownNames = useRef(new Map<StepKey, string>());
  // The name of the step that an edit has just selected. The names of the
  // server describe the text before the edit, so they give the name of
  // another step for its key until the next check. It counts only while the
  // text still gives that name to the step. `fresh` holds until the render
  // after the edit, where the model is the new one.
  const editedName = useRef<{
    key: StepKey;
    name: string;
    fresh: boolean;
  } | null>(null);
  /** Server name of a step key; the written name until the server knows it. */
  const serverNameOf = useCallback(
    (key: StepKey | null): string | null => {
      if (key === null) return null;
      const edited = editedName.current;
      if (
        edited?.key === key &&
        (edited.fresh ||
          model.steps.find((s) => s.key === key)?.explicitName === edited.name)
      ) {
        return edited.name;
      }
      const name = own(names.byKey, key);
      if (name !== undefined) {
        knownNames.current.set(key, name);
        return name;
      }
      const step =
        model.steps.find((s) => s.key === key) ??
        model.handlers.find((s) => s.key === key);
      return step?.explicitName ?? knownNames.current.get(key) ?? null;
    },
    [names, model]
  );
  const keyOf = useCallback(
    (name: string): StepKey | null =>
      own(names.byName, name) ??
      graph.nodes.find((node) => node.serverName === name)?.key ??
      model.steps.find((step) => step.explicitName === name)?.key ??
      null,
    [names, graph, model]
  );

  // The name that the host and the editor agree on, for a controlled selection.
  const syncedName = useRef<string | null | undefined>(undefined);
  // The key that syncedName stands for: the rename check below acts only on it.
  const syncedKey = useRef<StepKey | null>(null);
  const select = useCallback(
    (key: StepKey | null) => {
      setSelected(key);
      syncedKey.current = key;
      if (!onSelectedStepNameChange) return;
      const name = serverNameOf(key);
      if (name === syncedName.current) return;
      syncedName.current = name;
      onSelectedStepNameChange(name);
    },
    [onSelectedStepNameChange, serverNameOf]
  );

  // The canvas pans to a step once its node is drawn.
  const pendingReveal = useRef<StepKey | null>(null);
  const [revealRequest, setRevealRequest] = useState(0);
  const revealSoon = useCallback((key: StepKey) => {
    pendingReveal.current = key;
    setRevealRequest((n) => n + 1);
  }, []);
  useEffect(() => {
    const key = pendingReveal.current;
    if (key === null || !graph.nodes.some((node) => node.key === key)) return;
    pendingReveal.current = null;
    canvasView.current?.revealStep(key);
  }, [graph, revealRequest]);

  // A selection from the host. A name that no node has yet waits for the
  // names of the server.
  useEffect(() => {
    if (selectedStepName === undefined) return;
    if (selectedStepName === syncedName.current) return;
    const key = selectedStepName === null ? null : keyOf(selectedStepName);
    if (selectedStepName !== null && key === null) return;
    syncedName.current = selectedStepName;
    syncedKey.current = key;
    setSelected(key);
    if (key !== null) revealSoon(key);
  }, [selectedStepName, keyOf, revealSoon]);

  // A rename changes the server name of the selected step: tell the host.
  // A selection that the host has just cleared or moved is not a rename.
  useEffect(() => {
    if (!onSelectedStepNameChange || selected === null) return;
    if (syncedName.current === undefined) return;
    if (syncedKey.current !== selected) return;
    const name = serverNameOf(selected);
    if (name === null || name === syncedName.current) return;
    syncedName.current = name;
    onSelectedStepNameChange(name);
  }, [selected, serverNameOf, onSelectedStepNameChange]);
  // The YAML, the form of a step and the workflow panel share the side drawer.
  // A click on a step shows its YAML; its form opens from the small button at
  // its declaration in the YAML, or from the Open button of the step.
  const [workflowOpen, setWorkflowOpen] = useState(false);
  // The launcher whose panel is open, and what the host side of the launchers
  // reads from the server. Only a host that gives `launcherHost` has them.
  const [launcherOpen, setLauncherOpen] = useState<LauncherId | null>(null);
  const [launcherData, setLauncherData] = useState<LauncherData | null>(null);
  const schedule = useMemo(
    () => (launcherHost ? readSchedule(text) : null),
    [launcherHost, text]
  );
  const [ownFormOpen, setOwnFormOpen] = useState(false);
  const formOpen = formVisible ?? ownFormOpen;
  const setFormOpen = useCallback(
    (open: boolean) => {
      setOwnFormOpen(open);
      onFormVisibleChange?.(open);
    },
    [onFormVisibleChange]
  );
  // The form shares the drawer of the YAML when the page gives its body.
  const inDrawer = !!drawerBodyRoot;
  // The YAML button of the toolbar shows the whole file ('file'), with no
  // step choice and no form. A step that the user opens (double click, Enter)
  // shows its own YAML or its form ('step'), with the YAML | Form bar. A host
  // that opens the view of a step itself gets the 'step' mode.
  const [yamlMode, setYamlMode] = useState<'file' | 'step'>(initialYamlMode);
  const reportedMode = useRef<'file' | 'step' | null>(null);
  useEffect(() => {
    if (reportedMode.current === yamlMode) return;
    reportedMode.current = yamlMode;
    onYamlModeChange?.(yamlMode);
  });
  // The step that the cursor of the YAML is in.
  const [highlighted, setHighlighted] = useState<StepKey | null>(null);
  // The light stands on the selected step only: a click on another step in
  // the graph moves the selection, and the light of the cursor goes out.
  useEffect(() => {
    setHighlighted((current) =>
      current !== null && current !== selected ? null : current
    );
  }, [selected]);
  const [direction, setDirection] = useCanvasDirection();
  const [notice, setNotice] = useState<EditNotice | null>(null);
  // Where the step that the user is adding goes; the list of steps shows in
  // the side drawer while this is set.
  const [addTarget, setAddTarget] = useState<PaletteTarget | null>(null);
  // The choice that the list shows as active: the ghost on the canvas wears it.
  const [hoverEntry, setHoverEntry] = useState<ActionCatalogEntry | null>(null);
  const ghostLook = useMemo(
    () => (hoverEntry ? ghostLookOf(hoverEntry) : null),
    [hoverEntry]
  );
  const [recentActions, setRecentActions] = useState(readRecentActions);
  const readOnly = !editable;
  const canEdit = editable && !frozen;
  // A reader or a frozen canvas adds no step: the list closes.
  useEffect(() => {
    if (!canEdit) setAddTarget(null);
  }, [canEdit]);

  const showYamlFor = useCallback(
    (key: StepKey) => {
      select(key);
      setYamlMode('step');
      setFormOpen(false);
      setWorkflowOpen(false);
      setLauncherOpen(null);
      setAddTarget(null);
      onYamlVisibleChange?.(true);
    },
    [onYamlVisibleChange, setFormOpen, select]
  );
  useEffect(() => {
    if (editedName.current) editedName.current.fresh = false;
  });
  // The agent node of an agent DAG is no step: it has no form.
  const hasForm = useCallback(
    (key: StepKey) =>
      model.steps.some((step) => step.key === key) ||
      model.handlers.some((step) => step.key === key),
    [model]
  );
  const showFormFor = useCallback(
    (key: StepKey) => {
      if (!hasForm(key)) {
        showYamlFor(key);
        return;
      }
      select(key);
      setYamlMode('step');
      setFormOpen(true);
      setWorkflowOpen(false);
      setLauncherOpen(null);
      setAddTarget(null);
      onYamlVisibleChange?.(inDrawer);
    },
    [hasForm, showYamlFor, onYamlVisibleChange, inDrawer, setFormOpen, select]
  );
  // A click on a step, or a selection from the host: with openNdvOn 'open',
  // it only selects, and the form opens on Enter or on the Open command. The
  // YAML of the whole file does not follow the selection.
  const wholeFileShown = yamlVisible && yamlMode === 'file' && !formOpen;
  const pickStep = useCallback(
    (key: StepKey) => {
      // A launcher panel and a step take turns: the panel closes (through
      // the guard of the host for a webhook) and the step takes the focus.
      if (launcherOpen !== null) {
        const closing = launcherOpen;
        const done = () => {
          setLauncherOpen(null);
          if (closing === 'webhook') launcherData?.reloadWebhook();
        };
        if (closing === 'webhook' && launcherHost) launcherHost.guard(done);
        else done();
      }
      if (formOpen) showFormFor(key);
      else if (openNdvOn === 'open' || wholeFileShown) select(key);
      else showYamlFor(key);
    },
    [
      launcherOpen,
      launcherData,
      launcherHost,
      formOpen,
      openNdvOn,
      wholeFileShown,
      showFormFor,
      select,
      showYamlFor,
    ]
  );

  const openAdd = useCallback(
    (target: PaletteTarget) => {
      if (!canEdit) return;
      setAddTarget(target);
      setWorkflowOpen(false);
      setLauncherOpen(null);
      // The YAML and the form come back, in the same mode, with the new step.
      onYamlVisibleChange?.(false);
    },
    [canEdit, onYamlVisibleChange]
  );

  const setText = useCallback(
    (next: string) => {
      onTextChange(next);
      sync(next);
    },
    [onTextChange, sync]
  );
  const session = useEditSession({
    text,
    setText,
    names,
    editorRef: yamlVisible ? yamlEditorRef : undefined,
  });

  const afterEdit = useCallback(
    (result: EditResult) => {
      if (result.ok) {
        setNotice(null);
        if (result.select) {
          const written = parseDagDocLenient(result.text).steps.find(
            (step) => step.key === result.select
          )?.explicitName;
          editedName.current =
            written === undefined
              ? null
              : { key: result.select, name: written, fresh: true };
          // The new step shows where the last one did: its form or its YAML.
          pickStep(result.select);
          revealSoon(result.select);
        }
        return;
      }
      const message = editErrorMessage(result.code, ts);
      if (message) {
        setNotice({
          kind: 'edit-refused',
          message,
          convertible: canConvertForEdit(result.code),
        });
      }
    },
    [ts, pickStep, revealSoon]
  );
  const onEdit = useCallback(
    (op: DagEditOp) => afterEdit(session.apply(op)),
    [session, afterEdit]
  );
  // A clipboard edit goes through the edit session, so each paste or
  // duplicate is one undo step of its own.
  const applyResult = useCallback(
    (compute: (input: string) => EditResult): EditResult => {
      const result = session.applyWith(compute);
      afterEdit(result);
      return result;
    },
    [session, afterEdit]
  );

  const nameOf = (key: StepKey | null | undefined) =>
    graph.nodes.find((node) => node.key === key)?.label ?? '';
  function targetLabelOf(target: PaletteTarget): string {
    switch (target.mode) {
      case 'child':
        return ts('After {name}', { name: nameOf(target.source) });
      case 'route':
        return ts('Route {port} of {router}', {
          port: target.port,
          router: nameOf(target.router),
        });
      case 'parallel': {
        const edge = graph.edges.find((e) => e.id === target.edgeId);
        return edge
          ? ts('Parallel to {name}', { name: nameOf(edge.target) })
          : '';
      }
      case 'first':
        return ts('First step');
      case 'insert': {
        const edge = graph.edges.find((e) => e.id === target.edgeId);
        return edge
          ? ts('Between {from} and {to}', {
              from: nameOf(edge.source),
              to: nameOf(edge.target),
            })
          : '';
      }
      case 'after':
        // An add after nothing or after the agent node goes at the end, as
        // paletteTargetOp does.
        return selected && selected !== AGENT_NODE_KEY
          ? ts('After {name}', { name: nameOf(selected) })
          : ts('At the end of the workflow');
    }
  }
  const addLabel = !addTarget ? '' : targetLabelOf(addTarget);
  // A route target also needs the new step in the routes of the router. The
  // two edits are in one task, so they make one undo step.
  const addFromPalette = (
    target: PaletteTarget,
    entry: ActionCatalogEntry
  ): EditResult | null => {
    const op = paletteTargetOp(graph, selected, target, entry);
    if (!op) return null;
    let result = session.apply(op);
    // An unnamed step that the server has not named cannot be a dependency:
    // the step then goes after it without one, rather than no step.
    if (
      !result.ok &&
      result.code === 'invalidName' &&
      target.mode === 'after' &&
      op.kind === 'addStep' &&
      op.after &&
      op.dependsOn
    ) {
      result = session.apply(paletteAddOp(entry, op.after));
    }
    if (!result.ok || target.mode !== 'route' || !result.select) return result;
    const added = parseDagDocLenient(result.text).steps.find(
      (step) => step.key === result.select
    );
    if (!added?.explicitName) return result;
    const routed = session.apply({
      kind: 'setRouteTargets',
      step: target.router,
      pattern: target.port,
      targets: [
        ...routeTargetsOf(graph, target.router, target.port),
        added.explicitName,
      ],
    });
    return routed.ok ? { ...routed, select: result.select } : result;
  };
  const pickAdd = (entry: ActionCatalogEntry, options?: { stay: true }) => {
    const target = addTarget;
    const stay = !!options?.stay;
    if (!stay) setAddTarget(null);
    const next = withRecentAction(recentActions, entry.name);
    setRecentActions(next);
    writeRecentActions(next);
    if (!target || !canEdit) return;
    const result = addFromPalette(target, entry);
    if (!result) return;
    if (!stay || !result.ok || !result.select) {
      afterEdit(result);
      return;
    }
    // Shift+Enter: the list stays open for a step after the new one.
    setNotice(null);
    select(result.select);
    revealSoon(result.select);
    setAddTarget({ mode: 'child', source: result.select });
  };

  const runNameOf = (key: StepKey | null) =>
    key
      ? (names.byKey[key] ??
        graph.nodes.find((node) => node.key === key)?.serverName ??
        null)
      : null;
  // The step that waits for the confirmation of a run.
  const [runTarget, setRunTarget] = useState<StepKey | null>(null);
  const runTargetName = runNameOf(runTarget);
  const runUpstreamNames = upstreamStepNames(
    details?.steps,
    runTargetName ?? ''
  );

  // The text editor tells the canvas where its cursor is. In the whole
  // file, a cursor in the block of a step selects the step on the canvas,
  // as a click on its node does, and moves the canvas to it; a cursor
  // outside every step changes nothing. In the view of a step, the editor
  // shows that step only.
  const noEditor = useRef<CodeEditorLike | null>(null);
  const followCursor = useCallback(
    (key: StepKey | null, byUser: boolean) => {
      setHighlighted(key);
      // An edit of the text that the canvas makes moves the cursor too: only
      // the cursor of the user selects.
      if (!byUser || key === null || !wholeFileShown) return;
      if (key === syncedKey.current) return;
      select(key);
      revealSoon(key);
    },
    [wholeFileShown, select, revealSoon]
  );
  // The saved lines of the selected step, for the bars of the step view.
  const savedModel = useMemo(
    () => (savedText === undefined ? null : parseDagDocLenient(savedText)),
    [savedText]
  );
  const stepBaseline =
    savedModel && selected && yamlMode === 'step' && !frozen
      ? savedStepText(savedModel, model, selected)
      : undefined;
  const yamlShownNow = yamlVisible && !(inDrawer && formOpen);
  useYamlEditorSync({
    editorRef: yamlEditorRef ?? noEditor,
    visible: yamlShownNow,
    model,
    frozen,
    selected,
    scope: yamlMode === 'step' ? 'step' : 'file',
    baseline: stepBaseline,
    onCursorStep: followCursor,
  });
  // The step that the drawer shows alone, as a YAML path for the docs.
  const stepPath = yamlStepPath(model, selected);
  const stepScopeKey =
    yamlVisible && !(inDrawer && formOpen) && yamlMode === 'step' && !frozen
      ? (stepPath?.join('\u0000') ?? null)
      : null;
  const reportedStepScope = useRef<string | null>(null);
  useEffect(() => {
    if (!onYamlStepPathChange || reportedStepScope.current === stepScopeKey) {
      return;
    }
    reportedStepScope.current = stepScopeKey;
    onYamlStepPathChange(stepScopeKey === null ? null : (stepPath ?? null));
  });
  // The light of the cursor goes out with the drawer.
  const shownHighlight = yamlVisible ? highlighted : null;

  // The steps that a clipboard operation acts on: the step of a node menu,
  // else the selected step.
  const clipboardSteps = (step?: StepKey): StepKey[] => {
    const key = step ?? selected;
    return key !== null && hasForm(key) ? [key] : [];
  };
  // What the user copied last; the paste of the canvas is on while it is set.
  const [copied, setCopied] = useState<string | null>(null);
  const copySelection = (step?: StepKey): string | null => {
    const steps = clipboardSteps(step);
    if (steps.length === 0) return null;
    const yaml = copyStepsYaml(parseDagDoc(text), steps);
    if (yaml !== '') setCopied(yaml);
    return yaml;
  };
  const paste = (yaml: string): EditResult => {
    if (!canEdit) return NOTHING_TO_EDIT;
    const after = clipboardSteps()[0] ?? null;
    return applyResult((input) =>
      pasteStepsOp(parseDagDoc(input), yaml, after)
    );
  };
  const duplicateSelection = (step?: StepKey): EditResult => {
    const steps = clipboardSteps(step);
    if (!canEdit || steps.length === 0) return NOTHING_TO_EDIT;
    return applyResult((input) => duplicateStepsOp(parseDagDoc(input), steps));
  };

  const onCommand = (command: CanvasCommand) => {
    switch (command.kind) {
      case 'runStep': {
        if (!onStepCommand) {
          setRunTarget(command.step);
          return;
        }
        // The host confirms the run itself.
        const name = runNameOf(command.step);
        if (name !== null) onStepCommand({ kind: 'runStep', step: name });
        return;
      }
      case 'step':
        onStepCommand?.(command.cmd);
        return;
      case 'showYaml':
        showYamlFor(command.step);
        return;
      case 'copy': {
        const yaml = copySelection(command.step);
        if (yaml) void copyText(yaml);
        return;
      }
      case 'paste': {
        const yaml = command.text.trim() !== '' ? command.text : copied;
        if (yaml) paste(yaml);
        return;
      }
      case 'duplicate':
        duplicateSelection(command.step);
        return;
      // Opening a step shows its YAML first; the form is one click away.
      case 'openInspector':
        showYamlFor(command.step);
        return;
      // The name is the first field of the form, and the form holds the
      // run panel.
      case 'rename':
      case 'showRun':
        showFormFor(command.step);
        return;
    }
  };

  const confirmRunStep = useCallback(async () => {
    setRunTarget(null);
    if (!runTargetName) {
      return;
    }
    // The steps that the step needs run first, on no earlier output.
    const upstream = upstreamStepNames(details?.steps, runTargetName);
    const id = await actions.runSteps([...upstream, runTargetName]);
    if (id !== null) {
      followRun(id);
    }
  }, [actions, details?.steps, runTargetName, followRun]);

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const action = readOnly ? null : historyKeyAction(event);
    if (action) {
      event.preventDefault();
      session[action]();
    }
  };
  const selectedStep = selected
    ? (model.steps.find((step) => step.key === selected) ??
      model.handlers.find((step) => step.key === selected))
    : undefined;
  const serverStep = selected
    ? findServerStep(details, names, selected)
    : undefined;
  const serverName = selected
    ? (names.byKey[selected] ?? serverStep?.name ?? serverNameOf(selected))
    : null;
  const lastField = useLastFocusedField();
  // The form of another step has its own fields.
  const resetLastField = lastField.reset;
  useEffect(() => {
    resetLastField();
  }, [selected, resetLastField]);
  // False when no field can take the token: the panel copies it instead.
  const insertReference = useCallback(
    (token: string) => {
      const field = lastField.get();
      if (!field) return false;
      insertIntoField(field, token);
      return true;
    },
    [lastField]
  );

  const candidates = useMemo(
    () => buildReferenceCandidates(validation?.referenceScope, serverName),
    [validation?.referenceScope, serverName]
  );

  // A host can show the run of the step and the upstream data in columns
  // of its own.
  const stepRunFacts =
    renderStepRun && serverName ? renderStepRun(serverName, 'facts') : null;
  // The INPUT run: the followed upstream run, else the run that feeds the
  // step (a host that gives `inputRun`), else the selected run.
  // The newest run of the step: the run of the upstream steps, or the run of
  // the step alone when it executed no upstream step. Of two entries of one
  // run, the upstream one.
  const followedInput =
    (followedRuns ?? [])
      .filter((run) => run.step === serverName)
      .sort(
        (a, b) =>
          b.startedAt - a.startedAt ||
          Number(b.kind === 'upstream') - Number(a.kind === 'upstream')
      )[0] ?? null;
  const inputRunId = followedInput
    ? followedInput.runId
    : inputRun
      ? inputRun.runId
      : resolvedRunId;
  const upstreamPanel = (
    <UpstreamDataPanel
      dagName={dagName}
      fileName={fileName}
      runId={inputRunId}
      inputRun={inputRun}
      scope={validation?.referenceScope}
      stepName={serverName}
      onInsert={readOnly || frozen ? undefined : insertReference}
      onRunUpstream={
        onStepCommand && stepCommandGates?.canRunStep && serverName
          ? () => onStepCommand({ kind: 'runUpstream', step: serverName })
          : undefined
      }
      runUpstreamBlocked={
        frozen ? ts('Fix the file to run the upstream steps') : undefined
      }
      followedRun={followedInput}
      layout={inputColumnRoot ? 'column' : 'bottom'}
    />
  );

  const inspectorContent =
    formOpen &&
    !addTarget &&
    selected &&
    selectedStep &&
    (!inDrawer || yamlVisible) ? (
      <div
        onFocus={lastField.onFocus}
        className="flex min-h-0 flex-1 flex-col"
        data-testid="ve-inspector"
      >
        <div className="min-h-0 flex-1">
          <StepInspector
            model={model}
            step={selected}
            serverStep={serverStep}
            schema={formSchema}
            catalog={catalog}
            customActions={customActions}
            candidates={candidates}
            issues={issues.byStep[selected] ?? []}
            readOnly={readOnly || frozen}
            onEdit={onEdit}
            embedded={inDrawer}
            onClose={() => {
              setFormOpen(false);
              select(null);
            }}
            onShowYaml={
              onYamlVisibleChange && !inDrawer
                ? () => showYamlFor(selected)
                : undefined
            }
            runPanel={
              !serverName || outputColumnRoot
                ? undefined
                : (stepRunFacts ?? (
                    <StepRunPanel
                      dagName={dagName}
                      runId={resolvedRunId}
                      stepName={serverName}
                    />
                  ))
            }
          />
        </div>
        {!inputColumnRoot && (
          <div className="max-h-[35%] flex-shrink-0 overflow-y-auto border-t px-2 py-1">
            {upstreamPanel}
          </div>
        )}
      </div>
    ) : null;

  // The columns of the host show the selected step while its form or its
  // YAML shows, not only with the form.
  const columnsShown =
    selected !== null &&
    !addTarget &&
    (!!inspectorContent || (yamlVisible && yamlMode === 'step'));
  // The run of 'Execute step' fills the OUTPUT column of its step.
  const followedExecute =
    followedRuns?.find(
      (run) => run.kind === 'execute' && run.step === serverName
    ) ?? null;
  const hostColumns = columnsShown ? (
    <>
      {inputColumnRoot && createPortal(upstreamPanel, inputColumnRoot)}
      {outputColumnRoot &&
        serverName &&
        createPortal(
          stepRunFacts ??
            (renderStepOutput ? (
              renderStepOutput(serverName, {
                runId: followedExecute?.runId ?? resolvedRunId,
                declared: (
                  <DeclaredOutputsPanel
                    scope={validation?.referenceScope}
                    stepName={serverName}
                    action={actionIdOf(selectedStep)}
                  />
                ),
              })
            ) : followedExecute ? (
              <div className="p-3">
                <StepRunPanel
                  dagName={dagName}
                  runId={followedExecute.runId}
                  stepName={serverName}
                />
              </div>
            ) : resolvedRunId === null ? (
              <DeclaredOutputsPanel
                scope={validation?.referenceScope}
                stepName={serverName}
                action={actionIdOf(selectedStep)}
              />
            ) : (
              <StepRunPanel
                dagName={dagName}
                runId={resolvedRunId}
                stepName={serverName}
              />
            )),
          outputColumnRoot
        )}
    </>
  ) : null;

  const toggleWorkflow = (open?: boolean) => {
    const next = open ?? !workflowOpen;
    // A draft in a tab of the panel is at risk when the panel closes.
    if (!next && workflowOpen && launcherHost) {
      launcherHost.guard(() => applyWorkflow(false));
      return;
    }
    applyWorkflow(next);
  };
  const applyWorkflow = (next: boolean) => {
    setAddTarget(null);
    if (next) {
      setFormOpen(false);
      setLauncherOpen(null);
      onYamlVisibleChange?.(false);
    }
    setWorkflowOpen(next);
  };
  const openLauncher = (id: LauncherId) => {
    if (!launcherHost) return;
    // An old /webhook link on an instance without webhooks opens nothing.
    if (id === 'webhook' && launcherData && !launcherData.webhooksAvailable) {
      return;
    }
    // The click on a launcher ends the focus on a step: the panel of the
    // launcher takes the place of the file and of the step.
    select(null);
    setHighlighted(null);
    setAddTarget(null);
    setFormOpen(false);
    setWorkflowOpen(false);
    onYamlVisibleChange?.(false);
    setLauncherOpen(id);
  };
  const closeLauncher = () => {
    const closing = launcherOpen;
    const done = () => {
      setLauncherOpen(null);
      // A webhook made in the panel changes the node.
      if (closing === 'webhook') launcherData?.reloadWebhook();
    };
    if (closing === 'webhook' && launcherHost) launcherHost.guard(done);
    else done();
  };
  const setSchedule = (expressions: string[]) => {
    if (!canEdit) return;
    afterEdit(session.apply({ kind: 'setSchedule', expressions }));
  };
  const toggleYaml = (open?: boolean) => {
    if (!onYamlVisibleChange) return;
    // The form shows in place of the text: the button then shows the text.
    const next = open ?? !(yamlVisible && !(inDrawer && formOpen));
    setAddTarget(null);
    setFormOpen(false);
    setYamlMode('file');
    if (next) {
      setWorkflowOpen(false);
      setLauncherOpen(null);
    }
    if (next) onYamlVisibleChange(true, 'panel');
    else onYamlVisibleChange(false);
  };

  // The handle, the notices and the canvas commands live longer than one
  // render: they call the functions of the latest render.
  const live = useRef({
    onEdit,
    onCommand,
    copySelection,
    paste,
    duplicateSelection,
    toggleWorkflow,
    openLauncher,
    toggleYaml,
    pickStep,
    showFormFor,
    showYamlFor,
    openAdd,
    keyOf,
    hasForm,
    session,
    canEdit,
    editable,
    workflowPanel,
  });
  live.current = {
    onEdit,
    onCommand,
    copySelection,
    paste,
    duplicateSelection,
    toggleWorkflow,
    openLauncher,
    toggleYaml,
    pickStep,
    showFormFor,
    showYamlFor,
    openAdd,
    keyOf,
    hasForm,
    session,
    canEdit,
    editable,
    workflowPanel,
  };
  const handleCommand = useCallback(
    (command: CanvasCommand) => live.current.onCommand(command),
    []
  );

  // The handle does not change, so a host that keeps it renders once.
  useImperativeHandle(
    handleRef,
    (): VisualEditorHandle => ({
      fitView: () => canvasView.current?.fitView(),
      revealStep: (name) => {
        const key = live.current.keyOf(name);
        if (key === null) return false;
        live.current.pickStep(key);
        revealSoon(key);
        return true;
      },
      highlightStep: (name) =>
        canvasView.current?.highlightStep(
          name === null ? null : live.current.keyOf(name)
        ),
      addStep: (target) => {
        if (live.current.canEdit)
          live.current.openAdd(target ?? { mode: 'after' });
      },
      openWorkflowPanel: (open) => {
        // An explicit open waits for the panel: its DAG may not have loaded.
        if (open !== undefined || live.current.workflowPanel) {
          live.current.toggleWorkflow(open);
        }
      },
      openLauncher: (id) => live.current.openLauncher(id),
      toggleYaml: (open) => live.current.toggleYaml(open),
      openForm: (name) => {
        const key = live.current.keyOf(name);
        if (key === null || !live.current.hasForm(key)) return false;
        live.current.showFormFor(key);
        revealSoon(key);
        return true;
      },
      openYaml: (name) => {
        const key = live.current.keyOf(name);
        if (key === null) return false;
        live.current.showYamlFor(key);
        revealSoon(key);
        return true;
      },
      copySelection: () => live.current.copySelection(),
      paste: (yaml) => live.current.paste(yaml),
      duplicateSelection: () => live.current.duplicateSelection(),
      undo: () => {
        if (live.current.editable) live.current.session.undo();
      },
      redo: () => {
        if (live.current.editable) live.current.session.redo();
      },
    }),
    [revealSoon]
  );

  // A host with compact notices shows the notices of the editor itself.
  const compactNotices = !!chrome?.compactNotices || !!onNotice;
  const band = useViewportBand(!!chrome);
  const rootRef = useRef<HTMLDivElement>(null);
  const zoneWidth = useZoneWidth(rootRef);
  // The cap of a panel is a share of its containing block: the zone for a
  // panel in the graph zone, else the window (a fixed panel).
  const panelBase =
    usePanelPlacement() === 'zone' ? zoneWidth : window.innerWidth;
  const drawerWidth = effectivePanelWidth(useDrawerWidth(), panelBase);
  const drawerShrinksCanvas =
    !chrome ||
    (band === 'wide' && panelBase - drawerWidth >= MIN_CANVAS_BESIDE_DRAWER);
  const panelOpen =
    !!inspectorContent ||
    yamlVisible ||
    !!addTarget ||
    launcherOpen !== null ||
    (workflowOpen && !!workflowPanel);
  const drawerCovers = !!chrome && panelOpen && !drawerShrinksCanvas;
  // A toolbar in the row of the zone never meets a panel: only a narrow
  // window, where the row is hidden, has the 'Tools' sheet.
  const toolbarHosted = useCanvasToolbarHost() !== undefined;
  const compactTools =
    !!chrome &&
    (band === 'narrow' ||
      (!toolbarHosted &&
        drawerCovers &&
        panelBase - drawerWidth < MIN_FREE_FOR_TOOLBAR));
  const reportedNotice = useRef<EditNotice | null>(null);
  useEffect(() => {
    if (!compactNotices || !onNotice || notice === reportedNotice.current) {
      return;
    }
    reportedNotice.current = notice;
    onNotice(
      notice
        ? {
            kind: notice.kind,
            message: notice.message,
            convertible: notice.convertible && !readOnly,
            convert: () =>
              live.current.onEdit({ kind: 'convertStepsToExplicitForm' }),
            dismiss: () => setNotice(null),
          }
        : null
    );
  }, [compactNotices, onNotice, notice, readOnly]);

  const drawer: VisualEditorState['drawer'] =
    addTarget && canEdit
      ? 'add-step'
      : launcherOpen !== null
        ? 'launcher'
        : workflowOpen && workflowPanel
          ? 'workflow'
          : inspectorContent
            ? 'form'
            : yamlVisible
              ? 'yaml'
              : 'none';
  const editorState: VisualEditorState = {
    frozen,
    unparsedLine,
    canUndo: editable && session.canUndo,
    canRedo: editable && session.canRedo,
    selectedStepName: serverNameOf(selected),
    drawer,
    direction,
  };
  // Called after each commit, but only when a field changed.
  const reportedState = useRef<VisualEditorState | null>(null);
  useEffect(() => {
    const last = reportedState.current;
    if (!onStateChange || (last && sameState(last, editorState))) return;
    reportedState.current = editorState;
    onStateChange(editorState);
  });

  const yamlShown = yamlVisible && !(inDrawer && formOpen);
  const toolbarEnd = (
    <>
      {toolbarLead}
      {!chrome?.hideRunBar && (
        <RunBar
          dagName={dagName}
          fileName={fileName}
          runId={runSource ? runSource.runId : runId}
          onRunIdChange={followRun}
          actions={actions}
          isDirty={isDirty}
        />
      )}
      {readOnly ? null : (
        <>
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
          {onRevertToSave ? (
            <IconButton
              label={ts('Revert to the last save')}
              icon={ArrowLeftToLine}
              disabledReason={isDirty ? undefined : ts('Nothing to revert')}
              onClick={onRevertToSave}
            />
          ) : null}
          <IconButton
            label={ts('Undo')}
            icon={Undo2}
            shortcut={canvasShortcut('undo')}
            disabledReason={session.canUndo ? undefined : ts('Nothing to undo')}
            onClick={session.undo}
          />
          <IconButton
            label={ts('Redo')}
            icon={Redo2}
            shortcut={canvasShortcut('redo')}
            disabledReason={session.canRedo ? undefined : ts('Nothing to redo')}
            onClick={session.redo}
          />
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
        </>
      )}
      {workflowPanel && (
        <IconButton
          label={ts('Workflow')}
          icon={SlidersHorizontal}
          pressed={workflowOpen}
          onClick={() => toggleWorkflow()}
        />
      )}
      {onYamlVisibleChange && (
        <IconButton
          label={ts('YAML')}
          icon={FileCode}
          pressed={yamlShown}
          onClick={() => toggleYaml()}
          onPointerEnter={onYamlIntent}
          onFocus={onYamlIntent}
        />
      )}
      {toolbarTrail}
    </>
  );

  // One bar for the two modes of the drawer, so that it does not change when
  // the user goes from the YAML to the form: the mode, then which part of the
  // YAML. The page owns the bar and the close button.
  const formMode = inDrawer && formOpen;
  const canForm = !!selectedStep;
  const segment = (active: boolean) =>
    cn(
      'h-6 px-2 text-xs disabled:opacity-50',
      active ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground'
    );
  const yamlControls =
    yamlToolbarRoot && yamlVisible && (yamlMode === 'step' || formMode)
      ? createPortal(
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <div
              role="group"
              aria-label={ts('Editor')}
              className="flex shrink-0 overflow-hidden rounded border border-border"
            >
              <button
                type="button"
                aria-pressed={!formMode}
                onClick={() => setFormOpen(false)}
                className={segment(!formMode)}
              >
                {ts('YAML')}
              </button>
              <button
                type="button"
                aria-pressed={formMode}
                disabled={!canForm}
                title={canForm ? undefined : ts('Select a step first')}
                onClick={() => selected && showFormFor(selected)}
                className={segment(formMode)}
              >
                {ts('Form')}
              </button>
            </div>
          </div>,
          yamlToolbarRoot
        )
      : null;

  return (
    <div
      ref={rootRef}
      className="flex h-full min-h-0 flex-col gap-2"
      // The drawer is over the page: leave its width free, so that the
      // toolbar and the canvas stay in reach. In host mode the drawer covers
      // the canvas instead on a window narrower than WIDE_VIEWPORT, and when
      // the canvas would keep less than MIN_CANVAS_BESIDE_DRAWER.
      style={
        drawerShrinksCanvas && panelOpen
          ? { paddingRight: drawerWidth }
          : undefined
      }
      data-testid="visual-editor"
      onKeyDown={onKeyDown}
    >
      {notice && !compactNotices && (
        <NoticeLine
          notices={[
            {
              id: 'edit-refused',
              tone: 'warning',
              role: 'alert',
              message: notice.message,
              action:
                notice.convertible && !readOnly
                  ? {
                      label: ts('Convert steps'),
                      run: () => onEdit({ kind: 'convertStepsToExplicitForm' }),
                    }
                  : undefined,
              dismissible: true,
              onDismiss: () => setNotice(null),
              priority: 0,
            },
          ]}
        />
      )}

      <ConfirmDialog
        title={ts('Run step')}
        buttonText={ts('Run')}
        visible={runTargetName !== null}
        dismissModal={() => setRunTarget(null)}
        onSubmit={() => void confirmRunStep()}
      >
        <div className="space-y-1 text-sm">
          <p>
            {runUpstreamNames.length > 0
              ? ts('Run the step {name} with its {count} upstream steps?', {
                  name: runTargetName ?? '',
                  count: String(runUpstreamNames.length),
                })
              : ts('Run only the step {name} now? Its command runs for real.', {
                  name: runTargetName ?? '',
                })}
          </p>
          {isDirty && (
            <p className="text-muted-foreground">
              {ts('The step runs from the unsaved editor text.')}
            </p>
          )}
        </div>
      </ConfirmDialog>

      <div className="flex min-h-0 flex-1 gap-2">
        <div
          data-testid="ve-canvas-frame"
          data-striped={chrome?.striped ? 'true' : undefined}
          className={cn(
            '@container/ve relative min-w-0 flex-1 overflow-hidden rounded-md border',
            // A host gives the canvas its zone; classic keeps a floor.
            chrome ? 'min-h-0' : 'min-h-[420px]',
            chrome?.striped && STRIPED_CANVAS,
            compactTools && HIDE_CANVAS_TOOLBAR
          )}
        >
          <AgentRunContext.Provider value={agentRun}>
            <VisualCanvas
              graph={graph}
              direction={direction}
              selected={selected}
              frozen={frozen}
              unparsedLine={unparsedLine}
              readOnly={readOnly}
              hideReadOnlyChip={chrome?.hideReadOnlyChip}
              catalog={catalog}
              highlighted={shownHighlight}
              onAddStep={openAdd}
              ghost={
                addTarget && canEdit
                  ? { target: addTarget, look: ghostLook }
                  : null
              }
              onSelect={(key) => {
                if (key === null) {
                  setFormOpen(false);
                  select(null);
                } else {
                  pickStep(key);
                }
              }}
              onEdit={onEdit}
              onCommand={handleCommand}
              onDirectionChange={setDirection}
              toolbarEnd={compactTools ? undefined : toolbarEnd}
              exportName={dagName}
              viewRef={canvasView}
              hideFrozenNotice={compactNotices}
              stepCommandGates={onStepCommand ? stepCommandGates : undefined}
              canPaste={copied !== null}
              escapeToHost={!!chrome?.escapeToHost}
              onRefused={(code) =>
                setNotice({
                  kind: 'edit-refused',
                  message: editErrorMessage(code, ts) ?? '',
                  convertible: false,
                })
              }
              launchers={launcherHost ? launcherData?.states : undefined}
              activeLauncher={launcherOpen}
              onLauncher={openLauncher}
            />
          </AgentRunContext.Provider>
          {canvasOverlay ? (
            <div
              data-testid="ve-canvas-overlay"
              className={cn(
                'pointer-events-none absolute inset-x-2 z-20 flex justify-center',
                // Under the toolbars, which float over the top of the canvas.
                'top-12'
              )}
            >
              {canvasOverlay}
            </div>
          ) : null}
          {compactTools && (
            <CanvasToolbar
              compact
              direction={direction}
              onDirectionChange={setDirection}
              readOnly={readOnly && !chrome?.hideReadOnlyChip}
              toolbarEnd={toolbarEnd}
            />
          )}
        </div>
      </div>
      {launcherHost && schedule && (
        <LauncherDataProbe
          fileName={fileName}
          scheduleCount={schedule.expressions.length + schedule.others}
          onData={setLauncherData}
        />
      )}
      {yamlControls}
      {hostColumns}
      {inDrawer && inspectorContent && drawerBodyRoot
        ? createPortal(inspectorContent, drawerBodyRoot)
        : null}
      {!inDrawer && (
        <StepSideDrawer
          isOpen={!!inspectorContent}
          onClose={() => {
            setFormOpen(false);
            select(null);
          }}
          hasContent={!!inspectorContent}
          modal={false}
          label={ts('Step inspector')}
          panelId="ndv"
        >
          {inspectorContent}
        </StepSideDrawer>
      )}
      <StepSideDrawer
        isOpen={!!addTarget && canEdit}
        onClose={() => setAddTarget(null)}
        hasContent={!!addTarget && canEdit}
        modal={false}
        label={ts('Add a step')}
        panelId="add-step"
        closeOnPageEscape
      >
        {addTarget && canEdit && (
          <AddStepPanel
            catalog={catalog}
            targetLabel={addLabel}
            recent={recentActions}
            onPick={pickAdd}
            onHoverEntry={setHoverEntry}
            onClose={() => setAddTarget(null)}
          />
        )}
      </StepSideDrawer>
      {launcherHost && (
        <StepSideDrawer
          isOpen={launcherOpen !== null}
          onClose={closeLauncher}
          hasContent={launcherOpen !== null}
          modal={false}
          label={ts('Launcher')}
          panelId="launcher"
          closeOnPageEscape
        >
          {launcherOpen === 'manual' && (
            <ManualLauncherPanel
              params={details?.params ?? []}
              onClose={closeLauncher}
            />
          )}
          {launcherOpen === 'cron' && schedule && (
            <CronLauncherPanel
              schedule={schedule}
              editable={canEdit}
              upcomingRuns={details?.upcomingRuns}
              schedules={details?.schedule}
              onChange={setSchedule}
              onClose={closeLauncher}
            />
          )}
          {launcherOpen === 'webhook' && (
            <WebhookLauncherPanel
              fileName={fileName}
              dagName={dagName}
              onSecretVisibleChange={launcherHost.setSecretVisible}
              onClose={closeLauncher}
            />
          )}
        </StepSideDrawer>
      )}
      {workflowPanel && (
        <WorkflowDrawer
          open={workflowOpen}
          onClose={() => toggleWorkflow(false)}
          bare={workflowPanelBare}
        >
          {workflowPanel}
        </WorkflowDrawer>
      )}
    </div>
  );
}

/** The YAML path of a step in the text: steps, then its index or key. */
function yamlStepPath(
  model: DagDocModel,
  key: StepKey | null
): string[] | null {
  if (key === null) return null;
  const step = model.steps.find((item) => item.key === key);
  if (step) {
    if (model.stepsForm === 'array' && step.index !== null) {
      return ['steps', String(step.index)];
    }
    if (model.stepsForm === 'map' && step.explicitName) {
      return ['steps', step.explicitName];
    }
    return null;
  }
  const handler = model.handlers.find((item) => item.key === key);
  return handler?.handler ? ['handler_on', handler.handler] : null;
}

/** The action id that the action picker shows for a step. */
function actionIdOf(step: StepView | undefined): string | undefined {
  if (!step) return undefined;
  switch (step.kind) {
    case 'action':
    case 'agent':
      return step.action ?? step.kind;
    case 'run':
      return 'run';
    case 'call':
      return 'dag.run';
    case 'router':
      return 'router.route';
    default:
      return step.kind;
  }
}

export default VisualEditor;
