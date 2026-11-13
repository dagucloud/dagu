// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { KeyboardEvent, ReactNode, Ref } from 'react';
import type { components, Status } from '@/api/v1/schema';
import type { RepeatPolicyView } from '@/components/ui/repeat-summary';
import type { JSONSchema } from '@/lib/schema-utils';
import type { TrustSource } from './agentRun/agentRun';
import type { LauncherId, LauncherState } from './launchers/model';
import type { InputProvenance } from './run/inputProvenance';
import type { SourceOption } from './run/runSourceOptions';
import type { SkillsChange, ToolsChange } from './tools/edit';
import type { ToolCategory } from './tools/model';
import type { EditorCustomActionHint } from '@/features/dags/components/dag-editor/customActionSchema';

export type ApiDAGDetails = components['schemas']['DAGDetails'];
export type ApiStep = components['schemas']['Step'];
export type ApiDAGRunDetails = components['schemas']['DAGRunDetails'];
export type ValidationIssue = components['schemas']['ValidationIssue'];
export type ReferenceScope = components['schemas']['ReferenceScope'];
export type StepOutputs = components['schemas']['StepOutputs'];
export type NodeStatus = components['schemas']['NodeStatus'];
export type { JSONSchema, EditorCustomActionHint };

/** "steps[3]" (normalized top-level order, as the server numbers steps) or "handler_on.failure". */
export type StepKey = string;

/** Offsets are UTF-16 indexes into the text, 0-based. Lines and columns are 1-based. */
export interface TextRange {
  from: number;
  to: number;
  startLine: number;
  startColumn: number;
  endLine: number;
  endColumn: number;
}
/** Edits refer to the input text; sorted by `from`; never overlap. */
export interface TextEdit {
  from: number;
  to: number;
  insert: string;
}

export type DagType = 'graph' | 'chain' | 'agent' | 'build';
export type StepsForm = 'array' | 'map' | 'none';
export type StepEntryForm =
  | 'block'
  | 'shorthand'
  | 'parallelGroup'
  | 'mapEntry';
export type StepKind =
  | 'run'
  | 'action'
  | 'call'
  | 'router'
  | 'parallel'
  | 'agent'
  | 'other';
export type HandlerEvent =
  | 'init'
  | 'success'
  | 'failure'
  | 'abort'
  | 'exit'
  | 'wait';

export interface StepView {
  key: StepKey;
  index: number | null; // null for handlers
  handler?: HandlerEvent;
  form: StepEntryForm;
  parallelGroup?: number; // group index when form === 'parallelGroup'
  explicitName?: string; // `name:` value, or the key in map form
  id?: string;
  description?: string;
  kind: StepKind;
  action?: string;
  runPreview?: string; // first line of `run:` (or legacy command/script)
  depends: string[]; // as written; [] when absent
  value: Record<string, unknown>; // plain JS of the entry; shorthand "echo x" gives { run: "echo x" }
  range: TextRange; // the whole entry
  /** The YAML error of an entry that cannot be read. The step is a placeholder: only its name and depends are known, when the text gives them. */
  broken?: string;
  /** A problem of a step that reads: for example a `depends` of the wrong type. */
  problem?: string;
}

export interface DagDocModel {
  text: string;
  /** False when document 0 has YAML syntax errors that leave no readable graph. */
  ok: boolean;
  parseErrors: { message: string; line: number; column: number }[];
  documentCount: number; // only document 0 is edited; later documents are never changed
  dagType: DagType;
  stepsForm: StepsForm;
  steps: StepView[];
  handlers: StepView[];
  /**
   * Set by the lenient parse when entries of `steps` have YAML errors: the
   * text with those entries replaced, with the same offsets as `text`. Code
   * that reads the tree of the text reads this text.
   */
  sanitizedText?: string;
}

export interface NewStep {
  name?: string;
  id?: string;
  run?: string;
  action?: string;
  with?: Record<string, unknown>;
}

