// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { availableParallelism, loadavg } from 'node:os';
import { describe, expect, it } from 'vitest';
import { buildCanvasGraph } from '../graph';
import { layoutRoutes, layoutGraph } from '../layout';
import { withToolSatellites } from '../toolSatellites';
import {
  makeLargeGraphInput,
  makeModel,
  makeNames,
  makeStep,
} from './fixtures';

const input = makeLargeGraphInput(300);
const BUDGET_MS = 200;
// Ceiling on a busy machine: it still catches a large regression.
const BUSY_CEILING_MS = 1000;

// CPU time of this process, in ms. It swings less than wall time when other
// processes share the machine.
function cpuMs(): number {
  const { user, system } = process.cpuUsage();
  return (user + system) / 1000;
}

// When other processes use more than half of the cores, shared caches and
// cores slow the layout down to about twice its time on an idle machine.
function busy(): boolean {
  const [oneMinute = 0] = loadavg();
  return oneMinute > availableParallelism() / 2;
}

function project() {
  const graph = buildCanvasGraph(input);
  return { graph, positions: layoutGraph(graph, 'LR') };
}

describe('canvas projection and layout', () => {
  // Other test files run in parallel workers; a retry measures again after
  // they have used the cores.
  it(`handles 300 steps in under ${BUDGET_MS} ms`, { retry: 3 }, () => {
    project();
    let best = Infinity;
    for (let run = 0; run < 5; run++) {
      const start = cpuMs();
      const { graph, positions } = project();
      best = Math.min(best, cpuMs() - start);
      expect(Object.keys(positions)).toHaveLength(graph.nodes.length);
    }
    if (busy()) {
      console.warn(`canvas perf: ${best.toFixed(0)} ms on a busy machine`);
      expect(best).toBeLessThan(BUSY_CEILING_MS);
      return;
    }
    expect(best).toBeLessThan(BUDGET_MS);
  });
});

// 100 agent steps in 10 parallel chains, each with a Model, a Prompt, Skills
// and a Tools card: the layout, the places of the sub-nodes and the waypoints.
describe('layout with sub-nodes', () => {
  const AGENT = {
    action: 'agent.run',
    with: {
      endpoint: { name: 'main', model: 'm' },
      prompt: 'Fix the test.',
      skills: ['a', 'b'],
      dags: Array.from({ length: 6 }, (_, i) => ({ name: `d${i}` })),
      tools: {
        builtin: { read: true, write: true },
      },
    },
  };
  function chains() {
    const steps = Array.from({ length: 100 }, (_, i) =>
      makeStep({
        key: `steps[${i}]`,
        explicitName: `s${i}`,
        kind: 'action',
        action: 'agent.run',
        depends: i < 10 ? [] : [`s${i - 10}`],
        value: AGENT,
      })
    );
    const model = makeModel(steps);
    return withToolSatellites(
      buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: { byStep: {}, dag: [] },
      })
    );
  }

  it(
    `lays out 100 agent steps with their sub-nodes in under ${BUDGET_MS} ms`,
    { retry: 3 },
    () => {
      const graph = chains();
      expect(graph.nodes.length).toBeGreaterThan(400);
      for (const direction of ['TB', 'LR'] as const) {
        layoutRoutes(graph, direction);
        let best = Infinity;
        for (let run = 0; run < 4; run++) {
          const start = cpuMs();
          layoutRoutes(graph, direction);
          best = Math.min(best, cpuMs() - start);
        }
        expect(best).toBeLessThan(busy() ? BUSY_CEILING_MS : BUDGET_MS);
      }
    }
  );
});
