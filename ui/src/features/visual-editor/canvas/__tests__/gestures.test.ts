// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasEdge, CanvasGraph, CanvasNode } from '../../contracts';
import { applyEdit, parseDagDoc, resolveStepNames } from '../../doc';
import { catalogSample } from '../../testing/fixtures';
import { toFlowNodes } from '../flow';
import { buildCanvasGraph } from '../graph';
import {
  canInsertOnEdge,
  connectionToOp,
  dropAddOp,
  droppedConnectionSource,
  edgeDeleteOp,
  edgeLockReason,
  insertOnEdgeOp,
  paletteAddOp,
  lastLeafOf,
  paletteTargetOp,
  refusedConnection,
  routeTargetsOf,
  selectionDeleteOps,
  stepDeleteOp,
  stepRefName,
  wouldCreateCycle,
} from '../gestures';
import {
  AGENT_NODE_KEY,
  routeHandleId,
  SOURCE_HANDLE,
  type CanvasEdgeWithRef,
  type CanvasNodeWithRoutes,
} from '../types';

function node(key: string, label = key, extra: Partial<CanvasNode> = {}) {
  return {
    key,
    label,
    kind: 'run',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...extra,
  } satisfies CanvasNode;
}

// a -> b, c alone, one handler.
const graph: CanvasGraph = {
  nodes: [
    node('steps[0]', 'a'),
    node('steps[1]', 'b'),
    node('steps[2]', 'c'),
    node('handler_on.failure', 'handler_on.failure', { lane: 'handler' }),
  ],
  edges: [
    {
      id: 'depends:steps[0]->steps[1]',
      source: 'steps[0]',
      target: 'steps[1]',
      kind: 'depends',
    },
  ],
  editableEdges: true,
};

describe('stepRefName', () => {
  it('uses the label when it is a name', () => {
    expect(stepRefName(node('steps[0]', 'fetch'))).toBe('fetch');
  });

  it('falls back to the server name', () => {
    expect(
      stepRefName(node('steps[0]', 'steps[0]', { serverName: 'http_1' }))
    ).toBe('http_1');
  });

  it('returns null without a name', () => {
    expect(stepRefName(node('steps[0]'))).toBeNull();
  });
});

// pick routes "5xx" to server_error and to a name that does not resolve.
const routerGraph: CanvasGraph = {
  nodes: [
    {
      ...node('steps[0]', 'pick', { kind: 'router' }),
      routes: ['5xx', 're:.*'],
      routeTargets: { '5xx': ['server_error', 'missing'], 're:.*': [] },
    } as CanvasNodeWithRoutes,
    node('steps[1]', 'server_error'),
    node('steps[2]', 'steps[2]', { serverName: 'other_2' }),
  ],
  edges: [
    {
      id: 'route:steps[0]:5xx->steps[1]',
      source: 'steps[0]',
      target: 'steps[1]',
      kind: 'route',
      label: '5xx',
      written: 'server_error',
    } as CanvasEdgeWithRef,
  ],
  editableEdges: true,
};

describe('router ports', () => {
  it('adds a target and keeps unresolved targets', () => {
    expect(
      connectionToOp(routerGraph, {
        source: 'steps[0]',
        sourceHandle: routeHandleId('5xx'),
        target: 'steps[2]',
      })
    ).toEqual({
      kind: 'setRouteTargets',
      step: 'steps[0]',
      pattern: '5xx',
      targets: ['server_error', 'missing', 'other_2'],
    });
  });

  it('ignores a target that the route already reaches', () => {
    expect(
      connectionToOp(routerGraph, {
        source: 'steps[0]',
        sourceHandle: routeHandleId('5xx'),
        target: 'steps[1]',
      })
    ).toBeNull();
  });

  it('reads targets from route edges without written targets', () => {
    const plain: CanvasGraph = {
      ...routerGraph,
      nodes: routerGraph.nodes.map(({ ...n }) => {
        delete (n as CanvasNodeWithRoutes).routeTargets;
        return n;
      }),
    };
    expect(routeTargetsOf(plain, 'steps[0]', '5xx')).toEqual(['server_error']);
  });

  it('uses the default handle for a plain dependency', () => {
    expect(
      connectionToOp(routerGraph, {
        source: 'steps[0]',
        sourceHandle: SOURCE_HANDLE,
        target: 'steps[2]',
      })
    ).toEqual({
      kind: 'addDependency',
      step: 'steps[2]',
      dependsOn: 'steps[0]',
    });
  });
});

