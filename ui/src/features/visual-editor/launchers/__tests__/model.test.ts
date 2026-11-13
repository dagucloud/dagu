// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { CanvasGraph, CanvasNode } from '../../contracts';
import { layoutGraph } from '../../canvas/layout';
import { toFlowNodes, withLauncherStates } from '../../canvas/flow';
import {
  drawnLauncher,
  isLauncherKey,
  launcherIdOf,
  launcherKey,
  launcherStates,
  SLOT_IDS,
  startSteps,
  type LauncherInputs,
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
  it('keeps Manual active and sets the Schedule slot and Webhook dashed by default', () => {
    expect(
      launcherStates({
        scheduleCount: 0,
        webhookCount: 0,
        webhooksAvailable: true,
      })
    ).toEqual([
      { id: 'manual', status: 'active' },
      { id: 'schedule', status: 'dashed' },
      { id: 'webhook', status: 'dashed' },
    ]);
  });

  it('draws Always on solid with the policy, the wait and the runs kept', () => {
    const states = launcherStates({
      scheduleCount: 0,
      restartPolicy: 'unless-stopped',
      restartWaitSec: 5,
      restartRetentionRuns: 20,
      webhookCount: 0,
      webhooksAvailable: false,
    });
    expect(states.find((s) => s.id === 'always')).toEqual({
      id: 'always',
      status: 'active',
      policy: 'unless-stopped',
      waitSec: 5,
      retentionRuns: 20,
    });
  });

  // Cron and Always on are one slot: a policy cannot go with a schedule
  // (spec 080).
  describe('the Cron and Always on slot', () => {
    type Slot = Partial<LauncherInputs> & { scheduleCount: number };
    const ids = (input: Slot) =>
      launcherStates({ webhookCount: 0, webhooksAvailable: false, ...input })
        .filter((s) => SLOT_IDS.includes(s.id))
        .map((s) => `${s.id}:${s.status}`);

    it('draws Always on in place of Cron with a policy', () => {
      expect(
        ids({ scheduleCount: 0, restartPolicy: 'always', restartWaitSec: 5 })
      ).toEqual(['always:active']);
      expect(
        ids({ scheduleCount: 0, restartPolicy: 'unless-stopped' })
      ).toEqual(['always:active']);
    });

    it('draws Cron alone with a schedule', () => {
      expect(ids({ scheduleCount: 2 })).toEqual(['cron:active']);
      expect(ids({ scheduleCount: 1, restartPolicy: 'no' })).toEqual([
        'cron:active',
      ]);
    });

    it('draws Cron for a schedule of any form, even with no start entry', () => {
      expect(ids({ scheduleCount: 0, schedulePresent: true })).toEqual([
        'cron:active',
      ]);
    });

    it('draws one Schedule placeholder with neither', () => {
      expect(ids({ scheduleCount: 0 })).toEqual(['schedule:dashed']);
      expect(ids({ scheduleCount: 0, restartPolicy: 'no' })).toEqual([
        'schedule:dashed',
      ]);
    });

    // The file is invalid: the diagnostics flag it, and the graph shows it.
    it('draws both solid with a policy and a schedule', () => {
      expect(
        ids({ scheduleCount: 1, restartPolicy: 'always', restartWaitSec: 5 })
      ).toEqual(['cron:active', 'always:active']);
    });

    it('keeps Manual first and Webhook last, and counts the launchers', () => {
      const count = (input: Slot) =>
        launcherStates({ webhookCount: 0, webhooksAvailable: true, ...input });
      const set = count({ scheduleCount: 0, restartPolicy: 'always' });
      expect(set.map((s) => s.id)).toEqual(['manual', 'always', 'webhook']);
      expect(count({ scheduleCount: 2 }).map((s) => s.id)).toEqual([
        'manual',
        'cron',
        'webhook',
      ]);
      expect(count({ scheduleCount: 0 }).map((s) => s.id)).toEqual([
        'manual',
        'schedule',
        'webhook',
      ]);
      expect(
        count({ scheduleCount: 1, restartPolicy: 'always' }).map((s) => s.id)
      ).toEqual(['manual', 'cron', 'always', 'webhook']);
    });
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
    expect(launcherIdOf('launcher:always')).toBe('always');
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
  // The widest set: a file with both keys draws Cron and Always on.
  const states = launcherStates({
    scheduleCount: 1,
    restartPolicy: 'always',
    restartWaitSec: 5,
    webhookCount: 0,
    webhooksAvailable: true,
  });

  it('connects every launcher to every start step', () => {
    const graph = graphOf(['a', 'b', 'c'], ['a>c', 'b>c']);
    const launched = withLaunchers(graph, states);
    expect(launched.edges).toHaveLength(8);
    expect(launched.edges.map((e) => `${e.source}>${e.target}`)).toContain(
      'launcher:cron>b'
    );
    expect(launched.edges.map((e) => `${e.source}>${e.target}`)).toContain(
      'launcher:always>a'
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
    ).toBe('active');
  });
});

describe('drawnLauncher', () => {
  const states = (input: Parameters<typeof launcherStates>[0]) =>
    launcherStates(input);
  const none = { webhookCount: 0, webhooksAvailable: false };

  it('keeps an id that is drawn, and an id outside the slot', () => {
    const set = states({ scheduleCount: 1, ...none });
    expect(drawnLauncher('cron', set)).toBe('cron');
    expect(drawnLauncher('manual', set)).toBe('manual');
    expect(drawnLauncher('webhook', set)).toBe('webhook');
    expect(drawnLauncher(null, set)).toBeNull();
  });

  // The panel of the slot follows the node as the author sets a value.
  it('follows the node of the slot when its id changes', () => {
    const cron = states({ scheduleCount: 1, ...none });
    const always = states({
      scheduleCount: 0,
      restartPolicy: 'always',
      ...none,
    });
    const neither = states({ scheduleCount: 0, ...none });
    expect(drawnLauncher('schedule', cron)).toBe('cron');
    expect(drawnLauncher('schedule', always)).toBe('always');
    expect(drawnLauncher('cron', neither)).toBe('schedule');
    expect(drawnLauncher('always', cron)).toBe('cron');
  });
});
