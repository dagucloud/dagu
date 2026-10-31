// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { splitAtJunction } from '../bundles';
import { routedCurve } from '../edgeRouting';
import {
  curvePathData,
  curvePointAt,
  Position,
  useNodeBoxes,
  type EdgeProps,
  type CurveSegment,
} from '../engine';
import type { CanvasFlowEdge } from '../types';

export interface EdgeCurve {
  path: string;
  /** The middle of the curve: where the buttons of the edge go. */
  labelX: number;
  labelY: number;
  segments: CurveSegment[];
  /**
   * The path of an edge of a bundle in three pieces: the branch to the
   * junction, the shared trunk and the branch to the target. Each piece has
   * its path data and the middle point, where the buttons of the edge go.
   * Null for an edge that is alone, and for one that no longer goes through
   * its junction.
   */
  pieces: Record<
    'in' | 'trunk' | 'out',
    { path: string; x: number; y: number }
  > | null;
}

/**
 * The curve of an edge: smooth, through the waypoints of the layout, and
 * around every card but the two that it links. It ends on the outer edge of
 * the entry dot of its target.
 */
export function useEdgeCurve({
  data,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
}: Pick<
  EdgeProps<CanvasFlowEdge>,
  | 'data'
  | 'sourceX'
  | 'sourceY'
  | 'targetX'
  | 'targetY'
  | 'sourcePosition'
  | 'targetPosition'
>): EdgeCurve {
  const boxes = useNodeBoxes();
  const sourceId = data?.edge.source;
  const targetId = data?.edge.target;
  const waypoints = data?.waypoints;
  const bundle = data?.bundle;
  return useMemo(() => {
    const others = boxes.filter(
      (box) => box.id !== sourceId && box.id !== targetId
    );
    const ends = {
      source: boxes.find((box) => box.id === sourceId),
      target: boxes.find((box) => box.id === targetId),
    };
    const segments = routedCurve(
      {
        sourceX,
        sourceY,
        sourcePosition,
        targetX,
        targetY,
        targetPosition,
        waypoints,
      },
      others,
      undefined,
      ends
    );
    const middle = curvePointAt(segments, 0.5);
    const split = bundle
      ? splitAtJunction(
          segments,
          bundle.merge,
          bundle.split,
          sourcePosition === undefined ||
            sourcePosition === Position.Bottom ||
            sourcePosition === Position.Top,
          1
        )
      : null;
    const piece = (list: CurveSegment[]) => {
      const point = curvePointAt(list, 0.5);
      return { path: curvePathData(list), x: point.x, y: point.y };
    };
    return {
      path: curvePathData(segments),
      labelX: middle.x,
      labelY: middle.y,
      segments,
      pieces: split
        ? {
            in: piece(split.in),
            trunk: piece(split.trunk),
            out: piece(split.out),
          }
        : null,
    };
  }, [
    boxes,
    sourceId,
    targetId,
    waypoints,
    bundle,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  ]);
}
