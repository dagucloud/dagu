// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import type { NodeStatus } from '@/api/v1/schema';
import { cn } from '@/lib/utils';
import {
  edgePath,
  fitScale,
  type MiniBox,
  type MiniGraph,
  type MiniNode,
} from './miniGraph';
import { statusStrokeClass } from './statusStyle';

/** The room that the miniature of a DAG has in a DAGs card, in px. */
export const MINI_BOX: MiniBox = { width: 232, height: 44 };

// The most px for one unit of the graph: a DAG of one step is a small card in
// the middle of the box, not a block that fills it.
const MAX_UNIT_PX = 13;

// A summary draws a cell for each step of a rank up to this many steps.
const SUMMARY_CELLS_MAX = 14;

// The room around the drawing, in the units of the graph, so that the line of
// an outer node is not cut.
const PAD = 0.25;

function nodeClass(node: MiniNode, status: NodeStatus | undefined): string {
  if (status !== undefined) return cn('fill-card', statusStrokeClass(status));
  return node.call
    ? 'fill-tool-dag/15 stroke-tool-dag'
    : 'fill-card stroke-muted-foreground';
}

function MiniShape({
  node,
  status,
}: {
  node: MiniNode;
  status: NodeStatus | undefined;
}): ReactElement {
  const { x, y, width, height } = node;
  const common = {
    className: nodeClass(node, status),
    strokeWidth: 1.25,
    vectorEffect: 'non-scaling-stroke' as const,
    ...(node.conditional ? { strokeDasharray: '3 2' } : {}),
  };
  if (node.router) {
    const cx = x + width / 2;
    const cy = y + height / 2;
    return (
      <polygon
        {...common}
        points={`${cx},${y} ${x + width},${cy} ${cx},${y + height} ${x},${cy}`}
      />
    );
  }
  return (
    <>
      {node.parallel && (
        // A fan-out: a second card behind the first.
        <rect
          x={x + 0.22}
          y={y + 0.22}
          width={width}
          height={height}
          rx={0.22}
          className="fill-card stroke-muted-foreground/60"
          strokeWidth={1}
          vectorEffect="non-scaling-stroke"
        />
      )}
      <rect {...common} x={x} y={y} width={width} height={height} rx={0.22} />
    </>
  );
}

/**
 * The miniature of a DAG: its steps as small cards and its edges as curves,
 * drawn in one SVG that scales to its box. A step that calls a DAG is in the
 * colour of the DAG tools, a step with a precondition has a dashed line, a
 * router is a diamond and a fan-out has a second card behind it. In a run
 * view, `steps` gives the line of each step the colour of its status. A DAG
 * with many steps is a row of bars, one for each rank, as high as the steps
 * that it holds.
 */
export function DagMiniature({
  graph,
  steps,
  box = MINI_BOX,
  className,
}: {
  graph: Exclude<MiniGraph, { kind: 'empty' }>;
  steps?: Record<string, NodeStatus> | undefined;
  /** The room of the drawing, in px: the one that `buildMiniGraph` fitted the graph to. */
  box?: MiniBox;
  className?: string;
}): ReactElement {
  if (graph.kind === 'summary') {
    const tallest = Math.max(1, ...graph.ranks);
    const count = graph.ranks.length;
    const pitch = 1.6;
    // A few steps in a rank are cells, one for each step: the DAG reads as a
    // grid. A rank of many steps is a bar as high as they would be.
    const cells = tallest <= SUMMARY_CELLS_MAX;
    const row = cells ? 1.3 : 1;
    return (
      <svg
        data-testid="ve-dag-mini-svg"
        data-kind="summary"
        aria-hidden="true"
        viewBox={`0 0 ${count * pitch} ${tallest * row}`}
        preserveAspectRatio="xMidYMid meet"
        className={cn('block h-full w-full', className)}
      >
        {graph.ranks.map((rank, index) =>
          cells ? (
            Array.from({ length: rank }, (_, cell) => (
              <rect
                key={`${index}:${cell}`}
                x={index * pitch}
                y={((tallest - rank) * row) / 2 + cell * row}
                width={pitch * 0.7}
                height={0.95}
                rx={0.2}
                className="fill-muted-foreground/55"
              />
            ))
          ) : (
            <rect
              key={index}
              x={index * pitch}
              y={(tallest - rank) / 2}
              width={pitch * 0.62}
              height={rank}
              rx={0.2}
              className="fill-muted-foreground/45"
            />
          )
        )}
      </svg>
    );
  }
  const width = graph.width + 2 * PAD;
  const height = graph.height + 2 * PAD;
  const scale = Math.min(fitScale(width, height, box), MAX_UNIT_PX);
  return (
    <svg
      data-testid="ve-dag-mini-svg"
      data-kind="graph"
      data-direction={graph.direction}
      aria-hidden="true"
      viewBox={`${-PAD} ${-PAD} ${width} ${height}`}
      preserveAspectRatio="xMidYMid meet"
      style={{ width: width * scale, height: height * scale }}
      className={cn('mx-auto block max-h-full max-w-full', className)}
    >
      {graph.edges.map((edge) => (
        <path
          key={edge.id}
          d={edgePath(edge)}
          fill="none"
          className="stroke-muted-foreground/70"
          strokeWidth={1}
          vectorEffect="non-scaling-stroke"
        />
      ))}
      {graph.nodes.map((node) => (
        <MiniShape key={node.name} node={node} status={steps?.[node.name]} />
      ))}
    </svg>
  );
}
