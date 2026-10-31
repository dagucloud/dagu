// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { BaseEdge, type EdgeProps } from '../engine';
import type { CanvasFlowEdge } from '../types';
import { DASHED } from './dash';
import { useEdgeCurve } from './useEdgeCurve';

/**
 * Edge from or to the ghost step of the Add step list. It is dashed, as the
 * ghost is, and the pointer goes through it: it is not in the YAML.
 */
function GhostEdgeComponent({
  id,
  data,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
}: EdgeProps<CanvasFlowEdge>) {
  const { path } = useEdgeCurve({
    data,
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  return (
    <g data-testid="ve-canvas-ghost-edge">
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        interactionWidth={0}
        style={{
          stroke: 'var(--primary)',
          strokeWidth: 1.5,
          strokeDasharray: DASHED,
          opacity: 0.8,
          pointerEvents: 'none',
        }}
      />
    </g>
  );
}

export const GhostEdge = memo(GhostEdgeComponent);