export type DagEditOp =
  // atStart: before the first step (ignored with `after` or `insertBetween`); else after `after`, else at the end.
  | {
      kind: 'addStep';
      step: NewStep;
      after?: StepKey | null;
      atStart?: boolean;
      dependsOn?: StepKey[];
      insertBetween?: { from: StepKey; to: StepKey };
    }
  | { kind: 'deleteStep'; step: StepKey; rewire: boolean }
  | { kind: 'renameStep'; step: StepKey; newName: string } // rewrites depends, routes, ${steps.<name|id>...}
  | { kind: 'setStepId'; step: StepKey; id: string | null }
  | { kind: 'addDependency'; step: StepKey; dependsOn: StepKey }
  | { kind: 'removeDependency'; step: StepKey; dependsOn: StepKey }
  | {
      kind: 'setStepField';
      step: StepKey;
      path: (string | number)[];
      value: unknown;
    } // value undefined removes the key
  | { kind: 'replaceStepYaml'; step: StepKey; yaml: string }
  | {
      kind: 'setRouteTargets';
      step: StepKey;
      pattern: string;
      targets: string[];
    }
  | { kind: 'convertStepsToExplicitForm' }
  /** Sets the start expressions of `schedule`; the other entries stay. */
  | { kind: 'setSchedule'; expressions: string[] }
  /** Sets `restart_policy`, `restart_wait_sec` and `hist_retention_runs`. A field that is left out stays; `null` removes the key. */
  | {
      kind: 'setRestartPolicy';
      policy?: 'no' | 'always' | 'unless-stopped';
      waitSec?: number | null;
      histRetentionRuns?: number | null;
    }
  /** Changes the tools of a `chat.completion` or `agent.run` step: `with.dags`, `with.tools` and its limits. */
  | { kind: 'editStepTools'; step: StepKey; change: ToolsChange }
  /** Changes the skills of a `chat.completion` or `agent.run` step: `with.skills`. */
  | { kind: 'editStepSkills'; step: StepKey; change: SkillsChange };

export interface StepNameMap {
  byKey: Record<StepKey, string>;
  byName: Record<string, StepKey>;
}
export interface ApplyEditOptions {
  names?: StepNameMap;
} // server names; used to name an unnamed step before a reference to it

export type EditErrorCode =
  | 'parseError'
  | 'unknownStep'
  | 'unsupportedForm'
  | 'chainType'
  | 'agentType'
  | 'duplicateName'
  | 'invalidName'
  | 'cycle'
  | 'invalidYaml'
  | 'handlerStep'
  | 'restartConflict'
  | 'invalidValue'
  | 'noop';
export type EditResult =
  | { ok: true; text: string; edits: TextEdit[]; select?: StepKey }
  | { ok: false; code: EditErrorCode; message: string };

export interface IssueGroups {
  byStep: Record<StepKey, ValidationIssue[]>;
  dag: ValidationIssue[];
}

export interface TextHistory {
  push(text: string, coalesceKey?: string): void;
  undo(): string | null;
  redo(): string | null;
  canUndo(): boolean;
  canRedo(): boolean;
  reset(text: string): void;
}

/* Run state */
export interface StepRunState {
  status: NodeStatus;
  statusLabel: string;
  startedAt?: string;
  finishedAt?: string;
  retryCount: number;
  doneCount: number;
  error?: string;
  /** The run did not execute the step but reused its outputs: where they came from. */
  reused?: InputProvenance;
  /**
   * The run reused outputs from a source run (`runId`), and the source gave
   * none for this step: it did not succeed there. Set only on a step that
   * the run skipped without carrying outputs.
   */
  reuseFailed?: { runId: string };
}
export type RunOverlay = Record<string /* server step name */, StepRunState>;

/* Action catalog */
export interface ActionCatalogEntry {
  name: string; // "http.request", "run" (shell command), "my-action" (custom)
  namespace: string; // "http", "core", "custom"
  title: string;
  description?: string;
  source: 'core' | 'builtin' | 'custom';
  template: NewStep; // what addStep inserts, e.g. { action: 'http.request', with: { method: 'GET', url: '' } }
}

/* References */
export type ReferenceGroup =
  | 'foreach'
  | 'inputs'
  | 'outputs'
  | 'params'
  | 'consts'
  | 'stepEnv'
  | 'env'
  | 'secrets'
  | 'steps'
  | 'context';