describe('wouldCreateCycle', () => {
  it('follows edges of every kind', () => {
    const chain: CanvasGraph = {
      ...graph,
      edges: [
        ...graph.edges,
        {
          id: 'inferred:steps[1]->steps[2]',
          source: 'steps[1]',
          target: 'steps[2]',
          kind: 'inferred',
        },
      ],
    };
    expect(wouldCreateCycle(chain, 'steps[2]', 'steps[0]')).toBe(true);
    expect(wouldCreateCycle(chain, 'steps[0]', 'steps[2]')).toBe(false);
  });
});

describe('connectionToOp', () => {
  it('adds a dependency on the source', () => {
    expect(
      connectionToOp(graph, { source: 'steps[1]', target: 'steps[2]' })
    ).toEqual({
      kind: 'addDependency',
      step: 'steps[2]',
      dependsOn: 'steps[1]',
    });
  });

  it('ignores an existing dependency', () => {
    expect(
      connectionToOp(graph, { source: 'steps[0]', target: 'steps[1]' })
    ).toBeNull();
  });

  it('ignores self, unknown and handler nodes', () => {
    expect(
      connectionToOp(graph, { source: 'steps[0]', target: 'steps[0]' })
    ).toBeNull();
    expect(
      connectionToOp(graph, { source: 'steps[0]', target: 'steps[9]' })
    ).toBeNull();
    expect(
      connectionToOp(graph, {
        source: 'steps[0]',
        target: 'handler_on.failure',
      })
    ).toBeNull();
  });

  it('rejects a cycle', () => {
    expect(
      connectionToOp(graph, { source: 'steps[1]', target: 'steps[0]' })
    ).toBeNull();
  });

  it('ignores graphs with fixed edges', () => {
    expect(
      connectionToOp(
        { ...graph, editableEdges: false },
        { source: 'steps[1]', target: 'steps[2]' }
      )
    ).toBeNull();
  });
});

const [depEdge] = graph.edges as [CanvasEdgeWithRef];
const [routeEdge] = routerGraph.edges as [CanvasEdgeWithRef];

describe('edge deletion', () => {
  const inferred: CanvasEdgeWithRef = {
    id: 'inferred:steps[0]->steps[2]',
    source: 'steps[0]',
    target: 'steps[2]',
    kind: 'inferred',
  };
  const order: CanvasEdgeWithRef = { ...inferred, id: 'o', kind: 'order' };

  it('removes a dependency', () => {
    expect(edgeDeleteOp(graph, depEdge)).toEqual({
      kind: 'removeDependency',
      step: 'steps[1]',
      dependsOn: 'steps[0]',
    });
  });

  it('removes a route target and keeps the others', () => {
    expect(edgeDeleteOp(routerGraph, routeEdge)).toEqual({
      kind: 'setRouteTargets',
      step: 'steps[0]',
      pattern: '5xx',
      targets: ['missing'],
    });
  });

  it('never emits for inferred and order edges', () => {
    expect(edgeDeleteOp(graph, inferred)).toBeNull();
    expect(edgeDeleteOp(graph, order)).toBeNull();
    expect(edgeLockReason(graph, inferred)).toMatch(/^Inferred/);
    expect(edgeLockReason(graph, order)).toMatch(/^Chain DAGs/);
  });

  // The steps of an agent DAG hang from the agent and have no order.
  it('never emits for an edge from the agent, and says why', () => {
    const fromAgent = {
      id: 'agent:agent->steps[0]',
      source: 'agent',
      target: 'steps[0]',
      kind: 'agent' as const,
    };
    expect(edgeDeleteOp(graph, fromAgent)).toBeNull();
    expect(edgeLockReason(graph, fromAgent)).toMatch(/^The agent chooses/);
  });

  it('locks every edge of a graph with fixed edges', () => {
    const fixed = { ...graph, editableEdges: false };
    expect(edgeLockReason(fixed, depEdge)).toMatch(/DAG type/);
    expect(edgeDeleteOp(fixed, depEdge)).toBeNull();
    expect(edgeLockReason(graph, depEdge)).toBeNull();
  });
});

