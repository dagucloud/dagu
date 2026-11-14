// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { NodeStatus } from '@/api/v1/schema';
import { describe, expect, it } from 'vitest';
import type { CanvasGraph, CanvasNode } from '../../contracts';
import { flowNodeType, toFlowEdges, toFlowNodes, toolMatches } from '../flow';
import { routeHandleId, SOURCE_HANDLE, TARGET_HANDLE } from '../types';

function node(key: string, extra: Partial<CanvasNode> = {}): CanvasNode {
  return {
    key,
    label: key,
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...extra,
  };
}

const graph: CanvasGraph = {
  nodes: [
    node('steps[0]', { kind: 'router', routes: ['ok'] }),
    node('steps[1]'),
    node('steps[2]'),
    node('handler_on.exit', { lane: 'handler' }),
  ],
  edges: [
    {
      id: 'route:steps[0]:ok->steps[1]',
      source: 'steps[0]',
      target: 'steps[1]',
      kind: 'route',
      label: 'ok',
    },
    {
      id: 'depends:steps[1]->steps[2]',
      source: 'steps[1]',
      target: 'steps[2]',
      kind: 'depends',
    },
    {
      id: 'inferred:steps[0]->steps[2]',
      source: 'steps[0]',
      target: 'steps[2]',
      kind: 'inferred',
    },
  ],
  editableEdges: true,
};

describe('toFlowNodes', () => {
  it('maps types, positions and selection', () => {
    const nodes = toFlowNodes(
      graph,
      { 'steps[1]': { x: 10, y: 20 } },
      'steps[1]'
    );
    expect(nodes.map((n) => n.type)).toEqual([
      'router',
      'step',
      'step',
      'handler',
    ]);
    expect(nodes[1]).toMatchObject({
      id: 'steps[1]',
      position: { x: 10, y: 20 },
      selected: true,
      width: 240,
      height: 64,
    });
    expect(nodes[2]).toMatchObject({
      position: { x: 0, y: 0 },
      selected: false,
    });
    expect(nodes[0]?.data.node).toBe(graph.nodes[0]);
  });

  it('marks sinks and router ports without targets for the stalks', () => {
    const withOpenPort: CanvasGraph = {
      ...graph,
      nodes: graph.nodes.map((n) =>
        n.key === 'steps[0]'
          ? {
              ...n,
              routes: ['ok', 'fail', 'gone'],
              // A written target that names no step fills its port too.
              routeTargets: { gone: ['missing'] },
            }
          : n
      ),
    };
    const data = toFlowNodes(withOpenPort, {}, null).map((n) => n.data);
    expect(data.map((d) => d.isSink)).toEqual([false, false, true, false]);
    expect(data[0]?.openPorts).toEqual(['fail']);
    expect(data[3]?.openPorts).toEqual([]);
  });

  it('gives no stalk when the edges cannot change', () => {
    const data = toFlowNodes({ ...graph, editableEdges: false }, {}, null).map(
      (n) => n.data
    );
    expect(data.every((d) => !d.isSink && d.openPorts?.length === 0)).toBe(
      true
    );
  });

  it('treats a node with routes as a router', () => {
    expect(flowNodeType(node('a', { kind: 'action', routes: ['x'] }))).toBe(
      'router'
    );
  });
});

