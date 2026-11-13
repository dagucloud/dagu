// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { NodeStatus } from '@/api/v1/schema';
import type { CanvasEdge, CanvasNode, GhostLook, StepKey } from '../contracts';
import type { LauncherState, LauncherStatus } from '../launchers/model';
import type {
  BuiltinEntryModel,
  StepToolsModel,
  ToolEntryModel,
} from '../tools/model';
import type { EdgeBundleData } from './bundles';
import type { AgentFacts, RetryFacts } from './agentCard';
import type {
  ChatInfo,
  ModelEditData,
  SkillEntry,
  ToolLimits,
} from './chatModel';
import type { FlowEdge, FlowNode, Waypoint } from './engine';
import type { CardSlot, SubRowShape, SubSlot } from './subRow';

/** Node type names of the canvas. */
export type CanvasNodeType =
  | 'step'
  | 'router'
  | 'handler'
  | 'agent'
  | 'tool'
  | 'slotcard'
  | 'dagcard'
  | 'modelsat'
  | 'promptsat'
  | 'addsat'
  | 'launcher'
  | 'ghost';

/** Key of the agent node of an agent DAG. It is not a step of the YAML. */
export const AGENT_NODE_KEY = 'agent';

/** Edge type names of the canvas: one for each `CanvasEdge` kind. */
export type CanvasEdgeType =
  | CanvasEdge['kind']
  | 'launcher'
  | 'ghost'
  | 'toolsat';

/** Handle of a node that is not a router port. */
export const SOURCE_HANDLE = 'source';
export const TARGET_HANDLE = 'target';

const ROUTE_HANDLE_PREFIX = 'route:';
const SLOT_HANDLE_PREFIX = 'slot:';

/** Source handle id of the connector of a slot of a chat step. */
export function slotHandleId(slot: SubSlot): string {
  return SLOT_HANDLE_PREFIX + slot;
}

/** Source handle id of the router port for `pattern`. */
export function routeHandleId(pattern: string): string {
  return ROUTE_HANDLE_PREFIX + pattern;
}

/** Route pattern of a router port handle id, or null for any other handle. */
export function patternFromHandle(
  handleId: string | null | undefined
): string | null {
  if (!handleId || !handleId.startsWith(ROUTE_HANDLE_PREFIX)) {
    return null;
  }
  return handleId.slice(ROUTE_HANDLE_PREFIX.length);
}

/** Status of a task of an agent DAG: the `AgentTask` status of a run. */
export type AgentTaskState = 'open' | 'completed' | 'skipped' | 'failed';

/** A task on the hub of an agent. */
export interface AgentTaskView {
  name: string;
  description?: string;
}

/** What the hub of an agent DAG shows. */
export interface AgentHubData {
  provider?: string;
  model?: string;
  systemFirstLine?: string;
  maxIterations?: number;
  tasks: AgentTaskView[];
}

/** A tool card of an agent DAG: a step that the agent can choose to run. */
export interface ToolCardData {
  description?: string;
}

/**
 * The card of a Tools, Skills or DAGs slot under a chat step: a header with
 * the count, one row for each entry and a row that adds one. The DAGs card
 * has a miniature of the called DAG in each entry. It is not a step and not in
 * the graph of the edits: the canvas adds it for the layout and the drawing
 * only.
 */
export interface SlotCardData {
  /** Key of the chat step that the card belongs to. */
  owner: StepKey;
  slot: CardSlot;
  /** Server name of the chat step, for the facts of the run. */
  stepName?: string;
  /** True while the chat step runs: its catalog is read again. */
  active?: boolean;
  /** The tool entries, in order: the MCP servers on the Tools card, the DAG tools on the DAGs card. */
  entries: ToolEntryModel[];
  /** The built-in tools of an agent step, after the entries. Set on its Tools card. */
  builtin?: BuiltinEntryModel[];
  /** The skills, in order, each with its place in `with.skills`. Set on the Skills card. */
  skills: SkillEntry[];
  /**
   * The limits of the tool loop: the Tools card shows them in a row under its
   * header. A step with DAG tools only has no Tools card: its DAGs card shows them.
   */
  limits?: ToolLimits;
  /** The card has an add row. */
  editable: boolean;
  /**
   * Rows under the header: the layout reads it for the height. On the DAGs
   * card: the rows of the height of a Tools row under its entries (limits,
   * "+N more", add).
   */
  rows: number;
  /** Columns of the Tools card, each one `SLOT_CARD_WIDTH` wide: the layout reads it for the width. One when unset. */
  cols?: number;
}

/**
 * The Prompt card of an agent step: the system message and the prompt as
 * text. The card shows the first characters of each; a click opens the dock
 * that edits both. It is not a step and not in the graph of the edits.
 */
export interface PromptCardData {
  owner: StepKey;
  system: string;
  prompt: string;
  /** The dock can edit the texts. */
  editable: boolean;
}