describe('selectionDeleteOps', () => {
  it('deletes steps with rewire', () => {
    expect(stepDeleteOp('steps[2]')).toEqual({
      kind: 'deleteStep',
      step: 'steps[2]',
      rewire: true,
    });
    expect(
      selectionDeleteOps(graph, {
        nodes: ['steps[1]'],
        edgeIds: [depEdge.id],
      })
    ).toEqual([{ kind: 'deleteStep', step: 'steps[1]', rewire: true }]);
  });

  it('deletes edges when no step is selected', () => {
    expect(
      selectionDeleteOps(graph, {
        nodes: [],
        edgeIds: [depEdge.id, 'unknown'],
      })
    ).toEqual([
      { kind: 'removeDependency', step: 'steps[1]', dependsOn: 'steps[0]' },
    ]);
  });
});

describe('added steps', () => {
  const [, http] = catalogSample as [
    (typeof catalogSample)[number],
    (typeof catalogSample)[number],
  ];

  it('adds after the selected step', () => {
    const op = paletteAddOp(http, 'steps[1]');
    expect(op).toEqual({
      kind: 'addStep',
      step: { action: 'http.request', with: { method: 'GET', url: '' } },
      after: 'steps[1]',
    });
    expect(op.kind === 'addStep' && op.step).not.toBe(http.template);
    expect(paletteAddOp(http, null)).toMatchObject({ after: null });
  });

  it('adds a child of the drop source', () => {
    expect(dropAddOp(http, 'steps[0]')).toEqual({
      kind: 'addStep',
      step: http.template,
      dependsOn: ['steps[0]'],
    });
  });

  it('inserts on a dependency edge', () => {
    expect(canInsertOnEdge(graph, depEdge)).toBe(true);
    expect(canInsertOnEdge(routerGraph, routeEdge)).toBe(false);
    expect(canInsertOnEdge({ ...graph, editableEdges: false }, depEdge)).toBe(
      false
    );
    // An inferred edge takes the same buttons as a standard link.
    const inferredEdge: CanvasEdge = { ...depEdge, id: 'i', kind: 'inferred' };
    expect(canInsertOnEdge(graph, inferredEdge)).toBe(true);
    expect(
      canInsertOnEdge({ ...graph, editableEdges: false }, inferredEdge)
    ).toBe(false);
    expect(canInsertOnEdge(graph, { ...depEdge, kind: 'order' })).toBe(false);
    expect(insertOnEdgeOp(http, depEdge)).toEqual({
      kind: 'addStep',
      step: http.template,
      insertBetween: { from: 'steps[0]', to: 'steps[1]' },
    });
  });
});

describe('droppedConnectionSource', () => {
  const end = {
    isValid: null,
    fromNode: { id: 'steps[1]' },
    fromHandle: { type: 'source' as const },
    toNode: null,
  };

  it('gives the source of a drop on empty space', () => {
    expect(droppedConnectionSource(graph, end)).toBe('steps[1]');
  });

  it('ignores drops on a node, target handles and fixed graphs', () => {
    expect(
      droppedConnectionSource(graph, { ...end, toNode: { id: 'steps[2]' } })
    ).toBeNull();
    expect(
      droppedConnectionSource(graph, { ...end, isValid: true })
    ).toBeNull();
    expect(
      droppedConnectionSource(graph, {
        ...end,
        fromHandle: { type: 'target' },
      })
    ).toBeNull();
    expect(
      droppedConnectionSource({ ...graph, editableEdges: false }, end)
    ).toBeNull();
    expect(
      droppedConnectionSource(graph, {
        ...end,
        fromNode: { id: 'handler_on.failure' },
      })
    ).toBeNull();
  });
});

