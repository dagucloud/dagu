// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  bundleProblems,
  place,
  problems,
  randomGraph,
  type Drag,
  type Scenario,
} from './routingHarness';

// Seeded random workflows, laid out and drawn as the canvas does. Every edge
// must keep clear of the cards that it does not link, and must have no
// corner: no sharp join between pieces and no bend under the minimum radius.

const SIZES = [5, 9, 14, 22, 33, 47, 60];

interface Case {
  name: string;
  scenario: Scenario;
}

function cases(drag: Drag, extra: Partial<Scenario> = {}): Case[] {
  return SIZES.flatMap((size, index) =>
    [11, 12].flatMap((salt) =>
      (['TB', 'LR'] as const).flatMap((direction) =>
        [false, true].map((compact) => {
          const seed = size * 100 + salt + index;
          return {
            name: `n${size} seed${seed} ${direction}${compact ? ' simplified' : ''}`,
            scenario: {
              graph: randomGraph(seed, size),
              direction,
              compact,
              drag,
              seed,
              ...extra,
            },
          };
        })
      )
    )
  );
}

function report(list: string[]): string[] {
  return list.slice(0, 6);
}

// Dense graphs with a card dropped beside a long edge still leave a few
// edges that cross a card or bend tightly: they are listed here, and each
// one must keep failing until the routing handles it (`it.fails`).
const KNOWN_LIMITS = new Set([
  'a card dragged onto an edge: n47 seed4716 LR',
  'a card dragged onto an edge: n47 seed4717 LR',
  'a card dragged anywhere: n22 seed2214 LR',
  // The 72 px links of a top-to-bottom layout leave one more.
  'a card dragged anywhere: n47 seed4717 TB',
  // A card dropped far from the flow: its long edges cross a router or a card.
  'a card dragged anywhere: n22 seed2214 TB',
  'a card dragged anywhere: n22 seed2214 TB simplified',
]);

function run(group: string, list: Case[]) {
  for (const { name, scenario } of list) {
    const title = `${group}: ${name}`;
    const test = KNOWN_LIMITS.has(title) ? it.fails : it;
    test(title, () => {
      expect(report(problems(place(scenario)))).toEqual([]);
    });
  }
}

describe('bundles of random workflows', () => {
  const list = [
    ...cases('none'),
    ...cases('none', { launchers: true }),
    ...cases('random', { launchers: true }),
  ];
  it('keeps one straight trunk in each bundle', () => {
    let bundled = 0;
    const bad: string[] = [];
    for (const { name, scenario } of list) {
      const placed = place(scenario);
      bundled += new Set(Object.values(placed.bundles).map((b) => b.id)).size;
      bad.push(...bundleProblems(placed).map((m) => `${name}: ${m}`));
    }
    expect(bad.slice(0, 6)).toEqual([]);
    // The seeded graphs hold bundles, so that the check is not empty.
    expect(bundled).toBeGreaterThan(50);
  });
});

describe('edges of random workflows', () => {
  run('layout', cases('none'));
  run('with launchers', cases('none', { launchers: true }));
  run('with a ghost step', cases('none', { ghost: true }));
  run('a card dragged onto an edge', cases('onEdge'));
  run('a card dragged anywhere', cases('random', { launchers: true }));
});