/** What a pick must change for an output to exist: the step, and its id if it has none. */
export interface PublishOnPick {
  step: StepKey;
  id?: string;
}
export interface ReferenceCandidate {
  token: string; // "${steps.fetch.outputs.body}"
  label: string;
  group: ReferenceGroup;
  detail?: string;
  stepName?: string;
  distance?: number; // 1 = direct parent
  known: 'static' | 'runtime';
  /** A step env name that the root env has too: the root value until the step sets it. */
  alsoRoot?: boolean;
  /** The step does not publish this output yet: a pick must make it. */
  publish?: PublishOnPick;
  /** A model wrote this value, in a form that a shell can run as code. */
  modelOutput?: boolean;
  /** The field is read by a shell, and this value is not safe there. */
  shellRisk?: boolean;
  /** Called when the author picks this candidate, before the token is inserted. */
  onPicked?(): void;
}
export interface ReferenceInputProps {
  value: string;
  onChange(value: string): void;
  candidates: ReferenceCandidate[];
  multiline?: boolean;
  /** Visible lines of a multi-line field (default 3); the field is at least this tall. */
  rows?: number;
  /** A multi-line field that the user can make taller by dragging its corner. */
  resizable?: boolean;
  placeholder?: string;
  disabled?: boolean;
  id?: string;
  'aria-label'?: string;
  /**
   * Plain-text suggestions, such as the models of an endpoint. They filter by
   * the text of the field. A pick replaces the whole text. The text stays free:
   * a value that is not in the list is as valid as one that is.
   */
  suggestions?: readonly string[];
  /** The accessible name of the list of `suggestions`. */
  suggestionsLabel?: string;
  /**
   * A multi-line field of free text, such as a prompt: references and the marks
   * of Markdown show in colour while the text is edited.
   */
  highlight?: boolean;
  'aria-invalid'?: boolean;
  onBlur?(): void;
  /** Called for a key that the open list does not use. */
  onKeyDown?(event: KeyboardEvent): void;
}

/* Canvas */
export type CanvasDirection = 'LR' | 'TB';
/**
 * What `continue_on` of a step lets the run do after the step: carry on when
 * the step failed, was skipped, ended with one of the exit codes, or wrote a
 * matching output. `markSuccess` turns that end into a success.
 */
export interface ContinueOnView {
  failure?: true;
  skipped?: true;
  exitCodes?: number[];
  output?: true;
  markSuccess?: true;
}

/**
 * The condition on a link, from a precondition of the step that the link
 * reaches that reads the step that the link leaves. `failed` and `succeeded`
 * come from a test of `${step.exit_code}` against 0. `text` is the
 * precondition as written, for the tooltip.
 */
export interface EdgeCondition {
  when: 'failed' | 'succeeded' | 'exit' | 'match';
  /** The name of the step that the link leaves. */
  step: string;
  /** The exit code (`exit`), or the comparison (`match`). */
  value?: string;
  /** `exit`: the step ended with any other code. */
  negate?: boolean;
  text: string;
}