describe('refusedConnection', () => {
  // a -> b: a drag from b to a closes a cycle.
  const end = {
    isValid: false,
    fromNode: { id: 'steps[1]' },
    fromHandle: { type: 'source' as const },
    toNode: { id: 'steps[0]' },
  };

  it('names a cycle and a self link', () => {
    expect(refusedConnection(graph, end)).toBe('cycle');
    expect(
      refusedConnection(graph, { ...end, toNode: { id: 'steps[1]' } })
    ).toBe('cycle');
  });

  it('reads a drag that starts at a target handle the other way', () => {
    expect(
      refusedConnection(graph, {
        ...end,
        fromNode: { id: 'steps[0]' },
        fromHandle: { type: 'target' },
        toNode: { id: 'steps[1]' },
      })
    ).toBe('cycle');
  });

  it('is silent for other drops', () => {
    expect(refusedConnection(graph, { ...end, isValid: true })).toBeNull();
    expect(refusedConnection(graph, { ...end, isValid: null })).toBeNull();
    expect(refusedConnection(graph, { ...end, toNode: null })).toBeNull();
    // An edge that exists already is no cycle.
    expect(
      refusedConnection(graph, {
        ...end,
        fromNode: { id: 'steps[0]' },
        toNode: { id: 'steps[1]' },
      })
    ).toBeNull();
    expect(
      refusedConnection({ ...graph, editableEdges: false }, end)
    ).toBeNull();
    expect(
      refusedConnection(graph, { ...end, toNode: { id: 'handler_on.failure' } })
    ).toBeNull();
  });
});

describe('paletteTargetOp', () => {
  const entry = catalogSample[0]!;

  // The step goes after the selected one in the file and depends on it.
  it('adds after the selected step and depends on it', () => {
    expect(
      paletteTargetOp(graph, 'steps[1]', { mode: 'after' }, entry)
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      after: 'steps[1]',
      dependsOn: ['steps[1]'],
    });
  });

  it('adds after a selected handler step without a dependency', () => {
    expect(
      paletteTargetOp(graph, 'handler_on.failure', { mode: 'after' }, entry)
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      after: 'handler_on.failure',
    });
  });

  it('adds after the selected step of a chain without a dependency', () => {
    const chain = { ...graph, editableEdges: false };
    expect(
      paletteTargetOp(chain, 'steps[1]', { mode: 'after' }, entry)
    ).toEqual({ kind: 'addStep', step: entry.template, after: 'steps[1]' });
  });

  // With no selection, or the agent node, the new step goes at the end and
  // depends on the last step that nothing depends on: a -> b and c alone.
  it('chains after the last leaf with no selection or the agent node', () => {
    const chained = {
      kind: 'addStep',
      step: entry.template,
      dependsOn: ['steps[2]'],
    };
    expect(paletteTargetOp(graph, null, { mode: 'after' }, entry)).toEqual(
      chained
    );
    expect(
      paletteTargetOp(graph, AGENT_NODE_KEY, { mode: 'after' }, entry)
    ).toEqual(chained);
  });

  it('adds at the end of a chain without editable edges', () => {
    const chain = { ...graph, editableEdges: false };
    expect(paletteTargetOp(chain, null, { mode: 'after' }, entry)).toEqual({
      kind: 'addStep',
      step: entry.template,
      after: null,
    });
    expect(lastLeafOf(chain)).toBeNull();
    expect(lastLeafOf(graph)).toBe('steps[2]');
    expect(lastLeafOf({ ...graph, nodes: [], edges: [] })).toBeNull();
  });

  it('adds a child of the source step', () => {
    expect(
      paletteTargetOp(graph, null, { mode: 'child', source: 'steps[0]' }, entry)
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      dependsOn: ['steps[0]'],
    });
  });

  it('inserts on a known edge only', () => {
    expect(
      paletteTargetOp(
        graph,
        null,
        { mode: 'insert', edgeId: 'depends:steps[0]->steps[1]' },
        entry
      )
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      insertBetween: { from: 'steps[0]', to: 'steps[1]' },
    });
    expect(
      paletteTargetOp(graph, null, { mode: 'insert', edgeId: 'nope' }, entry)
    ).toBeNull();
  });

  it('adds a step that depends on the router for a route target', () => {
    expect(
      paletteTargetOp(
        graph,
        'steps[2]',
        { mode: 'route', router: 'steps[0]', port: '2xx' },
        entry
      )
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      dependsOn: ['steps[0]'],
    });
  });

  // The new step has the source of the link and nothing depends on it.
  it('adds a step beside the target of a link', () => {
    expect(
      paletteTargetOp(
        graph,
        null,
        { mode: 'parallel', edgeId: 'depends:steps[0]->steps[1]' },
        entry
      )
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      after: 'steps[1]',
      dependsOn: ['steps[0]'],
    });
    expect(
      paletteTargetOp(graph, null, { mode: 'parallel', edgeId: 'nope' }, entry)
    ).toBeNull();
  });

  it('adds no parallel step in a chain: it has no depends', () => {
    const chain = { ...graph, editableEdges: false };
    expect(
      paletteTargetOp(
        chain,
        null,
        { mode: 'parallel', edgeId: 'depends:steps[0]->steps[1]' },
        entry
      )
    ).toBeNull();
  });

  it('adds before the first step for the first target, whatever the selection', () => {
    expect(
      paletteTargetOp(graph, 'steps[1]', { mode: 'first' }, entry)
    ).toEqual({
      kind: 'addStep',
      step: entry.template,
      after: null,
      atStart: true,
    });
  });

  it('gives the edit its own copy of the template', () => {
    const op = paletteTargetOp(graph, null, { mode: 'first' }, entry);
    expect(op?.kind === 'addStep' && op.step).not.toBe(entry.template);
  });
});

