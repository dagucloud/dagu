// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasGraph, CanvasNode } from '../../contracts';
import { layoutGraph } from '../../canvas/layout';
import { toFlowNodes, withLauncherStates } from '../../canvas/flow';
import {
  isLauncherKey,
  launcherIdOf,
  launcherKey,
  launcherStates,
  startSteps,
  withLaunchers,
} from '../model';

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

function graphOf(keys: string[], pairs: string[] = []): CanvasGraph {
  return {
    nodes: keys.map((key) => node(key)),
    edges: pairs.map((pair) => {
      const [source = '', target = ''] = pair.split('>');
      return {
        id: `depends:${source}->${target}`,
        source,
        target,
        kind: 'depends' as const,
      };
    }),
    editableEdges: true,
  };
}

describe('launcherStates', () => {
  it('keeps Manual active and sets Cron and Webhook dashed by default', () => {
    expect(
      launcherStates({
        scheduleCount: 0,
        webhookCount: 0,
        webhooksAvailable: true,
      })
    ).toEqual([
      { id: 'manual', status: 'active' },
      { id: 'cron', status: 'dashed', count: 0 },
      { id: 'webhook', status: 'dashed' },
    ]);
  });

  it('draws Cron solid with a schedule', () => {
    const states = launcherStates({
      scheduleCount: 2,
      webhookCount: 0,
      webhooksAvailable: true,
    });
    expect(states.find((s) => s.id === 'cron')?.status).toBe('active');
  });

  it('gives Cron the number of expressions of the schedule', () => {
    const states = launcherStates({
      scheduleCount: 2,
      webhookCount: 0,
      webhooksAvailable: true,
    });
    expect(states.find((s) => s.id === 'cron')?.count).toBe(2);
    expect(states.find((s) => s.id === 'manual')?.count).toBeUndefined();
  });

  it('draws Webhook solid with one webhook or more', () => {
    const states = launcherStates({
      scheduleCount: 0,
      webhookCount: 1,
      webhooksAvailable: true,
    });
    expect(states.find((s) => s.id === 'webhook')?.status).toBe('active');
  });

  it('draws Webhook dashed while the webhook is not known', () => {
    const states = launcherStates({
      scheduleCount: 0,
      webhookCount: null,
      webhooksAvailable: true,
    });
    expect(states.find((s) => s.id === 'webhook')?.status).toBe('dashed');
  });

  it('hides Webhook when the instance has no webhooks', () => {
    const states = launcherStates({
      scheduleCount: 1,
      webhookCount: 3,
      webhooksAvailable: false,
    });
    expect(states.map((s) => s.id)).toEqual(['manual', 'cron']);
  });
});

describe('launcher keys', () => {
  it('tells launchers from steps', () => {
    expect(isLauncherKey(launcherKey('cron'))).toBe(true);
    expect(isLauncherKey('steps[0]')).toBe(false);
    expect(launcherIdOf('launcher:webhook')).toBe('webhook');
    expect(launcherIdOf('launcher:other')).toBeNull();
    expect(launcherIdOf('steps[0]')).toBeNull();
  });
});

describe('startSteps', () => {
  it('finds the steps that nothing reaches', () => {
    const graph = graphOf(['a', 'b', 'c', 'd'], ['a>c', 'b>c']);
    expect(startSteps(graph)).toEqual(['a', 'b', 'd']);
  });

  it('gives the first step of a chain DAG', () => {
    const graph: CanvasGraph = {
      ...graphOf(['a', 'b', 'c']),
      edges: [
        { id: 'o1', source: 'a', target: 'b', kind: 'order' },
        { id: 'o2', source: 'b', target: 'c', kind: 'order' },
      ],
    };
    expect(startSteps(graph)).toEqual(['a']);
  });

  it('ignores handlers and gives nothing for an agent graph', () => {
    const handlers: CanvasGraph = {
      ...graphOf(['a']),
      nodes: [node('a'), node('handler_on.failure', { lane: 'handler' })],
    };
    expect(startSteps(handlers)).toEqual(['a']);
    const agent: CanvasGraph = {
      nodes: [node('agent', { kind: 'agent' }), node('tool')],
      edges: [],
      editableEdges: false,
    };
    expect(startSteps(agent)).toEqual([]);
  });
});

describe('withLaunchers', () => {
  const states = launcherStates({
    scheduleCount: 0,
    webhookCount: 0,
    webhooksAvailable: true,
  });

  it('connects every launcher to every start step', () => {
    const graph = graphOf(['a', 'b', 'c'], ['a>c', 'b>c']);
    const launched = withLaunchers(graph, states);
    expect(launched.edges).toHaveLength(6);
    expect(launched.edges.map((e) => `${e.source}>${e.target}`)).toContain(
      'launcher:cron>b'
    );
    // The graph of the edits is untouched.
    expect(graph.edges).toHaveLength(2);
    expect(graph.nodes).toHaveLength(3);
  });

  it('adds nothing to an agent graph or to a graph without steps', () => {
    const agent: CanvasGraph = {
      nodes: [node('agent', { kind: 'agent' }), node('tool')],
      edges: [],
      editableEdges: false,
    };
    expect(withLaunchers(agent, states).graph).toBe(agent);
    const empty = graphOf([]);
    expect(withLaunchers(empty, states).graph).toBe(empty);
    expect(withLaunchers(graphOf(['a']), []).edges).toEqual([]);
  });

  it('puts the launchers before the start steps in the layout', () => {
    const graph = graphOf(['a', 'b'], ['a>b']);
    const { graph: launched } = withLaunchers(graph, states);
    const lr = layoutGraph(launched, 'LR');
    for (const id of ['manual', 'cron', 'webhook']) {
      expect(lr[`launcher:${id}`]!.x).toBeLessThan(lr['a']!.x);
    }
    expect(lr['a']!.x).toBeLessThan(lr['b']!.x);
    const tb = layoutGraph(launched, 'TB');
    for (const id of ['manual', 'cron', 'webhook']) {
      expect(tb[`launcher:${id}`]!.y).toBeLessThan(tb['a']!.y);
    }
  });

  it('lays a chain DAG out under its launchers', () => {
    const chain: CanvasGraph = {
      ...graphOf(['a', 'b', 'c']),
      edges: [
        { id: 'o1', source: 'a', target: 'b', kind: 'order' },
        { id: 'o2', source: 'b', target: 'c', kind: 'order' },
      ],
    };
    const { graph } = withLaunchers(chain, states);
    const tb = layoutGraph(graph, 'TB');
    expect(tb['launcher:manual']!.y).toBeLessThan(tb['a']!.y);
    expect(tb['a']!.y).toBeLessThan(tb['b']!.y);
    expect(tb['b']!.y).toBeLessThan(tb['c']!.y);
  });

  it('draws a launcher as a small square in the compact layout', () => {
    const { graph } = withLaunchers(graphOf(['a']), states);
    const nodes = toFlowNodes(
      graph,
      layoutGraph(graph, 'TB', 6, true),
      null,
      null,
      '',
      true
    );
    const launcher = nodes.find((n) => n.id === 'launcher:cron')!;
    expect(launcher.type).toBe('launcher');
    expect(launcher.width).toBe(56);
    const lit = withLauncherStates(nodes, states, 'launcher:cron');
    expect(lit.find((n) => n.id === 'launcher:cron')!.selected).toBe(true);
    expect(
      lit.find((n) => n.id === 'launcher:cron')!.data.launcher?.status
    ).toBe('dashed');
  });
});
