// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { cn } from '@/lib/utils';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import { EdgeLabelRenderer, useEdgeHover, type EdgeProps } from '../engine';
import { clamp } from '../engine/geometry';
import { statusStrokeClass, edgeTintStatus } from '../statusStyle';
import type { CanvasFlowEdge } from '../types';
import { EdgeButtons } from './EdgeButtons';
import { useEdgeCurve } from './useEdgeCurve';

const INTERACTION_WIDTH = 20;
const LABEL_GAP = 14;
// A pattern label is at most 160 px wide (max-w-40).
const LABEL_MAX_HALF = 80;
const LABEL_CLEARANCE = 6;
// In a top-to-bottom layout the buttons lie in a row across the link: at most
// four of 24 px with a gap of 4 px. The pattern sits beside the row.
const BUTTONS_HALF = 54;
// The flow sets `data-hovered` on the group of the hovered edge. The line takes
// the colour and the width of a selected edge from it, with no draw of React.
const HOVER_LINE =
  'group-data-[hovered]/edge:stroke-primary! group-data-[hovered]/edge:[stroke-width:2.5]!';

/**
 * Edge of a `depends` or `route` link. A route edge shows its pattern. On
 * hover or selection, a "+" button at the middle inserts a step, a split
 * button adds a step in parallel with the target, and a trash button removes
 * the link. The stroke takes the run status of the source
 * step, unless the edge is selected or hovered, or its target was skipped or
 * did not start. A selected or hovered edge takes the colour of the
 * selection and is drawn over the others.
 */
function DependencyEdgeComponent({
  id,
  data,
  selected,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
}: EdgeProps<CanvasFlowEdge>) {
  const { direction, editable } = useCanvasActions();
  const affordances = useCanvasAffordances();
  const [hovered, setHovered] = useEdgeHover(id);
  const curve = useEdgeCurve({
    data,
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  const { path, pieces } = curve;
  const bundle = pieces ? data?.bundle : undefined;
  // The buttons of an edge of a bundle sit on the piece where the pointer
  // reaches it, so that they stay under the pointer.
  const buttonsAt = bundle && pieces ? pieces[bundle.piece ?? 'out'] : null;
  const labelX = buttonsAt?.x ?? curve.labelX;
  const labelY = buttonsAt?.y ?? curve.labelY;
  const pattern = data?.edge.kind === 'route' ? data.edge.label : undefined;
  const insertable = Boolean(data?.insertable) && editable;
  const deletable = affordances !== null && data?.lockReason === null;
  const status = edgeTintStatus(data?.sourceStatus, data?.targetStatus);

  // The pattern sits beside the middle of the edge, away from the buttons. In
  // a left-to-right layout the label is as wide as its text: its centre stays
  // between the two nodes, so that it covers neither the diamond of the
  // router nor the card that the edge reaches.
  const half = Math.min(LABEL_MAX_HALF, (pattern?.length ?? 0) * 3 + 6);
  const near = sourceX + half + LABEL_CLEARANCE;
  const far = targetX - half - LABEL_CLEARANCE;
  const patternX = near <= far ? clamp(labelX, near, far) : labelX;
  // In a top-to-bottom layout the label goes to the outer side of the curve:
  // right of an edge that heads right, left of one that heads left. Two
  // routes of one router then never put their labels on each other.
  const leftward = targetX < sourceX - 0.5;
  const patternPlace =
    direction === 'TB'
      ? leftward
        ? `translate(-100%, -50%) translate(${labelX - BUTTONS_HALF - LABEL_GAP}px, ${labelY}px)`
        : `translate(0, -50%) translate(${labelX + BUTTONS_HALF + LABEL_GAP}px, ${labelY}px)`
      : `translate(-50%, -100%) translate(${patternX}px, ${labelY - LABEL_GAP / 2}px)`;

  return (
    <>
      {bundle && pieces ? (
        <>
          {(['in', 'trunk', 'out'] as const).map((name) => {
            const tint =
              name === 'in'
                ? bundle.inStatus
                : name === 'trunk'
                  ? bundle.trunkStatus
                  : bundle.outStatus;
            return (
              <path
                key={name}
                id={name === 'out' ? id : undefined}
                d={pieces[name].path}
                fill="none"
                markerEnd={name === 'out' ? markerEnd : undefined}
                data-flow-edge-path=""
                data-bundle-piece={name}
                className={
                  tint !== undefined ? statusStrokeClass(tint) : undefined
                }
                style={{
                  stroke:
                    tint === undefined ? 'var(--muted-foreground)' : undefined,
                  strokeWidth: 1.5,
                  pointerEvents: 'none',
                }}
              />
            );
          })}
          {/* Drawn for each bundled edge and hidden: a hover shows it by CSS. */}
          <path
            d={path}
            fill="none"
            markerEnd={markerEnd}
            data-flow-edge-path=""
            data-flow-edge-overlay=""
            data-active={selected || undefined}
            className={cn(
              !selected && 'hidden',
              'group-data-[hovered]/edge:inline'
            )}
            style={{
              stroke: 'var(--primary)',
              strokeWidth: 2.5,
              pointerEvents: 'none',
            }}
          />
          {bundle.piece && (
            <path
              d={pieces[bundle.piece].path}
              fill="none"
              strokeOpacity={0}
              strokeWidth={INTERACTION_WIDTH}
              data-flow-edge-interaction=""
              data-bundle-piece={bundle.piece}
              style={{ pointerEvents: 'stroke' }}
              onMouseEnter={() => setHovered(true)}
              onMouseLeave={() => setHovered(false)}
            />
          )}
        </>
      ) : (
        <>
          <path
            id={id}
            d={path}
            fill="none"
            markerEnd={markerEnd}
            data-flow-edge-path=""
            data-active={selected || undefined}
            className={cn(
              !selected && status !== undefined && statusStrokeClass(status),
              HOVER_LINE
            )}
            style={{
              stroke: selected
                ? 'var(--primary)'
                : status === undefined
                  ? 'var(--muted-foreground)'
                  : undefined,
              strokeWidth: selected ? 2.5 : 1.5,
            }}
          />
          <path
            d={path}
            fill="none"
            strokeOpacity={0}
            strokeWidth={INTERACTION_WIDTH}
            data-flow-edge-interaction=""
            style={{ pointerEvents: 'stroke' }}
            onMouseEnter={() => setHovered(true)}
            onMouseLeave={() => setHovered(false)}
          />
        </>
      )}
      {pattern && (
        <EdgeLabelRenderer>
          <div
            data-testid="ve-canvas-edge-label"
            title={pattern}
            style={{ transform: patternPlace }}
            className="nodrag nopan pointer-events-none absolute max-w-40 truncate rounded border bg-card px-1 font-mono text-[11px] leading-4 text-foreground/70"
          >
            {pattern}
          </div>
        </EdgeLabelRenderer>
      )}
      <EdgeButtons
        id={id}
        x={labelX}
        y={labelY}
        shown={hovered || selected}
        insertable={insertable}
        deletable={deletable}
        onHover={setHovered}
      />
    </>
  );
}

export const DependencyEdge = memo(DependencyEdgeComponent);
