// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasGraph, PaletteTarget } from '../../contracts';
import { parseDagDoc, resolveStepNames } from '../../doc';
import {
  catalogSample,
  yamlArraySteps,
  yamlChain,
  yamlRouter,
} from '../../testing/fixtures';
import { ghostFlowEdges, toFlowNodes, withGhostLook } from '../flow';
import { buildCanvasGraph } from '../graph';
import { GHOST_KEY, ghostLookOf, isGhostKey, planGhost } from '../ghost';
import { layoutGraph, nodeSize } from '../layout';
import { routeHandleId } from '../types';

function graphOf(text: string): CanvasGraph {
  const model = parseDagDoc(text);
  return buildCanvasGraph({
    model,
    names: resolveStepNames(model),
    issues: { byStep: {}, dag: [] },
  });
}

// fetch -> transform -> publish
const chain3 = graphOf(yamlArraySteps);
const FETCH = 'steps[0]';
const TRANSFORM = 'steps[1]';
const PUBLISH = 'steps[2]';

function pairs(plan: ReturnType<typeof planGhost>): string[] {
  return (plan?.edges ?? []).map((edge) => `${edge.source}>${edge.target}`);
}

describe('planGhost', () => {
  it('puts the ghost after the selected step', () => {
    const plan = planGhost(chain3, TRANSFORM, { mode: 'after' });
    expect(pairs(plan)).toEqual([`${TRANSFORM}>${GHOST_KEY}`]);
    expect(plan?.hidden).toEqual([]);
    expect(plan?.graph.nodes.map((node) => node.key)).toEqual([
      FETCH,
      TRANSFORM,
      GHOST_KEY,
      PUBLISH,
    ]);
  });

  it('puts the ghost at the end, after the last leaf, with no selection', () => {
    expect(pairs(planGhost(chain3, null, { mode: 'after' }))).toEqual([
      `${PUBLISH}>${GHOST_KEY}`,
    ]);
  });

  it('puts the ghost after the step of a child target', () => {
    expect(
      pairs(planGhost(chain3, null, { mode: 'child', source: FETCH }))
    ).toEqual([`${FETCH}>${GHOST_KEY}`]);
  });

  it('puts the ghost between the two steps of a link and hides the link', () => {
    const edgeId = `depends:${FETCH}->${TRANSFORM}`;
    const plan = planGhost(chain3, null, { mode: 'insert', edgeId });
    expect(pairs(plan)).toEqual([
      `${FETCH}>${GHOST_KEY}`,
      `${GHOST_KEY}>${TRANSFORM}`,
    ]);
    expect(plan?.hidden).toEqual([edgeId]);
    expect(plan?.graph.edges.some((edge) => edge.id === edgeId)).toBe(false);
  });

  // The new step has the source of the link and no child.
  it('puts the ghost beside the target of a link', () => {
    const edgeId = `depends:${FETCH}->${TRANSFORM}`;
    const plan = planGhost(chain3, null, { mode: 'parallel', edgeId });
    expect(pairs(plan)).toEqual([`${FETCH}>${GHOST_KEY}`]);
    expect(plan?.hidden).toEqual([]);
    expect(plan?.graph.edges.some((edge) => edge.id === edgeId)).toBe(true);
    // The ghost shares the rank of the target.
    const positions = layoutGraph(plan!.graph, 'LR');
    expect(positions[GHOST_KEY]?.x).toBe(positions[TRANSFORM]?.x);
    expect(positions[GHOST_KEY]?.y).not.toBe(positions[TRANSFORM]?.y);
  });

  it('puts a first ghost with no edge in a graph with edges', () => {
    const plan = planGhost(chain3, TRANSFORM, { mode: 'first' });
    expect(plan?.edges).toEqual([]);
    expect(plan?.graph.nodes[0]?.key).toBe(GHOST_KEY);
  });

  it('puts the ghost on a route port, leaving from the port', () => {
    const graph = graphOf(yamlRouter);
    const plan = planGhost(graph, null, {
      mode: 'route',
      router: 'steps[0]',
      port: 're:.*',
    });
    expect(plan?.edges).toEqual([
      {
        id: 'ghost:steps[0]>',
        source: 'steps[0]',
        target: GHOST_KEY,
        port: 're:.*',
      },
    ]);
    expect(ghostFlowEdges(plan)[0]?.sourceHandle).toBe(routeHandleId('re:.*'));
  });

  describe('in a chain DAG', () => {
    const chain = graphOf(yamlChain);

    it('puts the ghost between the selected step and the next one', () => {
      const plan = planGhost(chain, 'steps[0]', { mode: 'after' });
      expect(pairs(plan)).toEqual([
        `steps[0]>${GHOST_KEY}`,
        `${GHOST_KEY}>steps[1]`,
      ]);
      expect(plan?.hidden).toEqual(['order:steps[0]->steps[1]']);
    });

    it('puts the ghost after the last step with no selection', () => {
      const plan = planGhost(chain, null, { mode: 'after' });
      expect(pairs(plan)).toEqual([`steps[2]>${GHOST_KEY}`]);
      expect(plan?.hidden).toEqual([]);
    });

    it('puts the ghost before the first step', () => {
      const plan = planGhost(chain, null, { mode: 'first' });
      expect(pairs(plan)).toEqual([`${GHOST_KEY}>steps[0]`]);
    });

    it('has no parallel ghost: a chain has no depends to share', () => {
      expect(
        planGhost(chain, null, {
          mode: 'parallel',
          edgeId: 'order:steps[0]->steps[1]',
        })
      ).toBeNull();
    });
  });

  it('has no child or route ghost in a chain: it has no depends', () => {
    const chain = graphOf(yamlChain);
    expect(
      planGhost(chain, null, { mode: 'child', source: 'steps[0]' })
    ).toBeNull();
    expect(
      planGhost(chain, null, { mode: 'route', router: 'steps[0]', port: 'a' })
    ).toBeNull();
  });

  it('keeps the graph and its edges unchanged', () => {
    const before = JSON.stringify(chain3);
    planGhost(chain3, FETCH, { mode: 'after' });
    expect(JSON.stringify(chain3)).toBe(before);
  });

  it('gives no ghost for an unknown link or an agent graph', () => {
    const target: PaletteTarget = { mode: 'insert', edgeId: 'nope' };
    expect(planGhost(chain3, null, target)).toBeNull();
    const agent: CanvasGraph = {
      ...chain3,
      nodes: [{ ...chain3.nodes[0]!, kind: 'agent' }, ...chain3.nodes.slice(1)],
    };
    expect(planGhost(agent, null, { mode: 'after' })).toBeNull();
  });

  it('adds the ghost to an empty graph', () => {
    const empty: CanvasGraph = { nodes: [], edges: [], editableEdges: true };
    const plan = planGhost(empty, null, { mode: 'after' });
    expect(plan?.graph.nodes.map((node) => node.key)).toEqual([GHOST_KEY]);
    expect(plan?.edges).toEqual([]);
  });
});