/**
 * The Model sub-node of a chat step: the models of `with.model`, at least one.
 * `edit` and `card` are set while the canvas can show the controls of the node
 * (it is not a run view): the node is a card with an Endpoint select and a
 * Model field for each model, in a canvas with details. A compact canvas draws
 * the round node of the text.
 */
export interface ModelSatelliteData {
  owner: StepKey;
  models: ChatInfo['models'];
  edit?: ModelEditData;
  /** The size of the card of controls, for the layout. */
  card?: { rows: number; cli: boolean; effort: boolean };
}

/**
 * The "+" sub-node of a slot that has no entry: the dashed one of the Model
 * slot while a canvas with no controls shows a step that names no model, and
 * the small round one of the Tools, Skills or DAGs slot while the step has no
 * tool, no skill or no DAG tool.
 */
export interface AddSatelliteData {
  owner: StepKey;
  slot: SubSlot;
}

/**
 * The agent facts of the run that is shown. They are not in the graph: the
 * graph is what the YAML decides, and the nodes read the run from
 * `AgentRunContext`.
 */
export interface AgentRunInfo {
  /** Turns of the agent: the highest turn of its events. */
  iterations: number;
  /** Calls of each tool, by server step name. */
  calls: Record<string, number>;
  /** Status of each task of the run, by task name. */
  tasks: Record<string, AgentTaskState>;
}

/**
 * Canvas node with the targets of each route pattern as written in the YAML.
 * Gestures need the written targets, so that a target that does not resolve
 * to a step stays in the list when a port changes.
 */
export interface CanvasNodeWithRoutes extends CanvasNode {
  routeTargets?: Record<string, string[]>;
  /** Set on the agent node of an agent DAG. */
  hub?: AgentHubData;
  /** Set on the steps of an agent DAG, which draw as tool cards. */
  tool?: ToolCardData;
  /** Set on a chat step or an agent step that offers tools: its tool entries. */
  tools?: StepToolsModel;
  /** Set on the card of the Tools or Skills slot of a chat step. */
  slotCard?: SlotCardData;
  /** Set on a chat step: the model it names. Its sub-nodes are drawn under it. */
  chat?: ChatInfo;
  /** Set on an agent step: the keys that its card shows besides the model. */
  agent?: AgentFacts;
  /** The retry policy of the step, when it has one: its card draws the loop. */
  retry?: RetryFacts;
  /** Set on a chat step of the drawn graph: the shape of its row of sub-nodes. */
  subRow?: SubRowShape;
  /** Set on the Model sub-node of a chat step. */
  modelSat?: ModelSatelliteData;
  /** Set on the Prompt card of an agent step. */
  promptCard?: PromptCardData;
  /** Set on the dashed "+" sub-node of a chat step. */
  addSat?: AddSatelliteData;
}

/**
 * Canvas edge with the name or id as written in the YAML that gave the edge.
 * Set on `depends` and `route` edges.
 */
export interface CanvasEdgeWithRef extends CanvasEdge {
  written?: string;
}

/** Data of a flow node of the canvas. */
export interface StepNodeData extends Record<string, unknown> {
  node: CanvasNodeWithRoutes;
  /** True while the cursor of the YAML is in the step. */
  highlighted?: boolean;
  /** Set on tool cards while the tool filter has text: does the tool match. */
  filterMatch?: boolean;
  /** True when a "+" stalk can add a child: the step has no outgoing edge. */
  isSink?: boolean;
  /** Router ports with no target, where a "+" stalk can add a step. */
  openPorts?: string[];
  /** Set on a launcher node: how it is drawn. */
  launcher?: LauncherState;
  /** Set on the ghost node of the Add step list: the choice that it shows, or null. */
  ghost?: GhostLook | null;
}

/** Data of a flow edge of the canvas. */
export interface CanvasEdgeData extends Record<string, unknown> {
  edge: CanvasEdgeWithRef;
  /** English i18n source text that tells why the edge cannot be deleted, or null. */
  lockReason: string | null;
  /** True when the "+" button can insert a step on this edge. */
  insertable: boolean;
  /** True on an inferred edge whose explicit link the canvas can write again. */
  restorable?: boolean;
  /** Run status of the source step, when a run is shown. */
  sourceStatus?: NodeStatus;
  /** Run status of the target step, when a run is shown. */
  targetStatus?: NodeStatus;
  /** Set on a launcher edge: the status of its launcher. */
  launcher?: LauncherStatus;
  /**
   * Points that the curve of a long edge crosses, from the layout: the
   * edge goes around the cards of the ranks between its ends.
   */
  waypoints?: Waypoint[];
  /**
   * Set on an edge that shares a trunk with others: the junctions, and the
   * pieces of its path that it draws and where the pointer reaches it.
   */
  bundle?: EdgeBundleData;
}

export type StepFlowNode = FlowNode<StepNodeData, CanvasNodeType>;
export type CanvasFlowEdge = FlowEdge<CanvasEdgeData, CanvasEdgeType>;

/** Top-left positions of the nodes, keyed by step. */
export type NodePositions = Record<StepKey, { x: number; y: number }>;