// The edits of the "+" stalks and of the empty tile, on YAML text.
describe('paletteTargetOp on text', () => {
  const entry = catalogSample[0]!;

  function graphOf(text: string): CanvasGraph {
    const model = parseDagDoc(text);
    return buildCanvasGraph({
      model,
      names: resolveStepNames(model),
      issues: { byStep: {}, dag: [] },
    });
  }

  it('adds the first step of an empty workflow and keeps comments', () => {
    const text = 'name: empty\n# Steps come later.\nsteps: []\n';
    const op = paletteTargetOp(graphOf(text), null, { mode: 'first' }, entry);
    const result = applyEdit(text, op!);
    expect(result.ok).toBe(true);
    const after = result.ok ? result.text : '';
    expect(after).toContain('# Steps come later.');
    expect(parseDagDoc(after).steps).toHaveLength(1);
  });

  it('routes the port to the new step and keeps comments', () => {
    const text = [
      'name: routed',
      'steps:',
      '  - name: pick',
      '    action: router.route',
      '    with:',
      '      value: ok',
      '      routes:',
      '        ok: [done] # The happy path.',
      '        fail: []',
      '  # Last step.',
      '  - name: done',
      '    run: echo ok',
      '',
    ].join('\n');
    const graph = graphOf(text);
    const target = { mode: 'route', router: 'steps[0]', port: 'fail' } as const;
    // The stalk shows on the port without targets only.
    expect(graph.nodes[0]?.routes).toEqual(['ok', 'fail']);
    expect(toFlowNodes(graph, {}, null)[0]?.data.openPorts).toEqual(['fail']);

    const added = applyEdit(text, paletteTargetOp(graph, null, target, entry)!);
    expect(added.ok).toBe(true);
    if (!added.ok) return;
    const name = parseDagDoc(added.text).steps.find(
      (step) => step.key === added.select
    )?.explicitName;
    expect(name).toBeTruthy();
    // The host writes the route after the add, as the editor does.
    const routed = applyEdit(added.text, {
      kind: 'setRouteTargets',
      step: target.router,
      pattern: target.port,
      targets: [...routeTargetsOf(graph, target.router, target.port), name!],
    });
    expect(routed.ok).toBe(true);
    const after = routed.ok ? routed.text : '';
    expect(after).toContain(`fail: [${name}]`);
    expect(after).toContain('ok: [done] # The happy path.');
    expect(after).toContain('# Last step.');
  });
});

