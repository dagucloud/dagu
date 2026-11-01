// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasGraph, CanvasNode } from '../../contracts';
import {
  AGENT_DEFAULT_COLUMNS,
  AGENT_MAX_COLUMNS,
  AGENT_MIN_COLUMNS,
  HUB_MAX_TASKS,
  HUB_WIDTH,
  TOOL_GAP,
  TOOL_HEIGHT,
  TOOL_WIDTH,
  agentColumns,
  graphShapeKey,
  hubHeight,
  isAgentGraph,
  layoutGraph,
  nodeSize,
} from '../layout';
import type { CanvasNodeWithRoutes, NodePositions } from '../types';

function hub(taskCount = 0): CanvasNodeWithRoutes {
  return {
    key: 'agent',
    label: 'Agent',
    kind: 'agent',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    hub: {
      tasks: Array.from({ length: taskCount }, (_, i) => ({ name: `t${i}` })),
    },
  };
}

function tool(i: number): CanvasNodeWithRoutes {
  return {
    key: `steps[${i}]`,
    label: `tool ${i}`,
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    tool: {},
  };
}

function agentGraph(tools: number, tasks = 0, handlers = 0): CanvasGraph {
  const nodes: CanvasNode[] = [
    hub(tasks),
    ...Array.from({ length: tools }, (_, i) => tool(i)),
    ...Array.from({ length: handlers }, (_, i) => ({
      key: `handler_on.${i}`,
      label: `h${i}`,
      kind: 'run' as const,
      lane: 'handler' as const,
      issues: { errors: 0, warnings: 0 },
    })),
  ];
  return {
    nodes,
    edges: nodes
      .filter((node) => (node as CanvasNodeWithRoutes).tool)
      .map((node) => ({
        id: `agent:agent->${node.key}`,
        source: 'agent',
        target: node.key,
        kind: 'agent' as const,
      })),
    editableEdges: false,
  };
}