describe('toFlowEdges', () => {
  it('maps handles, lock reasons and insert flags', () => {
    const edges = toFlowEdges(graph, {
      editable: true,
      selectedEdgeId: 'depends:steps[1]->steps[2]',
    });
    const [route, depends, inferred] = edges;
    expect(route).toMatchObject({
      type: 'route',
      sourceHandle: routeHandleId('ok'),
      targetHandle: TARGET_HANDLE,
      selected: false,
    });
    expect(route?.data?.insertable).toBe(false);
    expect(depends).toMatchObject({
      type: 'depends',
      sourceHandle: SOURCE_HANDLE,
      selected: true,
      data: { lockReason: null, insertable: true },
    });
    expect(inferred?.data?.lockReason).toMatch(/^Inferred/);
  });

  it('gives each edge the run status of its source step', () => {
    const run = {
      status: NodeStatus.Failed,
      statusLabel: 'failed',
      retryCount: 0,
      doneCount: 0,
    };
    const edges = toFlowEdges(
      {
        ...graph,
        nodes: graph.nodes.map((n) =>
          n.key === 'steps[1]' ? { ...n, run } : n
        ),
      },
      { editable: false, selectedEdgeId: null }
    );
    expect(edges.map((edge) => edge.data?.sourceStatus)).toEqual([
      undefined,
      NodeStatus.Failed,
      undefined,
    ]);
    // The run status of the target is there too.
    expect(edges.map((edge) => edge.data?.targetStatus)).toEqual([
      NodeStatus.Failed,
      undefined,
      undefined,
    ]);
  });

  it('turns off inserts when the canvas is not editable', () => {
    const edges = toFlowEdges(graph, { editable: false, selectedEdgeId: null });
    expect(edges.every((edge) => edge.data?.insertable === false)).toBe(true);
  });
});

describe('agent graph', () => {
  const tool = (key: string, label: string): CanvasNode =>
    ({
      ...node(key, { label, serverName: label }),
      tool: {},
    }) as CanvasNode;
  const agentGraph: CanvasGraph = {
    nodes: [
      node('agent', { kind: 'agent', label: 'Agent' }),
      tool('steps[0]', 'Fetch data'),
      tool('steps[1]', 'build'),
    ],
    edges: ['steps[0]', 'steps[1]'].map((target) => ({
      id: `agent:agent->${target}`,
      source: 'agent',
      target,
      kind: 'agent' as const,
    })),
    editableEdges: false,
  };

  it('draws the steps as tool cards and the agent as the hub', () => {
    expect(flowNodeType(agentGraph.nodes[0]!)).toBe('agent');
    expect(flowNodeType(agentGraph.nodes[1]!)).toBe('tool');
    const nodes = toFlowNodes(agentGraph, {}, null);
    expect(nodes.map((n) => n.type)).toEqual(['agent', 'tool', 'tool']);
    expect(nodes[1]).toMatchObject({ width: 190, height: 52 });
  });

  it('draws no edge from the agent to its tools', () => {
    expect(
      toFlowEdges(agentGraph, { editable: false, selectedEdgeId: null })
    ).toEqual([]);
  });

  it('marks the tools that match the filter, whatever the case', () => {
    expect(
      toFlowNodes(agentGraph, {}, null, null, ' FETCH ').map(
        (n) => n.data.filterMatch
      )
    ).toEqual([undefined, true, false]);
    expect(
      toFlowNodes(agentGraph, {}, null, null, '').map((n) => n.data.filterMatch)
    ).toEqual([undefined, undefined, undefined]);
    expect(toolMatches(agentGraph.nodes[2]!, 'bui')).toBe(true);
    expect(toolMatches(agentGraph.nodes[2]!, 'zzz')).toBe(false);
  });
});

describe('toFlowEdges restorable', () => {
  const inferredGraph: CanvasGraph = {
    nodes: [node('steps[0]'), node('steps[1]')],
    edges: [
      {
        id: 'inferred:steps[0]->steps[1]',
        source: 'steps[0]',
        target: 'steps[1]',
        kind: 'inferred',
      },
    ],
    editableEdges: true,
  };
  const opts = { editable: true, selectedEdgeId: null };

  it('marks an inferred edge that a dependency can replace', () => {
    expect(toFlowEdges(inferredGraph, opts)[0]?.data.restorable).toBe(true);
  });

  it('does not mark it on a chain, a read-only canvas or another edge', () => {
    expect(
      toFlowEdges({ ...inferredGraph, editableEdges: false }, opts)[0]?.data
        .restorable
    ).toBe(false);
    expect(
      toFlowEdges(inferredGraph, { ...opts, editable: false })[0]?.data
        .restorable
    ).toBe(false);
    expect(
      toFlowEdges(graph, opts)
        .filter((e) => e.data.edge.kind !== 'inferred')
        .every((e) => !e.data.restorable)
    ).toBe(true);
  });
});
