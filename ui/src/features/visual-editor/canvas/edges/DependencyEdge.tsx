// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import { EdgeLabelRenderer, useEdgeHover, type EdgeProps } from '../engine';
import { clamp } from '../engine/geometry';
import {
  statusDashArray,
  statusStrokeClass,
  edgeTintStatus,
} from '../statusStyle';
import type { CanvasFlowEdge } from '../types';
import { CONDITIONAL } from './dash';
import { conditionLabel, conditionTitle } from './conditionLabel';
import { EdgeButtons } from './EdgeButtons';
import { useEdgeCurve } from './useEdgeCurve';

// An edge that a run tints is thicker than a plain one, so that the path of
// the run reads at any zoom.
const PLAIN_WIDTH = 1.5;
const RUN_WIDTH = 2;

const INTERACTION_WIDTH = 20;
const LABEL_GAP = 14;
// A pattern label is at most 160 px wide (max-w-40), a condition label 288 px (max-w-72).
const LABEL_MAX_HALF = 80;
const CONDITION_MAX_HALF = 144;
const LABEL_CLEARANCE = 6;
// The narrowest width that a label of a short link can have.
const LABEL_MIN_WIDTH = 48;
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
  const { ts } = useI18n();
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
  const condition = data?.edge.condition;
  const pattern =
    data?.edge.kind === 'route'
      ? data.edge.label
      : condition
        ? conditionLabel(ts, condition)
        : undefined;
  const dash = condition ? CONDITIONAL : undefined;
  const insertable = Boolean(data?.insertable) && editable;
  const deletable = affordances !== null && data?.lockReason === null;
  const status = edgeTintStatus(data?.sourceStatus, data?.targetStatus);

  // The pattern sits beside the middle of the edge, away from the buttons. In
  // a left-to-right layout the label is as wide as its text: its centre stays
  // between the two nodes, so that it covers neither the diamond of the
  // router nor the card that the edge reaches. A label wider than the link
  // wraps (a condition) or is cut (a pattern) at the width of the link.
  const maxHalf = condition ? CONDITION_MAX_HALF : LABEL_MAX_HALF;
  const room =
    direction === 'TB'
      ? undefined
      : Math.min(
          Math.max(targetX - sourceX - 2 * LABEL_CLEARANCE, LABEL_MIN_WIDTH),
          2 * maxHalf
        );
  const half = Math.min(
    maxHalf,
    (pattern?.length ?? 0) * 3 + 6,
    (room ?? Infinity) / 2
  );
  const near = sourceX + half + LABEL_CLEARANCE;
  const far = targetX - half - LABEL_CLEARANCE;
  const patternX = near <= far ? clamp(labelX, near, far) : labelX;
  // In a top-to-bottom layout the label goes to the outer side of the curve:
  // right of an edge that heads right, left of one that heads left. Two
  // routes of one router then never put their labels on each other.
  const leftward = targetX < sourceX - 0.5;
  // A condition label is wide: top to bottom it sits on the edge, above the
  // buttons, where the corridor between the two columns of sub-nodes is free.
  const patternPlace =
    direction === 'TB' && condition
      ? `translate(-50%, -100%) translate(${labelX}px, ${labelY - 16}px)`
      : direction === 'TB'
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
                  strokeWidth: tint === undefined ? PLAIN_WIDTH : RUN_WIDTH,
                  strokeDasharray:
                    name === 'out' && dash
                      ? dash
                      : tint === undefined
                        ? undefined
                        : statusDashArray(tint),
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
              strokeWidth: selected
                ? 2.5
                : status === undefined
                  ? PLAIN_WIDTH
                  : RUN_WIDTH,
              strokeDasharray:
                dash ??
                (selected || status === undefined
                  ? undefined
                  : statusDashArray(status)),
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
            data-conditional={condition ? '' : undefined}
            title={condition ? conditionTitle(ts, condition) : pattern}
            style={{ transform: patternPlace, maxWidth: room }}
            className={cn(
              'nodrag nopan absolute max-w-40 truncate rounded border bg-card px-1 text-[11px] leading-4 text-foreground/70',
              condition
                ? 'pointer-events-auto max-w-72 border-dashed font-medium text-foreground'
                : 'pointer-events-none font-mono',
              condition &&
                room !== undefined &&
                'line-clamp-3 w-max whitespace-normal [overflow-wrap:anywhere]'
            )}
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
