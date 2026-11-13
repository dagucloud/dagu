// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { RestartPolicy } from '../doc/ops/restart';
import type {
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  StepKey,
} from '../contracts';

/**
 * The ways to start a workflow. They are not steps. Always on stands for the
 * `restart_policy` of the YAML. Schedule is the placeholder of the slot that
 * Cron and Always on share, while neither is set.
 */
export type LauncherId = 'manual' | 'schedule' | 'cron' | 'always' | 'webhook';

export const LAUNCHER_IDS: readonly LauncherId[] = [
  'manual',
  'schedule',
  'cron',
  'always',
  'webhook',
];

/** The launchers that share the slot of the schedule. */
export const SLOT_IDS: readonly LauncherId[] = ['schedule', 'cron', 'always'];

/** 'active' draws solid. 'dashed' means: not set up yet. */
export type LauncherStatus = 'active' | 'dashed';

export interface LauncherState {
  id: LauncherId;
  status: LauncherStatus;
  /** Set on Cron: the number of cron expressions of the schedule. */
  count?: number;
  /**
   * Set on an active Always on: the policy, the wait in seconds and the runs
   * kept, each null when the file has none.
   */
  policy?: Exclude<RestartPolicy, 'no'>;
  waitSec?: number | null;
  retentionRuns?: number | null;
}

/** The runs that the history keeps for a service DAG with no retention (spec 080). */
export const DEFAULT_RETENTION_RUNS = 100;

export interface LauncherInputs {
  /** Number of schedule entries in the YAML. */
  scheduleCount: number;
  /** True when the YAML has a schedule of any form. The default is `scheduleCount > 0`. */
  schedulePresent?: boolean;
  /** The `restart_policy` of the YAML. The default is `no`. */
  restartPolicy?: RestartPolicy;
  /** The `restart_wait_sec` of the YAML. */
  restartWaitSec?: number | null;
  /** The `hist_retention_runs` of the YAML. */
  restartRetentionRuns?: number | null;
  /**
   * Number of webhooks of the workflow, or null while it is not known (the
   * request is running or failed).
   */
  webhookCount: number | null;
  /** False when the instance has no webhooks: the node is not drawn. */
  webhooksAvailable: boolean;
}

/**
 * The launcher nodes to draw, in order. Manual is always active. Cron and
 * Always on share one slot, because a restart policy cannot go with a
 * schedule (spec 080): a policy draws Always on in place of Cron, a schedule
 * draws Cron alone, and neither draws one dashed Schedule placeholder. A file
 * with both is invalid: both are drawn solid, so that the conflict shows. Webhook is
 * active with at least one webhook, and is absent when the instance has no
 * webhooks.
 */
export function launcherStates(input: LauncherInputs): LauncherState[] {
  const states: LauncherState[] = [{ id: 'manual', status: 'active' }];
  const policy = input.restartPolicy ?? 'no';
  const scheduled = input.schedulePresent ?? input.scheduleCount > 0;
  if (policy === 'no' && !scheduled) {
    states.push({ id: 'schedule', status: 'dashed' });
  }
  if (scheduled) {
    states.push({ id: 'cron', status: 'active', count: input.scheduleCount });
  }
  if (policy !== 'no') {
    states.push({
      id: 'always',
      status: 'active',
      policy,
      waitSec: input.restartWaitSec ?? null,
      retentionRuns: input.restartRetentionRuns ?? null,
    });
  }
  if (input.webhooksAvailable) {
    states.push({
      id: 'webhook',
      status: (input.webhookCount ?? 0) > 0 ? 'active' : 'dashed',
    });
  }
  return states;
}

/**
 * The launcher that an open panel belongs to. A node of the slot changes its
 * id when the author sets or removes a value (Schedule, Cron, Always on):
 * the panel stays open and follows the node that is drawn.
 */
export function drawnLauncher(
  open: LauncherId | null,
  states: readonly LauncherState[] | undefined
): LauncherId | null {
  if (open === null || !SLOT_IDS.includes(open) || !states) return open;
  if (states.some((state) => state.id === open)) return open;
  return states.find((state) => SLOT_IDS.includes(state.id))?.id ?? open;
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
