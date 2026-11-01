// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ApiDAGDetails, ApiStep } from '../../contracts';
import { buildDetailsGraph } from '../fromDetails';

const dag = (steps: Partial<ApiStep>[], extra: object = {}): ApiDAGDetails =>
  ({ name: 'child', steps, ...extra }) as ApiDAGDetails;

describe('buildDetailsGraph', () => {
  it('makes a node for each step, keyed by its name, and no editable edge', () => {
    const graph = buildDetailsGraph(
      dag([
        { name: 'say', commands: [{ command: 'echo', args: ['hello', 'x'] }] },
        { name: 'shout', depends: ['say'], script: '\n  echo HI\n' },
      ])
    );

    expect(graph.nodes.map((node) => node.key)).toEqual(['say', 'shout']);
    expect(graph.nodes[0]).toMatchObject({
      label: 'say',
      kind: 'run',
      lane: 'main',
      subtitle: 'echo hello x',
    });
    expect(graph.nodes[1]?.subtitle).toBe('echo HI');
    expect(graph.edges).toEqual([
      {
        id: 'depends:say->shout',
        source: 'say',
        target: 'shout',
        kind: 'depends',
      },
    ]);
    expect(graph.editableEdges).toBe(false);
  });

  it('shows a sub-DAG call and a fan-out as the editor does', () => {
    const graph = buildDetailsGraph(
      dag([
        { name: 'greet', call: 'greeter' },
        { name: 'many', call: 'worker', parallel: { items: ['a', 'b'] } },
      ])
    );

    expect(graph.nodes[0]).toMatchObject({
      kind: 'call',
      subDag: 'greeter',
      subtitle: '→ greeter',
    });
    expect(graph.nodes[0]?.parallel).toBeUndefined();
    expect(graph.nodes[1]).toMatchObject({
      kind: 'call',
      subDag: 'worker',
      parallel: true,
    });
  });

  it('shows a router with its patterns, its value and route edges', () => {
    const graph = buildDetailsGraph(
      dag([
        {
          name: 'route',
          router: {
            value: '${STATUS}',
            routes: [
              { pattern: 'ok', targets: ['good'] },
              { pattern: 're:^f', targets: ['bad'] },
            ],
          },
        },
        { name: 'good', depends: ['route'] },
        { name: 'bad', depends: ['route'] },
      ])
    );

    expect(graph.nodes[0]).toMatchObject({
      kind: 'router',
      routes: ['ok', 're:^f'],
      routeValue: '${STATUS}',
    });
    // The dependency of a routed step is the route edge, not a second edge.
    expect(
      graph.edges.map((edge) => [
        edge.kind,
        edge.source,
        edge.target,
        edge.label,
      ])
    ).toEqual([
      ['route', 'route', 'good', 'ok'],
      ['route', 'route', 'bad', 're:^f'],
    ]);
  });

  it('draws an inferred dependency that depends does not give', () => {
    const graph = buildDetailsGraph(
      dag([
        { name: 'a' },
        { name: 'b', depends: ['a'], inferredDepends: ['a', 'c'] },
        { name: 'c' },
      ])
    );

    expect(graph.edges.map((edge) => edge.id)).toEqual([
      'depends:a->b',
      'inferred:c->b',
    ]);
  });

  it('ignores a dependency on a step that does not exist', () => {
    const graph = buildDetailsGraph(dag([{ name: 'a', depends: ['missing'] }]));
    expect(graph.edges).toEqual([]);
  });

  it('puts the lifecycle handlers in their own lane', () => {
    const graph = buildDetailsGraph(
      dag([{ name: 'a' }], {
        handlerOn: { failure: { name: 'notify' }, exit: { name: 'cleanup' } },
      })
    );

    expect(
      graph.nodes.map((node) => [node.key, node.lane, node.label])
    ).toEqual([
      ['a', 'main', 'a'],
      ['handler_on.failure', 'handler', 'notify'],
      ['handler_on.exit', 'handler', 'cleanup'],
    ]);
  });

  it('gives an empty graph for a DAG without steps', () => {
    expect(buildDetailsGraph({ name: 'x' } as ApiDAGDetails)).toEqual({
      nodes: [],
      edges: [],
      editableEdges: false,
    });
  });
});
