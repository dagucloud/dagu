// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  CanvasDirection,
  CanvasGraph,
  CanvasNode,
  GhostLook,
  StepKey,
} from '../contracts';
import {
  isLauncherKey,
  type LauncherGraph,
  type LauncherState,
} from '../launchers/model';
import { assignPieces, type EdgeBundleRef } from './bundles';
import { isGhostKey, type GhostPlan } from './ghost';
import { edgeTintStatus } from './statusStyle';
import { canInsertOnEdge, connectionToOp, edgeLockReason } from './gestures';
import { nodeSize } from './layout';
import {
  routeHandleId,
  type CanvasNodeWithRoutes,
  SOURCE_HANDLE,
  TARGET_HANDLE,
  type CanvasFlowEdge,
  type CanvasNodeType,
  type NodePositions,
  type StepFlowNode,
} from './types';

/** Flow node type of a canvas node. */
export function flowNodeType(node: CanvasNode): CanvasNodeType {
  if (isGhostKey(node.key)) {
    return 'ghost';
  }
  if (isLauncherKey(node.key)) {
    return 'launcher';
  }
  if (node.kind === 'agent') {
    return 'agent';
  }
  if ((node as CanvasNodeWithRoutes).tool && node.lane === 'main') {
    return 'tool';
  }
  if (node.lane === 'handler') {
    return 'handler';
  }
  if (node.kind === 'router' || (node.routes?.length ?? 0) > 0) {
    return 'router';
  }
  return 'step';
}

/**
 * Where a "+" stalk of the node can add a step. Only a main step of a graph
 * whose edges the canvas can change gets stalks.
 */
function stalksOf(
  graph: CanvasGraph,
  node: CanvasNode,
  sources: Set<StepKey>
): { isSink: boolean; openPorts: string[] } {
  if (!graph.editableEdges || node.lane !== 'main' || node.kind === 'agent') {
    return { isSink: false, openPorts: [] };
  }
  return {
    isSink: !sources.has(node.key),
    // A written target that names no step still fills its port.
    openPorts: (node.routes ?? []).filter(
      (pattern) =>
        (node.routeTargets?.[pattern]?.length ?? 0) === 0 &&
        !graph.edges.some(
          (edge) =>
            edge.kind === 'route' &&
            edge.source === node.key &&
            edge.label === pattern
        )
    ),
  };
}

/** True when the name or the id of a tool contains the lower-case text. */
export function toolMatches(node: CanvasNode, needle: string): boolean {
  return [node.label, node.serverName].some((name) =>
    name?.toLowerCase().includes(needle)
  );
}

/**
 * Maps the canvas nodes to flow nodes. The size of a node is the one that
 * the layout uses.
 */
export function toFlowNodes(
  graph: CanvasGraph,
  positions: NodePositions,
  selected: StepKey | null,
  highlighted: StepKey | null = null,
  toolFilter = '',
  compact = false,
  direction: CanvasDirection = 'TB'
): StepFlowNode[] {
  const needle = toolFilter.trim().toLowerCase();
  const sources = new Set(graph.edges.map((edge) => edge.source));
  return graph.nodes.map((node) => ({
    id: node.key,
    type: flowNodeType(node),
    position: positions[node.key] ?? { x: 0, y: 0 },
    ...nodeSize(node, compact, direction),
    data: {
      node,
      highlighted: node.key === highlighted,
      ...(needle !== '' && (node as CanvasNodeWithRoutes).tool
        ? { filterMatch: toolMatches(node, needle) }
        : {}),
      ...stalksOf(graph, node, sources),
    },
    selected: node.key === selected,
  }));
}

/**
 * Maps the canvas edges to flow edges. Route edges leave from their port. An
 * edge carries the run status of its source and target steps, when a run is shown. The
 * edges from the agent to its tools are not drawn: the grid under the hub
 * says it, and a hundred lines would hide the cards.
 */
export function toFlowEdges(
  graph: CanvasGraph,
  opts: { editable: boolean; selectedEdgeId: string | null }
): CanvasFlowEdge[] {
  const runs = new Map(graph.nodes.map((node) => [node.key, node.run]));
  return graph.edges
    .filter((edge) => edge.kind !== 'agent')
    .map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      type: edge.kind,
      sourceHandle:
        edge.kind === 'route' && edge.label !== undefined
          ? routeHandleId(edge.label)
          : SOURCE_HANDLE,
      targetHandle: TARGET_HANDLE,
      data: {
        edge,
        lockReason: edgeLockReason(graph, edge),
        insertable: opts.editable && canInsertOnEdge(graph, edge),
        restorable:
          opts.editable &&
          edge.kind === 'inferred' &&
          connectionToOp(graph, {
            source: edge.source,
            target: edge.target,
          }) !== null,
        sourceStatus: runs.get(edge.source)?.status,
        targetStatus: runs.get(edge.target)?.status,
      },
      selected: edge.id === opts.selectedEdgeId,
    }));
}

/**
 * The edges with the waypoints of the layout: the points that the curve of a
 * long edge crosses. An edge without waypoints keeps its data as it is.
 */
