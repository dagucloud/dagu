// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { NodeStatus } from '@/api/v1/schema';
import type { CanvasEdge, CanvasNode, GhostLook, StepKey } from '../contracts';
import type { LauncherState, LauncherStatus } from '../launchers/model';
import type { EdgeBundleData } from './bundles';
import type { FlowEdge, FlowNode, Waypoint } from './engine';

/** Node type names of the canvas. */
export type CanvasNodeType =
  | 'step'
  | 'router'
  | 'handler'
  | 'agent'
  | 'tool'
  | 'launcher'
  | 'ghost';

/** Key of the agent node of an agent DAG. It is not a step of the YAML. */
export const AGENT_NODE_KEY = 'agent';

/** Edge type names of the canvas: one for each `CanvasEdge` kind. */
export type CanvasEdgeType = CanvasEdge['kind'] | 'launcher' | 'ghost';

/** Handle of a node that is not a router port. */
export const SOURCE_HANDLE = 'source';
export const TARGET_HANDLE = 'target';

const ROUTE_HANDLE_PREFIX = 'route:';

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
