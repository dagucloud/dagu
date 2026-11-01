// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import {
  BaseEdge,
  EdgeLabelRenderer,
  useEdgeHover,
  type EdgeProps,
} from '../engine';
import type { CanvasFlowEdge } from '../types';
import { DASHED, DOTTED } from './dash';
import { EdgeButtons } from './EdgeButtons';
import { useEdgeCurve } from './useEdgeCurve';

const INTERACTION_WIDTH = 20;
// The flow sets `data-hovered` on the group of the hovered edge: the line takes
// the colour of a selected edge from it, with no draw of React. It keeps its dashes.
const HOVER_LINE =
  'group-data-[hovered]/edge:stroke-primary! group-data-[hovered]/edge:[stroke-width:2.5]! group-data-[hovered]/edge:opacity-100!';

/**
 * Edge that the canvas cannot delete: `inferred` (dashed) and `order`
 * (dotted, muted). On hover, a tooltip at the middle tells why. An inferred
 * edge has the same buttons as a standard link: the "+" that inserts a step
 * and the split that adds a step in parallel, and an arrow that restores the
 * explicit link, with the reason in its tooltip. It has no trash, because
 * the dependency comes from a reference. The dashes stay in every state.
 */
function LockedEdgeComponent({
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
  const affordances = useCanvasAffordances();
  const { direction, editable } = useCanvasActions();
  const [hovered, setHovered] = useEdgeHover(id);
  const { path, labelX, labelY } = useEdgeCurve({
    data,
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  const isOrder = data?.edge.kind === 'order' || data?.edge.kind === 'agent';
  const reason = data?.lockReason ? ts(data.lockReason) : null;
  const restorable = data?.restorable === true && affordances !== null;
  const insertable =
    data?.edge.kind === 'inferred' && Boolean(data.insertable) && editable;

  return (
    <g
      data-testid="ve-canvas-locked-edge"
      data-kind={data?.edge.kind}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        interactionWidth={INTERACTION_WIDTH}
        className={HOVER_LINE}
        style={{
          stroke: selected ? 'var(--primary)' : 'var(--muted-foreground)',
          strokeWidth: selected ? 2.5 : 1.5,
          strokeDasharray: isOrder ? DOTTED : DASHED,
          opacity: isOrder && !selected ? 0.6 : 1,
        }}
      />
      <EdgeButtons
        id={id}
        x={labelX}
        y={labelY}
        shown={hovered || selected}
        insertable={insertable}
        restorable={restorable}
        deletable={false}
        restoreTitle={
          reason ? `${ts('Restore the link')}. ${reason}` : undefined
        }
        onHover={setHovered}
      />
      {reason && hovered && (
        <EdgeLabelRenderer>
          <div
            role="tooltip"
            data-testid="ve-canvas-edge-lock-reason"
            style={{
              // Over a vertical link the message sits beside the buttons,
              // clear of the two cards; over a horizontal one it sits above.
              transform:
                direction === 'TB'
                  ? `translate(0, -50%) translate(${labelX + 48}px, ${labelY}px)`
                  : `translate(-50%, -100%) translate(${labelX}px, ${labelY - 20}px)`,
            }}
            className="nodrag nopan pointer-events-none absolute z-10 flex w-max min-w-56 max-w-72 items-start gap-2 rounded-md border bg-popover px-3 py-2 text-xs leading-snug text-popover-foreground shadow-md"
          >
            <span className="min-w-0 flex-1">{reason}</span>
          </div>
        </EdgeLabelRenderer>
      )}
    </g>
  );
}

export const LockedEdge = memo(LockedEdgeComponent);
