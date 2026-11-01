// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, useContext } from 'react';
import { DASHED } from '../canvas/edges/dash';
import { useEdgeCurve } from '../canvas/edges/useEdgeCurve';
import { BaseEdge, type EdgeProps } from '../canvas/engine';
import type { CanvasFlowEdge } from '../canvas/types';
import { LauncherActionsContext } from './LauncherNode';
import { launcherKey } from './model';

/**
 * Edge from a launcher to a start step. The user cannot select, change or
 * delete it: it is not in the YAML. It is dashed while the launcher is not
 * set up, and stays dashed when the launcher is open: its colour changes,
 * its dashes do not. The edges of all the launchers of a graph share one
 * trunk, drawn once.
 */
function LauncherEdgeComponent({
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
  const { path, pieces } = useEdgeCurve({
    data,
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  });
  const { active } = useContext(LauncherActionsContext);
  const dashed = data?.launcher === 'dashed';
  const lit = active !== null && data?.edge.source === launcherKey(active);
  const bundle = pieces ? data?.bundle : undefined;
  if (bundle && pieces) {
    // The launchers of a bundle merge into one trunk, which splits towards
    // the start steps. Each launcher keeps its own short line, dashed while
    // it is not set up; the trunk and the branches to the steps are shared,
    // so they are solid: Manual is always set up.
    const sharedLit =
      active !== null && bundle.sources.includes(launcherKey(active));
    const shared = {
      stroke: sharedLit ? 'var(--primary)' : 'var(--muted-foreground)',
      strokeWidth: sharedLit ? 2 : 1.5,
      pointerEvents: 'none',
    } as const;
    return (
      <g
        data-testid="ve-launcher-edge"
        data-status={data?.launcher}
        data-active={lit || undefined}
      >
        {bundle.draws.in && (
          <BaseEdge
            path={pieces.in.path}
            interactionWidth={0}
            style={{
              stroke: lit ? 'var(--primary)' : 'var(--muted-foreground)',
              strokeWidth: lit ? 2 : 1.5,
              strokeDasharray: dashed ? DASHED : undefined,
              opacity: dashed && !lit ? 0.55 : 1,
              pointerEvents: 'none',
            }}
          />
        )}
        {bundle.draws.trunk && (
          <BaseEdge
            path={pieces.trunk.path}
            interactionWidth={0}
            style={shared}
          />
        )}
        {bundle.draws.out && (
          <BaseEdge
            id={id}
            path={pieces.out.path}
            markerEnd={markerEnd}
            interactionWidth={0}
            style={shared}
          />
        )}
      </g>
    );
  }
  return (
    <g
      data-testid="ve-launcher-edge"
      data-status={data?.launcher}
      data-active={lit || undefined}
    >
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        interactionWidth={0}
        style={{
          stroke: lit ? 'var(--primary)' : 'var(--muted-foreground)',
          strokeWidth: lit ? 2 : 1.5,
          strokeDasharray: dashed ? DASHED : undefined,
          opacity: dashed && !lit ? 0.55 : 1,
          pointerEvents: 'none',
        }}
      />
    </g>
  );
}

export const LauncherEdge = memo(LauncherEdgeComponent);
