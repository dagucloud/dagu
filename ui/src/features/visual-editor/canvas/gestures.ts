// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  ActionCatalogEntry,
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  DagEditOp,
  NewStep,
  PaletteTarget,
  StepKey,
} from '../contracts';
import {
  AGENT_NODE_KEY,
  patternFromHandle,
  type CanvasEdgeWithRef,
  type CanvasNodeWithRoutes,
} from './types';

/** A connection that the user drew from a source handle to a target node. */
export interface CanvasConnection {
  source: string;
  target: string;
  sourceHandle?: string | null;
}

/** The name to write in YAML for a step, or null when the step has no name. */
export function stepRefName(node: CanvasNode): string | null {
  if (node.label !== node.key && !node.placeholderLabel) {
    return node.label;
  }
  return node.serverName ?? null;
}

function findNode(graph: CanvasGraph, key: string): CanvasNode | undefined {
  return graph.nodes.find((node) => node.key === key);
}

function hasEdge(
  graph: CanvasGraph,
  kind: CanvasEdge['kind'],
  source: StepKey,
  target: StepKey
): boolean {
  return graph.edges.some(
    (edge) =>
      edge.kind === kind && edge.source === source && edge.target === target
  );
}

/** True when the connection gives an edit. */
export function isValidConnection(
  graph: CanvasGraph,
  conn: CanvasConnection
): boolean {
  return connectionToOp(graph, conn) !== null;
}

/**
 * English i18n source text that tells why an edge cannot be deleted on the
 * canvas, or null when the edge can be deleted.
 */
export function edgeLockReason(
  graph: CanvasGraph,
  edge: CanvasEdge
): string | null {
  switch (edge.kind) {
    case 'inferred':
      return 'Inferred from a step reference. Remove the reference to remove this edge.';
    case 'order':
      return 'Chain DAGs run steps in YAML order. Move the step in the YAML to change the order.';
    case 'agent':
      return 'The agent chooses which step to run, so the steps have no order.';
    default:
      return graph.editableEdges
        ? null
        : 'Edges of this DAG type cannot be changed on the canvas.';
  }
}

/** Maps the deletion of an edge to an edit, or null for a locked edge. */
export function edgeDeleteOp(
  graph: CanvasGraph,
  edge: CanvasEdgeWithRef
): DagEditOp | null {
  if (edgeLockReason(graph, edge) !== null) {
    return null;
  }
  if (edge.kind === 'depends') {
    return {
      kind: 'removeDependency',
      step: edge.target,
      dependsOn: edge.source,
    };
  }
  if (edge.kind !== 'route' || edge.label === undefined) {
    return null;
  }
  const target = findNode(graph, edge.target);
  const name = edge.written ?? (target ? stepRefName(target) : null);
  if (name === null) {
    return null;
  }
  return {
    kind: 'setRouteTargets',
    step: edge.source,
    pattern: edge.label,
    targets: routeTargetsOf(graph, edge.source, edge.label).filter(
      (written) => written !== name
    ),
  };
}

/** Deletes a step and connects its parents to its children. */
export function stepDeleteOp(step: StepKey): DagEditOp {
  return { kind: 'deleteStep', step, rewire: true };
}

/**
 * Maps the deletion of a selection to edits. Selected steps win over
 * selected edges, because deleting a step also removes its edges.
 */
export function selectionDeleteOps(
  graph: CanvasGraph,
  selection: { nodes: StepKey[]; edgeIds: string[] }
): DagEditOp[] {
  if (selection.nodes.length > 0) {
    return selection.nodes
      .filter((key) => findNode(graph, key))
      .map(stepDeleteOp);
  }
  const ops: DagEditOp[] = [];
  for (const id of selection.edgeIds) {
    const edge = graph.edges.find((candidate) => candidate.id === id);
    const op = edge ? edgeDeleteOp(graph, edge) : null;
    if (op) {
      ops.push(op);
    }
  }
  return ops;
}

/**
 * Targets of a route pattern as written in the YAML. Falls back to the route
 * edges when the node does not carry the written targets.
 */
export function routeTargetsOf(
  graph: CanvasGraph,
  router: StepKey,
  pattern: string
): string[] {
  const node = findNode(graph, router) as CanvasNodeWithRoutes | undefined;
  const written = node?.routeTargets?.[pattern];
  if (written) {
    return [...written];
  }
  const targets: string[] = [];
  for (const edge of graph.edges as CanvasEdgeWithRef[]) {
    if (
      edge.kind !== 'route' ||
      edge.source !== router ||
      edge.label !== pattern
    ) {
      continue;
    }
    const target = findNode(graph, edge.target);
    const name = edge.written ?? (target ? stepRefName(target) : null);
    if (name !== null) {
      targets.push(name);
    }
  }
  return targets;
}

