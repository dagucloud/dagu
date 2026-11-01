// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  ActionCatalogEntry,
  CanvasEdge,
  CanvasGraph,
  CanvasNode,
  GhostLook,
  PaletteTarget,
  StepKey,
} from '../contracts';
import { canInsertOnEdge, lastLeafOf } from './gestures';
import { AGENT_NODE_KEY } from './types';

/** Key of the ghost node. It is not a step of the YAML. */
export const GHOST_KEY = 'ghost:new';

/** True when the node key is the ghost node. */
export function isGhostKey(key: string): boolean {
  return key === GHOST_KEY;
}

/** An edge from or to the ghost. `port` is the route port of a router source. */
export interface GhostEdge {
  id: string;
  source: string;
  target: string;
  port?: string;
}

export interface GhostPlan {
  /**
   * The graph with the ghost in it, for the layout and the drawing only. The
   * edge that the new step replaces is not in it.
   */
  graph: CanvasGraph;
  /** The edges of the ghost, as they will be after the add. */
  edges: GhostEdge[];
  /** Ids of the edges of the graph that the drawing must hide. */
  hidden: string[];
}

/** How the ghost looks for a choice of the Add step list. */
export function ghostLookOf(entry: ActionCatalogEntry): GhostLook {
  const kind =
    entry.name === 'run'
      ? 'run'
      : entry.name === 'router.route'
        ? 'router'
        : entry.name === 'dag.run'
          ? 'call'
          : 'action';
  return { title: entry.title, action: entry.name, kind };
}

function ghostNode(): CanvasNode {
  return {
    key: GHOST_KEY,
    label: 'New step',
    kind: 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
  };
}

interface Place {
  sources: Array<{ key: StepKey; port?: string }>;
  targets: StepKey[];
  hidden: string[];
  /** Where the ghost goes among the nodes: the order breaks ties in a rank. */
  at: 'end' | 'start' | { after: StepKey };
}

function mainNode(graph: CanvasGraph, key: StepKey): CanvasNode | undefined {
  return graph.nodes.find((node) => node.key === key && node.lane === 'main');
}

/** The steps of a chain that nothing follows, or that nothing precedes. */
function chainEnds(graph: CanvasGraph, end: 'last' | 'first'): StepKey[] {
  const linked = new Set(
    graph.edges
      .filter((edge) => edge.kind === 'order')
      .map((edge) => (end === 'last' ? edge.source : edge.target))
  );
  return graph.nodes
    .filter((node) => node.lane === 'main' && !linked.has(node.key))
    .map((node) => node.key);
}

/** Where the new step goes in the graph, as `paletteTargetOp` makes it. */
function placeOf(
  graph: CanvasGraph,
  selected: StepKey | null,
  target: PaletteTarget
): Place | null {
  const none: Place = { sources: [], targets: [], hidden: [], at: 'end' };
  switch (target.mode) {
    case 'after': {
      const after = selected === AGENT_NODE_KEY ? null : selected;
      if (after === null) {
        const leaf = lastLeafOf(graph);
        if (leaf !== null) return { ...none, sources: [{ key: leaf }] };
        // A chain runs in order: the new step follows the last one.
        const last = graph.editableEdges ? [] : chainEnds(graph, 'last');
        return {
          ...none,
          sources: last.slice(-1).map((key) => ({ key })),
        };
      }
      if (!mainNode(graph, after)) return none;
      if (graph.editableEdges) {
        return { ...none, sources: [{ key: after }], at: { after } };
      }
      // In a chain, the new step takes the place between the step and the
      // steps that follow it.
      const next = graph.edges.filter(
        (edge) => edge.kind === 'order' && edge.source === after
      );
      return {
        sources: [{ key: after }],
        targets: next.map((edge) => edge.target),
        hidden: next.map((edge) => edge.id),
        at: { after },
      };
    }
    // The edit writes a dependency: a chain refuses it, so it has no ghost.
    case 'child':
      return graph.editableEdges && mainNode(graph, target.source)
        ? { ...none, sources: [{ key: target.source }] }
        : null;
    case 'route':
      return graph.editableEdges && mainNode(graph, target.router)
        ? {
            ...none,
            sources: [{ key: target.router, port: target.port }],
          }
        : null;
    case 'insert': {
      const edge = graph.edges.find((e) => e.id === target.edgeId);
      return edge && canInsertOnEdge(graph, edge)
        ? {
            sources: [{ key: edge.source }],
            targets: [edge.target],
            hidden: [edge.id],
            at: { after: edge.source },
          }
        : null;
    }
    case 'parallel': {
      const edge = graph.edges.find((e) => e.id === target.edgeId);
      return edge && canInsertOnEdge(graph, edge)
        ? {
            ...none,
            sources: [{ key: edge.source }],
            at: { after: edge.target },
          }
        : null;
    }
    case 'first':
      return {
        ...none,
        // In a chain the new step runs before the first ones.
        targets: graph.editableEdges ? [] : chainEnds(graph, 'first'),
        at: 'start',
      };
  }
}

/**
 * The graph with a ghost step where the Add step list will put the new step,
 * and the edges that the ghost has. It is a pure projection: the ghost is in
 * no edit, no selection and no export. Returns null when the graph has no
 * place for it (an agent DAG draws its steps as tools) or the target does not
 * match the graph.
 */
export function planGhost(
  graph: CanvasGraph,
  selected: StepKey | null,
  target: PaletteTarget
): GhostPlan | null {
  if (graph.nodes.some((node) => node.kind === 'agent')) return null;
  const place = placeOf(graph, selected, target);
  if (place === null) return null;
  const kind: CanvasEdge['kind'] = graph.editableEdges ? 'depends' : 'order';
  const edges: GhostEdge[] = [
    ...place.sources.map(({ key, port }) => ({
      id: `ghost:${key}>`,
      source: key,
      target: GHOST_KEY,
      ...(port === undefined ? {} : { port }),
    })),
    ...place.targets.map((key) => ({
      id: `ghost:>${key}`,
      source: GHOST_KEY,
      target: key,
    })),
  ];
  const nodes = [...graph.nodes];
  const ghost = ghostNode();
  const { at } = place;
  if (at === 'start') {
    nodes.unshift(ghost);
  } else if (at === 'end') {
    nodes.push(ghost);
  } else {
    const index = nodes.findIndex((node) => node.key === at.after);
    nodes.splice(index < 0 ? nodes.length : index + 1, 0, ghost);
  }
  const hidden = new Set(place.hidden);
  return {
    graph: {
      ...graph,
      nodes,
      edges: [
        ...graph.edges.filter((edge) => !hidden.has(edge.id)),
        ...edges.map(
          (edge): CanvasEdge => ({
            id: edge.id,
            source: edge.source,
            target: edge.target,
            kind,
            ...(edge.port === undefined ? {} : { label: edge.port }),
          })
        ),
      ],
    },
    edges,
    hidden: place.hidden,
  };
}