export function withWaypoints(
  edges: CanvasFlowEdge[],
  waypoints: Record<string, { x: number; y: number }[]>
): CanvasFlowEdge[] {
  return edges.map((edge) => {
    const way = waypoints[edge.id];
    return way ? { ...edge, data: { ...edge.data, waypoints: way } } : edge;
  });
}

/**
 * The edges with their bundle: the junction that they share, the pieces of
 * their path that they draw, and the piece where the pointer reaches them.
 * It reads the edges that are still in the bundle: an edge that the user
 * moved away from its bundle has no entry, and the others stay. The edges of
 * a launcher draw each shared piece once. A piece takes a run status only
 * when all the edges that use it have the same one.
 */
export function withBundles(
  edges: CanvasFlowEdge[],
  bundles: Record<string, EdgeBundleRef>
): CanvasFlowEdge[] {
  const groups = new Map<string, CanvasFlowEdge[]>();
  for (const edge of edges) {
    const ref = bundles[edge.id];
    if (ref) {
      const list = groups.get(ref.id);
      if (list) {
        list.push(edge);
      } else {
        groups.set(ref.id, [edge]);
      }
    }
  }
  if (groups.size === 0) {
    return edges;
  }
  const tint = (edge: CanvasFlowEdge) =>
    edgeTintStatus(edge.data?.sourceStatus, edge.data?.targetStatus);
  const common = (list: CanvasFlowEdge[]) => {
    const first = list[0] ? tint(list[0]) : undefined;
    return list.every((edge) => tint(edge) === first) ? first : undefined;
  };
  const bundled = new Map<string, CanvasFlowEdge>();
  for (const list of groups.values()) {
    const pieces = assignPieces(list);
    const firstOf = (key: 'source' | 'target') => {
      const seen = new Set<string>();
      const firsts = new Set<string>();
      for (const edge of list) {
        if (!seen.has(edge[key])) {
          seen.add(edge[key]);
          firsts.add(edge.id);
        }
      }
      return firsts;
    };
    const firstOfSource = firstOf('source');
    const firstOfTarget = firstOf('target');
    const trunkStatus = common(list);
    for (const edge of list) {
      const ref = bundles[edge.id]!;
      const launcher = ref.kind === 'launcher';
      bundled.set(edge.id, {
        ...edge,
        data: {
          ...edge.data!,
          bundle: {
            ...ref,
            piece: launcher ? null : (pieces.get(edge.id) ?? null),
            draws: {
              in: !launcher || firstOfSource.has(edge.id),
              trunk: !launcher || list[0]!.id === edge.id,
              out: !launcher || firstOfTarget.has(edge.id),
            },
            inStatus: common(
              list.filter((other) => other.source === edge.source)
            ),
            trunkStatus,
            outStatus: common(
              list.filter((other) => other.target === edge.target)
            ),
          },
        },
      });
    }
  }
  return edges.map((edge) => bundled.get(edge.id) ?? edge);
}

/**
 * Flow nodes of the launchers: the nodes that `toFlowNodes` made from the
 * graph with launchers get the state that draws them.
 */
export function withLauncherStates(
  nodes: StepFlowNode[],
  launchers: readonly LauncherState[],
  activeKey: string | null
): StepFlowNode[] {
  const byKey = new Map(
    launchers.map((state) => [`launcher:${state.id}`, state])
  );
  return nodes.map((node) => {
    const state = byKey.get(node.id);
    return state
      ? {
          ...node,
          data: { ...node.data, launcher: state },
          selected: node.id === activeKey,
        }
      : node;
  });
}

/** Flow edges from the launchers to the start steps: not editable, not selectable. */
export function launcherFlowEdges(
  launched: LauncherGraph,
  launchers: readonly LauncherState[]
): CanvasFlowEdge[] {
  const status = new Map(launchers.map((state) => [state.id, state.status]));
  return launched.edges.map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    type: 'launcher',
    sourceHandle: SOURCE_HANDLE,
    targetHandle: TARGET_HANDLE,
    data: {
      edge: {
        id: edge.id,
        source: edge.source,
        target: edge.target,
        kind: 'order',
      },
      lockReason: null,
      insertable: false,
      launcher: status.get(edge.launcher),
    },
    selected: false,
  }));
}

/** The ghost node shows the choice of the Add step list: the others stay. */
export function withGhostLook(
  nodes: StepFlowNode[],
  look: GhostLook | null
): StepFlowNode[] {
  return nodes.map((node) =>
    isGhostKey(node.id)
      ? { ...node, data: { ...node.data, ghost: look } }
      : node
  );
}

/**
 * Flow edges of the ghost: dashed, not selectable, not in the YAML. An edge
 * from a router port leaves from that port.
 */
export function ghostFlowEdges(plan: GhostPlan | null): CanvasFlowEdge[] {
  return (plan?.edges ?? []).map((edge) => ({
    id: edge.id,
    source: edge.source,
    target: edge.target,
    type: 'ghost',
    sourceHandle:
      edge.port === undefined ? SOURCE_HANDLE : routeHandleId(edge.port),
    targetHandle: TARGET_HANDLE,
    data: {
      edge: {
        id: edge.id,
        source: edge.source,
        target: edge.target,
        kind: 'order',
      },
      lockReason: null,
      insertable: false,
    },
    selected: false,
  }));
}