export interface CanvasNode {
  key: StepKey;
  serverName?: string;
  label: string;
  /** True when `label` is a placeholder: the step cannot be read and has no name. */
  placeholderLabel?: boolean;
  subtitle?: string;
  kind: StepKind;
  lane: 'main' | 'handler';
  action?: string;
  routes?: string[];
  /** Name of the DAG that a `call` step runs, as written. */
  subDag?: string;
  /** True when the step fans out over `parallel` items. */
  parallel?: boolean;
  /** The repeat policy of the step, when it repeats. */
  repeat?: RepeatPolicyView;
  /** What a router step routes on, as written in `value`. */
  routeValue?: string;
  /** The YAML of the step says `untrusted: true`: the Untrusted mark. A default shows none. */
  untrusted?: boolean;
  /** The trust that the YAML writes: `marked` or `opted-out` (the Trusted mark). */
  trust?: TrustSource;
  /** The `continue_on` of the step, when it has one. */
  continueOn?: ContinueOnView;
  /** The preconditions of the step, one line each, when it has some. */
  preconditions?: string[];
  /** The number of tasks of an agent DAG, on its agent node. */
  taskCount?: number;
  issues: { errors: number; warnings: number };
  run?: StepRunState;
  /**
   * The dashed outline of a step that the run reused (`success`) or could not
   * reuse (`failure`): set only on the previous steps of the steps that the
   * run executed.
   */
  reuseOutline?: 'success' | 'failure';
  /** Messages behind the issue counts, in the order of the issues. Absent when the node has no issue. */
  issueMessages?: { errors: string[]; warnings: string[] };
  /** Targets of each route pattern as written in the YAML (router nodes). */
  routeTargets?: Record<string, string[]>;
}
export interface CanvasEdge {
  id: string;
  source: StepKey;
  target: StepKey;
  kind: 'depends' | 'inferred' | 'route' | 'order' | 'agent';
  label?: string;
  /** Set on a `depends` edge that a precondition of its target reads. */
  condition?: EdgeCondition;
}
export interface CanvasGraph {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  editableEdges: boolean;
}
export interface CanvasGraphInput {
  model: DagDocModel;
  names: StepNameMap;
  details?: ApiDAGDetails;
  issues: IssueGroups;
  overlay?: RunOverlay;
}
/**
 * The sections of the form of a step that a sub-node of the canvas opens. The
 * Tools and Skills cards open the dock of their list (`openSlotPanel`), not
 * the form.
 */
export type FormSection = 'model';
/**
 * A tool entry of a step, as the canvas and the editor select it: the list
 * that holds it (`dags` is `with.dags`, `mcp` is `with.tools.mcp`) and its
 * place in it.
 * The built-in tools of an agent step are one entry: `builtin`, place 0.
 */
export interface ToolSelection {
  step: StepKey;
  category: ToolCategory | 'builtin';
  index: number;
}
export type CanvasCommand =
  // Shows the YAML of the step.
  | { kind: 'openInspector'; step: StepKey }
  // Opens the form of the step with one of its sections open and in view:
  // a double click on the Model node, or a click on its add button.
  | { kind: 'openForm'; step: StepKey; section: FormSection }
  // Opens the dock that picks a tool or a skill to add to a chat step or an agent step.
  | { kind: 'openSlotDock'; step: StepKey; slot: 'tools' | 'skills' | 'dags' }
  // Opens the dock that edits the whole list of the DAGs, the Tools or the
  // Skills of a step: a double click, or Enter, on the card of the slot. The
  // DAGs dock lists the DAG tools only, and the Tools dock the MCP servers
  // and the built-in tools.
  | { kind: 'openSlotPanel'; step: StepKey; slot: 'tools' | 'skills' | 'dags' }
  // Opens the enlarged view of the DAG that a DAG tool calls, in the dock.
  | { kind: 'openDagView'; tool: ToolSelection }
  // Opens the dock that edits the system message and the prompt of an agent step.
  | { kind: 'openPromptDock'; step: StepKey }
  // Selects a tool entry and opens its panel at the right.
  | { kind: 'openTool'; tool: ToolSelection }
  | { kind: 'runStep'; step: StepKey }
  | { kind: 'showRun'; step: StepKey }
  | { kind: 'rename'; step: StepKey }
  | { kind: 'showYaml'; step: StepKey }
  // `step` is the node of a node menu; without it the command acts on the
  // selection. A paste with an empty text pastes the steps copied last.
  | { kind: 'copy'; step?: StepKey }
  | { kind: 'paste'; text: string }
  | { kind: 'duplicate'; step?: StepKey }
  | { kind: 'step'; cmd: StepCommand };
/** Where a step that the user picks in the step list goes. */
export type PaletteTarget =
  | { mode: 'after' }
  | { mode: 'child'; source: StepKey }
  | { mode: 'insert'; edgeId: string }
  | { mode: 'route'; router: StepKey; port: string }
  /** A new step beside the target step of the link: same source, nothing after it. */
  | { mode: 'parallel'; edgeId: string }
  | { mode: 'first' };

/** How the ghost step looks: the action choice that the Add step list shows. */
export interface GhostLook {
  title: string;
  /** Action name, shown under the title. */
  action?: string;
  kind: StepKind;
}

