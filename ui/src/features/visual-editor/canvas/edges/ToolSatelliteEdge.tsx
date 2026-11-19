// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { BaseEdge, Position, useNodeBoxes, type EdgeProps } from '../engine';
import type { CanvasFlowEdge } from '../types';
import { SUB_LINK_COLOR, SUB_LINK_DASH } from './dash';

// How far a link that doubles back leaves its connector, how far it passes
// outside the box of the step, and (when the box is not known) from the connector.
const LEAD = 40;
const CLEAR = 20;
const BOW = 96;
// A sub-node that is less than this far in front of its connector is behind it.
const AHEAD_MIN = 12;

const OUTWARD: Record<Position, { x: number; y: number }> = {
  [Position.Right]: { x: 1, y: 0 },
  [Position.Left]: { x: -1, y: 0 },
  [Position.Bottom]: { x: 0, y: 1 },
  [Position.Top]: { x: 0, y: -1 },
};

/**
 * True when the sub-node is not in front of its connector: it sits behind the
 * side where the connector leaves, because the user moved it to the other
 * side of the step. A link that went straight to it would cross the step.
 */
export function linkDoublesBack(
  source: { x: number; y: number },
  target: { x: number; y: number },
  out: Position
): boolean {
  const o = OUTWARD[out];
  return (target.x - source.x) * o.x + (target.y - source.y) * o.y < AHEAD_MIN;
}

/** The box of the step with its sub-nodes, in the coordinates of the link. */
export interface LinkBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

// A link that doubles back: it leaves its connector, passes the box of the
// step by the side of the target (over it, or under it) and enters the
// sub-node from the side that faces the step. Two smooth curves that meet on
// a straight run.
function backPath(
  sourceX: number,
  sourceY: number,
  targetX: number,
  targetY: number,
  out: Position,
  into: Position,
  box?: LinkBox
): string {
  const o = OUTWARD[out];
  const entry = OUTWARD[into];
  const sideways = o.x !== 0;
  // The way round the step: on the side of the target, outside its box.
  const outside = box
    ? {
        top: box.y - CLEAR,
        bottom: box.y + box.height + CLEAR,
        left: box.x - CLEAR,
        right: box.x + box.width + CLEAR,
      }
    : {
        top: sourceY - BOW,
        bottom: sourceY + BOW,
        left: sourceX - BOW,
        right: sourceX + BOW,
      };
  const around = sideways
    ? targetY < sourceY
      ? Math.min(outside.top, targetY)
      : Math.max(outside.bottom, targetY)
    : targetX < sourceX
      ? Math.min(outside.left, targetX)
      : Math.max(outside.right, targetX);
  const mid = sideways
    ? { x: (sourceX + targetX) / 2, y: around }
    : { x: around, y: (sourceY + targetY) / 2 };
  // The run at the middle point goes the way of the target.
  const run = sideways
    ? { x: Math.sign(targetX - sourceX) || 1, y: 0 }
    : { x: 0, y: Math.sign(targetY - sourceY) || 1 };
  const half = sideways
    ? Math.max(LEAD, Math.abs(targetX - sourceX) / 4)
    : Math.max(LEAD, Math.abs(targetY - sourceY) / 4);
  const reach = Math.max(12, Math.hypot(targetX - mid.x, targetY - mid.y) / 3);
  const c1 = `${sourceX + o.x * LEAD},${sourceY + o.y * LEAD}`;
  const c2 = `${mid.x - run.x * half},${mid.y - run.y * half}`;
  const c3 = `${mid.x + run.x * half},${mid.y + run.y * half}`;
  const c4 = `${targetX + entry.x * reach},${targetY + entry.y * reach}`;
  return `M ${sourceX},${sourceY} C ${c1} ${c2} ${mid.x},${mid.y} C ${c3} ${c4} ${targetX},${targetY}`;
}

/**
 * A smooth S: it leaves the connector and enters the sub-node in the sense
 * of each end, with no detour around the sub-nodes beside it. Beside the step
 * an end is sideways; above or under it, up or down. A sub-node that the user
 * moved under its step has a sideways start and a vertical end. A sub-node
 * that the user moved behind its connector, to the other side of the step,
 * gets a link that goes round the step (see `linkDoublesBack`).
 */
export function linkPath(
  sourceX: number,
  sourceY: number,
  targetX: number,
  targetY: number,
  out: Position,
  into: Position,
  box?: LinkBox
): string {
  if (
    linkDoublesBack({ x: sourceX, y: sourceY }, { x: targetX, y: targetY }, out)
  ) {
    return backPath(sourceX, sourceY, targetX, targetY, out, into, box);
  }
  const sidewaysOut = out === Position.Left || out === Position.Right;
  const sidewaysIn = into === Position.Left || into === Position.Right;
  const signX = targetX >= sourceX ? 1 : -1;
  const signY = targetY >= sourceY ? 1 : -1;
  const reachX = Math.max(12, Math.abs(targetX - sourceX) / 2);
  const reachY = Math.max(12, Math.abs(targetY - sourceY) / 2);
  const start = sidewaysOut
    ? `${sourceX + signX * reachX},${sourceY}`
    : `${sourceX},${sourceY + signY * reachY}`;
  const end = sidewaysIn
    ? `${targetX - signX * reachX},${targetY}`
    : `${targetX},${targetY - signY * reachY}`;
  return `M ${sourceX},${sourceY} C ${start} ${end} ${targetX},${targetY}`;
}

/**
 * The line from a slot connector of a chat step to one of its sub-nodes, under
 * the step, above it or beside it. It is thin, dashed and purple-grey, has no arrow (the model chooses the tool)
 * and no button, and the pointer goes through it: it is not an edge of the
 * YAML.
 */
function ToolSatelliteEdgeComponent({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps<CanvasFlowEdge>) {
  const boxes = useNodeBoxes();
  const owner = boxes.find((box) => box.id === data?.edge.source);
  return (
    <g data-testid="ve-canvas-tool-edge">
      <BaseEdge
        id={id}
        path={linkPath(
          sourceX,
          sourceY,
          targetX,
          targetY,
          sourcePosition,
          targetPosition,
          owner
        )}
        interactionWidth={0}
        style={{
          stroke: SUB_LINK_COLOR,
          strokeWidth: 1.25,
          strokeDasharray: SUB_LINK_DASH,
          pointerEvents: 'none',
        }}
      />
    </g>
  );
}

export const ToolSatelliteEdge = memo(ToolSatelliteEdgeComponent);