function routeConnectionToOp(
  graph: CanvasGraph,
  source: CanvasNode,
  target: CanvasNode,
  pattern: string
): DagEditOp | null {
  const isRouter = source.kind === 'router' || !!source.routes?.length;
  if (!isRouter) {
    return null;
  }
  const reached = graph.edges.some(
    (edge) =>
      edge.kind === 'route' &&
      edge.source === source.key &&
      edge.target === target.key &&
      edge.label === pattern
  );
  const name = stepRefName(target);
  if (
    reached ||
    name === null ||
    wouldCreateCycle(graph, source.key, target.key)
  ) {
    return null;
  }
  return {
    kind: 'setRouteTargets',
    step: source.key,
    pattern,
    targets: [...routeTargetsOf(graph, source.key, pattern), name],
  };
}

/**
 * Tells if an edge from `source` to `target` closes a cycle: `source` is
 * reachable from `target` through edges of any kind.
 */
export function wouldCreateCycle(
  graph: CanvasGraph,
  source: StepKey,
  target: StepKey
): boolean {
  const children = new Map<StepKey, StepKey[]>();
  for (const edge of graph.edges) {
    const list = children.get(edge.source);
    if (list) {
      list.push(edge.target);
    } else {
      children.set(edge.source, [edge.target]);
    }
  }
  const seen = new Set<StepKey>([target]);
  const queue: StepKey[] = [target];
  while (queue.length > 0) {
    const current = queue.pop() as StepKey;
    if (current === source) {
      return true;
    }
    for (const child of children.get(current) ?? []) {
      if (!seen.has(child)) {
        seen.add(child);
        queue.push(child);
      }
    }
  }
  return false;
}

/**
 * Maps a connection to an edit. The target step gets a dependency on the
 * source step. Returns null when the connection must not change the YAML.
 */
export function connectionToOp(
  graph: CanvasGraph,
  conn: CanvasConnection
): DagEditOp | null {
  if (!graph.editableEdges || conn.source === conn.target) {
    return null;
  }
  const source = findNode(graph, conn.source);
  const target = findNode(graph, conn.target);
  if (!source || !target) {
    return null;
  }
  if (source.lane === 'handler' || target.lane === 'handler') {
    return null;
  }
  const pattern = patternFromHandle(conn.sourceHandle);
  if (pattern !== null) {
    return routeConnectionToOp(graph, source, target, pattern);
  }
  if (
    hasEdge(graph, 'depends', source.key, target.key) ||
    wouldCreateCycle(graph, source.key, target.key)
  ) {
    return null;
  }
  return { kind: 'addDependency', step: target.key, dependsOn: source.key };
}

/**
 * True when the "+" button of the edge can insert a step on it, and the split
 * button can add a step beside its target. An inferred edge counts: the
 * dependency comes from a reference, and the new step goes in parallel or
 * between the two ends without touching that reference.
 */
export function canInsertOnEdge(graph: CanvasGraph, edge: CanvasEdge): boolean {
  return (
    (edge.kind === 'depends' || edge.kind === 'inferred') && graph.editableEdges
  );
}

// The template belongs to the catalog: the edit gets its own copy.
function newStep(entry: ActionCatalogEntry): NewStep {
  return structuredClone(entry.template);
}

/** Adds a step from the palette after the selected step. */
export function paletteAddOp(
  entry: ActionCatalogEntry,
  selected: StepKey | null
): DagEditOp {
  return { kind: 'addStep', step: newStep(entry), after: selected };
}

/** The end of a connection drag, as the flow reports it. */
export interface ConnectionEnd {
  isValid: boolean | null;
  fromNode: { id: string } | null;
  fromHandle: { type: 'source' | 'target'; id?: string | null } | null;
  toNode: { id: string } | null;
}

/**
 * 'cycle' when a connection drag ended on a step handle and was refused
 * because it links a step to itself or closes a cycle, else null. The caller
 * tells the user, as the drop made no edit.
 */