describe('ghost layout', () => {
  it('makes room: the steps after the ghost move one rank', () => {
    const base = layoutGraph(chain3, 'LR');
    const plan = planGhost(chain3, FETCH, { mode: 'after' })!;
    // fetch -> ghost, and fetch -> transform stays: the ghost is a sibling.
    const sibling = layoutGraph(plan.graph, 'LR');
    expect(sibling[TRANSFORM]?.x).toBe(base[TRANSFORM]?.x);
    const edgeId = `depends:${FETCH}->${TRANSFORM}`;
    const inserted = layoutGraph(
      planGhost(chain3, null, { mode: 'insert', edgeId })!.graph,
      'LR'
    );
    expect(inserted[TRANSFORM]!.x).toBeGreaterThan(base[TRANSFORM]!.x);
    expect(inserted[GHOST_KEY]!.x).toBeGreaterThan(inserted[FETCH]!.x);
    expect(inserted[GHOST_KEY]!.x).toBeLessThan(inserted[TRANSFORM]!.x);
  });

  it('draws the ghost as a plain card, even with the router icon', () => {
    const plan = planGhost(chain3, null, { mode: 'after' })!;
    const ghost = plan.graph.nodes.find((node) => isGhostKey(node.key))!;
    expect(nodeSize(ghost)).toEqual({ width: 240, height: 64 });
    expect(nodeSize(ghost, true)).toEqual({ width: 56, height: 56 });
    const flow = toFlowNodes(plan.graph, layoutGraph(plan.graph, 'LR'), null);
    expect(flow.find((node) => node.id === GHOST_KEY)?.type).toBe('ghost');
  });
});

describe('ghost look', () => {
  const router = {
    ...catalogSample[0]!,
    name: 'router.route',
    title: 'Router',
  };

  it('takes the title and the action of a choice', () => {
    expect(ghostLookOf(catalogSample[0]!)).toEqual({
      title: 'Run command',
      action: 'run',
      kind: 'run',
    });
    expect(ghostLookOf(router).kind).toBe('router');
  });

  it('sets the look on the ghost node only', () => {
    const plan = planGhost(chain3, null, { mode: 'after' })!;
    const nodes = toFlowNodes(plan.graph, {}, null);
    const look = ghostLookOf(catalogSample[0]!);
    const shown = withGhostLook(nodes, look);
    expect(shown.find((node) => node.id === GHOST_KEY)?.data.ghost).toBe(look);
    expect(shown.find((node) => node.id === FETCH)?.data.ghost).toBeUndefined();
    // The same nodes stay when the look is cleared.
    expect(
      withGhostLook(shown, null).find((node) => node.id === GHOST_KEY)?.data
        .ghost
    ).toBeNull();
  });

  it('draws the ghost edges as ghost edges', () => {
    const plan = planGhost(chain3, FETCH, { mode: 'after' });
    const edges = ghostFlowEdges(plan);
    expect(edges.map((edge) => edge.type)).toEqual(['ghost']);
    expect(edges[0]?.selected).toBe(false);
    expect(ghostFlowEdges(null)).toEqual([]);
  });
});
