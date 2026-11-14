// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useState } from 'react';
import type { CanvasDirection, CanvasGraph } from '../contracts';
import {
  graphShapeKey,
  layoutRoutes,
  reformRouting,
  type EdgeBundles,
  type EdgeWaypoints,
} from './layout';
import type { NodePositions } from './types';

export interface CanvasLayout {
  positions: NodePositions;
  /**
   * Points that the curve of each long edge crosses, by edge id. An edge
   * with an end that the user moved has none: the layout no longer knows
   * its way, and the edge goes around the cards by itself. The edges of a
   * bundle go through its junctions.
   */
  waypoints: EdgeWaypoints;
  /**
   * Where the edges that share a trunk merge and split, by edge id. A bundle
   * with a card that the user moved is formed again from the current places.
   * It falls apart when they leave no room for it.
   */
  bundles: EdgeBundles;
  /** Keeps manual moves of nodes until the graph changes shape. */
  moveNodes(moved: NodePositions): void;
  /** Drops the manual moves: the nodes return to the automatic layout. */
  realign(): void;
}

/**
 * Positions of the canvas nodes. The layout runs again only when the shape
 * of the graph, the direction, the compact flag or the columns of an agent
 * grid changes, so
 * that typing a label does not move the nodes. A shape change also drops the manual moves.
 */
export function useCanvasLayout(
  graph: CanvasGraph,
  direction: CanvasDirection,
  columns?: number,
  compact = false
): CanvasLayout {
  const shapeKey = graphShapeKey(graph, direction, columns, compact);
  const [layout, setLayout] = useState(() => ({
    shapeKey,
    ...layoutRoutes(graph, direction, columns, compact),
    dragged: {} as NodePositions,
  }));
  if (layout.shapeKey !== shapeKey) {
    setLayout({
      shapeKey,
      ...layoutRoutes(graph, direction, columns, compact),
      dragged: {},
    });
  }

  const positions = useMemo<NodePositions>(
    () => ({ ...layout.positions, ...layout.dragged }),
    [layout]
  );
  // After a drag, the edges of a moved card are routed again, and its bundle
  // is formed again from the current places of the cards.
  const routing = useMemo(
    () =>
      Object.keys(layout.dragged).length === 0
        ? { waypoints: layout.waypoints, bundles: layout.bundles }
        : reformRouting(
            graph,
            direction,
            compact,
            positions,
            layout,
            new Set(Object.keys(layout.dragged))
          ),
    [layout, graph, direction, compact, positions]
  );
  const moveNodes = useCallback((moved: NodePositions) => {
    setLayout((current) => ({
      ...current,
      dragged: { ...current.dragged, ...moved },
    }));
  }, []);

  const realign = useCallback(() => {
    setLayout((current) =>
      Object.keys(current.dragged).length === 0
        ? current
        : { ...current, dragged: {} }
    );
  }, []);

  return {
    positions,
    waypoints: routing.waypoints,
    bundles: routing.bundles,
    moveNodes,
    realign,
  };
}