export interface VisualCanvasProps {
  /**
   * The document that the graph draws, such as its file. Another document
   * fits its graph at once, as at the first draw, and keeps no view of the
   * last one.
   */
  documentKey?: string;
  graph: CanvasGraph;
  direction: CanvasDirection;
  selected: StepKey | null;
  /** The tool entry whose panel is open: its row on the Tools card is lit. */
  selectedTool?: ToolSelection | null;
  frozen: boolean; // text does not parse: show last graph + banner, no edits
  /**
   * Set while no text has parsed and the graph holds the steps that the
   * server read: the line of the first YAML error, which the banner names.
   */
  unparsedLine?: number;
  readOnly: boolean;
  /** The host shows its own read-only marker, so the toolbar has no chip. */
  hideReadOnlyChip?: boolean;
  catalog: ActionCatalogEntry[];
  onSelect(step: StepKey | null): void;
  onEdit(op: DagEditOp): void;
  onCommand(cmd: CanvasCommand): void;
  onDirectionChange?(direction: CanvasDirection): void;
  /** Controls at the top right of the canvas, such as run, undo and save. */
  toolbarEnd?: ReactNode;
  /**
   * Controls at the top left, before the canvas controls, such as the tabs
   * of the workflows of a file. They bring their own chrome.
   */
  toolbarStart?: ReactNode;
  /** File name, without extension, for the exported image. */
  exportName?: string;
  /** The step that the cursor of the YAML is in: the canvas lights it. */
  highlighted?: StepKey | null;
  /**
   * The user wants to add a step. When it is set, the caller shows the list of
   * steps, and the canvas does not show its own small palette.
   */
  onAddStep?(target: PaletteTarget): void;
  /**
   * The step that the open Add step list would add, drawn as a dashed ghost
   * where it will go. `look` is the choice that the list shows as active, or
   * null for a generic card. The ghost is not in the YAML and not selectable.
   */
  ghost?: { target: PaletteTarget; look: GhostLook | null } | null;
  /** Pan and zoom commands of the canvas, for the host. */
  viewRef?: Ref<CanvasViewHandle>;
  /** The host shows the frozen state itself: no banner, the grey style stays. */
  hideFrozenNotice?: boolean;
  /** Gates of the run items of the step menus. */
  stepCommandGates?: StepCommandGates;
  /** True when a paste has something to paste. */
  canPaste?: boolean;
  /** The host takes Escape: the canvas keeps its selection on Escape. */
  escapeToHost?: boolean;
  /** A connection drag that makes no edit because of a cycle: the host tells the user. */
  onRefused?(code: 'cycle'): void;
  /**
   * The DAG that holds the steps: the Model node reads the endpoints that it
   * may use. Without it the node has no endpoint to offer.
   */
  dag?: { name: string; workspace?: string | undefined };
  /** The launcher nodes to draw as roots of the graph. None: no launcher. */
  launchers?: readonly LauncherState[];
  /** The launcher whose panel is open: its node is lit. */
  activeLauncher?: LauncherId | null;
  /** True while the workflow is alive: the icon of an Always on card turns. */
  launcherAlive?: boolean;
  /** The user opens a launcher node. */
  onLauncher?(id: LauncherId): void;
  /**
   * A pointer click on a step node, after its selection, with the client
   * point of the click. A drag, a key, a click on a control of the card (its
   * toolbar) and a click on a launcher or a ghost are no step click.
   */
  onStepClick?(
    step: StepKey,
    at: Pick<MouseEvent, 'clientX' | 'clientY'>
  ): void;
  /**
   * Margin around the graph when it fits, as a share of the graph's size on
   * each side. A small preview that leaves no room for editing uses less.
   */
  fitPadding?: number;
}

/* Host seams of the visual editor. Absent props keep the classic behaviour. */

