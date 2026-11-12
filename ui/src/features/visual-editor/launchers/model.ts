// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  StepKey,
} from '../contracts';

/** The three ways to start a workflow. They are not steps and not in the YAML. */
export type LauncherId = 'manual' | 'cron' | 'webhook';

export const LAUNCHER_IDS: readonly LauncherId[] = [
  'manual',
  'cron',
  'webhook',
];

/** 'active' draws solid. 'dashed' means: not set up yet. */
export type LauncherStatus = 'active' | 'dashed';

export interface LauncherState {
  id: LauncherId;
  status: LauncherStatus;
  /** Set on Cron: the number of cron expressions of the schedule. */
  count?: number;
}

export interface LauncherInputs {
  /** Number of schedule entries in the YAML. */
  scheduleCount: number;
  /**
   * Number of webhooks of the workflow, or null while it is not known (the
   * request is running or failed).
   */
  webhookCount: number | null;
  /** False when the instance has no webhooks: the node is not drawn. */
  webhooksAvailable: boolean;
}

/**
 * The launcher nodes to draw, in order. Manual is always active. Cron is
 * active with a schedule. Webhook is active with at least one webhook, and is
 * absent when the instance has no webhooks.
 */
export function launcherStates(input: LauncherInputs): LauncherState[] {
  const states: LauncherState[] = [{ id: 'manual', status: 'active' }];
  states.push({
    id: 'cron',
    status: input.scheduleCount > 0 ? 'active' : 'dashed',
    count: input.scheduleCount,
  });
  if (input.webhooksAvailable) {
    states.push({
      id: 'webhook',
      status: (input.webhookCount ?? 0) > 0 ? 'active' : 'dashed',
    });
  }
  return states;
}

/** True when the node key is a launcher node, not a step. */
export function isLauncherKey(key: string): boolean {
  return key.startsWith(LAUNCHER_PREFIX);
}

const LAUNCHER_PREFIX = 'launcher:';

export function launcherKey(id: LauncherId): string {
  return LAUNCHER_PREFIX + id;
}

export function launcherIdOf(key: string): LauncherId | null {
  const id = key.startsWith(LAUNCHER_PREFIX)
    ? key.slice(LAUNCHER_PREFIX.length)
    : '';
  return LAUNCHER_IDS.find((candidate) => candidate === id) ?? null;
}

/**
 * The start steps of a graph: main-lane steps that no edge reaches. The
 * agent hub of an agent DAG is not a start step. An agent graph has none,
 * and so no launchers.
 */
export function startSteps(graph: CanvasGraph): StepKey[] {
  if (graph.nodes.some((node) => node.kind === 'agent')) return [];
  const targets = new Set(graph.edges.map((edge) => edge.target));
  return graph.nodes
    .filter((node) => node.lane === 'main' && !targets.has(node.key))
    .map((node) => node.key);
}

/** A launcher is drawn as a node that a step cannot be: this places it in the layout. */
function launcherNode(state: LauncherState): CanvasNode {
  return {
    key: launcherKey(state.id),
    label: state.id,
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
  };
}

/** The edge from a launcher to a start step. It is not an edit target. */
export function launcherEdgeId(id: LauncherId, step: StepKey): string {
  return `${launcherKey(id)}>${step}`;
}

export interface LauncherGraph {
  /** The graph with the launcher nodes and their edges, for the layout. */
  graph: CanvasGraph;
  /** The launcher edges alone, by launcher. */
  edges: Array<{
    id: string;
    source: string;
    target: StepKey;
    launcher: LauncherId;
  }>;
}

/**
 * Adds the launchers to a graph as roots: each launcher has an edge to every
 * start step. The result is for the layout and the drawing only. It is the
 * same graph when there is no launcher or no start step.
 */
export function withLaunchers(
  graph: CanvasGraph,
  launchers: readonly LauncherState[]
): LauncherGraph {
  const starts = startSteps(graph);
  if (launchers.length === 0 || starts.length === 0) {
    return { graph, edges: [] };
  }
  const edges = launchers.flatMap((state) =>
    starts.map((step) => ({
      id: launcherEdgeId(state.id, step),
      source: launcherKey(state.id),
      target: step,
      launcher: state.id,
    }))
  );
  const layoutEdges: CanvasEdge[] = edges.map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    kind: 'order',
  }));
  return {
    graph: {
      ...graph,
      // The launchers come first, so that they are the first of a rank.
      nodes: [...launchers.map(launcherNode), ...graph.nodes],
      edges: [...graph.edges, ...layoutEdges],
    },
    edges,
  };
}