export function refusedConnection(
  graph: CanvasGraph,
  end: ConnectionEnd
): 'cycle' | null {
  if (!graph.editableEdges || end.isValid !== false) return null;
  if (!end.fromNode || !end.fromHandle || !end.toNode) return null;
  const fromSource = end.fromHandle.type === 'source';
  const source = findNode(graph, fromSource ? end.fromNode.id : end.toNode.id);
  const target = findNode(graph, fromSource ? end.toNode.id : end.fromNode.id);
  if (!source || !target) return null;
  if (source.lane !== 'main' || target.lane !== 'main') return null;
  if (source.key === target.key) return 'cycle';
  if (hasEdge(graph, 'depends', source.key, target.key)) return null;
  return wouldCreateCycle(graph, source.key, target.key) ? 'cycle' : null;
}

/**
 * The step to give a new child when a connection from its source handle is
 * dropped on empty space, or null for any other drop.
 */
export function droppedConnectionSource(
  graph: CanvasGraph,
  end: ConnectionEnd
): StepKey | null {
  if (!graph.editableEdges || end.isValid || end.toNode) {
    return null;
  }
  if (!end.fromNode || end.fromHandle?.type !== 'source') {
    return null;
  }
  const source = findNode(graph, end.fromNode.id);
  return source && source.lane === 'main' ? source.key : null;
}

/** Adds a step that depends on the step where a connection was dropped. */
export function dropAddOp(
  entry: ActionCatalogEntry,
  source: StepKey
): DagEditOp {
  return { kind: 'addStep', step: newStep(entry), dependsOn: [source] };
}

/**
 * Adds a step beside the target of a dependency edge: it depends on the
 * source of the edge only, so it runs in parallel with the target. The step
 * goes after the target in the YAML.
 */
export function parallelOnEdgeOp(
  entry: ActionCatalogEntry,
  edge: CanvasEdge
): DagEditOp {
  return {
    kind: 'addStep',
    step: newStep(entry),
    after: edge.target,
    dependsOn: [edge.source],
  };
}

/** Adds a step between the two ends of a dependency edge. */
export function insertOnEdgeOp(
  entry: ActionCatalogEntry,
  edge: CanvasEdge
): DagEditOp {
  return {
    kind: 'addStep',
    step: newStep(entry),
    insertBetween: { from: edge.source, to: edge.target },
  };
}

/**
 * The last step of the main lane that has no edge going out, or null when the
 * graph has no editable edges (a chain runs in order) or no step.
 */
export function lastLeafOf(graph: CanvasGraph): StepKey | null {
  if (!graph.editableEdges) return null;
  const sources = new Set(graph.edges.map((edge) => edge.source));
  const leaves = graph.nodes.filter(
    (node) =>
      node.lane === 'main' &&
      node.key !== AGENT_NODE_KEY &&
      !sources.has(node.key)
  );
  return leaves[leaves.length - 1]?.key ?? null;
}

/**
 * The edit that a pick in the list of steps makes. `selected` is the step
 * that is selected on the canvas, for a target after it. The agent node is no
 * step: a step added after it goes at the end. In a graph with edges, the
 * new step depends on the selected step, or at the end on the last step that
 * nothing depends on, as the label 'After <step>' says. A route target gives a step that depends on
 * the router.
 */
export function paletteTargetOp(
  graph: CanvasGraph,
  selected: StepKey | null,
  target: PaletteTarget,
  entry: ActionCatalogEntry
): DagEditOp | null {
  switch (target.mode) {
    case 'after': {
      const after = selected === AGENT_NODE_KEY ? null : selected;
      const leaf = after === null ? lastLeafOf(graph) : null;
      if (leaf !== null) return dropAddOp(entry, leaf);
      // A step added after a main-lane step of a graph with edges also
      // depends on it, else it would float as an unconnected root.
      const source = after === null ? null : findNode(graph, after);
      if (after !== null && graph.editableEdges && source?.lane === 'main') {
        return {
          kind: 'addStep',
          step: newStep(entry),
          after,
          dependsOn: [after],
        };
      }
      return paletteAddOp(entry, after);
    }
    case 'child':
      return dropAddOp(entry, target.source);
    case 'insert': {
      const edge = graph.edges.find((e) => e.id === target.edgeId);
      return edge ? insertOnEdgeOp(entry, edge) : null;
    }
    case 'parallel': {
      const edge = graph.edges.find((e) => e.id === target.edgeId);
      return edge && canInsertOnEdge(graph, edge)
        ? parallelOnEdgeOp(entry, edge)
        : null;
    }
    // The route to the new step is written by the caller, after the add.
    case 'route':
      return dropAddOp(entry, target.router);
    case 'first':
      return {
        kind: 'addStep',
        step: newStep(entry),
        after: null,
        atStart: true,
      };
  }
}