/** The run that the host shows. When it is given, the editor reads no run itself. */
export interface RunSource {
  runId: string | null;
  details: ApiDAGRunDetails | null;
}
/** A run command on a step, by server step name. The host confirms `runStep`. */
export type StepCommand =
  | { kind: 'openSubRun'; step: string; newTab?: boolean }
  | { kind: 'setStatus'; step: string }
  | { kind: 'retryStep'; step: string }
  | { kind: 'viewLogs'; step: string; stream?: 'stdout' | 'stderr' }
  /** 'Run step' of a node and of the step view: asks the host to run the step; the host confirms. */
  | { kind: 'runStep'; step: string }
  /** Runs the steps that the step needs, not the step: its input data. */
  | { kind: 'runUpstream'; step: string };

/**
 * A run that the host started from the step view and that the view follows
 * in place: 'upstream' fills the INPUT column, 'execute' fills the OUTPUT
 * column.
 */
export interface FollowedStepRun {
  kind: 'upstream' | 'execute';
  /** Server name of the step that the user edits. */
  step: string;
  runId: string;
  /** Server names of the steps that the run executes, in run order. */
  steps: readonly string[];
  /** Start time, in ms since the epoch. */
  startedAt: number;
  /**
   * The previous steps that the request did not execute, with the source of
   * their outputs, as the front sent them.
   */
  reused?: readonly InputProvenance[];
}
export interface InputRun {
  runId: string | null;
  stale: ReadonlySet<string>;
  missing: ReadonlySet<string>;
  waitingFor: string | null;
  /** Run status and start time, for the header of the column. */
  status?: components['schemas']['Status'];
  startedAt?: string;
  /** Selects the run in the host. The header of the column is a link with it. */
  onSelectRun?(runId: string): void;
  /** The steps that the run in `runId` reused, by step name, with their sources. */
  provenance?: ReadonlyMap<string, InputProvenance>;
  /**
   * The id that the history holds for a source run named by a label (labels
   * are in lower case), or null when the run is not in the history.
   */
  resolveRun?(runId: string): string | null;
  /**
   * The previous steps that the browser cache of outputs covers: the steps
   * with a fresh cached entry, the oldest capture time, and the capture time
   * and the source run of each step.
   */
  cache?: {
    cached: ReadonlySet<string>;
    capturedAt: number | null;
    times: Readonly<Record<string, number>>;
    /** The run that each cached value was read from. */
    runs: Readonly<Record<string, string>>;
  };
  /**
   * The choice of the run that the next step run takes its inputs from. The
   * header of the column shows it as a run picker.
   */
  picker?: InputRunPicker;
}
/** The run picker of the INPUT column: the lines, the one in use, and the choice. */
export interface InputRunPicker {
  options: readonly SourceOption[];
  selectedId: string | null;
  onSelect(option: SourceOption): void;
}
export interface StepCommandGates {
  canRunStep: boolean;
  canRetry(step: string): boolean;
  canSetStatus(step: string): boolean;
  hasSubRuns(step: string): boolean;
}

/** A notice that the editor otherwise shows in its own alert bar. */
export interface EditorNotice {
  kind: 'edit-refused' | 'insert-hint';
  message: string; // already translated
  convertible: boolean; // offers 'Convert steps'
  convert(): void; // applies { kind: 'convertStepsToExplicitForm' }
  dismiss(): void;
}
export interface VisualEditorState {
  frozen: boolean;
  /** As VisualCanvasProps.unparsedLine: the canvas shows the steps that the server read. */
  unparsedLine?: number;
  canUndo: boolean;
  canRedo: boolean;
  selectedStepName: string | null; // server name
  drawer: 'none' | 'yaml' | 'form' | 'add-step' | 'workflow' | 'launcher';
  direction: CanvasDirection;
  /** The launcher whose panel is open; null or absent when none is. */
  launcher?: LauncherId | null;
}
export interface CanvasViewHandle {
  fitView(): void;
  revealStep(key: StepKey): void; // pan only when the node is not in the central zone of the free area; zoom kept
  /** Lights the node of the step, or no node with null. The view does not move. */
  highlightStep(key: StepKey | null): void;
  zoomTo(level: number): void; // 1 = 100 %
  getZoom(): number;
}
export interface VisualEditorHandle {
  fitView(): void;
  /** Select and pan to the step. Returns false when no node has this server name. NDV opening follows openNdvOn. */
  revealStep(serverStepName: string): boolean;
  /**
   * Lights the node of the step without a selection, a pan or a render of
   * the canvas; null, or a name that no node has, lights none.
   */
  highlightStep(serverStepName: string | null): void;
  /** Opens AddStepPanel. No-op when !editable or frozen. Default target: after the selection, else at the end. */
  addStep(target?: PaletteTarget): void;
  openWorkflowPanel(open?: boolean): void; // toggles when open is undefined
  /** Opens the panel of a launcher. No-op without launchers. Cron adds no schedule: the Add step panel does. */
  openLauncher(id: LauncherId): void;
  /** Closes the panel of a launcher, through the guard of its draft. */
  closeLauncher?(): void;
  toggleYaml(open?: boolean): void;
  openForm(serverStepName: string): boolean;
  /** Selects the step and shows its own YAML in the node view. False when no node has this server name. */
  openYaml(serverStepName: string): boolean;
  copySelection(): string | null; // copyStepsYaml of the selected steps
  paste(text: string): EditResult; // pasteStepsOp after the selection, through the edit session (undoable)
  duplicateSelection(): EditResult;
  undo(): void;
  redo(): void;
}
/**
 * The host of the launchers (Manual, Cron, Webhook). With it the canvas draws
 * them as roots of the graph. The webhook panel holds a one-time secret: the
 * host tells whether it shows and guards the close of the panel.
 */