// The parallel step and the restored link, on YAML text.
describe('parallel step and restored link on text', () => {
  const entry = catalogSample[0]!;

  function graphOf(text: string): CanvasGraph {
    const model = parseDagDoc(text);
    return buildCanvasGraph({
      model,
      names: resolveStepNames(model),
      issues: { byStep: {}, dag: [] },
    });
  }

  it('writes a sibling of the target in a graph DAG', () => {
    const text = [
      'name: g',
      'steps:',
      '  - name: a',
      '    run: echo a',
      '  - name: b',
      '    depends: [a]',
      '    run: echo b',
      '  - name: c',
      '    depends: [b]',
      '    run: echo c',
      '',
    ].join('\n');
    const graph = graphOf(text);
    const op = paletteTargetOp(
      graph,
      null,
      { mode: 'parallel', edgeId: 'depends:steps[0]->steps[1]' },
      entry
    );
    const result = applyEdit(text, op!);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const steps = parseDagDoc(result.text).steps;
    // The new step follows b in the file, depends on a only, and no step
    // depends on it.
    expect(steps.map((step) => step.explicitName)).toEqual([
      'a',
      'b',
      steps[2]!.explicitName,
      'c',
    ]);
    expect(steps[2]!.depends).toEqual(['a']);
    expect(steps[1]!.depends).toEqual(['a']);
    expect(steps[3]!.depends).toEqual(['b']);
    expect(result.select).toBe('steps[2]');
  });

  it('refuses the same edit in a chain DAG', () => {
    const text =
      'type: chain\nsteps:\n  - name: a\n    run: x\n  - name: b\n    run: y\n';
    const result = applyEdit(text, {
      kind: 'addStep',
      step: entry.template,
      after: 'steps[1]',
      dependsOn: ['steps[0]'],
    });
    expect(result.ok).toBe(false);
    expect(!result.ok && result.code).toBe('chainType');
  });

  // b reads ${steps.a.outputs.x}: deleting the link leaves the inferred edge,
  // and the restore button writes the link again.
  it('restores a deleted link that is still inferred', () => {
    const text = [
      'name: g',
      'steps:',
      '  - name: a',
      '    run: echo a',
      '    output: x',
      '  - name: b',
      '    depends: [a]',
      '    run: echo ${steps.a.outputs.x}',
      '',
    ].join('\n');
    const removed = applyEdit(text, {
      kind: 'removeDependency',
      step: 'steps[1]',
      dependsOn: 'steps[0]',
    });
    expect(removed.ok).toBe(true);
    if (!removed.ok) return;
    const model = parseDagDoc(removed.text);
    const graph = buildCanvasGraph({
      model,
      names: resolveStepNames(model),
      issues: { byStep: {}, dag: [] },
      details: {
        steps: [{ name: 'b', inferredDepends: ['a'] }],
      } as never,
    });
    const inferred = graph.edges.find((edge) => edge.kind === 'inferred');
    expect(inferred).toBeDefined();
    const op = connectionToOp(graph, {
      source: inferred!.source,
      target: inferred!.target,
    });
    expect(op).toEqual({
      kind: 'addDependency',
      step: 'steps[1]',
      dependsOn: 'steps[0]',
    });
    const restored = applyEdit(removed.text, op!);
    expect(restored.ok).toBe(true);
    if (!restored.ok) return;
    expect(parseDagDoc(restored.text).steps[1]!.depends).toEqual(['a']);
    // Once the link is written, no edge is inferred any more.
    const again = parseDagDoc(restored.text);
    const after = buildCanvasGraph({
      model: again,
      names: resolveStepNames(again),
      issues: { byStep: {}, dag: [] },
      details: {
        steps: [{ name: 'b', inferredDepends: ['a'] }],
      } as never,
    });
    expect(after.edges.map((edge) => edge.kind)).toEqual(['depends']);
  });

  // b reads ${steps.a.outputs.x} and lists no depends: the insert must still
  // make b wait for the new step, as the ghost on the edge shows.
  it('inserts a step on an inferred edge before its target', () => {
    const text = [
      'name: g',
      'steps:',
      '  - name: a',
      '    run: echo a',
      '    output: x',
      '  - name: b',
      '    run: echo ${steps.a.outputs.x}',
      '',
    ].join('\n');
    const model = parseDagDoc(text);
    const graph = buildCanvasGraph({
      model,
      names: resolveStepNames(model),
      issues: { byStep: {}, dag: [] },
      details: { steps: [{ name: 'b', inferredDepends: ['a'] }] } as never,
    });
    const inferred = graph.edges.find((edge) => edge.kind === 'inferred');
    expect(inferred).toBeDefined();
    const op = paletteTargetOp(
      graph,
      null,
      { mode: 'insert', edgeId: inferred!.id },
      entry
    );
    const result = applyEdit(text, op!);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    const steps = parseDagDoc(result.text).steps;
    const added = steps[1]!.explicitName!;
    expect(steps[1]!.depends).toEqual(['a']);
    expect(steps[2]!.depends).toEqual([added]);
    expect(result.text).toContain('run: echo ${steps.a.outputs.x}');
  });

  it('gives no restore op in a chain DAG', () => {
    const chain: CanvasGraph = {
      ...graphOf(yamlChainText),
      editableEdges: false,
    };
    expect(
      connectionToOp(chain, { source: 'steps[0]', target: 'steps[1]' })
    ).toBeNull();
  });
});

const yamlChainText =
  'type: chain\nsteps:\n  - name: a\n    run: x\n  - name: b\n    run: y\n';
