// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ReferenceScopeStepOutputsKnown } from '@/api/v1/schema';
import type { ReferenceScope } from '../../contracts';
import { scopeHttpJq } from '../../testing/fixtures';
import { upstreamTree, type UpstreamNode } from '../upstreamTree';

function scopeOf(
  steps: Array<[name: string, upstream: string[]]>
): ReferenceScope {
  return {
    ...scopeHttpJq,
    steps: steps.map(([name, upstream], i) => ({
      name,
      id: name,
      stepPath: `steps[${i}]`,
      upstream,
      outputs: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
    })),
  };
}

/** The tree as text: one name for each node, `~` after a short entry. */
function shape(nodes: UpstreamNode[]): unknown[] {
  return nodes.map((node) => [
    node.step.name + (node.repeated ? '~' : ''),
    ...(node.children.length > 0 ? [shape(node.children)] : []),
  ]);
}

describe('upstreamTree', () => {
  it('nests a chain of three, with the listed ancestors reduced to direct ones', () => {
    const chain = scopeOf([
      ['step1', []],
      ['step2', ['step1']],
      ['step3', ['step1', 'step2']],
    ]);
    expect(shape(upstreamTree(chain, 'step3'))).toEqual([
      ['step2', [['step1']]],
    ]);
    expect(shape(upstreamTree(chain, 'step2'))).toEqual([['step1']]);
    expect(upstreamTree(chain, 'step1')).toEqual([]);
  });

  it('lists the two branches of a diamond and the shared step in full once', () => {
    const diamond = scopeOf([
      ['a', []],
      ['b', ['a']],
      ['c', ['a']],
      ['d', ['a', 'b', 'c']],
    ]);
    expect(shape(upstreamTree(diamond, 'd'))).toEqual([
      ['b', [['a']]],
      ['c', [['a~']]],
    ]);
  });

  it('keeps the order of the scope between the dependencies of a step', () => {
    const wide = scopeOf([
      ['x', []],
      ['y', []],
      ['z', []],
      ['top', ['z', 'x', 'y']],
    ]);
    expect(upstreamTree(wide, 'top').map((n) => n.step.name)).toEqual([
      'x',
      'y',
      'z',
    ]);
  });

  it('gives nothing for an unknown step and ends on a cycle', () => {
    expect(upstreamTree(scopeOf([['a', []]]), 'ghost')).toEqual([]);
    const cycle = scopeOf([
      ['a', ['b']],
      ['b', ['a']],
      ['c', ['a', 'b']],
    ]);
    const names = (nodes: UpstreamNode[]): string[] =>
      nodes.flatMap((n) => [n.step.name, ...names(n.children)]);
    expect(names(upstreamTree(cycle, 'c')).length).toBeLessThanOrEqual(4);
  });
});