export interface LauncherHost {
  /** Runs `next` at once, or after the user confirms a loss. */
  guard(next: () => void): void;
  setSecretVisible(visible: boolean): void;
  /** True while the workflow is suspended: an Always on card does not turn. */
  suspended?: boolean;
  /**
   * The latest run of the workflow, kept live by the host. The page can show
   * an older run (its URL names it), but the workflow is alive by its latest
   * run. Without it, the shown run stands for it.
   */
  latestRun?: { status: Status; finishedAt?: string } | null;
}
export interface EditorHostProps {
  launcherHost?: LauncherHost;
  /**
   * Shows the part of the YAML of the file that a trigger stands for, such
   * as `schedule:` for Cron, and tells whether the file has one. A click on
   * a trigger uses it while the YAML of the file shows; a trigger with no
   * part opens its panel.
   */
  onRevealTrigger?(id: LauncherId): boolean;
  /** The workflow panel brings its own body and tabs: the drawer adds none. */
  workflowPanelBare?: boolean;
  runSource?: RunSource;
  onRunStarted?(runId: string): void;
  selectedStepName?: string | null; // controlled, server step name
  onSelectedStepNameChange?(name: string | null): void;
  /**
   * A pointer click on a step node, by its server name, after the selection
   * changed, with the client point of the click. A drag, a key or a step
   * with no server name yet calls nothing.
   */
  onStepClick?(name: string, at: Pick<MouseEvent, 'clientX' | 'clientY'>): void;
  openNdvOn?: 'select' | 'open'; // default 'select' (classic)
  renderStepRun?(stepName: string, variant: 'facts'): ReactNode; // replaces StepRunPanel
  /**
   * Replaces the OUTPUT column of the step view (outputColumnRoot) in the
   * editor: `runId` is the run that 'Execute step' started, null before it;
   * `declared` is the declared outputs of the step, for a view without run.
   */
  renderStepOutput?(
    stepName: string,
    ctx: { runId: string | null; declared: ReactNode }
  ): ReactNode;
  inputColumnRoot?: HTMLElement | null; // UpstreamDataPanel portal
  outputColumnRoot?: HTMLElement | null; // renderStepRun portal
  onStepCommand?(cmd: StepCommand): void; // run items hidden in menus when absent
  /**
   * Opening a step (a double click, Enter or Open step) asks the host first:
   * true when the host opened what the step calls, such as a child run or a
   * child workflow. The step view opens otherwise.
   */
  onOpenCall?(step: string): boolean;
  stepCommandGates?: StepCommandGates;
  /**
   * The runs that the step view follows in place: the last 'upstream' run
   * fills the INPUT column, the last 'execute' run the OUTPUT column.
   */
  followedRuns?: readonly FollowedStepRun[];
  /**
   * The run that feeds the step of the INPUT column. The followed 'upstream'
   * run wins over it; without this field the column shows the selected run.
   * `stale` and `missing` are server step names of the previous steps that
   * changed since the run or hold no output in it. `waitingFor` is an active
   * run that the step waits for when `runId` is null.
   */
  inputRun?: InputRun;
  /** escapeToHost: Escape on the canvas goes to the host (an open details view), not to 'clear selection'. */
  chrome?: {
    hideRunBar?: boolean;
    compactNotices?: boolean;
    striped?: boolean;
    escapeToHost?: boolean;
    /** The host has its own read-only marker, such as a lock: no chip in the toolbar. */
    hideReadOnlyChip?: boolean;
  };
  /** With compactNotices or onNotice: VisualEditor renders no alert bar and VisualCanvas no frozen bar. */
  onNotice?(notice: EditorNotice | null): void;
  onStateChange?(s: VisualEditorState): void; // called after commit when a field changed
  handleRef?: Ref<VisualEditorHandle>;
}