interface Box {
  key: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

function boxes(graph: CanvasGraph, positions: NodePositions): Box[] {
  return graph.nodes.map((node) => ({
    key: node.key,
    ...positions[node.key]!,
    ...nodeSize(node),
  }));
}

function overlap(a: Box, b: Box): boolean {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

function anyOverlap(list: Box[]): boolean {
  return list.some((a, i) => list.slice(i + 1).some((b) => overlap(a, b)));
}

describe('agentColumns', () => {
  it('follows the width, from 4 to 8 columns', () => {
    expect(agentColumns(600)).toBe(AGENT_MIN_COLUMNS);
    expect(agentColumns(1000)).toBe(4);
    expect(agentColumns(1100)).toBe(5);
    expect(agentColumns(1300)).toBe(6);
    expect(agentColumns(1500)).toBe(7);
    expect(agentColumns(1920)).toBe(8);
    expect(agentColumns(5000)).toBe(AGENT_MAX_COLUMNS);
  });

  it('gives the default for a width that is not known yet', () => {
    expect(agentColumns(0)).toBe(AGENT_DEFAULT_COLUMNS);
    expect(agentColumns(Number.NaN)).toBe(AGENT_DEFAULT_COLUMNS);
  });
});

describe('agent layout', () => {
  it('is the layout of an agent graph and of no other', () => {
    expect(isAgentGraph(agentGraph(3))).toBe(true);
    expect(
      isAgentGraph({ nodes: [tool(0)], edges: [], editableEdges: true })
    ).toBe(false);
  });

  it('sizes the hub by its tasks and a tool card by a fixed size', () => {
    expect(nodeSize(tool(0))).toEqual({
      width: TOOL_WIDTH,
      height: TOOL_HEIGHT,
    });
    expect(nodeSize(hub(0))).toEqual({
      width: HUB_WIDTH,
      height: hubHeight(0),
    });
    expect(hubHeight(4)).toBeGreaterThan(hubHeight(0));
    expect(hubHeight(10)).toBeGreaterThan(hubHeight(4));
    // The hub lists a few tasks and counts the rest: it stops growing.
    expect(hubHeight(HUB_MAX_TASKS + 40)).toBe(hubHeight(HUB_MAX_TASKS) + 20);
  });

  it('wraps the tools in rows of `columns`, in the YAML order', () => {
    const graph = agentGraph(10);
    const positions = layoutGraph(graph, 'TB', 4);
    const pitchX = TOOL_WIDTH + TOOL_GAP;
    const pitchY = TOOL_HEIGHT + TOOL_GAP;
    const first = positions['steps[0]']!;
    for (let i = 0; i < 10; i++) {
      expect(positions[`steps[${i}]`]).toEqual({
        x: first.x + (i % 4) * pitchX,
        y: first.y + Math.floor(i / 4) * pitchY,
      });
    }
    const rows = new Set(
      Object.entries(positions)
        .filter(([key]) => key !== 'agent')
        .map(([, p]) => p.y)
    );
    expect(rows.size).toBe(3);
  });

  it('puts the hub on top, centred over the grid', () => {
    const graph = agentGraph(8, 3);
    const positions = layoutGraph(graph, 'TB', 6);
    const hubBox = boxes(graph, positions)[0]!;
    const tools = boxes(graph, positions).slice(1);
    const left = Math.min(...tools.map((box) => box.x));
    const right = Math.max(...tools.map((box) => box.x + box.width));
    expect(hubBox.y).toBe(0);
    expect(hubBox.y + hubBox.height).toBeLessThanOrEqual(
      Math.min(...tools.map((box) => box.y))
    );
    expect(
      Math.abs(hubBox.x + hubBox.width / 2 - (left + right) / 2)
    ).toBeLessThan(1);
  });

  it('gives no overlap and the same result twice, for 20, 50 and 100 tools', () => {
    for (const count of [20, 50, 100]) {
      for (const columns of [4, 6, 8]) {
        const graph = agentGraph(count, Math.round(count / 10), 2);
        const positions = layoutGraph(graph, 'TB', columns);
        expect(anyOverlap(boxes(graph, positions))).toBe(false);
        expect(layoutGraph(graph, 'TB', columns)).toEqual(positions);
        for (const p of Object.values(positions)) {
          expect(Number.isInteger(p.x) && Number.isInteger(p.y)).toBe(true);
        }
      }
    }
  });

  it('keeps 100 tools compact: 13 rows of 8, not a chain of 100', () => {
    const graph = agentGraph(100, 10);
    const positions = layoutGraph(graph, 'TB', 8);
    const list = boxes(graph, positions);
    const bottom = Math.max(...list.map((box) => box.y + box.height));
    const right = Math.max(...list.map((box) => box.x + box.width));
    expect(Math.ceil(100 / 8)).toBe(13);
    // A chain of 100 cards of 64 px with a gap of 64 px is 12 700 px high.
    expect(bottom).toBeLessThan(1500);
    expect(right).toBeLessThan(1700);
  });

  it('does not depend on the direction', () => {
    const graph = agentGraph(12, 2);
    expect(layoutGraph(graph, 'LR', 5)).toEqual(layoutGraph(graph, 'TB', 5));
  });

  it('puts the handlers under the grid', () => {
    const graph = agentGraph(9, 0, 2);
    const positions = layoutGraph(graph, 'TB', 4);
    const list = boxes(graph, positions);
    const grid = list.filter((box) => box.key.startsWith('steps'));
    const handlers = list.filter((box) => box.key.startsWith('handler'));
    expect(handlers).toHaveLength(2);
    for (const box of handlers) {
      expect(box.y).toBeGreaterThan(
        Math.max(...grid.map((tool) => tool.y + tool.height))
      );
    }
  });

  it('lays out a hub with no tool, and a grid wider than the tools', () => {
    const lone = layoutGraph(agentGraph(0, 2), 'TB', 6);
    expect(Object.keys(lone)).toEqual(['agent']);
    const few = agentGraph(2);
    const positions = layoutGraph(few, 'TB', 8);
    expect(positions['steps[1]']!.x - positions['steps[0]']!.x).toBe(
      TOOL_WIDTH + TOOL_GAP
    );
  });

  it('lays the columns in the shape key, for an agent graph only', () => {
    const agent = agentGraph(5);
    expect(graphShapeKey(agent, 'TB', 4)).not.toBe(
      graphShapeKey(agent, 'TB', 6)
    );
    // A change of the tasks changes the height of the hub.
    expect(graphShapeKey(agentGraph(5, 1), 'TB', 4)).not.toBe(
      graphShapeKey(agentGraph(5, 2), 'TB', 4)
    );
    const classic: CanvasGraph = {
      nodes: [{ ...tool(0), tool: undefined } as CanvasNode],
      edges: [],
      editableEdges: true,
    };
    expect(graphShapeKey(classic, 'TB', 4)).toBe(
      graphShapeKey(classic, 'TB', 6)
    );
  });

  it('changes with the card shape of a node', () => {
    const step = { ...tool(0), tool: undefined } as CanvasNode;
    const keyOf = (node: CanvasNode) =>
      graphShapeKey({ nodes: [node], edges: [], editableEdges: true }, 'TB');
    const base = keyOf(step);
    // A router with no routes yet still has the router size.
    expect(keyOf({ ...step, action: 'router.route' })).not.toBe(base);
    expect(keyOf(tool(0))).not.toBe(base);
  });
});