/* Inspector */
export interface StepInspectorProps {
  model: DagDocModel;
  step: StepKey;
  serverStep?: ApiStep;
  schema: JSONSchema; // dereferenced DAG schema from SchemaContext
  catalog: ActionCatalogEntry[];
  customActions: EditorCustomActionHint[];
  candidates: ReferenceCandidate[];
  /** The references of a field of the step YAML, by its path below the step entry. */
  candidatesAtPath?(path: string[]): ReferenceCandidate[];
  issues: ValidationIssue[];
  readOnly: boolean;
  onEdit(op: DagEditOp): void;
  onClose(): void;
  /** Shows the YAML of the step instead of its form. The button is hidden without it. */
  onShowYaml?(): void;
  /** Selects a tool entry of the step: its panel opens at the right. */
  onOpenTool?(tool: ToolSelection): void;
  /** Opens the dock that picks a DAG (`dags`) or a tool (`tools`) to add to the step. */
  onAddTool?(slot: 'tools' | 'dags'): void;
  /**
   * A request from the canvas to open a section and put the focus in it: a
   * double click on the Model node. A new `seq` is a new
   * request. The inspector calls `onRevealed` once it has shown the section.
   */
  reveal?: { section: FormSection; seq: number };
  onRevealed?(): void;
  /** The form is in a drawer that has its own bar: no header, no name, no close button. */
  embedded?: boolean;
  /**
   * The DAG that holds the step. The Endpoint choice of an agent step lists
   * the endpoints that this DAG may use (Spec 093). `workspace` is the value
   * of the label `workspace` of the DAG.
   */
  dag?: { name: string; workspace?: string | undefined };
  runPanel?: ReactNode; // P2 passes <StepRunPanel/>
}

/* Run and save */
export interface RunOverlayResult {
  overlay: RunOverlay;
  runId: string | null;
  isLoading: boolean;
}
/** What a step run takes from earlier runs: the browser names runs, never values. */
export interface StepRunInputs {
  /** A finished run whose outputs and work directory the skipped steps carry. */
  reuseOutputsFrom?: string;
  /** The run that each of some skipped steps carries its outputs from, by step name. */
  outputsFromRuns?: Readonly<Record<string, string>>;
  /** Where the outputs of the skipped steps come from, for the run record shown at once. */
  reused?: readonly InputProvenance[];
}
export interface EditorRunActions {
  runDag(): Promise<string | null>; // saved definition
  runDraft(): Promise<string | null>; // unsaved buffer through POST /dag-runs
  runStep(stepName: string, opts?: StepRunInputs): Promise<string | null>;
  /**
   * Runs the named steps in order and no other step. Steps of an unsaved
   * buffer run as an ephemeral run, which leaves no trace.
   */
  runSteps(steps: string[], opts?: StepRunInputs): Promise<string | null>;
  isStarting: boolean;
  error: string | null;
}
export type SaveResult =
  | { kind: 'saved'; sha256: string; errors: string[] }
  | { kind: 'conflict' }
  | { kind: 'error'; message: string };
export interface StoredDraft {
  text: string;
  savedAt: number;
  baseSha256?: string;
}
